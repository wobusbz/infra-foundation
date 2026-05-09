package cluster

import (
	"infra-foundation/logx"
	"infra-foundation/protocol"
	"infra-foundation/session"
)

type ClusterResponseHandler struct {
	connMgr ClientStore
}

func NewClusterResponseHandler(connMgr ClientStore) *ClusterResponseHandler {
	return &ClusterResponseHandler{connMgr: connMgr}
}

func (h *ClusterResponseHandler) RegisterHandlers(r *clusterMsgRouter) {
	r.Register(protocol.ClusterResponse, h.HandleResponse)
}

func (h *ClusterResponseHandler) HandleResponse(pk *protocol.Pkt, peer *PeerConn) error {
	sess, ok := h.connMgr.GetByID(session.SessionID(pk.SID()))
	if !ok {
		logx.Dbg.Printf("[ResponseHandler] session %s not found (already removed)", pk.SID())
		return nil
	}
	return sendToClientConn(sess, pk.ID(), pk.Data())
}
