package cluster

import (
	"fmt"
	"infra-foundation/config"
	"infra-foundation/message"
	"infra-foundation/protocol"
	"infra-foundation/session"

	pbp "google.golang.org/protobuf/proto"
)

type Node struct {
	router *MessageRouter
	peer   *PeerManager
}

func newNode(peerMgr *session.Manager) *Node {
	registry := NewServiceRegistry()
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

	return &Node{router: router, peer: peer}
}

func (n *Node) SetConnectionFactory(factory func(addr, id, name string, frontend bool) error) {
	n.peer.SetConnectionFactory(factory)
}

func (n *Node) LoadBalancer() *LoadBalancer {
	return n.router.loadBalancer
}

func (n *Node) PeerMgr() *session.Manager {
	return n.peer.peerMgr
}

func (n *Node) LocalNode() *NodeInfo {
	return n.peer.getLocalNode()
}

func (n *Node) SetLocalNode(name, id, addr string, frontend bool, rids []int32) {
	node := &NodeInfo{Id: id, Name: name, Addr: addr, Frontend: frontend, Routes: rids}
	n.peer.setLocalNode(node)
	n.router.sessionBinder.SetLocalNode(node)
}

func (n *Node) GatewayBySession(s session.Session) (session.Session, error) {
	return n.router.sessionBinder.GetFrontendNode(s, n.router.registry)
}

func (n *Node) GetNodes(name string) []*NodeInfo {
	return n.router.registry.GetNodes(name)
}

func (n *Node) RemoveNode(name, id string) {
	if n.peer.peerMgr != nil {
		if conn, ok := n.peer.peerMgr.GetByID(session.SessionID(id)); ok {
			conn.Close()
		}
	}
	n.router.registry.RemoveNode(name, id)
}

func (n *Node) bindNodeConn(id string, conn session.Session) error {
	return n.router.bindNodeConn(id, conn)
}

func (n *Node) broadcastSessionBind(s session.Session) error {
	return n.router.broadcastSessionBind(s)
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

func (n *Node) forwardPkt(s session.Session, codec *protocol.Codec, pack *protocol.Pkt, nodeName string) error {
	defer pack.Free()
	localNode := n.peer.getLocalNode()
	var agent session.Session
	switch {
	case n.router.hasRoute(pack.ID()):
		a, err := n.router.nodeBySession(s, nodeName)
		if err != nil {
			return err
		}
		agent = a
	case localNode != nil && localNode.Frontend:
		if cs, ok := s.(interface {
			ClientProtocol() protocol.ClientProtocol
			SendData([]byte) error
		}); ok {
			return cs.SendData(cs.ClientProtocol().PackPooled(pack.ID(), pack.Data()))
		}
		return fmt.Errorf("no route for message %d", pack.ID())
	default:
		a, err := n.GatewayBySession(s)
		if err != nil {
			return err
		}
		agent = a
	}
	buf, err := codec.Pack(pack.ClusterType(), pack.ID(), pack.SID(), pack.Data())
	if err != nil {
		return err
	}
	if err := agent.SendData(buf); err != nil {
		protocol.PutBuf(buf)
		return err
	}
	return nil
}

func (n *Node) sendPkt(s session.Session, codec *protocol.Codec, sid string, pb message.Message, typ protocol.ClusterType, nodeName string) error {
	pbdata, err := pbp.Marshal(pb)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	pack := protocol.NewWithSID(typ, pb.MessageID(), sid, pbdata)
	return n.forwardPkt(s, codec, pack, nodeName)
}

func (n *Node) SendPb(s session.Session, codec *protocol.Codec, sid string, pb message.Message) error {
	return n.sendPkt(s, codec, sid, pb, protocol.ClusterRequest, pb.ServiceName())
}
