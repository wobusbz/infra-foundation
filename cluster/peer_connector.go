package cluster

import (
	"fmt"
	"infra-foundation/logx"
	"infra-foundation/session"
	"strconv"
	"strings"
)

type PeerConnector struct {
	registry          *ServiceRegistry
	peerStore         PeerStore
	clientStore       ClientStore
	sessionBinder     *SessionBinder
	connectionFactory func(addr, id, name string, frontend bool) error
	localNode         func() *NodeInfo
}

func NewPeerConnector(
	registry *ServiceRegistry,
	peerStore PeerStore,
	clientStore ClientStore,
	sessionBinder *SessionBinder,
	connectionFactory func(addr, id, name string, frontend bool) error,
	localNode func() *NodeInfo,
) *PeerConnector {
	return &PeerConnector{
		registry:          registry,
		peerStore:         peerStore,
		clientStore:       clientStore,
		sessionBinder:     sessionBinder,
		connectionFactory: connectionFactory,
		localNode:         localNode,
	}
}

func (pc *PeerConnector) HandleDiscoveryEvent(ev DiscoveryEvent) {
	switch ev.Type {
	case NodeAdded, NodeUpdated:
		pc.handleNodeAdded(ev.Name, ev.Data)
	case NodeRemoved:
		pc.handleNodeRemoved(ev.Name, ev.ID)
	}
}

func (pc *PeerConnector) handleNodeAdded(name string, data []byte) error {
	existing := make(map[string]struct{})
	for _, node := range pc.registry.GetNodes(name) {
		existing[node.Id] = struct{}{}
	}

	nodes, err := pc.registry.Unmarshal(name, data)
	if err != nil {
		return err
	}

	for _, node := range nodes {
		if _, ok := existing[node.Id]; ok {
			continue
		}
		if !pc.shouldConnectTo(node) {
			continue
		}
		if err := pc.connectToNode(node); err != nil {
			logx.Err.Printf("[PeerConnector] connect to %s@%s failed: %v", node.Name, node.Addr, err)
			continue
		}
	}
	return nil
}

func (pc *PeerConnector) handleNodeRemoved(name, id string) {
	if conn, ok := pc.peerStore.GetByID(session.SessionID(id)); ok {
		_ = conn.Close()
	}
	if pc.clientStore != nil {
		pc.clientStore.Range(func(sess session.Session) error {
			if bs, ok := sess.(BoundSession); ok {
				bs.UnbindServers(name)
			}
			return nil
		})
	}
	pc.registry.RemoveNode(name, id)
}

func (pc *PeerConnector) connectToNode(targetNode *NodeInfo) error {
	if pc.connectionFactory == nil {
		logx.Dbg.Printf("[PeerConnector] connectionFactory not set, skip connecting to %s", targetNode.Name)
		return nil
	}
	localNode := pc.localNode()
	if localNode == nil {
		return fmt.Errorf("local node not set")
	}
	return pc.connectionFactory(targetNode.Addr, localNode.Id, localNode.Name, localNode.Frontend)
}

func (pc *PeerConnector) shouldConnectTo(node *NodeInfo) bool {
	localNode := pc.localNode()
	if localNode == nil {
		logx.Dbg.Printf("[PeerConnector] shouldConnectTo: localNode is nil, skip")
		return false
	}
	if node.Id == localNode.Id {
		logx.Dbg.Printf("[PeerConnector] shouldConnectTo: same ID %s, skip", node.Id)
		return false
	}
	if strings.EqualFold(localNode.Name, node.Name) {
		return false
	}
	localNumId, _ := strconv.ParseInt(localNode.Id, 10, 64)
	targetNumId, _ := strconv.ParseInt(node.Id, 10, 64)
	ok := localNumId < targetNumId
	logx.Dbg.Printf("[PeerConnector] shouldConnectTo: local(%s/%s) -> target(%s/%s) = %v", localNode.Name, localNode.Id, node.Name, node.Id, ok)
	return ok
}
