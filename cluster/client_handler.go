package cluster

import (
	"context"
	"fmt"
	"infra-foundation/logx"
	"infra-foundation/model"
	"infra-foundation/protocol"
	"infra-foundation/queue"
	"infra-foundation/session"
	"infra-foundation/transport"

	"github.com/cloudwego/netpoll"
)

type clientContextKey struct{}

var clientCtxKey clientContextKey

type ClientHandler struct {
	connMgr        *session.Manager
	modelManager   *model.ModelManager
	taskQueue      *queue.TaskQueue
	node           *Node
	codec          *protocol.Codec
	clientProtocol protocol.ClientProtocol
}

func NewClientHandler(connMgr *session.Manager, modelManager *model.ModelManager, taskQueue *queue.TaskQueue, node *Node) *ClientHandler {
	return &ClientHandler{
		connMgr:        connMgr,
		modelManager:   modelManager,
		taskQueue:      taskQueue,
		node:           node,
		codec:          protocol.NewCodec(),
		clientProtocol: protocol.NewClientCodec(),
	}
}

func (h *ClientHandler) OnPrepare(connection netpoll.Connection) context.Context {
	sid := session.DefaultIDPool.NextID()
	remoteAddr := connection.RemoteAddr()
	localAddr := connection.LocalAddr()
	logx.Inf.Printf("[ClientHandler/OnPrepare] new client connection from %s to %s, sid=%s", remoteAddr, localAddr, sid)

	conn := transport.NewConn(connection, sid)
	clientConn := NewClientConn(conn, h.clientProtocol, h.codec, h.node, h.connMgr, h.modelManager)
	return context.WithValue(context.Background(), clientCtxKey, clientConn)
}

func (h *ClientHandler) OnDisconnect(ctx context.Context, connection netpoll.Connection) {
	conn, ok := ctx.Value(clientCtxKey).(*ClientConn)
	if !ok {
		logx.War.Printf("[ClientHandler/OnDisconnect] connection from %s, no conn in context", connection.RemoteAddr())
		return
	}
	logx.Inf.Printf("[ClientHandler/OnDisconnect] client connection closed from %s, sid=%s", connection.RemoteAddr(), conn.ID())
	if err := conn.Close(); err != nil {
		logx.Err.Printf("[ClientHandler/OnDisconnect] client connection closed from %s, sid=%s err=%v", connection.RemoteAddr(), conn.ID(), err)
	}
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

	data := make([]byte, len(payload))
	copy(data, payload)
	if err = h.taskQueue.Put(string(conn.ID()), func() {
		if err := h.handleClientMessage(conn, msgID, data); err != nil {
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
	pack := protocol.NewWithSID(protocol.ClusterRequest, msgID, string(conn.ID()), data)
	return h.node.forwardPkt(conn, conn.codec, pack, h.node.serviceByRoute(msgID))
}
