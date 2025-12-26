package scheduler

import (
	"errors"
	"infra-foundation/logx"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"
)

type Scheduler struct {
	chDie     chan struct{}
	taskLock  sync.Mutex
	taskCond  *sync.Cond
	tasks     []TimerFunc
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
		chDie:   make(chan struct{}),
		tasks:   make([]TimerFunc, 0, 4096),
		tick:    tick,
		slotNum: slotNum,
	}
	s.taskCond = sync.NewCond(&s.taskLock)
	s.timeWheel = newTimerWheel(slotNum, tick, s)
	s.started.Store(true)

	s.wg.Go(s.sched)
	s.wg.Go(s.runExecutor)
	return s
}

func (s *Scheduler) Stop() {
	if !s.started.CompareAndSwap(true, false) {
		return
	}
	close(s.chDie)

	s.taskLock.Lock()
	s.taskCond.Broadcast()
	s.taskLock.Unlock()

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

func (s *Scheduler) runExecutor() {
	s.taskLock.Lock()
	defer s.taskLock.Unlock()

	for {
		for len(s.tasks) == 0 {
			if !s.started.Load() {
				return
			}
			s.taskCond.Wait()
		}

		fn := s.tasks[0]

		s.tasks = s.tasks[1:]

		if len(s.tasks) == 0 {
			s.tasks = s.tasks[:0]
		}

		s.taskLock.Unlock()

		try(fn)

		s.taskLock.Lock()
	}
}

func (s *Scheduler) PushTask(fn TimerFunc) {
	if !s.started.Load() || fn == nil {
		return
	}

	s.taskLock.Lock()
	s.tasks = append(s.tasks, fn)
	s.taskCond.Signal()
	s.taskLock.Unlock()
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
