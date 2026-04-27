package cluster

import (
	"context"
	"fmt"
	"infra-foundation/logx"
	"infra-foundation/model"
	"infra-foundation/protocol"
	"infra-foundation/queue"
	"infra-foundation/scheduler"
	"infra-foundation/session"
	"infra-foundation/transport"

	"github.com/cloudwego/netpoll"
)

type clientContextKey struct{}

var clientCtxKey clientContextKey

type ClientHandler struct {
	connMgr        *session.Manager
	modelManager   *model.ModelManager
	scheduler      *scheduler.Scheduler
	taskQueue      *queue.TaskQueue
	node           *Node
	clientProtocol protocol.ClientProtocol
}

func NewClientHandler(svr Server) *ClientHandler {
	return &ClientHandler{
		connMgr:        svr.ConnMgr(),
		modelManager:   svr.ModelManager(),
		scheduler:      svr.Scheduler(),
		taskQueue:      svr.TaskQueue(),
		node:           svr.ClusterNode(),
		clientProtocol: protocol.NewClientCodec(),
	}
}

func (h *ClientHandler) OnPrepare(connection netpoll.Connection) context.Context {
	sid := session.DefaultIDPool.NextID("cl")
	remoteAddr := connection.RemoteAddr()
	localAddr := connection.LocalAddr()
	logx.Inf.Printf("[ClientHandler/OnPrepare] new client connection from %s to %s, sid=%s", remoteAddr, localAddr, sid)

	conn := transport.NewConn(connection, sid, -1)
	clientConn := NewClientConn(conn, h.clientProtocol, h.node)
	h.connMgr.Store(clientConn)
	return context.WithValue(context.Background(), clientCtxKey, clientConn)
}

func (h *ClientHandler) OnDisconnect(ctx context.Context, connection netpoll.Connection) {
	conn, ok := ctx.Value(clientCtxKey).(*ClientConn)
	if !ok {
		logx.War.Printf("[ClientHandler/OnDisconnect] connection from %s, no conn in context", connection.RemoteAddr())
		return
	}
	logx.Inf.Printf("[ClientHandler/OnDisconnect] client connection closed from %s, sid=%s", connection.RemoteAddr(), conn.ID())
	conn.closed.Store(true)
	h.modelManager.OnDisconnection(conn)
	h.connMgr.RemoveByID(conn.ID())
	session.DefaultIDPool.Remove(conn.ID())
	_ = conn.Conn.Close()
	h.node.broadcastSessionClose(conn)
}

func (h *ClientHandler) OnRequest(ctx context.Context, connection netpoll.Connection) error {
	conn, ok := ctx.Value(clientCtxKey).(*ClientConn)
	if !ok {
		logx.Err.Println("[ClientHandler/OnRequest] get conn from context failed")
		return fmt.Errorf("get conn from context failed")
	}

	msgID, payload, err := h.clientProtocol.NextPacket(connection.Reader())
	if err != nil {
		logx.Err.Printf("[ClientHandler/OnRequest] protocol error from %s: %v", conn.ID(), err)
		conn.Close()
		return fmt.Errorf("protocol error: %w", err)
	}
	if msgID == 0 && payload == nil {
		return nil
	}

	if err = h.taskQueue.Put(string(conn.ID()), func() {
		if err := h.handleClientMessage(conn, msgID, payload); err != nil {
			logx.Err.Println(err)
		}
	}); err != nil {
		logx.War.Printf("[ClientHandler/OnRequest] overload, closing conn %s: %v", conn.ID(), err)
		conn.Close()
		return err
	}
	return nil
}

func (h *ClientHandler) handleClientMessage(conn *ClientConn, msgID int32, data []byte) error {
	if h.modelManager.IsLocalHandler(msgID) {
		return h.modelManager.Dispatch(conn, msgID, data)
	}
	conn.Conn.RefreshHeartbeat()
	pack := protocol.NewWithSID(protocol.ClusterServiceCall, msgID, string(conn.ID()), data)
	return h.node.RemoteCallWithAgent(conn, conn.Conn.Codec, pack, h.node.serviceByRoute(msgID))
}
