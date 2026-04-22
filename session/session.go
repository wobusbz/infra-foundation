package session

import (
	"context"
	"errors"
	"infra-foundation/message"
	"maps"
	"sync"
	"sync/atomic"
)

var DefaultIDPool = sessionIDPool{ids: map[SessionID]struct{}{}}

type sessionIDPool struct {
	nextID int64
	ids    map[SessionID]struct{}
	idsrw  sync.RWMutex
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

func (d *sessionIDPool) NextID(prefix string) SessionID {
	id := GenerateSessionID(prefix)
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

type TypePbSender interface {
	SendTypePb(typ int8, pb message.Message) error
}

type Messenger interface {
	Send(pb message.Message) error
	Notify(targets []Session, pb message.Message) error
	Close() error
}

// Session 接口定义
type Session interface {
	ID() SessionID
	UID() int64
	BindID(id SessionID)
	BindUID(uid int64)
	GetServers(name string) string
	BindServers(name, id string)
	Servers() map[string]string
	RangeServers(fn func(name, id string) bool)
	Send(pb message.Message) error
	Notify(targets []Session, pb message.Message) error
	Close() error
	PacketSender
	TypePbSender
}

type SessionBase struct {
	*SessionEntity
	Messenger Messenger
}

func (b *SessionBase) Send(pb message.Message) error {
	if b.Messenger == nil {
		return errors.New("session: messenger not set")
	}
	return b.Messenger.Send(pb)
}

func (b *SessionBase) Notify(targets []Session, pb message.Message) error {
	if b.Messenger == nil {
		return errors.New("session: messenger not set")
	}
	return b.Messenger.Notify(targets, pb)
}

func (b *SessionBase) Close() error {
	if b.Messenger == nil {
		return nil
	}
	return b.Messenger.Close()
}

func (b *SessionBase) SendData(data []byte) error {
	return errors.New("session: SendData not implemented")
}

func (b *SessionBase) SendTypePb(typ int8, pb message.Message) error {
	return errors.New("session: SendTypePb not implemented")
}

type SessionEntity struct {
	id         atomic.Value // SessionID
	uid        atomic.Int64
	isPeerConn atomic.Bool
	servers    map[string]string
	serversrw  sync.RWMutex
}

// NewSessionEntity 创建新的 SessionEntity
func NewSessionEntity(id SessionID, uid int64) *SessionEntity {
	n := &SessionEntity{servers: map[string]string{}}
	n.uid.Store(uid)
	n.id.Store(id)
	return n
}

func (n *SessionEntity) ID() SessionID {
	v := n.id.Load()
	if v == nil {
		return ""
	}
	return v.(SessionID)
}

func (n *SessionEntity) UID() int64           { return n.uid.Load() }
func (n *SessionEntity) BindID(id SessionID)    { n.id.Store(id) }
func (n *SessionEntity) BindUID(uid int64)      { n.uid.Store(uid) }
func (n *SessionEntity) IsPeerConn() bool       { return n.isPeerConn.Load() }
func (n *SessionEntity) SetPeerConn(v bool)     { n.isPeerConn.Store(v) }

func (n *SessionEntity) GetServers(name string) string {
	n.serversrw.RLock()
	defer n.serversrw.RUnlock()
	return n.servers[name]
}

func (n *SessionEntity) BindServers(name, id string) {
	n.serversrw.Lock()
	n.servers[name] = id
	n.serversrw.Unlock()
}

func (n *SessionEntity) Servers() map[string]string {
	n.serversrw.RLock()
	defer n.serversrw.RUnlock()
	if len(n.servers) == 0 {
		return nil
	}
	servers := make(map[string]string, len(n.servers))
	maps.Copy(servers, n.servers)
	return servers
}

func (n *SessionEntity) RangeServers(fn func(name, id string) bool) {
	n.serversrw.RLock()
	defer n.serversrw.RUnlock()
	for name, id := range n.servers {
		if !fn(name, id) {
			break
		}
	}
}
