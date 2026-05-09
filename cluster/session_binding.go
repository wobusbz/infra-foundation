package cluster

import (
	"maps"
	"sync"

	"infra-foundation/session"
)

type ServerBinding interface {
	GetServers(name string) string
	BindServers(name, id string)
	UnbindServers(name string)
	Servers() map[string]string
	RangeServers(fn func(name, id string) bool)
}

type BoundSession interface {
	session.Session
	ServerBinding
}

type ClusterSessionBase struct {
	*session.SessionCore
	servers   map[string]string
	serversrw sync.RWMutex
}

func NewClusterSessionBase(id session.SessionID) *ClusterSessionBase {
	return &ClusterSessionBase{
		SessionCore: session.NewSessionCore(id),
		servers:     map[string]string{},
	}
}

func (c *ClusterSessionBase) GetServers(name string) string {
	c.serversrw.RLock()
	defer c.serversrw.RUnlock()
	return c.servers[name]
}

func (c *ClusterSessionBase) BindServers(name, id string) {
	c.serversrw.Lock()
	c.servers[name] = id
	c.serversrw.Unlock()
}

func (c *ClusterSessionBase) UnbindServers(name string) {
	c.serversrw.Lock()
	delete(c.servers, name)
	c.serversrw.Unlock()
}

func (c *ClusterSessionBase) Servers() map[string]string {
	c.serversrw.RLock()
	defer c.serversrw.RUnlock()
	if len(c.servers) == 0 {
		return nil
	}
	servers := make(map[string]string, len(c.servers))
	maps.Copy(servers, c.servers)
	return servers
}

func (c *ClusterSessionBase) RangeServers(fn func(name, id string) bool) {
	c.serversrw.RLock()
	defer c.serversrw.RUnlock()
	for name, id := range c.servers {
		if !fn(name, id) {
			break
		}
	}
}
