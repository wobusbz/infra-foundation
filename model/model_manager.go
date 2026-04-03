package model

import (
	"context"
	"errors"
	"fmt"
	"infra-foundation/logx"
	"infra-foundation/metrics"
	protomessage "infra-foundation/protomessage"
	"infra-foundation/session"
	"net/http"
	"sync"

	"google.golang.org/protobuf/proto"
)

type ModelManager struct {
	mu    sync.RWMutex
	modes map[string]*modelActor
	order []string
}

func NewModelManager() *ModelManager {
	return &ModelManager{modes: map[string]*modelActor{}}
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

func (m *ModelManager) OnDisconnection(session session.Session) {
	m.mu.RLock()
	for _, name := range m.order {
		md, ok := m.modes[name]
		if !ok {
			continue
		}
		md.OnDisconnection(session)
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

func (m *ModelManager) Dispatch(sess session.Session, id int32, msg []byte) error {
	handlersMu.RLock()
	hand, ok := handlers[id]
	handlersMu.RUnlock()
	if !ok {
		return fmt.Errorf("[ModelManager/Dispatch] %d handlers not found", id)
	}

	md := hand.model
	if md == nil {
		var ok bool
		md, ok = m.GetModel(hand.name)
		if !ok {
			return fmt.Errorf("[ModelManager/Dispatch] %s Model not found", hand.name)
		}
		hand.model = md
	}

	var pb protomessage.ProtoMessage
	if len(msg) > 0 {
		pb = hand.pbPool.Get().(protomessage.ProtoMessage)
		if err := proto.Unmarshal(msg, pb); err != nil {
			hand.Put(pb)
			return fmt.Errorf("[ModelManager/Dispatch] %d protomessage Unmarshal failed %w", id, err)
		}
	}

	md.Post(func() {
		ctx := &session.Context{
			Context: context.Background(),
			Session: sess,
			MsgID:   id,
		}
		hand.handle(ctx, pb)
		hand.Put(pb)
		metrics.CounterOf("model_dispatch_total").Inc()
	})
	return nil
}

func (m *ModelManager) DispatchHTTP(mname string, w http.ResponseWriter, r *http.Request) {
	md, ok := m.GetModel(mname)
	if !ok {
		logx.Err.Println(fmt.Errorf("[ModelManager/DispatchHTTP] %s Model not found", mname))
		http.NotFound(w, r)
		return
	}
	cb, ok := httpHandlers.Load(r.URL.Path)
	if !ok {
		logx.Err.Println(fmt.Errorf("[ModelManager/DispatchHTTP] %s Http Handler not found", r.URL.Path))
		http.NotFound(w, r)
		return
	}
	recorder := NewResponseRecorder()
	done := make(chan struct{})
	md.Post(func() {
		cb.(httpHandlr)(recorder, r)
		close(done)
	})
	<-done
	recorder.WriteTo(w)
}
