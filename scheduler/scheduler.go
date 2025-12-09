package scheduler

import (
	"errors"
	"infra-foundation/logx"
	"infra-foundation/queue"
	"runtime"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"
)

type TimerFunc func()
type TimerID uint64

type Scheduler struct {
	chDie     chan struct{}
	qTask     *queue.Queue[TimerFunc]
	qTaskCond *sync.Cond
	started   atomic.Bool
	timeWheel *TimerWheel
	tick      time.Duration
	slotNum   int
	wg        sync.WaitGroup
}

const (
	defaultTick    = time.Second
	defaultSlotNum = 1024
)

func NewScheduler() *Scheduler {
	return NewSchedulerWith(defaultSlotNum, defaultTick)
}

func NewSchedulerWith(slotNum int, tick time.Duration) *Scheduler {
	if slotNum <= 0 {
		slotNum = defaultSlotNum
	}
	if tick <= 0 {
		tick = defaultTick
	}

	s := &Scheduler{
		chDie:     make(chan struct{}),
		qTask:     queue.New[TimerFunc](),
		qTaskCond: sync.NewCond(&sync.Mutex{}),
		tick:      tick,
		slotNum:   slotNum,
	}
	s.timeWheel = newTimerWheel(slotNum, tick, s)
	s.started.Store(true)

	s.wg.Go(s.sched)
	s.wg.Go(s.runQueuedTasks)
	return s
}

func (s *Scheduler) Stop() {
	if !s.started.CompareAndSwap(true, false) {
		return
	}
	close(s.chDie)
	s.qTaskCond.L.Lock()
	s.qTaskCond.Signal()
	s.qTaskCond.L.Unlock()
	s.Wait()
}

func (s *Scheduler) Wait() {
	s.wg.Wait()
}

func (s *Scheduler) sched() {
	ticker := time.NewTicker(s.tick)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.timeWheel.tickerHandler()
		case <-s.chDie:
			return
		}
	}
}

func (s *Scheduler) runQueuedTasks() {
	for {
		select {
		case <-s.chDie:
			logx.Dbg.Println("runQueuedTasks die")
			return
		default:
		}
		s.qTaskCond.L.Lock()
		if s.qTask.Empty() {
			runtime.Gosched()
			s.qTaskCond.Wait()
		}
		f, _ := s.qTask.PopSingleThread()
		s.qTaskCond.L.Unlock()
		if f != nil {
			try(f)
		}
	}
}

func (s *Scheduler) PushTask(fn TimerFunc) {
	if !s.started.Load() || fn == nil {
		return
	}
	s.qTaskCond.L.Lock()
	s.qTask.Push(fn)
	s.qTaskCond.Signal()
	s.qTaskCond.L.Unlock()
}

func (s *Scheduler) PushAfter(delay time.Duration, fn TimerFunc) (TimerID, error) {
	if !s.started.Load() || fn == nil {
		return 0, errors.New("scheduler not started or nil func")
	}
	return s.timeWheel.addTimer(delay, false, fn)
}

func (s *Scheduler) PushEvery(interval time.Duration, fn TimerFunc) (TimerID, error) {
	if !s.started.Load() || fn == nil {
		return 0, errors.New("scheduler not started or nil func")
	}
	return s.timeWheel.addTimer(interval, true, fn)
}

func (s *Scheduler) PushInfiniteTimer(interval time.Duration, infinite bool, fn TimerFunc) TimerID {
	var id TimerID
	if infinite {
		id, _ = s.PushEvery(interval, fn)
	} else {
		id, _ = s.PushAfter(interval, fn)
	}
	return id
}

func (s *Scheduler) CancelTimer(id TimerID) bool {
	return s.timeWheel.cancelTimer(id)
}

func try(cb func()) {
	defer func() {
		if err := recover(); err != nil {
			logx.Err.Printf("scheduler: panic: %+v\n%s\n", err, debug.Stack())
		}
	}()
	cb()
}
