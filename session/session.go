package session

import (
	protomessage "infra-foundation/protomessage"
	"maps"
	"sync"
	"sync/atomic"
)

var DefaultConnSession = defaultConnectionSession{ids: map[int64]struct{}{}}

type defaultConnectionSession struct {
	ids   map[int64]struct{}
	idsrw sync.RWMutex
}

func (d *defaultConnectionSession) Count() int64 {
	return int64(len(d.ids))
}

func (d *defaultConnectionSession) Remove(id int64) {
	d.idsrw.Lock()
	delete(d.ids, id)
	d.idsrw.Unlock()
}

func (d *defaultConnectionSession) Reset() {
	d.idsrw.Lock()
	d.ids = map[int64]struct{}{}
	d.idsrw.Unlock()
}

func (d *defaultConnectionSession) SessionID() int64 {
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

type HandlerFunc func(Session, protomessage.ProtoMessage)

type Session interface {
	ID() int64
	UID() int64
	BindID(id int64)
	BindUID(uid int64)
	GetServers(name string) string
	BindServers(name, id string)
	Servers() map[string]string
	Send(pb protomessage.ProtoMessage) error
	Notify(s []Session, pb protomessage.ProtoMessage) error
	Close() error
}

type NetworkEntities struct {
	Id        atomic.Int64
	Uid       atomic.Int64
	servers   map[string]string
	serversrw sync.RWMutex
}

func NewNetworkEntities(id, uid int64) *NetworkEntities {
	n := &NetworkEntities{servers: map[string]string{}}
	n.Uid.Store(uid)
	n.Id.Store(id)
	return n
}

func (n *NetworkEntities) ID() int64         { return n.Id.Load() }
func (n *NetworkEntities) UID() int64        { return n.Uid.Load() }
func (n *NetworkEntities) BindID(id int64)   { n.Id.Store(id) }
func (n *NetworkEntities) BindUID(uid int64) { n.Uid.Store(uid) }

func (n *NetworkEntities) GetServers(name string) string {
	n.serversrw.RLock()
	defer n.serversrw.RUnlock()
	return n.servers[name]
}
func (n *NetworkEntities) BindServers(name, id string) {
	n.serversrw.Lock()
	n.servers[name] = id
	n.serversrw.Unlock()
}

func (n *NetworkEntities) Servers() map[string]string {
	n.serversrw.RLock()
	defer n.serversrw.RUnlock()
	servers := make(map[string]string, len(n.servers))
	maps.Copy(servers, n.servers)
	return servers
}
