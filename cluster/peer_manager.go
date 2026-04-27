package cluster

import (
	"fmt"
	"infra-foundation/config"
	"infra-foundation/logx"
	"infra-foundation/session"
)

type PeerManager struct {
	localNode         *NodeInfo
	peerMgr           *session.Manager
	connectionFactory func(addr, id, name string, frontend bool) error
	connectionPolicy  config.ConnectionPolicy
}

func (pm *PeerManager) connectToNode(targetNode *NodeInfo) error {
	if pm.connectionFactory == nil {
		logx.Dbg.Printf("[PeerManager] connectionFactory not set, skip connecting to %s (passive connection will work)", targetNode.Name)
		return nil
	}
	if pm.localNode == nil {
		return fmt.Errorf("local node not set")
	}
	return pm.connectionFactory(targetNode.Addr, pm.localNode.Id, pm.localNode.Name, targetNode.Frontend)
}

func (pm *PeerManager) shouldConnectTo(node *NodeInfo) bool {
	if pm.localNode == nil {
		logx.Dbg.Printf("[PeerManager] shouldConnectTo: localNode is nil, skip")
		return false
	}
	if node.Id == pm.localNode.Id {
		logx.Dbg.Printf("[PeerManager] shouldConnectTo: same ID %s, skip", node.Id)
		return false
	}
	switch pm.connectionPolicy {
	case config.ConnectPolicyNone:
		return false
	case config.ConnectPolicyAll:
		return true
	case config.ConnectPolicyFrontendToBackend:
		return pm.localNode.Frontend && !node.Frontend
	case config.ConnectPolicyBackendToFrontend:
		return !pm.localNode.Frontend && node.Frontend
	case config.ConnectPolicyByServicePriority:
		if pm.localNode.Name == node.Name {
			return false
		}
		result := config.ShouldConnectByPriority(pm.localNode.Name, pm.localNode.Id, node.Name, node.Id)
		logx.Dbg.Printf("[PeerManager] shouldConnectTo: local(%s/%s) -> target(%s/%s) = %v", pm.localNode.Name, pm.localNode.Id, node.Name, node.Id, result)
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
			return err
		}
	}
	return nil
}
