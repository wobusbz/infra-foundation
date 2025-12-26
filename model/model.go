package model

import (
	"context"
	protomessage "infra-foundation/protomessage"
	"infra-foundation/scheduler"
	"infra-foundation/session"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"
)

type Model interface {
	Name() string
	OnInit() error
	OnStart() error
	OnStop() error
	OnDisconnection(session.Session)
}

type handler struct {
	model  *model
	name   string
	pbPool sync.Pool
	handle session.HandlerFunc
}

func (h *handler) Put(pb protomessage.ProtoMessage) {
	if pb == nil {
		return
	}
	proto.Reset(pb)
	h.pbPool.Put(pb)
}

var Handlers sync.Map

func IsLocalHandler(id int32) bool {
	_, ok := Handlers.Load(id)
	return ok
}

func HandlersRoutes() []int32 {
	var routes []int32
	Handlers.Range(func(key, _ any) bool {
		routes = append(routes, key.(int32))
		return true
	})
	return routes
}

func RegisterHandler(pb protomessage.ProtoMessage, hanHandlerFunc session.HandlerFunc) {
	hd := &handler{name: pb.ModeName(), handle: hanHandlerFunc}
	hd.pbPool = sync.Pool{New: func() any { return proto.Clone(pb) }}
	Handlers.Store(pb.MessageID(), hd)
}

type model struct {
	Model
	mailbox *scheduler.Scheduler
}

func newModel(m Model) *model {
	return &model{mailbox: scheduler.NewScheduler(), Model: m}
}

func (m *model) PostFunc(f func()) {
	m.mailbox.PushTask(f)
}

func (m *model) PushInfiniteTimer(interval time.Duration, infiniter bool, f func()) scheduler.TimerID {
	return m.mailbox.PushInfiniteTimer(interval, infiniter, f)
}

func (m *model) CancelTimer(id scheduler.TimerID) bool {
	return m.mailbox.CancelTimer(id)
}

func (m *model) DoAsync(md *model, cb func()) {
	md.mailbox.PushTask(cb)
}

func (m *model) OnDisconnection(s session.Session) {
	m.mailbox.PushTask(func() { m.Model.OnDisconnection(s) })
}

func (m *model) Stop() {
	if m.Model != nil {
		m.Model.OnStop()
	}
	m.mailbox.Stop()
}

type result[T any] struct {
	Result T
	Error  error
}

func Do[T any](ctx context.Context, m *model, fn func() (T, error)) (T, error) {
	resultChan := make(chan result[T], 1)

	m.PostFunc(func() {
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
