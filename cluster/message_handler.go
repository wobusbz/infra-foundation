package cluster

import (
	"errors"
	"fmt"
	"infra-foundation/clusterpb"
	"infra-foundation/logx"
	"infra-foundation/model"
	"infra-foundation/protocol"
	"infra-foundation/queue"
	"infra-foundation/scheduler"
	"infra-foundation/session"

	"google.golang.org/protobuf/proto"
	pbp "google.golang.org/protobuf/proto"
)

type MessageHandler struct {
	ConnMgr      *session.Manager
	ModelManager *model.ModelManager
	Scheduler    *scheduler.Scheduler
	TaskQueue    *queue.TaskQueue
	Node         *Node
}

func NewMessageHandler(
	connMgr *session.Manager,
	modelManager *model.ModelManager,
	scheduler *scheduler.Scheduler,
	msgQueue *queue.TaskQueue,
	node *Node,
) *MessageHandler {
	return &MessageHandler{
		ConnMgr:      connMgr,
		ModelManager: modelManager,
		Scheduler:    scheduler,
		TaskQueue:    msgQueue,
		Node:         node,
	}
}

func (h *MessageHandler) OnMessage(sconn *InboundPeerConn, codec *protocol.Codec, typ protocol.ClusterType, id int32, sid string, data []byte) (err error) {
	switch typ {
	case protocol.ClusterHeartbeat:
	case protocol.ClusterRequest:
		if h.ModelManager.IsLocalHandler(id) {
			err = h.ModelManager.Dispatch(sconn, id, data)
		} else {
			pack := protocol.NewWithSID(protocol.ClusterServiceCall, id, string(sconn.ID()), data)
			err = h.Node.RemoteCallWithAgent(sconn, codec, pack, h.Node.serviceByRoute(id))
		}
	case protocol.ClusterHandshake:
		var pb = &clusterpb.N2MOnConnection{}
		if err := pbp.Unmarshal(data, pb); err != nil {
			return fmt.Errorf("unmarshal N2MOnConnection: %w", err)
		}
		h.ConnMgr.RemoveByID(sconn.ID())
		if err = h.Node.bindNodeConn(pb.ID, sconn); err != nil {
			return fmt.Errorf("bind node connection %s: %w", pb.ID, err)
		}
		localNode := h.Node.LocalNode()
		if localNode == nil {
			return fmt.Errorf("local node not set")
		}
		if err = sconn.SendTypePb(int8(protocol.ClusterHandshake), &clusterpb.M2NOnConnection{
			ID:       localNode.Id,
			Name:     localNode.Name,
			Frontend: localNode.Frontend,
		}); err != nil {
			return fmt.Errorf("send conn ack to %s(%s): %w", pb.Name, pb.ID, err)
		}
		logx.Inf.Printf("peer connection established: %s(%s)", pb.Name, pb.ID)
	case protocol.ClusterDisconnect:
		err = h.handleDisconnect(data)
	case protocol.ClusterBindSession:
		err = h.handleBindSession(data)
	case protocol.ClusterServiceCall:
		err = h.handleServiceCall(id, sid, data)
	case protocol.ClusterResponse:
		err = h.handleResponse(sid, id, data)
	case protocol.ClusterPush:
		err = h.handlePush(data)
	}

	if err == nil {
		sconn.Conn.RefreshHeartbeat()
	}
	return
}

func (h *MessageHandler) handleDisconnect(data []byte) error {
	var pb clusterpb.N2MOnSessionClose
	if err := proto.Unmarshal(data, &pb); err != nil {
		return fmt.Errorf("unmarshal session close: %w", err)
	}
	conn, ok := h.ConnMgr.GetByID(session.SessionID(pb.SessionID))
	if !ok {
		return fmt.Errorf("session %s not found", pb.SessionID)
	}
	return conn.Close()
}

func (h *MessageHandler) handleBindSession(data []byte) error {
	var pb clusterpb.N2MOnSessionBindServer
	if err := proto.Unmarshal(data, &pb); err != nil {
		return fmt.Errorf("unmarshal bind connection: %w", err)
	}
	conn, ok := h.ConnMgr.GetByID(session.SessionID(pb.SessionID))
	if !ok {
		conn = NewProxySession(session.NewSessionEntity(session.SessionID(pb.SessionID), pb.UID), h.Node, h.ModelManager, h.ConnMgr, h.Node.PeerMgr())
		h.ConnMgr.Store(conn)
	}
	for name, id := range pb.GetServers() {
		conn.BindServers(name, id)
	}
	return nil
}

func (h *MessageHandler) handleServiceCall(id int32, sid string, data []byte) error {
	if !h.ModelManager.IsLocalHandler(id) {
		return fmt.Errorf("message %d not found", id)
	}
	conn, ok := h.ConnMgr.GetByID(session.SessionID(sid))
	if !ok {
		return fmt.Errorf("session %s not found", sid)
	}
	return h.ModelManager.Dispatch(conn, id, data)
}

func (h *MessageHandler) handleResponse(sid string, id int32, data []byte) error {
	conn, ok := h.ConnMgr.GetByID(session.SessionID(sid))
	if !ok {
		return fmt.Errorf("session %s not found", sid)
	}
	if cc, ok := conn.(*ClientConn); ok {
		// Use pooled packing to avoid heap allocation in the hot path.
		// The pooled buffer will be recycled by transport.Conn.writeLoop.
		if codec, ok := cc.clientProtocol.(*protocol.ClientCodec); ok {
			pack := codec.PackPooled(id, data)
			return cc.Conn.SendData(pack)
		}
		pack := cc.clientProtocol.Pack(id, data)
		return cc.Conn.SendData(pack)
	}
	return conn.SendData(data)
}

func (h *MessageHandler) handlePush(data []byte) error {
	var pb clusterpb.N2MNotify
	if err := proto.Unmarshal(data, &pb); err != nil {
		return fmt.Errorf("unmarshal notify: %w", err)
	}

	if len(pb.SessionID) == 0 {
		return h.ConnMgr.Range(func(s session.Session) error { return s.SendData(pb.Plyload) })
	}

	var errs []error
	for _, sid := range pb.SessionID {
		conn, ok := h.ConnMgr.GetByID(session.SessionID(sid))
		if !ok {
			errs = append(errs, fmt.Errorf("session %s not found", sid))
			continue
		}
		errs = append(errs, conn.SendData(pb.Plyload))
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("notify: %w", err)
	}
	return nil
}
