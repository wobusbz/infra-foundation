package cluster

import (
	"context"
	"fmt"
	"infra-foundation/logx"
	"infra-foundation/protocol"
	"infra-foundation/queue"
	"infra-foundation/session"
	"infra-foundation/transport"

	"github.com/cloudwego/netpoll"
)

type clientContextKey struct{}

var clientCtxKey clientContextKey

type ClientHandler struct {
	dispatcher     ModelDispatcher
	taskQueue      *queue.TaskQueue
	node           *Node
	lifecycle      *FrontendLifecycle
	codec          *protocol.ClusterCodec
	clientProtocol protocol.ClientProtocol
}

func NewClientHandler(dispatcher ModelDispatcher, taskQueue *queue.TaskQueue, node *Node, lifecycle *FrontendLifecycle) *ClientHandler {
	return &ClientHandler{
		dispatcher:     dispatcher,
		taskQueue:      taskQueue,
		node:           node,
		lifecycle:      lifecycle,
		codec:          protocol.NewClusterCodec(),
		clientProtocol: protocol.NewClientCodec(),
	}
}

func (h *ClientHandler) OnPrepare(connection netpoll.Connection) context.Context {
	sid := session.DefaultIDPool.NextID()
	remoteAddr := connection.RemoteAddr()
	localAddr := connection.LocalAddr()
	logx.Inf.Printf("[ClientHandler/OnPrepare] new client connection from %s to %s, sid=%s", remoteAddr, localAddr, sid)

	conn := transport.NewConn(connection, string(sid))
	conn.SetBufReleaser(protocolBufReleaser{})
	clientConn := NewClientConn(conn, h.clientProtocol, h.codec, h.node, h.lifecycle)
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

	msgID, payload, err := h.clientProtocol.NextPacket(adaptNetpollReader(connection.Reader()))
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
	decision := h.node.router.Decide(msgID, conn, DirClientInbound)
	switch decision.Kind {
	case RouteLocalModel:
		return h.dispatcher.Dispatch(conn, msgID, data)
	case RouteBackendNode:
		pack := protocol.NewWithSID(protocol.ClusterRequest, msgID, 0, string(conn.ID()), data)
		return h.node.ForwardPkt(conn, conn.codec, pack, decision.Service)
	case RouteGatewayNode:
		pack := protocol.NewWithSID(protocol.ClusterRequest, msgID, 0, string(conn.ID()), data)
		return h.node.ForwardPkt(conn, conn.codec, pack, "")
	case RouteFrontendClient:
		return fmt.Errorf("unexpected frontend route for inbound message %d", msgID)
	case RouteDrop:
		return fmt.Errorf("no route for message %d", msgID)
	}
	return nil
}
