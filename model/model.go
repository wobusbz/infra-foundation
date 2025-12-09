package model

import (
	"context"
	"errors"
	"fmt"
	"infra-foundation/pcall"
	protomessage "infra-foundation/protomessage"
	"infra-foundation/scheduler"
	"infra-foundation/session"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"time"
)

type Model interface {
	Name() string
	OnInit() error
	OnStart() error
	OnStop() error
	OnDisconnection(session.Session)
}

type handler struct {
	name   string
	pb     protomessage.ProtoMessage
	handle session.HandlerFunc
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
	Handlers.Store(pb.MessageID(), &handler{name: pb.ModeName(), pb: pb, handle: hanHandlerFunc})
}

type model struct {
	Model
	internalHandler map[string]reflect.Value
	scheduler       *scheduler.Scheduler
}

func buildArgs(fn reflect.Value, args ...any) ([]reflect.Value, error) {
	ft := fn.Type()
	numIn := ft.NumIn()
	if len(args) != numIn {
		return nil, fmt.Errorf("arg number mismatch: expected %d, got %d", numIn, len(args))
	}
	argv := make([]reflect.Value, numIn)
	for i := range numIn {
		pt := ft.In(i)
		a := args[i]
		if a == nil {
			argv[i] = reflect.Zero(pt)
			continue
		}
		v := reflect.ValueOf(a)
		if v.IsValid() && v.Type().AssignableTo(pt) {
			argv[i] = v
			continue
		}
		if v.IsValid() && v.Type().ConvertibleTo(pt) {
			argv[i] = v.Convert(pt)
			continue
		}
		return nil, fmt.Errorf("arg %d type mismatch: have %v, need %v", i, v.Type(), pt)
	}
	return argv, nil
}

func newModel(m Model) *model {
	return &model{internalHandler: map[string]reflect.Value{}, scheduler: scheduler.NewScheduler(), Model: m}
}

func (m *model) RegisterFunc(f any) {
	valueOf := reflect.ValueOf(f)
	if valueOf.Kind() != reflect.Func {
		return
	}
	full := strings.Split(runtime.FuncForPC(valueOf.Pointer()).Name(), ".")
	names := strings.TrimSuffix(full[len(full)-1], "-fm")
	m.internalHandler[names] = valueOf
}

func (m *model) PostFunc(f func()) {
	m.scheduler.PushTask(f)
}

func (m *model) PushInfiniteTimer(interval time.Duration, infiniter bool, f func()) scheduler.TimerID {
	return m.scheduler.PushInfiniteTimer(interval, infiniter, f)
}

func (m *model) CancelTimer(id scheduler.TimerID) bool {
	return m.scheduler.CancelTimer(id)
}

func (m *model) CallAsync(name string, args ...any) error {
	funcname, ok := m.internalHandler[name]
	if !ok {
		return fmt.Errorf("[component] CallAsync internalHandler[%s] nof found", name)
	}
	argv, err := buildArgs(funcname, args...)
	if err != nil {
		return err
	}
	m.scheduler.PushTask(func() { pcall.Pcall2(funcname, argv) })
	return nil
}

func (m *model) CallSync(name string, args ...any) ([]any, error) {
	return m.CallSyncWithContext(context.Background(), name, args...)
}

func (m *model) CallSyncWithContext(ctx context.Context, name string, args ...any) ([]any, error) {
	funcname, ok := m.internalHandler[name]
	if !ok {
		return nil, fmt.Errorf("[component] CallSync internalHandler[%s] nof found", name)
	}
	argv, err := buildArgs(funcname, args...)
	if err != nil {
		return nil, err
	}
	var reply = make(chan []reflect.Value)
	m.scheduler.PushTask(func() { reply <- pcall.Pcall2(funcname, argv) })
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	select {
	case rvs := <-reply:
		var errs []error
		var result []any
		for _, rv := range rvs {
			if rv.IsValid() && rv.Type().Implements(reflect.TypeOf((*error)(nil)).Elem()) {
				if !rv.IsNil() {
					errs = append(errs, rv.Interface().(error))
				}
				continue
			}
			result = append(result, rv.Interface())
		}
		return result, errors.Join(errs...)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (m *model) Stop() {
	if m != nil {
		m.OnStop()
	}
	m.scheduler.Stop()
}
