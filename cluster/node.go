package cluster

import (
	"infra-foundation/config"
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

func (n *Node) SetConnectionFactory(factory func(addr, id, name string, frontend bool) error) {
	n.peer.connectionFactory = factory
}

func (n *Node) ServiceRegistry() *ServiceRegistry {
	return n.router.registry
}

func (n *Node) LoadBalancer() *LoadBalancer {
	return n.router.loadBalancer
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

func (n *Node) GatewayBySession(s session.Session) (session.Session, error) {
	return n.router.sessionBinder.GetFrontendNode(s, n.router.registry)
}

func (n *Node) bindNodeConn(id string, conn session.Session) error {
	return n.router.bindNodeConn(id, conn)
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
	buf, err := p.Pack(pack.ClusterType(), pack.ID(), pack.SID(), pack.Data())
	if err != nil {
		return err
	}
	return agent.SendData(buf)
}
