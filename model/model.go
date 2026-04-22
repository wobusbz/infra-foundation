package model

import (
	"context"
	"infra-foundation/scheduler"
	"infra-foundation/session"
	"time"
)

type Model interface {
	Name() string
	OnInit() error
	OnStart() error
	OnStop() error
	OnDisconnection(session.Session)
}

type modelActor struct {
	Model
	mailbox *scheduler.Scheduler
}

func newModelActor(m Model) *modelActor {
	return &modelActor{mailbox: scheduler.NewScheduler(), Model: m}
}

func (m *modelActor) Post(f func()) {
	m.mailbox.PushTask(f)
}

func (m *modelActor) PushInfiniteTimer(interval time.Duration, infiniter bool, f func()) scheduler.TimerID {
	return m.mailbox.ScheduleTimer(interval, infiniter, f)
}

func (m *modelActor) CancelTimer(id scheduler.TimerID) bool {
	return m.mailbox.CancelTimer(id)
}

func (m *modelActor) Forward(md *modelActor, cb func()) {
	md.mailbox.PushTask(cb)
}

func (m *modelActor) OnDisconnection(s session.Session) {
	m.mailbox.PushTask(func() { m.Model.OnDisconnection(s) })
}

func (m *modelActor) Stop() {
	if m.Model != nil {
		m.Model.OnStop()
	}
	m.mailbox.Stop()
}

type result[T any] struct {
	Result T
	Error  error
}

func Do[T any](ctx context.Context, m *modelActor, fn func() (T, error)) (T, error) {
	resultChan := make(chan result[T], 1)

	m.Post(func() {
		val, err := fn()
		resultChan <- result[T]{val, err}
	})
	select {
	case res := <-resultChan:
		return res.Result, res.Error
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	}
}

