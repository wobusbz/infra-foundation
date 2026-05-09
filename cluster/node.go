package cluster

import (
	"infra-foundation/config"
	"infra-foundation/message"
	"infra-foundation/protocol"
	"infra-foundation/session"
)

type Node struct {
	router    *MessageRouter
	peer      *PeerManager
	forwarder *ClusterForwarder
	clientMgr ClientStore
}

func newNode(clientMgr ClientStore, peerMgr PeerStore, dispatcher ModelDispatcher) *Node {
	registry := NewServiceRegistry()
	sessionBinder := NewSessionBinder(peerMgr)

	router := &MessageRouter{
		registry:      registry,
		loadBalancer:  NewLoadBalancer(registry),
		sessionBinder: sessionBinder,
		dispatcher:    dispatcher,
	}

	peer := &PeerManager{
		peerMgr:          peerMgr,
		connectionPolicy: config.Default.ConnectionPolicy,
	}

	n := &Node{router: router, peer: peer, clientMgr: clientMgr}
	n.forwarder = newClusterForwarder(router, n.isLocalFrontend)
	return n
}

func (n *Node) SetConnectionFactory(factory func(addr, id, name string, frontend bool) error) {
	n.peer.SetConnectionFactory(factory)
}

func (n *Node) LoadBalancer() *LoadBalancer {
	return n.router.loadBalancer
}

func (n *Node) LocalNode() *NodeInfo {
	return n.peer.getLocalNode()
}

func (n *Node) SetLocalNode(name, id, addr string, frontend bool, rids []int32) {
	node := &NodeInfo{Id: id, Name: name, Addr: addr, Frontend: frontend, Routes: rids}
	n.peer.setLocalNode(node)
	n.router.sessionBinder.SetLocalNode(node)
}

func (n *Node) GatewayBySession(s BoundSession) (NodeConn, error) {
	return n.router.sessionBinder.GetFrontendNode(s, n.router.registry)
}

func (n *Node) GetNodes(name string) []*NodeInfo {
	return n.router.registry.GetNodes(name)
}

func (n *Node) bindNodeConn(id string, conn NodeConn) error {
	return n.router.bindNodeConn(id, conn)
}

func (n *Node) BroadcastSessionBind(s BoundSession) error {
	return n.router.broadcastSessionBind(s)
}

func (n *Node) BroadcastSessionClose(s BoundSession) error {
	return n.router.broadcastSessionClose(s)
}

func (n *Node) ForwardPkt(s session.Identity, codec *protocol.ClusterCodec, pack *protocol.Pkt, nodeName string) error {
	return n.forwarder.ForwardPkt(s, codec, pack, nodeName)
}

func (n *Node) SendPb(s session.Identity, codec *protocol.ClusterCodec, sid string, pb message.Message) error {
	return n.forwarder.SendPb(s, codec, sid, pb)
}

func (n *Node) isLocalFrontend() bool {
	localNode := n.peer.getLocalNode()
	return localNode != nil && localNode.Frontend
}
