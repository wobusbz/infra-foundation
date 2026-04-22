package cluster

import (
	"context"
	"fmt"
	"infra-foundation/logx"
	"infra-foundation/scheduler"
	"infra-foundation/session"

	"github.com/cloudwego/netpoll"
)

type connContextKey struct{}

var connCtxKey connContextKey

type ServerHandler struct {
	*MessageHandler
	scheduler *scheduler.Scheduler
}

func NewServerHandler(svr Server) *ServerHandler {
	node := svr.ClusterNode()
	msgHandler := NewMessageHandler(
		svr.ConnMgr(),
		svr.ModelManager(),
		svr.Scheduler(),
		svr.TaskQueue(),
		node,
	)

	return &ServerHandler{MessageHandler: msgHandler, scheduler: svr.Scheduler()}
}

func (s *ServerHandler) OnPrepare(connection netpoll.Connection) context.Context {
	sid := session.DefaultIDPool.NextID("sv")
	remoteAddr := connection.RemoteAddr()
	localAddr := connection.LocalAddr()
	logx.Inf.Printf("[ServerHandler/OnPrepare] new connection from %s to %s, sid=%s", remoteAddr, localAddr, sid)
	return context.WithValue(context.TODO(), connCtxKey, NewInboundPeerConn(s, connection, sid))
}

func (s *ServerHandler) OnDisconnect(ctx context.Context, connection netpoll.Connection) {
	conn, ok := ctx.Value(connCtxKey).(*InboundPeerConn)
	if !ok {
		logx.War.Printf("[ServerHandler/OnDisconnect] connection from %s, no conn in context (closed before OnPrepare?)", connection.RemoteAddr())
		return
	}
	logx.Inf.Printf("[ServerHandler/OnDisconnect] connection closed from %s, sid=%s", connection.RemoteAddr(), conn.ID())
	conn.Close()
	s.Node.broadcastSessionClose(conn)
}

func (s *ServerHandler) OnRequest(ctx context.Context, connection netpoll.Connection) error {
	sconn, ok := ctx.Value(connCtxKey).(*InboundPeerConn)
	if !ok {
		logx.Err.Println("[ServerHandler/OnRequest] get conn from context failed")
		return fmt.Errorf("get conn from context failed")
	}
	r2, err := sconn.Conn.Codec.NextPacket(connection.Reader())
	if err != nil {
		logx.Err.Printf("[ServerHandler/OnRequest] NextPacket error %v", err)
		return fmt.Errorf("next packet: %w", err)
	}
	if r2 == nil {
		return nil
	}
	if err = s.TaskQueue.Put(string(sconn.ID()), func() {
		pk, err := sconn.Conn.Codec.Unpack1(r2)
		if err != nil {
			logx.Err.Printf("[ServerHandler/OnRequest] Unpack error %v", err)
			return
		}
		if err = s.OnMessage(sconn, sconn.Conn.Codec, pk.Type(), pk.ID(), pk.SID(), pk.Data()); err != nil {
			logx.Err.Println(err)
		}
		pk.Free()
	}); err != nil {
		logx.War.Printf("[ServerHandler/OnRequest] overload, closing conn %s: %v", sconn.ID(), err)
		sconn.Close()
		return err
	}
	return nil
}
