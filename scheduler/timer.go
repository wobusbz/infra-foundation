package scheduler

import (
	"infra-foundation/metric"
	"sync"
	"sync/atomic"
	"time"
)

var timerPool = sync.Pool{
	New: func() any { return &Timer{} },
}

type TaskFunc func()

type TimerID uint64

type Timer struct {
	id        TimerID
	fn        TaskFunc
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
	pendingTasks []TaskFunc
	pendingMu    sync.Mutex
}

func newTimerWheel(slotNum int, tick time.Duration, scheduler *Scheduler) *TimerWheel {
	tw := &TimerWheel{
		slots:        make([]*timerSlot, slotNum),
		slotNum:      slotNum,
		tick:         tick,
		scheduler:    scheduler,
		index:        make(map[TimerID]*Timer),
		pendingTasks: make([]TaskFunc, 0, 128),
	}
	for i := range slotNum {
		tw.slots[i] = &timerSlot{list: &TimerList{}}
	}
	return tw
}

func (t *TimerWheel) calcSlot(interval time.Duration) (ticks int, rounds int, slot int) {
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

func (t *TimerWheel) addTimer(interval time.Duration, recurring bool, fn TaskFunc) (TimerID, error) {
	if fn == nil {
		return 0, nil
	}

	id := TimerID(t.idSeq.Add(1))
	ticks, rounds, slotIdx := t.calcSlot(interval)

	timer := timerPool.Get().(*Timer)
	timer.id = id
	timer.fn = fn
	timer.interval = interval
	timer.recurring = recurring
	timer.rounds = rounds
	timer.ticks = ticks
	timer.slot = slotIdx
	timer.prev = nil
	timer.next = nil
	timer.list = nil

	t.indexMu.Lock()
	t.index[id] = timer
	t.indexMu.Unlock()

	s := t.slots[slotIdx]
	s.mu.Lock()
	s.list.PushBack(timer)
	s.mu.Unlock()

	metric.GaugeOf("scheduler_timer_active").Add(1)
	return id, nil
}

func (t *TimerWheel) cancelTimer(id TimerID) bool {
	t.indexMu.Lock()
	timer, ok := t.index[id]
	if !ok {
		t.indexMu.Unlock()
		return false
	}
	delete(t.index, id)
	t.indexMu.Unlock()

	if timer.list != nil {
		s := t.slots[timer.slot]
		s.mu.Lock()
		if timer.list != nil {
			timer.list.Remove(timer)
		}
		s.mu.Unlock()
	}

	metric.GaugeOf("scheduler_timer_active").Sub(1)
	t.resetAndPut(timer)
	return true
}

func (t *TimerWheel) resetAndPut(timer *Timer) {
	timer.id = 0
	timer.fn = nil
	timer.interval = 0
	timer.recurring = false
	timer.rounds = 0
	timer.ticks = 0
	timer.slot = 0
	timer.prev = nil
	timer.next = nil
	timer.list = nil
	timerPool.Put(timer)
}

func (t *TimerWheel) advance() {
	slotIdx := int(t.current.Load()) % t.slotNum
	s := t.slots[slotIdx]

	s.mu.Lock()

	var pendingBatch []TaskFunc

	type expiredTimer struct {
		timer     *Timer
		id        TimerID
		recurring bool
	}
	var expired []expiredTimer

	for timer := s.list.head; timer != nil; {
		next := timer.next
		if timer.rounds > 0 {
			timer.rounds--
			timer = next
			continue
		}

		pendingBatch = append(pendingBatch, timer.fn)
		s.list.Remove(timer)

		expired = append(expired, expiredTimer{timer: timer, id: timer.id, recurring: timer.recurring})
		timer = next
	}

	s.mu.Unlock()

	if len(pendingBatch) > 0 {
		t.pendingMu.Lock()
		t.pendingTasks = append(t.pendingTasks, pendingBatch...)
		t.pendingMu.Unlock()
	}

	t.current.Store(int64((slotIdx + 1) % t.slotNum))

	if len(expired) > 0 {
		type reschedule struct {
			timer *Timer
			id    TimerID
		}
		var toReschedule []reschedule

		t.indexMu.Lock()
		for _, et := range expired {
			if et.recurring {
				if existing, ok := t.index[et.id]; !ok || existing != et.timer {
					continue
				}
				delete(t.index, et.id)

				ticks, rounds, newSlotIdx := t.calcSlot(et.timer.interval)
				et.timer.ticks = ticks
				et.timer.rounds = rounds
				et.timer.slot = newSlotIdx
				et.timer.prev = nil
				et.timer.next = nil
				et.timer.list = nil

				toReschedule = append(toReschedule, reschedule{timer: et.timer, id: et.id})
			} else {
				delete(t.index, et.id)
			}
		}
		t.indexMu.Unlock()

		for _, rs := range toReschedule {
			ns := t.slots[rs.timer.slot]
			ns.mu.Lock()
			ns.list.PushBack(rs.timer)
			ns.mu.Unlock()

			t.indexMu.Lock()
			t.index[rs.id] = rs.timer
			t.indexMu.Unlock()
		}

		for _, et := range expired {
			if !et.recurring {
				t.resetAndPut(et.timer)
			}
		}
	}

	t.pendingMu.Lock()
	batch := t.pendingTasks
	t.pendingTasks = t.pendingTasks[:0]
	t.pendingMu.Unlock()

	if len(batch) > 0 && t.scheduler != nil {
		for _, fn := range batch {
			t.scheduler.PushTask(fn)
		}
	}
}
