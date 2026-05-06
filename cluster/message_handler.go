package cluster

import (
	"errors"
	"fmt"
	"infra-foundation/clusterpb"
	"infra-foundation/logx"
	"infra-foundation/message"
	"infra-foundation/model"
	"infra-foundation/protocol"
	"infra-foundation/queue"
	"infra-foundation/scheduler"
	"infra-foundation/session"

	pbp "google.golang.org/protobuf/proto"
)

type MessageHandler struct {
	connMgr      *session.Manager
	modelManager *model.ModelManager
	scheduler    *scheduler.Scheduler
	taskQueue    *queue.TaskQueue
	node         *Node
}

func NewMessageHandler(
	connMgr *session.Manager,
	modelManager *model.ModelManager,
	scheduler *scheduler.Scheduler,
	msgQueue *queue.TaskQueue,
	node *Node,
) *MessageHandler {
	return &MessageHandler{connMgr: connMgr, modelManager: modelManager, scheduler: scheduler, taskQueue: msgQueue, node: node}
}

func (h *MessageHandler) OnMessage(sconn *PeerConn, typ protocol.ClusterType, id int32, sid string, data []byte) (err error) {
	switch typ {
	case protocol.ClusterHeartbeat:
	case protocol.ClusterHandshake:
		err = h.handleOnHandshake(sconn, data)
	case protocol.ClusterSessionDisconnect:
		err = h.handleOnSessionDisconnect(data)
	case protocol.ClusterSessionBind:
		err = h.handleOnSessionBind(data)
	case protocol.ClusterRequest:
		err = h.handleRequest(id, sid, data)
	case protocol.ClusterResponse:
		err = h.handleResponse(sid, id, data)
	case protocol.ClusterPush:
		err = h.handlePush(data)
	default:
		err = fmt.Errorf("unknown cluster type: %d", typ)
	}
	if err == nil {
		sconn.Conn.RefreshHeartbeat()
	}
	return
}

func (h *MessageHandler) handleOnHandshake(sconn *PeerConn, data []byte) error {
	var pb = &clusterpb.N2MOnHandshake{}
	if err := pbp.Unmarshal(data, pb); err != nil {
		return fmt.Errorf("unmarshal N2MOnHandshake: %w", err)
	}
	oldID := sconn.ID()
	if err := h.node.bindNodeConn(pb.ID, sconn); err != nil {
		return fmt.Errorf("bind node connection %s: %w", pb.ID, err)
	}
	if sconn.isOutbound {
		h.node.LoadBalancer().MarkHealthy(pb.ID, true)
	} else {
		session.DefaultIDPool.Remove(oldID)
		h.node.PeerMgr().RemoveByID(oldID)
		localNode := h.node.LocalNode()
		if localNode == nil {
			return fmt.Errorf("local node not set")
		}
		if err := sconn.SendTypePb(int8(protocol.ClusterHandshake), &clusterpb.N2MOnHandshake{
			ID:       localNode.Id,
			Name:     localNode.Name,
			Frontend: localNode.Frontend,
		}); err != nil {
			return fmt.Errorf("send conn ack to %s(%s): %w", pb.Name, pb.ID, err)
		}
	}
	logx.Inf.Printf("peer connection established: %s(%s)", pb.Name, pb.ID)
	return nil
}

func (h *MessageHandler) handleOnSessionDisconnect(data []byte) error {
	var pb clusterpb.N2MOnSessionDisconnected
	if err := pbp.Unmarshal(data, &pb); err != nil {
		return fmt.Errorf("unmarshal session close: %w", err)
	}
	conn, ok := h.connMgr.GetByID(session.SessionID(pb.SessionID))
	if !ok {
		logx.Dbg.Printf("[MessageHandler/handleDisconnect] session %s not found (already removed)", pb.SessionID)
		return nil
	}
	if _, isLocal := conn.(*ClientConn); isLocal {
		h.modelManager.OnDisconnection(conn)
	}
	return conn.Close()
}

func (h *MessageHandler) handleOnSessionBind(data []byte) error {
	var pb clusterpb.N2MOnSessionBind
	if err := pbp.Unmarshal(data, &pb); err != nil {
		return fmt.Errorf("unmarshal bind: %w", err)
	}
	conn, ok := h.connMgr.GetByID(session.SessionID(pb.SessionID))
	if !ok {
		conn = NewProxySession(session.NewSessionEntity(session.SessionID(pb.SessionID)), h.node, h.modelManager, h.connMgr, h.node.PeerMgr(), h)
		h.connMgr.Store(conn)
	}
	for name, id := range pb.GetServers() {
		conn.BindServers(name, id)
	}
	if pb.UID != "" && conn.UID() == "" {
		conn.BindUid(pb.UID)
		h.modelManager.OnSessionInitialization(conn)
	}
	return nil
}

