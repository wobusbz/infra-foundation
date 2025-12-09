package scheduler

import (
	"container/list"
	"sync"
	"sync/atomic"
	"time"
)

type Timer struct {
	id        TimerID
	fn        TimerFunc
	interval  time.Duration
	recurring bool
	rounds    int
	ticks     int
	elem      *list.Element
	slot      int
}

type TimerWheel struct {
	slots     []*list.List
	current   int
	slotNum   int
	tick      time.Duration
	lock      sync.Mutex
	scheduler *Scheduler
	idSeq     atomic.Uint64
	index     map[TimerID]*Timer
}

func newTimerWheel(slotNum int, tick time.Duration, scheduler *Scheduler) *TimerWheel {
	tw := &TimerWheel{
		slots:     make([]*list.List, slotNum),
		slotNum:   slotNum,
		tick:      tick,
		scheduler: scheduler,
		index:     make(map[TimerID]*Timer),
	}
	for i := range slotNum {
		tw.slots[i] = list.New()
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
	timer.elem = t.slots[slot].PushBack(timer)
	t.index[id] = timer
	return id, nil
}

func (t *TimerWheel) cancelTimer(id TimerID) bool {
	t.lock.Lock()
	defer t.lock.Unlock()
	timer, ok := t.index[id]
	if !ok || timer.elem == nil {
		return false
	}
	t.slots[timer.slot].Remove(timer.elem)
	delete(t.index, id)
	return true
}

func (t *TimerWheel) tickerHandler() {
	t.lock.Lock()
	slot := t.slots[t.current]
	for el := slot.Front(); el != nil; {
		next := el.Next()
		timer := el.Value.(*Timer)
		if timer.rounds > 0 {
			timer.rounds--
			el = next
			continue
		}
		if t.scheduler != nil {
			t.scheduler.PushTask(timer.fn)
		} else {
			go timer.fn()
		}
		slot.Remove(el)
		delete(t.index, timer.id)

		if timer.recurring {
			ticks, rounds, slotIdx := t.plan(timer.interval)
			timer.ticks = ticks
			timer.rounds = rounds
			timer.slot = slotIdx
			timer.elem = t.slots[slotIdx].PushBack(timer)
			t.index[timer.id] = timer
		}

		el = next
	}
	t.current = (t.current + 1) % t.slotNum
	t.lock.Unlock()
}
