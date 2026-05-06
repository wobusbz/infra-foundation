package scheduler

import (
	"errors"
	"infra-foundation/config"
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
	tasks     []TaskFunc
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
	tick := config.Default.SchedulerTick
	if tick <= 0 {
		tick = defaultTick
	}
	slotNum := config.Default.SchedulerSlotNum
	if slotNum <= 0 {
		slotNum = defaultSlotNum
	}

	s := &Scheduler{
		chDie:   make(chan struct{}),
		tasks:   make([]TaskFunc, 0, config.Default.SchedulerTaskCap),
		tick:    tick,
		slotNum: slotNum,
	}
	s.taskCond = sync.NewCond(&s.taskLock)
	s.timeWheel = newTimerWheel(slotNum, tick, s)
	s.started.Store(true)

	s.wg.Go(s.runTicker)
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

func (s *Scheduler) runTicker() {
	ticker := time.NewTicker(s.tick)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.timeWheel.advance()
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

		batch := s.tasks
		s.tasks = make([]TaskFunc, 0, cap(batch))

		s.taskLock.Unlock()

		for _, fn := range batch {
			safeRun(fn)
		}

		s.taskLock.Lock()
	}
}

func (s *Scheduler) PushTask(fn TaskFunc) {
	if !s.started.Load() || fn == nil {
		return
	}

	s.taskLock.Lock()
	s.tasks = append(s.tasks, fn)
	s.taskCond.Signal()
	s.taskLock.Unlock()
}

func (s *Scheduler) PushAfter(delay time.Duration, fn TaskFunc) (TimerID, error) {
	if !s.started.Load() || fn == nil {
		return 0, errors.New("scheduler not started or nil func")
	}
	return s.timeWheel.addTimer(delay, false, fn)
}

func (s *Scheduler) PushEvery(interval time.Duration, fn TaskFunc) (TimerID, error) {
	if !s.started.Load() || fn == nil {
		return 0, errors.New("scheduler not started or nil func")
	}
	return s.timeWheel.addTimer(interval, true, fn)
}

func (s *Scheduler) ScheduleTimer(interval time.Duration, recurring bool, fn TaskFunc) TimerID {
	var id TimerID
	if recurring {
		id, _ = s.PushEvery(interval, fn)
	} else {
		id, _ = s.PushAfter(interval, fn)
	}
	return id
}

func (s *Scheduler) CancelTimer(id TimerID) bool {
	return s.timeWheel.cancelTimer(id)
}

func safeRun(cb func()) {
	defer func() {
		if err := recover(); err != nil {
			logx.Err.Printf("scheduler: panic: %+v\n%s\n", err, debug.Stack())
		}
	}()
	cb()
}
