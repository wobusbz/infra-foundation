package scheduler

import (
	"infra-foundation/metrics"
	"sync"
	"sync/atomic"
	"time"
)

type TimerFunc func()

type TimerID uint64

type Timer struct {
	id        TimerID
	fn        TimerFunc
	interval  time.Duration
	recurring bool
	rounds    int
	ticks     int
	slot      int

	prev *Timer
	next *Timer
	list *TimerList
}

type TimerList struct {
	head *Timer
	tail *Timer
}

func (l *TimerList) PushBack(t *Timer) {
	t.list = l
	if l.tail == nil {
		l.head = t
		l.tail = t
		t.prev = nil
		t.next = nil
	} else {
		l.tail.next = t
		t.prev = l.tail
		t.next = nil
		l.tail = t
	}
}

func (l *TimerList) Remove(t *Timer) {
	if t.list != l {
		return
	}
	if t.prev != nil {
		t.prev.next = t.next
	} else {
		l.head = t.next
	}
	if t.next != nil {
		t.next.prev = t.prev
	} else {
		l.tail = t.prev
	}
	t.next = nil
	t.prev = nil
	t.list = nil
}

type timerSlot struct {
	list *TimerList
	mu   sync.Mutex
}

type TimerWheel struct {
	slots        []*timerSlot
	current      atomic.Int64
	slotNum      int
	tick         time.Duration
	scheduler    *Scheduler
	idSeq        atomic.Uint64
	index        map[TimerID]*Timer
	indexMu      sync.RWMutex
	pendingTasks []TimerFunc
	pendingMu    sync.Mutex
}

func newTimerWheel(slotNum int, tick time.Duration, scheduler *Scheduler) *TimerWheel {
	tw := &TimerWheel{
		slots:        make([]*timerSlot, slotNum),
		slotNum:      slotNum,
		tick:         tick,
		scheduler:    scheduler,
		index:        make(map[TimerID]*Timer),
		pendingTasks: make([]TimerFunc, 0, 128),
	}
	for i := range slotNum {
		tw.slots[i] = &timerSlot{list: &TimerList{}}
	}
	return tw
}

func (t *TimerWheel) plan(interval time.Duration) (ticks int, rounds int, slot int) {
	if interval <= 0 {
		interval = t.tick
	}
	ticks = int(interval / t.tick)
	if ticks <= 0 {
		ticks = 1
	}
	rounds = ticks / t.slotNum
	slotOffset := ticks % t.slotNum
	slot = (int(t.current.Load()) + slotOffset) % t.slotNum
	return
}

func (t *TimerWheel) addTimer(interval time.Duration, recurring bool, fn TimerFunc) (TimerID, error) {
	if fn == nil {
		return 0, nil
	}

	id := TimerID(t.idSeq.Add(1))
	ticks, rounds, slotIdx := t.plan(interval)

	timer := &Timer{
		id:        id,
		fn:        fn,
		interval:  interval,
		recurring: recurring,
		rounds:    rounds,
		ticks:     ticks,
		slot:      slotIdx,
	}

	s := t.slots[slotIdx]
	s.mu.Lock()
	s.list.PushBack(timer)
	s.mu.Unlock()

	t.indexMu.Lock()
	t.index[id] = timer
	t.indexMu.Unlock()

	metrics.GaugeOf("scheduler_timer_active").Add(1)
	return id, nil
}

func (t *TimerWheel) cancelTimer(id TimerID) bool {
	t.indexMu.RLock()
	timer, ok := t.index[id]
	t.indexMu.RUnlock()
	if !ok {
		return false
	}

	if timer.list != nil {
		s := t.slots[timer.slot]
		s.mu.Lock()
		// double-check after acquiring slot lock
		if timer.list != nil {
			timer.list.Remove(timer)
		}
		s.mu.Unlock()
	}

	t.indexMu.Lock()
	delete(t.index, id)
	t.indexMu.Unlock()
	metrics.GaugeOf("scheduler_timer_active").Sub(1)
	return true
}

func (t *TimerWheel) tickerHandler() {
	slotIdx := int(t.current.Load()) % t.slotNum
	s := t.slots[slotIdx]

	s.mu.Lock()

	t.pendingMu.Lock()
	t.pendingTasks = t.pendingTasks[:0]
	t.pendingMu.Unlock()

	var recurTimers []*Timer

	for timer := s.list.head; timer != nil; {
		next := timer.next
		if timer.rounds > 0 {
			timer.rounds--
			timer = next
			continue
		}

		t.pendingMu.Lock()
		t.pendingTasks = append(t.pendingTasks, timer.fn)
		t.pendingMu.Unlock()

		s.list.Remove(timer)

		if timer.recurring {
			recurTimers = append(recurTimers, timer)
		} else {
			t.indexMu.Lock()
			delete(t.index, timer.id)
			t.indexMu.Unlock()
		}
		timer = next
	}

	s.mu.Unlock()

	t.current.Store(int64((slotIdx + 1) % t.slotNum))

	for _, timer := range recurTimers {
		ticks, rounds, newSlotIdx := t.plan(timer.interval)
		timer.ticks = ticks
		timer.rounds = rounds
		timer.slot = newSlotIdx

		ns := t.slots[newSlotIdx]
		ns.mu.Lock()
		ns.list.PushBack(timer)
		ns.mu.Unlock()

		t.indexMu.Lock()
		t.index[timer.id] = timer
		t.indexMu.Unlock()
	}

	t.pendingMu.Lock()
	batch := t.pendingTasks
	t.pendingMu.Unlock()

	if len(batch) > 0 && t.scheduler != nil {
		for _, fn := range batch {
			t.scheduler.PushTask(fn)
		}
	}
}
