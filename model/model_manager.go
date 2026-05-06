package model

import (
	"context"
	"errors"
	"fmt"
	"infra-foundation/logx"
	"infra-foundation/message"
	"infra-foundation/metric"
	"infra-foundation/session"
	"net/http"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"
)

type ModelManager struct {
	mu         sync.RWMutex
	modes      map[string]*modelActor
	order      []string
	handlersMu sync.RWMutex
	handlers   map[int32]*protoHandler
}

func NewModelManager() *ModelManager {
	return &ModelManager{
		modes:    map[string]*modelActor{},
		handlers: map[int32]*protoHandler{},
	}
}

var DefaultModelManager = NewModelManager()

func (m *ModelManager) Register(model Model) error {
	if model == nil {
		return errors.New("model.Manager.Register: model is nil")
	}
	name := model.Name()
	if name == "" {
		return errors.New("model.Manager.Register: Name is empty")
	}
	if err := model.OnInit(); err != nil {
		return fmt.Errorf("model.Manager.Register: OnInit %w", err)
	}
	if err := model.OnStart(); err != nil {
		return fmt.Errorf("model.Manager.Register: OnStart %w", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.modes[name]; exists {
		return fmt.Errorf("model.Manager.Register: duplicated model name %q", name)
	}
	m.modes[name] = newModelActor(model)

	m.order = append(m.order, name)
	return nil
}

func (m *ModelManager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := len(m.order) - 1; i >= 0; i-- {
		w, ok := m.modes[m.order[i]]
		if !ok {
			continue
		}
		w.Stop()
	}
	return nil
}

func (m *ModelManager) OnSessionInitialization(s session.Session) {
	m.mu.RLock()
	for _, name := range m.order {
		md, ok := m.modes[name]
		if !ok {
			continue
		}
		md.OnSessionInitialization(s)
	}
	m.mu.RUnlock()
}

func (m *ModelManager) OnDisconnection(s session.Session) {
	m.mu.RLock()
	for _, name := range m.order {
		md, ok := m.modes[name]
		if !ok {
			continue
		}
		md.OnSessionDisconnected(s)
	}
	m.mu.RUnlock()
}

func (m *ModelManager) GetModel(name string) (*modelActor, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	w, ok := m.modes[name]
	return w, ok
}

func (m *ModelManager) Unregister(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	model, ok := m.modes[name]
	if !ok {
		return fmt.Errorf("model.Manager.Unregister: %q not found", name)
	}
	model.Stop()
	delete(m.modes, name)
	for i, n := range m.order {
		if n == name {
			m.order = append(m.order[:i], m.order[i+1:]...)
			break
		}
	}
	return nil
}

func (m *ModelManager) RegisterHandler(id int32, name string, newProto func() message.Message, handle func(*session.Context, message.Message)) {
	m.handlersMu.Lock()
	defer m.handlersMu.Unlock()
	m.handlers[id] = &protoHandler{name: name, newProto: newProto, handle: handle}
}

func (m *ModelManager) IsLocalHandler(id int32) bool {
	m.handlersMu.RLock()
	defer m.handlersMu.RUnlock()
	_, ok := m.handlers[id]
	return ok
}

func (m *ModelManager) GetLocalHandlerIDs() []int32 {
	m.handlersMu.RLock()
	defer m.handlersMu.RUnlock()
	ids := make([]int32, 0, len(m.handlers))
	for id := range m.handlers {
		ids = append(ids, id)
	}
	return ids
}

func (m *ModelManager) Dispatch(sess session.Session, id int32, msg []byte) error {
	m.handlersMu.RLock()
	hand, ok := m.handlers[id]
	m.handlersMu.RUnlock()
	if !ok {
		return fmt.Errorf("handlers for %d not found", id)
	}

	md, ok := m.GetModel(hand.name)
	if !ok {
		return fmt.Errorf("model %s not found", hand.name)
	}

	pb := hand.newProto()
	if len(msg) > 0 {
		if err := proto.Unmarshal(msg, pb); err != nil {
			return fmt.Errorf("unmarshal message %d: %w", id, err)
		}
	}

	md.Post(func() {
		ctx := &session.Context{Context: context.Background(), Session: sess, MsgID: id}
		hand.handle(ctx, pb)
		metric.CounterOf("model_dispatch_total").Inc()
	})
	return nil
}

func (m *ModelManager) DispatchHTTP(mname string, w http.ResponseWriter, r *http.Request) {
	md, ok := m.GetModel(mname)
	if !ok {
		logx.Err.Printf("model %s not found", mname)
		http.NotFound(w, r)
		return
	}
	cb, ok := httpHandlers.Load(r.URL.Path)
	if !ok {
		logx.Err.Printf("http handler %s not found", r.URL.Path)
		http.NotFound(w, r)
		return
	}
	recorder := NewResponseRecorder()
	done := make(chan struct{})
	md.Post(func() {
		cb.(httpHandler)(recorder, r)
		close(done)
	})
	select {
	case <-done:
		recorder.WriteTo(w)
	case <-time.After(30 * time.Second):
		logx.Err.Printf("[ModelManager/DispatchHTTP] timeout waiting for model %s", mname)
		http.Error(w, "gateway timeout", http.StatusGatewayTimeout)
	}
}
