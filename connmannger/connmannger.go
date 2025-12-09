package connmannger

import (
	"errors"
	"infra-foundation/session"
	"sync"
)

type ConnManager struct {
	idToSession  map[int64]session.Session
	uidToSession map[int64]int64
	m            sync.RWMutex
}

func NewConnManager() *ConnManager {
	return &ConnManager{idToSession: map[int64]session.Session{}, uidToSession: map[int64]int64{}}
}

func (c *ConnManager) StoreSession(s session.Session) {
	c.m.Lock()
	c.idToSession[s.ID()] = s
	c.uidToSession[s.UID()] = s.ID()
	c.m.Unlock()
}

func (c *ConnManager) Count() int { return len(c.idToSession) }

func (c *ConnManager) GetByUID(uid int64) (session.Session, bool) {
	c.m.RLock()
	defer c.m.RUnlock()
	id, ok := c.uidToSession[uid]
	if !ok {
		return nil, false
	}
	s, ok := c.idToSession[id]
	return s, ok
}

func (c *ConnManager) GetByID(id int64) (session.Session, bool) {
	c.m.RLock()
	defer c.m.RUnlock()
	s, ok := c.idToSession[id]
	return s, ok
}

func (c *ConnManager) RemoveByID(id int64) {
	c.m.Lock()
	defer c.m.Unlock()
	s, ok := c.idToSession[id]
	if !ok {
		return
	}
	delete(c.idToSession, id)
	delete(c.uidToSession, s.UID())
}

func (c *ConnManager) RemoveByUID(uid int64) {
	c.m.Lock()
	defer c.m.Unlock()
	id, ok := c.uidToSession[uid]
	if !ok {
		return
	}
	delete(c.idToSession, id)
	delete(c.uidToSession, uid)
}

func (c *ConnManager) Range(cb func(s session.Session) error) error {
	c.m.RLock()
	idToSession := c.idToSession
	c.m.RUnlock()
	var errs []error
	for _, conn := range idToSession {
		errs = append(errs, cb(conn))
	}
	return errors.Join(errs...)
}
