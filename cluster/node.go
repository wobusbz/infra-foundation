package cluster

import (
	"errors"
	"fmt"
	"infra-foundation/clusterpb"
	"infra-foundation/config"
	"infra-foundation/connmanager"
	"infra-foundation/logx"
	"infra-foundation/packet"
	"infra-foundation/protomessage"
	"infra-foundation/session"
)

type Node struct {
	ctx               ServerContext
	localNode         *NodeInfo
	registry          *ServiceRegistry
	loadBalancer      *LoadBalancer
	sessionBinder     *SessionBinder
	nodeConnManager   *connmanager.SessionManager
	connectionFactory func(addr, id, name string, frontend bool) error
	connectionPolicy  config.ConnectionPolicy
}

func newNode() *Node {
	registry := NewServiceRegistry()
	nodeConnManager := connmanager.NewSessionManager()
	sessionBinder := NewSessionBinder(nodeConnManager)
	return &Node{
		registry:         registry,
		loadBalancer:     NewLoadBalancer(registry),
		sessionBinder:    sessionBinder,
		nodeConnManager:  nodeConnManager,
		connectionPolicy: config.Default.ConnectionPolicy,
	}
}

func (n *Node) bindServer(ctx ServerContext) {
	n.ctx = ctx
}

func (n *Node) SetConnectionFactory(factory func(addr, id, name string, frontend bool) error) {
	n.connectionFactory = factory
}

func (n *Node) ServiceRegistry() *ServiceRegistry {
	return n.registry
}

func (n *Node) LoadBalancer() *LoadBalancer {
	return n.loadBalancer
}

func (n *Node) SessionBinder() *SessionBinder {
	return n.sessionBinder
}

func (n *Node) NodeConnManager() *connmanager.SessionManager {
	return n.nodeConnManager
}

func (n *Node) LocalNode() *NodeInfo {
	return n.localNode
}

func (n *Node) SetLocalNode(name, id, addr string, frontend bool) {
	n.localNode = &NodeInfo{Id: id, Name: name, Addr: addr, Frontend: frontend}
	n.sessionBinder.SetLocalNode(n.localNode)
}

func (n *Node) connectToNode(targetNode *NodeInfo) error {
	if n.connectionFactory == nil {
		logx.Dbg.Printf("[Node] connectionFactory not set, skip connecting to %s (passive connection will work)", targetNode.Name)
		return nil
	}
	if n.localNode == nil {
		return fmt.Errorf("[Node] local node not set")
	}
	return n.connectionFactory(targetNode.Addr, n.localNode.Id, n.localNode.Name, targetNode.Frontend)
}

func (n *Node) nodeBySession(s session.Session, name string) (session.Session, error) {
	nodeID := s.GetServers(name)
	if nodeID == "" {
		return n.selectNode(name, s)
	}
	return n.sessionBinder.GetNodeByName(s, name)
}

func (n *Node) broadcastSessionClose(s session.Session) error {
	var errs []error
	for name, ids := range s.Servers() {
		if n.localNode != nil && n.localNode.Name == name {
			continue
		}

		conn, ok := n.sessionBinder.GetNodeConnection(ids)
		if !ok {
			continue
		}
		senderConn, ok := conn.(interface {
			SendTypePb(typ packet.Type, pb protomessage.ProtoMessage) error
		})
		if !ok {
			continue
		}
		errs = append(errs, senderConn.SendTypePb(packet.DisConnection, &clusterpb.N2MOnSessionClose{SessionID: s.ID()}))
	}
	return errors.Join(errs...)
}

func (n *Node) gatewayBySession(s session.Session) (session.Session, error) {
	return n.sessionBinder.GetGateNode(s, n.registry)
}

func (n *Node) selectNode(name string, s session.Session) (session.Session, error) {
	node, err := n.loadBalancer.Pick(name)
	if err != nil {
		return nil, err
	}

	if err = n.sessionBinder.BindSessionToNode(s, node); err != nil {
		return nil, err
	}

	conn, ok := n.sessionBinder.GetNodeConnection(node.Id)
	if !ok {
		return nil, fmt.Errorf("[Node] node %s connection not found", node.Id)
	}
	return conn, nil
}

func (n *Node) bindNodeConn(id string, conn session.Session) {
	_ = n.sessionBinder.StoreNodeConnection(id, conn)
}

func (n *Node) encodeRegistry(name, id, addr string, frontend bool, rids []int32) (string, error) {
	n.registry.AddNode(name, id, addr, frontend, rids)
	return n.registry.Marshal(name)
}

func (n *Node) shouldConnectTo(node *NodeInfo) bool {
	if n.localNode == nil {
		return false
	}
	if node.Id == n.localNode.Id {
		return false
	}
	switch n.connectionPolicy {
	case config.ConnectPolicyNone:
		return false
	case config.ConnectPolicyAll:
		return true
	case config.ConnectPolicyFrontendToBackend:
		return n.localNode.Frontend && !node.Frontend
	case config.ConnectPolicyBackendToFrontend:
		return !n.localNode.Frontend && node.Frontend
	default:
		return true
	}
}

func (n *Node) decodeRegistry(name string, sb []byte) error {
	existing := make(map[string]struct{})
	for _, node := range n.registry.GetNodes(name) {
		existing[node.Id] = struct{}{}
	}

	nodes, err := n.registry.Unmarshal(name, sb)
	if err != nil {
		return err
	}

	for _, node := range nodes {
		if _, ok := existing[node.Id]; ok {
			continue
		}
		if !n.shouldConnectTo(node) {
			continue
		}
		if err := n.connectToNode(node); err != nil {
			return err
		}
	}
	return nil
}

func (n *Node) serviceByRoute(id int32) string {
	return n.registry.GetServiceByRoute(id)
}

func (n *Node) hasRoute(id int32) bool {
	return n.registry.HasRoute(id)
}
