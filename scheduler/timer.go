package scheduler

import (
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

type TimerWheel struct {
	slots        []*TimerList
	current      int
	slotNum      int
	tick         time.Duration
	lock         sync.Mutex
	scheduler    *Scheduler
	idSeq        atomic.Uint64
	index        map[TimerID]*Timer
	pendingTasks []TimerFunc
}

func newTimerWheel(slotNum int, tick time.Duration, scheduler *Scheduler) *TimerWheel {
	tw := &TimerWheel{
		slots:        make([]*TimerList, slotNum),
		slotNum:      slotNum,
		tick:         tick,
		scheduler:    scheduler,
		index:        make(map[TimerID]*Timer),
		pendingTasks: make([]TimerFunc, 0, 128),
	}
	for i := range slotNum {
		tw.slots[i] = &TimerList{}
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
	slot = (t.current + slotOffset) % t.slotNum
	return
}

func (t *TimerWheel) addTimer(interval time.Duration, recurring bool, fn TimerFunc) (TimerID, error) {
	if fn == nil {
		return 0, nil
	}
	t.lock.Lock()
	defer t.lock.Unlock()

	id := TimerID(t.idSeq.Add(1))
	ticks, rounds, slot := t.plan(interval)

	timer := &Timer{
		id:        id,
		fn:        fn,
		interval:  interval,
		recurring: recurring,
		rounds:    rounds,
		ticks:     ticks,
		slot:      slot,
	}
	t.slots[slot].PushBack(timer)
	t.index[id] = timer
	return id, nil
}

func (t *TimerWheel) cancelTimer(id TimerID) bool {
	t.lock.Lock()
	defer t.lock.Unlock()
	timer, ok := t.index[id]
	if !ok {
		return false
	}
	if timer.list != nil {
		timer.list.Remove(timer)
	}
	delete(t.index, id)
	return true
}

func (t *TimerWheel) tickerHandler() {
	t.lock.Lock()
	slot := t.slots[t.current]
	t.pendingTasks = t.pendingTasks[:0]

	for timer := slot.head; timer != nil; {
		next := timer.next
		if timer.rounds > 0 {
			timer.rounds--
			timer = next
			continue
		}

		t.pendingTasks = append(t.pendingTasks, timer.fn)

		slot.Remove(timer)
		delete(t.index, timer.id)

		if timer.recurring {
			ticks, rounds, slotIdx := t.plan(timer.interval)
			timer.ticks = ticks
			timer.rounds = rounds
			timer.slot = slotIdx
			t.slots[slotIdx].PushBack(timer)
			t.index[timer.id] = timer
		}
		timer = next
	}
	t.current = (t.current + 1) % t.slotNum
	t.lock.Unlock()

	if len(t.pendingTasks) > 0 && t.scheduler != nil {
		for _, fn := range t.pendingTasks {
			t.scheduler.PushTask(fn)
		}
	}
}
