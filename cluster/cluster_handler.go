package cluster

import (
	"context"
	"fmt"
	"infra-foundation/logx"
	"infra-foundation/queue"
	"infra-foundation/session"

	"github.com/cloudwego/netpoll"
)

type connContextKey struct{}

var connCtxKey connContextKey

type ClusterHandler struct {
	ctrl          *ClusterCtrl
	peerMgr       PeerStore
	taskQueue     *queue.TaskQueue
	scheduler     TaskExecutor
	peerLifecycle *PeerLifecycle
	router        *clusterMsgRouter
}

func NewClusterHandler(
	clientMgr ClientStore,
	peerMgr PeerStore,
	dispatcher ModelDispatcher,
	executor TaskExecutor,
	taskQueue *queue.TaskQueue,
	node *Node,
	peerLifecycle *PeerLifecycle,
) *ClusterHandler {
	requestHandler := NewClusterRequestHandler(clientMgr, dispatcher, node.router)
	responseHandler := NewClusterResponseHandler(clientMgr)
	pushBroker := NewClusterPushBroker(clientMgr, peerMgr, node, dispatcher)
	ctrl := NewClusterCtrl(clientMgr, peerMgr, dispatcher, node, pushBroker)

	router := newClusterMsgRouter()
	ctrl.RegisterHandlers(router)
	requestHandler.RegisterHandlers(router)
	responseHandler.RegisterHandlers(router)
	pushBroker.RegisterHandlers(router)

	return &ClusterHandler{
		ctrl:          ctrl,
		peerMgr:       peerMgr,
		taskQueue:     taskQueue,
		scheduler:     executor,
		peerLifecycle: peerLifecycle,
		router:        router,
	}
}

func (h *ClusterHandler) OnPrepare(connection netpoll.Connection) context.Context {
	sid := session.DefaultIDPool.NextID()
	remoteAddr := connection.RemoteAddr()
	localAddr := connection.LocalAddr()
	logx.Inf.Printf("[ClusterHandler/OnPrepare] new connection from %s to %s, sid=%s", remoteAddr, localAddr, sid)
	return context.WithValue(context.Background(), connCtxKey, NewPeerConn(h.peerLifecycle, connection, sid))
}

func (h *ClusterHandler) OnDisconnect(ctx context.Context, connection netpoll.Connection) {
	conn, ok := ctx.Value(connCtxKey).(*PeerConn)
	if !ok {
		logx.War.Printf("[ClusterHandler/OnDisconnect] connection from %s, no conn in context (closed before OnPrepare?)", connection.RemoteAddr())
		return
	}
	logx.Inf.Printf("[ClusterHandler/OnDisconnect] connection closed from %s, sid=%s", connection.RemoteAddr(), conn.ID())
	conn.Close()
}

func (h *ClusterHandler) OnRequest(ctx context.Context, connection netpoll.Connection) error {
	sconn, ok := ctx.Value(connCtxKey).(*PeerConn)
	if !ok {
		logx.Err.Println("[ClusterHandler/OnRequest] get conn from context failed")
		return fmt.Errorf("get conn from context failed")
	}
	return processClusterMessage(sconn, h.router, h.taskQueue, connection, "ClusterHandler")
}

func (h *ClusterHandler) Router() *clusterMsgRouter { return h.router }
