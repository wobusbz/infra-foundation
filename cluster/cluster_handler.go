package cluster

import (
	"errors"
	"fmt"
	"infra-foundation/clusterpb"
	"infra-foundation/model"
	"infra-foundation/session"

	"google.golang.org/protobuf/proto"
)

type ClusterHandler struct {
	connMgr      *session.Manager
	modelManager *model.ModelManager
	node         *Node
}

func NewClusterHandler(connMgr *session.Manager, modelManager *model.ModelManager, node *Node) *ClusterHandler {
	return &ClusterHandler{
		connMgr:      connMgr,
		modelManager: modelManager,
		node:         node,
	}
}

func (h *ClusterHandler) handleDisconnect(data []byte) error {
	var pb clusterpb.N2MOnSessionClose
	if err := proto.Unmarshal(data, &pb); err != nil {
		return fmt.Errorf("unmarshal session close: %w", err)
	}
	conn, ok := h.connMgr.GetByID(session.SessionID(pb.SessionID))
	if !ok {
		return fmt.Errorf("session %s not found", pb.SessionID)
	}
	return conn.Close()
}

func (h *ClusterHandler) handleBindSession(data []byte) error {
	var pb clusterpb.N2MOnSessionBindServer
	if err := proto.Unmarshal(data, &pb); err != nil {
		return fmt.Errorf("unmarshal bind connection: %w", err)
	}
	conn, ok := h.connMgr.GetByID(session.SessionID(pb.SessionID))
	if !ok {
		conn = NewProxySession(session.NewSessionEntity(session.SessionID(pb.SessionID), pb.UID), h.node, h.modelManager, h.connMgr, h.node.PeerMgr())
		h.connMgr.Store(conn)
	}
	for name, id := range pb.GetServers() {
		conn.BindServers(name, id)
	}
	return nil
}

func (h *ClusterHandler) handleServiceCall(id int32, sid string, data []byte) error {
	if !h.modelManager.IsLocalHandler(id) {
		return fmt.Errorf("message %d not found", id)
	}
	conn, ok := h.connMgr.GetByID(session.SessionID(sid))
	if !ok {
		return fmt.Errorf("session %s not found", sid)
	}
	return h.modelManager.Dispatch(conn, id, data)
}

func (h *ClusterHandler) handleResponse(sid string, data []byte) error {
	conn, ok := h.connMgr.GetByID(session.SessionID(sid))
	if !ok {
		return fmt.Errorf("session %s not found", sid)
	}
	return conn.SendData(data)
}

func (h *ClusterHandler) handlePush(data []byte) error {
	var pb clusterpb.N2MNotify
	if err := proto.Unmarshal(data, &pb); err != nil {
		return fmt.Errorf("unmarshal notify: %w", err)
	}

	if len(pb.SessionID) == 0 {
		return h.connMgr.Range(func(s session.Session) error { return s.SendData(pb.Plyload) })
	}

	var errs []error
	for _, sid := range pb.SessionID {
		conn, ok := h.connMgr.GetByID(session.SessionID(sid))
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
