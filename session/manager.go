package session

import (
	"errors"
	"infra-foundation/metric"
	"sync"
)

type Manager struct {
	byID map[SessionID]Session
	mu   sync.RWMutex
}

func NewManager() *Manager {
	return &Manager{
		byID: map[SessionID]Session{},
	}
}

func (m *Manager) Store(s Session) {
	m.mu.Lock()
	m.byID[s.ID()] = s
	m.mu.Unlock()
	metric.CounterOf("sess_total").Inc()
	metric.GaugeOf("sess_active").Add(1)
}

func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.byID)
}

func (m *Manager) GetByID(id SessionID) (Session, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.byID[id]
	return s, ok
}

func (m *Manager) RemoveByID(id SessionID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.byID[id]; !ok {
		return
	}
	delete(m.byID, id)
	metric.CounterOf("sess_closed").Inc()
	metric.GaugeOf("sess_active").Sub(1)
}

func (m *Manager) Range(fn func(s Session) error) error {
	m.mu.RLock()
	sess := make([]Session, 0, len(m.byID))
	for _, s := range m.byID {
		sess = append(sess, s)
	}
	m.mu.RUnlock()
	var errs []error
	for _, s := range sess {
		errs = append(errs, fn(s))
	}
	return errors.Join(errs...)
}
