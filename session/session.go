package session

import (
	"context"
	"math/rand/v2"
	"sync"
	"sync/atomic"

	"infra-foundation/message"
)

const base62Chars = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

type SessionID string

func (t SessionID) String() string { return string(t) }

func GenerateSessionID() SessionID {
	var buf [16]byte
	for i := range buf {
		buf[i] = base62Chars[rand.IntN(62)]
	}
	return SessionID(string(buf[:]))
}

var DefaultIDPool = sessionIDPool{ids: map[SessionID]struct{}{}}

type sessionIDPool struct {
	ids   map[SessionID]struct{}
	idsrw sync.RWMutex
}

func (d *sessionIDPool) Count() int64 {
	d.idsrw.RLock()
	defer d.idsrw.RUnlock()
	return int64(len(d.ids))
}

func (d *sessionIDPool) Remove(id SessionID) {
	d.idsrw.Lock()
	delete(d.ids, id)
	d.idsrw.Unlock()
}

func (d *sessionIDPool) Reset() {
	d.idsrw.Lock()
	d.ids = map[SessionID]struct{}{}
	d.idsrw.Unlock()
}

func (d *sessionIDPool) NextID() SessionID {
	id := GenerateSessionID()
	d.idsrw.Lock()
	d.ids[id] = struct{}{}
	d.idsrw.Unlock()
	return id
}

type Context struct {
	context.Context
	Session Session
	MsgID   int32
}

type HandlerFunc func(*Context, message.Message)

type PacketSender interface {
	SendData(data []byte) error
}

type Identity interface {
	ID() SessionID
	BindID(id SessionID)
	UID() string
	BindUid(uid string) error
}

type AppSender interface {
	Send(pb message.Message) error
}

type Notifier interface {
	Notify(targets []Session, pb message.Message) error
}

type Closer interface {
	Close() error
}

type Session interface {
	Identity
	AppSender
	Notifier
	Closer
	PacketSender
}

type SessionState int32

const (
	SessionCreated SessionState = iota
	SessionUIDBound
	SessionClosing
	SessionClosed
)

type SessionCore struct {
	id    atomic.Value
	uid   atomic.Value
	state atomic.Int32
}

func NewSessionCore(id SessionID) *SessionCore {
	s := &SessionCore{}
	s.id.Store(id)
	return s
}

func (s *SessionCore) ID() SessionID {
	v := s.id.Load()
	if v == nil {
		return ""
	}
	return v.(SessionID)
}

func (s *SessionCore) BindID(id SessionID) { s.id.Store(id) }

func (s *SessionCore) BindUid(uid string) error { s.uid.Store(uid); return nil }

func (s *SessionCore) UID() string {
	v := s.uid.Load()
	if v == nil {
		return ""
	}
	return v.(string)
}

func (s *SessionCore) State() SessionState {
	return SessionState(s.state.Load())
}

func (s *SessionCore) TransitionToUIDBound() bool {
	state := s.state.Load()
	if state != int32(SessionCreated) {
		return false
	}
	return s.state.CompareAndSwap(int32(SessionCreated), int32(SessionUIDBound))
}

func (s *SessionCore) TransitionToClosing() bool {
	state := s.state.Load()
	if state == int32(SessionClosing) || state == int32(SessionClosed) {
		return false
	}
	return s.state.CompareAndSwap(state, int32(SessionClosing))
}

func (s *SessionCore) MarkClosed() {
	s.state.Store(int32(SessionClosed))
}
