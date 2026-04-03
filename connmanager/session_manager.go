package connmanager

import (
	"errors"
	"infra-foundation/metrics"
	"infra-foundation/session"
	"sync"
)

type SessionManager struct {
	idToSession  map[int64]session.Session
	uidToSession map[int64]int64
	m            sync.RWMutex
}

func NewSessionManager() *SessionManager {
	return &SessionManager{idToSession: map[int64]session.Session{}, uidToSession: map[int64]int64{}}
}

func (c *SessionManager) StoreSession(s session.Session) {
	c.m.Lock()
	c.idToSession[s.ID()] = s
	c.uidToSession[s.UID()] = s.ID()
	c.m.Unlock()
	metrics.CounterOf("conn_total").Inc()
	metrics.GaugeOf("conn_active").Add(1)
}

func (c *SessionManager) Count() int {
	c.m.RLock()
	defer c.m.RUnlock()
	return len(c.idToSession)
}

func (c *SessionManager) GetByUID(uid int64) (session.Session, bool) {
	c.m.RLock()
	defer c.m.RUnlock()
	id, ok := c.uidToSession[uid]
	if !ok {
		return nil, false
	}
	s, ok := c.idToSession[id]
	return s, ok
}

func (c *SessionManager) GetByID(id int64) (session.Session, bool) {
	c.m.RLock()
	defer c.m.RUnlock()
	s, ok := c.idToSession[id]
	return s, ok
}

func (c *SessionManager) RemoveByID(id int64) {
	c.m.Lock()
	defer c.m.Unlock()
	s, ok := c.idToSession[id]
	if !ok {
		return
	}
	delete(c.idToSession, id)
	delete(c.uidToSession, s.UID())
	session.DefaultConnSession.Remove(id)
	metrics.CounterOf("conn_closed_total").Inc()
	metrics.GaugeOf("conn_active").Sub(1)
}

func (c *SessionManager) RemoveByUID(uid int64) {
	c.m.Lock()
	defer c.m.Unlock()
	id, ok := c.uidToSession[uid]
	if !ok {
		return
	}
	delete(c.idToSession, id)
	delete(c.uidToSession, uid)
	session.DefaultConnSession.Remove(id)
}

func (c *SessionManager) Range(cb func(s session.Session) error) error {
	c.m.RLock()
	conns := make([]session.Session, 0, len(c.idToSession))
	for _, s := range c.idToSession {
		conns = append(conns, s)
	}
	c.m.RUnlock()
	var errs []error
	for _, conn := range conns {
		errs = append(errs, cb(conn))
	}
	return errors.Join(errs...)
}
