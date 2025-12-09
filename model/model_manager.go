package model

import (
	"errors"
	"fmt"
	protomessage "infra-foundation/protomessage"
	"infra-foundation/session"
	"sync"

	"google.golang.org/protobuf/proto"
)

type ModelManager struct {
	mu    sync.RWMutex
	modes map[string]*model
	order []string
}

func NewModelManager() *ModelManager {
	return &ModelManager{modes: map[string]*model{}}
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
	m.modes[name] = newModel(model)

	m.order = append(m.order, name)
	return nil
}

func (m *ModelManager) Stop() error {
	m.mu.Lock()
	for i := len(m.order) - 1; i >= 0; i-- {
		w, ok := m.modes[m.order[i]]
		if !ok {
			continue
		}
		w.Stop()
	}
	m.mu.Unlock()
	return nil
}

func (m *ModelManager) OnConnection(session session.Session) {
	m.mu.RLock()
	for range m.order {
		//m.modes[name].scheduler.PushTask(func() { m.modes[name].OnConnection(session) })
	}
	m.mu.RUnlock()
}

func (m *ModelManager) OnDisconnection(session session.Session) {
	m.mu.RLock()
	for _, name := range m.order {
		m.modes[name].scheduler.PushTask(func() { m.modes[name].OnDisconnection(session) })
	}
	m.mu.RUnlock()
}

func (m *ModelManager) GetModel(name string) (*model, bool) {
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

func (m *ModelManager) DispatchLocalAsync(session session.Session, id int32, msg []byte) error {
	value, ok := Handlers.Load(id)
	if !ok {
		return fmt.Errorf("[ModelManager/DispatchLocalAsync] %d handlers not found", id)
	}
	hand, ok := value.(*handler)
	if !ok {
		return errors.New("[ModelManager/DispatchLocalAsync] reflect *handler failed")
	}
	md, ok := m.GetModel(hand.name)
	if !ok {
		return fmt.Errorf("[ModelManager/DispatchLocalAsync] %s Model not found", hand.name)
	}
	var pb = proto.Clone(hand.pb).(protomessage.ProtoMessage)
	if len(msg) > 0 {
		if err := proto.Unmarshal(msg, pb); err != nil {
			return fmt.Errorf("[ModelManager/DispatchLocalAsync] %d protomessage Unmarshal failed %w", id, err)
		}
	}
	md.PostFunc(func() { hand.handle(session, pb) })
	return nil
}
