package cluster

import (
	"fmt"
	"infra-foundation/config"
	"infra-foundation/logx"
	"infra-foundation/session"
	"sync"
)

type PeerManager struct {
	mu                sync.RWMutex
	localNode         *NodeInfo
	peerMgr           *session.Manager
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

func (pm *PeerManager) connectToNode(targetNode *NodeInfo) error {
	if pm.connectionFactory == nil {
		logx.Dbg.Printf("[PeerManager] connectionFactory not set, skip connecting to %s (passive connection will work)", targetNode.Name)
		return nil
	}
	localNode := pm.getLocalNode()
	if localNode == nil {
		return fmt.Errorf("local node not set")
	}
	return pm.connectionFactory(targetNode.Addr, localNode.Id, localNode.Name, localNode.Frontend)
}

func (pm *PeerManager) shouldConnectTo(node *NodeInfo) bool {
	localNode := pm.getLocalNode()
	if localNode == nil {
		logx.Dbg.Printf("[PeerManager] shouldConnectTo: localNode is nil, skip")
		return false
	}
	if node.Id == localNode.Id {
		logx.Dbg.Printf("[PeerManager] shouldConnectTo: same ID %s, skip", node.Id)
		return false
	}
	switch pm.connectionPolicy {
	case config.ConnectPolicyNone:
		return false
	case config.ConnectPolicyAll:
		return true
	case config.ConnectPolicyFrontendToBackend:
		return localNode.Frontend && !node.Frontend
	case config.ConnectPolicyBackendToFrontend:
		return !localNode.Frontend && node.Frontend
	case config.ConnectPolicyByServicePriority:
		if localNode.Name == node.Name {
			return false
		}
		result := config.ShouldConnectByPriority(localNode.Name, localNode.Id, node.Name, node.Id)
		logx.Dbg.Printf("[PeerManager] shouldConnectTo: local(%s/%s) -> target(%s/%s) = %v", localNode.Name, localNode.Id, node.Name, node.Id, result)
		return result
	default:
		return true
	}
}

func (pm *PeerManager) decodeRegistryAndConnect(registry *ServiceRegistry, name string, sb []byte) error {
	existing := make(map[string]struct{})
	for _, node := range registry.GetNodes(name) {
		existing[node.Id] = struct{}{}
	}

	nodes, err := registry.Unmarshal(name, sb)
	if err != nil {
		return err
	}

	for _, node := range nodes {
		if _, ok := existing[node.Id]; ok {
			continue
		}
		if !pm.shouldConnectTo(node) {
			continue
		}
		if err := pm.connectToNode(node); err != nil {
			logx.Err.Printf("[PeerManager] connect to %s@%s failed: %v", node.Name, node.Addr, err)
			continue
		}
	}
	return nil
}
