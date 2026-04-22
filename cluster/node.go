package cluster

import (
	"errors"
	"fmt"
	"infra-foundation/clusterpb"
	"infra-foundation/config"

	"infra-foundation/logx"
	"infra-foundation/model"
	"infra-foundation/protocol"
	"infra-foundation/queue"
	"infra-foundation/scheduler"
	"infra-foundation/session"
)

type Router interface {
	RemoteCallWithAgent(s session.Session, p *protocol.Codec, pack *protocol.Pkt, nodeName string) error
	GatewayBySession(s session.Session) (session.Session, error)
}

var _ Router = (*Node)(nil)

type MessageRouter struct {
	registry      *ServiceRegistry
	loadBalancer  *LoadBalancer
	sessionBinder *SessionBinder
}

type PeerManager struct {
	localNode         *NodeInfo
	peerMgr           *session.Manager
	connectionFactory func(addr, id, name string, frontend bool) error
	connectionPolicy  config.ConnectionPolicy
}

type Node struct {
	router *MessageRouter
	peer   *PeerManager

	connMgr      *session.Manager
	modelManager *model.ModelManager
	scheduler    *scheduler.Scheduler
	msgQueue     *queue.TaskQueue
}

func newNode(connMgr *session.Manager, modelManager *model.ModelManager, scheduler *scheduler.Scheduler, msgQueue *queue.TaskQueue) *Node {
	registry := NewServiceRegistry()
	peerMgr := session.NewManager()
	sessionBinder := NewSessionBinder(peerMgr)

	router := &MessageRouter{
		registry:      registry,
		loadBalancer:  NewLoadBalancer(registry),
		sessionBinder: sessionBinder,
	}

	peer := &PeerManager{
		peerMgr:          peerMgr,
		connectionPolicy: config.Default.ConnectionPolicy,
	}

	return &Node{
		router:       router,
		peer:         peer,
		connMgr:      connMgr,
		modelManager: modelManager,
		scheduler:    scheduler,
		msgQueue:     msgQueue,
	}
}

func (n *Node) GetConnMgr() *session.Manager {
	return n.connMgr
}

func (n *Node) GetModelManager() *model.ModelManager {
	return n.modelManager
}

func (n *Node) GetScheduler() *scheduler.Scheduler {
	return n.scheduler
}

func (n *Node) GetMsgQueue() *queue.TaskQueue {
	return n.msgQueue
}

func (n *Node) SetConnectionFactory(factory func(addr, id, name string, frontend bool) error) {
	n.peer.connectionFactory = factory
}

func (n *Node) ServiceRegistry() *ServiceRegistry {
	return n.router.registry
}

func (n *Node) LoadBalancer() *LoadBalancer {
	return n.router.loadBalancer
}

func (n *Node) SessionBinder() *SessionBinder {
	return n.router.sessionBinder
}

func (n *Node) PeerMgr() *session.Manager {
	return n.peer.peerMgr
}

func (n *Node) LocalNode() *NodeInfo {
	return n.peer.localNode
}

func (n *Node) SetLocalNode(name, id, addr string, frontend bool, rids []int32) {
	node := &NodeInfo{Id: id, Name: name, Addr: addr, Frontend: frontend, Routes: rids}
	n.peer.localNode = node
	n.router.sessionBinder.SetLocalNode(node)
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

func (mr *MessageRouter) nodeBySession(s session.Session, name string) (session.Session, error) {
	nodeID := s.GetServers(name)
	if nodeID == "" {
		return mr.selectNode(name, s)
	}
	return mr.sessionBinder.GetNodeByName(s, name)
}

func (mr *MessageRouter) broadcastSessionClose(s session.Session) error {
	var errs []error
	s.RangeServers(func(name, nodeID string) bool {
		if mr.sessionBinder.localNode != nil && mr.sessionBinder.localNode.Name == name {
			return true
		}
		conn, ok := mr.sessionBinder.GetNodeConnection(nodeID)
		if !ok {
			return true
		}
		errs = append(errs, conn.SendTypePb(int8(protocol.Disconnect), &clusterpb.N2MOnSessionClose{SessionID: string(s.ID())}))
		return true
	})
	return errors.Join(errs...)
}

func (n *Node) GatewayBySession(s session.Session) (session.Session, error) {
	return n.router.sessionBinder.GetFrontendNode(s, n.router.registry)
}

func (mr *MessageRouter) selectNode(name string, s session.Session) (session.Session, error) {
	node, err := mr.loadBalancer.Pick(name)
	if err != nil {
		return nil, err
	}

	if err = mr.sessionBinder.BindSessionToNode(s, node); err != nil {
		return nil, err
	}

	conn, ok := mr.sessionBinder.GetNodeConnection(node.Id)
	if !ok {
		return nil, fmt.Errorf("node %s connection not found", node.Id)
	}
	return conn, nil
}

func (mr *MessageRouter) bindNodeConn(id string, conn session.Session) {
	_ = mr.sessionBinder.StoreNodeConnection(id, conn)
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

func (mr *MessageRouter) serviceByRoute(id int32) string {
	return mr.registry.GetServiceByRoute(id)
}

func (mr *MessageRouter) hasRoute(id int32) bool {
	return mr.registry.HasRoute(id)
}

func (n *Node) selectNode(name string, s session.Session) (session.Session, error) {
	return n.router.selectNode(name, s)
}

func (n *Node) connectToNode(targetNode *NodeInfo) error {
	return n.peer.connectToNode(targetNode)
}

func (n *Node) shouldConnectTo(node *NodeInfo) bool {
	return n.peer.shouldConnectTo(node)
}

func (n *Node) bindNodeConn(id string, conn session.Session) {
	n.router.bindNodeConn(id, conn)
}

func (n *Node) broadcastSessionClose(s session.Session) error {
	return n.router.broadcastSessionClose(s)
}

func (n *Node) decodeRegistryAndConnect(name string, sb []byte) error {
	return n.peer.decodeRegistryAndConnect(n.router.registry, name, sb)
}

func (n *Node) serviceByRoute(id int32) string {
	return n.router.serviceByRoute(id)
}

func (n *Node) hasRoute(id int32) bool {
	return n.router.hasRoute(id)
}

func (n *Node) nodeBySession(s session.Session, name string) (session.Session, error) {
	return n.router.nodeBySession(s, name)
}

func (n *Node) RemoteCallWithAgent(s session.Session, p *protocol.Codec, pack *protocol.Pkt, nodeName string) error {
	var (
		agent session.Session
		err   error
	)
	defer pack.Free()
	switch {
	case n.router.hasRoute(pack.ID()):
		agent, err = n.router.nodeBySession(s, nodeName)
		if err != nil {
			return err
		}
	case n.peer.localNode != nil && n.peer.localNode.Frontend:
		agent = s
	default:
		agent, err = n.GatewayBySession(s)
		if err != nil {
			return err
		}
	}
	buf, err := p.Pack(pack.Type(), pack.ID(), pack.SID(), pack.Data())
	if err != nil {
		return err
	}
	return agent.SendData(buf)
}
