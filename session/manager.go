package session

import (
	"errors"
	"infra-foundation/metric"
	"sync"
)

// Manager manages sessions
type Manager struct {
	byID  map[SessionID]Session
	byUID map[int64]SessionID
	mu    sync.RWMutex
}

// NewManager creates a new session manager
func NewManager() *Manager {
	return &Manager{
		byID:  map[SessionID]Session{},
		byUID: map[int64]SessionID{},
	}
}

// Store stores a session
func (m *Manager) Store(s Session) {
	m.mu.Lock()
	m.byID[s.ID()] = s
	if s.UID() >= 0 {
		m.byUID[s.UID()] = s.ID()
	}
	m.mu.Unlock()
	metric.CounterOf("sess_total").Inc()
	metric.GaugeOf("sess_active").Add(1)
}

// Count returns session count
func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.byID)
}

// GetByUID gets session by user ID
func (m *Manager) GetByUID(uid int64) (Session, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	id, ok := m.byUID[uid]
	if !ok {
		return nil, false
	}
	s, ok := m.byID[id]
	return s, ok
}

// GetByID gets session by session ID
func (m *Manager) GetByID(id SessionID) (Session, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.byID[id]
	return s, ok
}

// RemoveByID removes session by ID
func (m *Manager) RemoveByID(id SessionID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.byID[id]
	if !ok {
		return
	}
	delete(m.byID, id)
	delete(m.byUID, s.UID())
	metric.CounterOf("sess_closed").Inc()
	metric.GaugeOf("sess_active").Sub(1)
}

// RemoveByUID removes session by user ID
func (m *Manager) RemoveByUID(uid int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.byUID[uid]
	if !ok {
		return
	}
	delete(m.byID, id)
	delete(m.byUID, uid)
}

// Range iterates over all sessions
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
