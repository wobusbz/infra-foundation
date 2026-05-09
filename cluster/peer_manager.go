package cluster

import (
	"infra-foundation/config"
	"sync"
)

type PeerManager struct {
	mu                sync.RWMutex
	localNode         *NodeInfo
	peerMgr           PeerStore
	connectionFactory func(addr, id, name string, frontend bool) error
	connectionPolicy  config.ConnectionPolicy
}

func (pm *PeerManager) SetConnectionFactory(factory func(addr, id, name string, frontend bool) error) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.connectionFactory = factory
}

func (pm *PeerManager) getLocalNode() *NodeInfo {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	if pm.localNode == nil {
		return nil
	}
	return pm.localNode.Clone()
}

func (pm *PeerManager) setLocalNode(node *NodeInfo) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.localNode = node
}
