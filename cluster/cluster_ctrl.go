package cluster

import (
	"fmt"
	"infra-foundation/clusterpb"
	"infra-foundation/logx"
	"infra-foundation/protocol"
	"infra-foundation/session"

	pbp "google.golang.org/protobuf/proto"
)

type ClusterCtrl struct {
	connMgr    ClientStore
	peerMgr    PeerStore
	dispatcher ModelDispatcher
	node       *Node
	pusher     PushBroker
}

func NewClusterCtrl(connMgr ClientStore, peerMgr PeerStore, dispatcher ModelDispatcher, node *Node, pusher PushBroker) *ClusterCtrl {
	return &ClusterCtrl{connMgr: connMgr, peerMgr: peerMgr, dispatcher: dispatcher, node: node, pusher: pusher}
}

func (cc *ClusterCtrl) HandleHandshake(pk *protocol.Pkt, peer *PeerConn) error {
	var pb = &clusterpb.N2MOnHandshake{}
	if err := pbp.Unmarshal(pk.Data(), pb); err != nil {
		return fmt.Errorf("unmarshal N2MOnHandshake: %w", err)
	}
	oldID := peer.ID()
	if err := cc.node.bindNodeConn(pb.ID, peer); err != nil {
		return fmt.Errorf("bind node connection %s: %w", pb.ID, err)
	}
	if peer.isOutbound {
		cc.node.LoadBalancer().MarkHealthy(pb.ID, true)
	} else {
		peer.lifecycle.OnHandshake(oldID)
		localNode := cc.node.LocalNode()
		if localNode == nil {
			return fmt.Errorf("local node not set")
		}
		if err := peer.SendTypePb(int8(protocol.ClusterHandshake), &clusterpb.N2MOnHandshake{
			ID:       localNode.Id,
			Name:     localNode.Name,
			Frontend: localNode.Frontend,
		}); err != nil {
			return fmt.Errorf("send conn ack to %s(%s): %w", pb.Name, pb.ID, err)
		}
		cc.syncSessionsToPeer(peer)
	}
	logx.Inf.Printf("peer connection established: %s(%s)", pb.Name, pb.ID)
	return nil
}

func (cc *ClusterCtrl) syncSessionsToPeer(peer *PeerConn) {
	cc.connMgr.Range(func(sess session.Session) error {
		bs, ok := sess.(BoundSession)
		if !ok || bs.UID() == "" {
			return nil
		}
		pb := &clusterpb.N2MOnSessionBind{SessionID: string(bs.ID()), UID: bs.UID(), Servers: bs.Servers()}
		if err := peer.SendTypePb(int8(protocol.ClusterSessionBind), pb); err != nil {
			logx.War.Printf("[ClusterCtrl/syncSessions] failed to sync session %s to peer %s: %v", sess.ID(), peer.ID(), err)
		}
		return nil
	})
}

func (cc *ClusterCtrl) HandleSessionDisconnect(pk *protocol.Pkt, peer *PeerConn) error {
	var pb clusterpb.N2MOnSessionDisconnected
	if err := pbp.Unmarshal(pk.Data(), &pb); err != nil {
		return fmt.Errorf("unmarshal session close: %w", err)
	}
	sess, ok := cc.connMgr.GetByID(session.SessionID(pb.SessionID))
	if !ok {
		logx.Dbg.Printf("[ClusterCtrl/handleDisconnect] session %s not found (already removed)", pb.SessionID)
		return nil
	}
	return sess.Close()
}

func (cc *ClusterCtrl) HandleSessionBind(pk *protocol.Pkt, peer *PeerConn) error {
	var pb clusterpb.N2MOnSessionBind
	if err := pbp.Unmarshal(pk.Data(), &pb); err != nil {
		return fmt.Errorf("unmarshal bind: %w", err)
	}
	sess, ok := cc.connMgr.GetByID(session.SessionID(pb.SessionID))
	if !ok {
		sess = NewProxySession(
			session.NewSessionCore(session.SessionID(pb.SessionID)),
			cc.node,
			cc.pusher,
			cc.dispatcher,
			cc.connMgr,
		)
	}
	sb, ok := sess.(ServerBinding)
	if !ok {
		return fmt.Errorf("session %s does not support server binding", sess.ID())
	}
	for name, id := range pb.GetServers() {
		sb.BindServers(name, id)
	}
	if pb.UID != "" && sess.UID() == "" {
		sess.BindUid(pb.UID)
		if ps, ok := sess.(*ProxySession); ok {
			if ps.TransitionToUIDBound() {
				cc.dispatcher.OnSessionInitialization(ps)
			}
		}
	}
	return nil
}

func (cc *ClusterCtrl) RegisterHandlers(r *clusterMsgRouter) {
	r.Register(protocol.ClusterHandshake, cc.HandleHandshake)
	r.Register(protocol.ClusterSessionDisconnect, cc.HandleSessionDisconnect)
	r.Register(protocol.ClusterSessionBind, cc.HandleSessionBind)
}
