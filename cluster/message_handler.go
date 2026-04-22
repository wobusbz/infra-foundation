package cluster

import (
	"fmt"
	"infra-foundation/clusterpb"
	"infra-foundation/logx"
	"infra-foundation/model"
	"infra-foundation/protocol"
	"infra-foundation/queue"
	"infra-foundation/scheduler"
	"infra-foundation/session"

	pbp "google.golang.org/protobuf/proto"
)

type MessageHandler struct {
	ConnMgr        *session.Manager
	ModelManager   *model.ModelManager
	Scheduler      *scheduler.Scheduler
	TaskQueue      *queue.TaskQueue
	Node           *Node
	ClusterHandler *ClusterHandler
}

func NewMessageHandler(
	connMgr *session.Manager,
	modelManager *model.ModelManager,
	scheduler *scheduler.Scheduler,
	msgQueue *queue.TaskQueue,
	node *Node,
) *MessageHandler {
	return &MessageHandler{
		ConnMgr:        connMgr,
		ModelManager:   modelManager,
		Scheduler:      scheduler,
		TaskQueue:      msgQueue,
		Node:           node,
		ClusterHandler: NewClusterHandler(connMgr, modelManager, node),
	}
}

func (h *MessageHandler) OnMessage(sconn *InboundPeerConn, codec *protocol.Codec, typ protocol.Type, id int32, sid string, data []byte) (err error) {
	switch typ {
	case protocol.Heartbeat:
	case protocol.Request:
		if h.ModelManager.IsLocalHandler(id) {
			err = h.ModelManager.Dispatch(sconn, id, data)
		} else {
			pack := protocol.NewWithSID(protocol.ServiceCall, id, string(sconn.ID()), data)
			err = h.Node.RemoteCallWithAgent(sconn, codec, pack, h.Node.serviceByRoute(id))
		}
	case protocol.Handshake:
		var pb = &clusterpb.N2MOnConnection{}
		if err := pbp.Unmarshal(data, pb); err != nil {
			return fmt.Errorf("unmarshal N2MOnConnection: %w", err)
		}
		h.ConnMgr.RemoveByID(sconn.ID())
		h.Node.bindNodeConn(pb.ID, sconn)
		localNode := h.Node.LocalNode()
		if localNode == nil {
			return fmt.Errorf("local node not set")
		}
		if err = sconn.SendTypePb(int8(protocol.Handshake), &clusterpb.M2NOnConnection{
			ID:       localNode.Id,
			Name:     localNode.Name,
			Frontend: localNode.Frontend,
		}); err != nil {
			return fmt.Errorf("send conn ack to %s(%s): %w", pb.Name, pb.ID, err)
		}
		logx.Inf.Printf("peer connection established: %s(%s)", pb.Name, pb.ID)
	default:
		switch typ {
		case protocol.Disconnect:
			err = h.ClusterHandler.handleDisconnect(data)
		case protocol.BindSession:
			err = h.ClusterHandler.handleBindSession(data)
		case protocol.ServiceCall:
			err = h.ClusterHandler.handleServiceCall(id, sid, data)
		case protocol.Response:
			err = h.ClusterHandler.handleResponse(sid, data)
		case protocol.Push:
			err = h.ClusterHandler.handlePush(data)
		}
	}

	if err == nil {
		sconn.Conn.RefreshHeartbeat()
	}
	return
}
