package cluster

import (
	"fmt"
	"infra-foundation/logx"
	"infra-foundation/protocol"
	"infra-foundation/session"
)

type ClusterRequestHandler struct {
	connMgr    ClientStore
	dispatcher ModelDispatcher
	router     *MessageRouter
}

func NewClusterRequestHandler(connMgr ClientStore, dispatcher ModelDispatcher, router *MessageRouter) *ClusterRequestHandler {
	return &ClusterRequestHandler{
		connMgr:    connMgr,
		dispatcher: dispatcher,
		router:     router,
	}
}

func (h *ClusterRequestHandler) RegisterHandlers(r *clusterMsgRouter) {
	r.Register(protocol.ClusterRequest, h.HandleRequest)
}

func (h *ClusterRequestHandler) HandleRequest(pk *protocol.Pkt, peer *PeerConn) error {
	sess, ok := h.connMgr.GetByID(session.SessionID(pk.SID()))
	if !ok {
		logx.Dbg.Printf("[RequestHandler] session %s not found (already removed)", pk.SID())
		return nil
	}
	decision := h.router.Decide(pk.ID(), sess, DirClusterInbound)
	switch decision.Kind {
	case RouteLocalModel:
		return h.dispatcher.Dispatch(sess, pk.ID(), pk.Data())
	case RouteFrontendClient:
		return sendToClientConn(sess, pk.ID(), pk.Data())
	case RouteDrop:
		return fmt.Errorf("message %d not found", pk.ID())
	default:
		return fmt.Errorf("unexpected route kind %d for cluster request", decision.Kind)
	}
}
