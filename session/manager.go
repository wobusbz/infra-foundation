package session

import (
	"errors"
	"infra-foundation/metric"
	"sync"
)

type Identifiable interface {
	ID() SessionID
}

type Store[T Identifiable] interface {
	Store(s T)
	GetByID(id SessionID) (T, bool)
	RemoveByID(id SessionID)
	Range(fn func(s T) error) error
	Count() int
}

type Manager[T Identifiable] struct {
	byID map[SessionID]T
	mu   sync.RWMutex
}

func NewManager[T Identifiable]() *Manager[T] {
	return &Manager[T]{
		byID: map[SessionID]T{},
	}
}

func (m *Manager[T]) Store(s T) {
	id := s.ID()
	m.mu.Lock()
	m.byID[id] = s
	m.mu.Unlock()
	metric.CounterOf("sess_total").Inc()
	metric.GaugeOf("sess_active").Add(1)
}

func (m *Manager[T]) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.byID)
}

func (m *Manager[T]) GetByID(id SessionID) (T, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.byID[id]
	return s, ok
}

func (m *Manager[T]) RemoveByID(id SessionID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.byID[id]; !ok {
		return
	}
	delete(m.byID, id)
	metric.CounterOf("sess_closed").Inc()
	metric.GaugeOf("sess_active").Sub(1)
}

func (m *Manager[T]) Range(fn func(s T) error) error {
	m.mu.RLock()
	sess := make([]T, 0, len(m.byID))
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
