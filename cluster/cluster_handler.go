package cluster

import (
	"context"
	"fmt"
	"infra-foundation/logx"
	"infra-foundation/model"
	"infra-foundation/queue"
	"infra-foundation/scheduler"
	"infra-foundation/session"

	"github.com/cloudwego/netpoll"
)

type connContextKey struct{}

var connCtxKey connContextKey

type ClusterHandler struct {
	*MessageHandler
	peerMgr *session.Manager
}

func NewClusterHandler(clientMgr, peerMgr *session.Manager, modelManager *model.ModelManager, scheduler *scheduler.Scheduler, taskQueue *queue.TaskQueue, node *Node) *ClusterHandler {
	msgHandler := NewMessageHandler(clientMgr, modelManager, scheduler, taskQueue, node)
	return &ClusterHandler{MessageHandler: msgHandler, peerMgr: peerMgr}
}

func (s *ClusterHandler) OnPrepare(connection netpoll.Connection) context.Context {
	sid := session.DefaultIDPool.NextID()
	remoteAddr := connection.RemoteAddr()
	localAddr := connection.LocalAddr()
	logx.Inf.Printf("[ClusterHandler/OnPrepare] new connection from %s to %s, sid=%s", remoteAddr, localAddr, sid)
	return context.WithValue(context.Background(), connCtxKey, NewPeerConn(s.node, s.peerMgr, s.scheduler, connection, sid))
}

func (s *ClusterHandler) OnDisconnect(ctx context.Context, connection netpoll.Connection) {
	conn, ok := ctx.Value(connCtxKey).(*PeerConn)
	if !ok {
		logx.War.Printf("[ClusterHandler/OnDisconnect] connection from %s, no conn in context (closed before OnPrepare?)", connection.RemoteAddr())
		return
	}
	logx.Inf.Printf("[ClusterHandler/OnDisconnect] connection closed from %s, sid=%s", connection.RemoteAddr(), conn.ID())
	conn.Close()
}

func (s *ClusterHandler) OnRequest(ctx context.Context, connection netpoll.Connection) error {
	sconn, ok := ctx.Value(connCtxKey).(*PeerConn)
	if !ok {
		logx.Err.Println("[ClusterHandler/OnRequest] get conn from context failed")
		return fmt.Errorf("get conn from context failed")
	}
	r2, err := sconn.codec.NextPacket(connection.Reader())
	if err != nil {
		logx.Err.Printf("[ClusterHandler/OnRequest] NextPacket error %v", err)
		return fmt.Errorf("next packet: %w", err)
	}
	if r2 == nil {
		return nil
	}
	if err = s.taskQueue.Put(sconn.queueKey, func() {
		pk, err := sconn.codec.Unpack(r2)
		if err != nil {
			logx.Err.Printf("[ClusterHandler/OnRequest] Unpack error %v", err)
			return
		}
		if err = s.OnMessage(sconn, pk.ClusterType(), pk.ID(), pk.SID(), pk.Data()); err != nil {
			logx.Err.Println(err)
		}
		pk.Free()
	}); err != nil {
		logx.War.Printf("[ClusterHandler/OnRequest] overload, closing conn %s: %v", sconn.ID(), err)
		sconn.Close()
		return err
	}
	return nil
}
