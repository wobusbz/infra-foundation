package session

import (
	"context"
	"errors"
	protomessage "infra-foundation/protomessage"
	"maps"
	"sync"
	"sync/atomic"
)

var DefaultConnSession = sessionIDPool{ids: map[int64]struct{}{}}

type sessionIDPool struct {
	ids   map[int64]struct{}
	idsrw sync.RWMutex
}

func (d *sessionIDPool) Count() int64 {
	return int64(len(d.ids))
}

func (d *sessionIDPool) Remove(id int64) {
	d.idsrw.Lock()
	delete(d.ids, id)
	d.idsrw.Unlock()
}

func (d *sessionIDPool) Reset() {
	d.idsrw.Lock()
	d.ids = map[int64]struct{}{}
	d.idsrw.Unlock()
}

func (d *sessionIDPool) NextID() int64 {
	d.idsrw.Lock()
	defer d.idsrw.Unlock()
	var id int64 = 1
	for {
		if _, ok := d.ids[id]; !ok {
			break
		}
		id++
	}
	d.ids[id] = struct{}{}
	return id
}

type Context struct {
	context.Context
	Session Session
	MsgID   int32
}

type HandlerFunc func(*Context, protomessage.ProtoMessage)

// Messenger 负责消息的实际投递与连接关闭，可与 Session 身份解耦。
type Messenger interface {
	Send(pb protomessage.ProtoMessage) error
	Notify(targets []Session, pb protomessage.ProtoMessage) error
	Close() error
}

// Session 代表一个可寻址的会话实体，既可以是活跃的网络连接，也可以是离线代理。
type Session interface {
	ID() int64
	UID() int64
	BindID(id int64)
	BindUID(uid int64)
	GetServers(name string) string
	BindServers(name, id string)
	Servers() map[string]string
	Send(pb protomessage.ProtoMessage) error
	Notify(targets []Session, pb protomessage.ProtoMessage) error
	Close() error
}

// Base 提供了 Session 的通用实现，将 Messenger 委托给注入的实现。
// 这消除了 NetPollConnection/ClientConnection/TCPClient/acceptor 中重复的 Send/Notify/Close 代码。
type Base struct {
	*SessionEntity
	Messenger Messenger
}

func (b *Base) Send(pb protomessage.ProtoMessage) error {
	if b.Messenger == nil {
		return errors.New("session: messenger not set")
	}
	return b.Messenger.Send(pb)
}

func (b *Base) Notify(targets []Session, pb protomessage.ProtoMessage) error {
	if b.Messenger == nil {
		return errors.New("session: messenger not set")
	}
	return b.Messenger.Notify(targets, pb)
}

func (b *Base) Close() error {
	if b.Messenger == nil {
		return nil
	}
	return b.Messenger.Close()
}

type SessionEntity struct {
	Id        atomic.Int64
	Uid       atomic.Int64
	servers   map[string]string
	serversrw sync.RWMutex
}

func NewSessionEntity(id, uid int64) *SessionEntity {
	n := &SessionEntity{servers: map[string]string{}}
	n.Uid.Store(uid)
	n.Id.Store(id)
	return n
}

func (n *SessionEntity) ID() int64         { return n.Id.Load() }
func (n *SessionEntity) UID() int64        { return n.Uid.Load() }
func (n *SessionEntity) BindID(id int64)   { n.Id.Store(id) }
func (n *SessionEntity) BindUID(uid int64) { n.Uid.Store(uid) }

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
	servers := make(map[string]string, len(n.servers))
	maps.Copy(servers, n.servers)
	return servers
}
