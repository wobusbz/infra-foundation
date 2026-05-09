package cluster

import (
	"errors"
	"fmt"
	"infra-foundation/clusterpb"
	"infra-foundation/logx"
	"infra-foundation/message"
	"infra-foundation/protocol"
	"infra-foundation/session"

	pbp "google.golang.org/protobuf/proto"
)

type ClusterPushBroker struct {
	connMgr    ClientStore
	peerMgr    PeerStore
	node       *Node
	dispatcher ModelDispatcher
	codec      *protocol.ClusterCodec
}

func NewClusterPushBroker(connMgr ClientStore, peerMgr PeerStore, node *Node, dispatcher ModelDispatcher) *ClusterPushBroker {
	return &ClusterPushBroker{
		connMgr:    connMgr,
		peerMgr:    peerMgr,
		node:       node,
		dispatcher: dispatcher,
		codec:      protocol.NewClusterCodec(),
	}
}

func (b *ClusterPushBroker) RegisterHandlers(r *clusterMsgRouter) {
	r.Register(protocol.ClusterPush, b.HandlePush)
}

func (b *ClusterPushBroker) SendPush(targets []session.Session, pb message.Message) error {
	pdata, err := pbp.Marshal(pb)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	var errs []error
	if len(targets) == 0 {
		nodes := b.node.GetNodes(pb.ServiceName())
		if len(nodes) == 0 {
			return fmt.Errorf("service %s not found", pb.ServiceName())
		}
		for _, node := range nodes {
			errs = append(errs, b.sendPushToNode(node.Id, nil, pdata, pb.MessageID()))
		}
	} else {
		tempSession := make(map[string][]string)
		for _, sv := range targets {
			bs, ok := sv.(BoundSession)
			if !ok {
				errs = append(errs, fmt.Errorf("session %s does not support server binding", sv.ID()))
				continue
			}
			conn, err := b.node.GatewayBySession(bs)
			if err != nil {
				errs = append(errs, fmt.Errorf("gateway for %s: %w", sv.ID(), err))
				continue
			}
			tempSession[string(conn.ID())] = append(tempSession[string(conn.ID())], string(sv.ID()))
		}
		for connID, sessionIDs := range tempSession {
			errs = append(errs, b.sendPushToNode(connID, sessionIDs, pdata, pb.MessageID()))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("notify: %w", errors.Join(errs...))
	}
	return nil
}

func (b *ClusterPushBroker) sendPushToNode(nodeID string, sessionIDs []string, payload []byte, msgID int32) error {
	notifyPB := &clusterpb.N2MOnPush{
		SessionID: sessionIDs,
		Plyload:   payload,
		MsgID:     msgID,
	}
	notifyDataBuf, err := pbp.Marshal(notifyPB)
	if err != nil {
		return err
	}
	peer, ok := b.peerMgr.GetByID(session.SessionID(nodeID))
	if !ok {
		return fmt.Errorf("node %s not found", nodeID)
	}
	notifyData, err := b.codec.Pack(protocol.ClusterPush, 0, 0, "", notifyDataBuf)
	if err != nil {
		return err
	}
	if err := peer.SendData(notifyData); err != nil {
		protocol.PutBuf(notifyData)
		return err
	}
	return nil
}

func (b *ClusterPushBroker) HandlePush(pk *protocol.Pkt, peer *PeerConn) error {
	var pb clusterpb.N2MOnPush
	if err := pbp.Unmarshal(pk.Data(), &pb); err != nil {
		return fmt.Errorf("unmarshal N2MOnPush: %w", err)
	}

	act := func(s session.Session) error {
		decision := b.node.router.Decide(pb.MsgID, s, DirClusterInbound)
		switch decision.Kind {
		case RouteLocalModel:
			return b.dispatcher.Dispatch(s, pb.MsgID, pb.Plyload)
		case RouteFrontendClient:
			return sendToClientConn(s, pb.MsgID, pb.Plyload)
		case RouteDrop:
			return nil
		default:
			return fmt.Errorf("unexpected route kind %d for cluster push", decision.Kind)
		}
	}

	var errs []error
	if len(pb.SessionID) == 0 {
		errs = append(errs, b.connMgr.Range(func(sess session.Session) error {
			return act(sess)
		}))
	} else {
		for _, sid := range pb.SessionID {
			sess, ok := b.connMgr.GetByID(session.SessionID(sid))
			if !ok {
				logx.Dbg.Printf("[PushBroker/handlePush] session %s not found (already removed)", sid)
				continue
			}
			errs = append(errs, act(sess))
		}
	}
	return errors.Join(errs...)
}