func (h *MessageHandler) handleRequest(id int32, sid string, data []byte) error {
	if !h.modelManager.IsLocalHandler(id) {
		return fmt.Errorf("message %d not found", id)
	}
	conn, ok := h.connMgr.GetByID(session.SessionID(sid))
	if !ok {
		logx.Dbg.Printf("[MessageHandler/handleRequest] session %s not found (already removed)", sid)
		return nil
	}
	return h.modelManager.Dispatch(conn, id, data)
}

type clientSender interface {
	session.PacketSender
	ClientProtocol() protocol.ClientProtocol
}

func sendToClientConn(conn session.Session, msgID int32, data []byte) error {
	if cs, ok := conn.(clientSender); ok {
		pack := cs.ClientProtocol().PackPooled(msgID, data)
		return cs.SendData(pack)
	}
	return conn.SendData(data)
}

func (h *MessageHandler) handleResponse(sid string, id int32, data []byte) error {
	conn, ok := h.connMgr.GetByID(session.SessionID(sid))
	if !ok {
		logx.Dbg.Printf("[MessageHandler/handleResponse] session %s not found (already removed)", sid)
		return nil
	}
	return sendToClientConn(conn, id, data)
}

func (h *MessageHandler) sendPushToNode(codec *protocol.Codec, nodeID string, sessionIDs []string, payload []byte, msgID int32) error {
	notifyPB := &clusterpb.N2MOnPush{
		SessionID: sessionIDs,
		Plyload:   payload,
		MsgID:     msgID,
	}
	notifyDataBuf, err := pbp.Marshal(notifyPB)
	if err != nil {
		return err
	}
	conn, ok := h.node.PeerMgr().GetByID(session.SessionID(nodeID))
	if !ok {
		return fmt.Errorf("node %s not found", nodeID)
	}
	notifyData, err := codec.Pack(protocol.ClusterPush, 0, "", notifyDataBuf)
	if err != nil {
		return err
	}
	if err := conn.SendData(notifyData); err != nil {
		protocol.PutBuf(notifyData)
		return err
	}
	return nil
}

func (h *MessageHandler) SendPush(sess session.Session, codec *protocol.Codec, targets []session.Session, pb message.Message) error {
	pdata, err := pbp.Marshal(pb)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	var errs []error
	if len(targets) == 0 {
		nodes := h.node.GetNodes(pb.ServiceName())
		if len(nodes) == 0 {
			return fmt.Errorf("service %s not found", pb.ServiceName())
		}
		for _, node := range nodes {
			errs = append(errs, h.sendPushToNode(codec, node.Id, nil, pdata, pb.MessageID()))
		}
	} else {
		tempSession := make(map[string][]string)
		for _, sv := range targets {
			conn, err := h.node.GatewayBySession(sv)
			if err != nil {
				errs = append(errs, fmt.Errorf("gateway for %s: %w", sv.ID(), err))
				continue
			}
			tempSession[string(conn.ID())] = append(tempSession[string(conn.ID())], string(sv.ID()))
		}
		for connID, sessionIDs := range tempSession {
			errs = append(errs, h.sendPushToNode(codec, connID, sessionIDs, pdata, pb.MessageID()))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("notify: %w", errors.Join(errs...))
	}
	return nil
}

func (h *MessageHandler) handlePush(data []byte) error {
	var pb clusterpb.N2MOnPush
	if err := pbp.Unmarshal(data, &pb); err != nil {
		return fmt.Errorf("unmarshal N2MOnPush: %w", err)
	}

	isLocal := h.modelManager.IsLocalHandler(pb.MsgID)
	if !isLocal {
		localNode := h.node.LocalNode()
		if localNode == nil || !localNode.Frontend {
			return nil
		}
	}

	act := func(s session.Session) error {
		if isLocal {
			return h.modelManager.Dispatch(s, pb.MsgID, pb.Plyload)
		}
		return sendToClientConn(s, pb.MsgID, pb.Plyload)
	}

	var errs []error
	if len(pb.SessionID) == 0 {
		errs = append(errs, h.connMgr.Range(func(s session.Session) error { return act(s) }))
	} else {
		for _, sid := range pb.SessionID {
			conn, ok := h.connMgr.GetByID(session.SessionID(sid))
			if !ok {
				logx.Dbg.Printf("[MessageHandler/handlePush] session %s not found (already removed)", sid)
				continue
			}
			errs = append(errs, act(conn))
		}
	}
	return errors.Join(errs...)
}
