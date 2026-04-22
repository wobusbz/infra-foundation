package cluster

import (
	"context"
	"fmt"
	"infra-foundation/clusterpb"
	"infra-foundation/logx"
	"infra-foundation/protocol"

	"github.com/cloudwego/netpoll"
	pbp "google.golang.org/protobuf/proto"
)

type OutboundHandler struct {
	*OutboundPeerConn
	*MessageHandler
}

func NewOutboundHandler(svr Server) *OutboundHandler {
	msgHandler := NewMessageHandler(
		svr.ConnMgr(),
		svr.ModelManager(),
		svr.Scheduler(),
		svr.TaskQueue(),
		svr.ClusterNode(),
	)

	return &OutboundHandler{MessageHandler: msgHandler}
}

func (c *OutboundHandler) OnRequest(ctx context.Context, connection netpoll.Connection) error {
	r2, err := c.Conn.Codec.NextPacket(connection.Reader())
	if err != nil {
		logx.Err.Printf("[OutboundHandler/OnRequest] NextPacket error %v", err)
		return fmt.Errorf("next packet: %w", err)
	}
	if r2 == nil {
		return nil
	}
	if err = c.TaskQueue.Put(string(c.ID()), func() {
		pk, err := c.Conn.Codec.Unpack1(r2)
		if err != nil {
			logx.Err.Printf("[OutboundHandler/OnRequest] Unpack error %v", err)
			return
		}
		if err = c.onMessage(pk.Type(), pk.ID(), pk.SID(), pk.Data()); err != nil {
			logx.Err.Println(err)
		}
		pk.Free()
	}); err != nil {
		logx.Err.Println(err)
	}
	return err
}

func (c *OutboundHandler) onMessage(typ protocol.Type, id int32, sid string, data []byte) (err error) {
	switch typ {
	case protocol.Handshake:
		var pb = &clusterpb.M2NOnConnection{}
		if err := pbp.Unmarshal(data, pb); err != nil {
			return fmt.Errorf("unmarshal connection: %w", err)
		}
		c.Node.bindNodeConn(pb.ID, c)
		c.Node.LoadBalancer().MarkHealthy(pb.ID, true)
	default:
		switch typ {
		case protocol.Disconnect:
			err = c.ClusterHandler.handleDisconnect(data)
		case protocol.BindSession:
			err = c.ClusterHandler.handleBindSession(data)
		case protocol.ServiceCall:
			err = c.ClusterHandler.handleServiceCall(id, sid, data)
		case protocol.Response:
			c.Conn.Unpack(data)
			err = c.ClusterHandler.handleResponse(sid, data)
		case protocol.Push:
			err = c.ClusterHandler.handlePush(data)
		}
	}

	if err == nil {
		c.Conn.RefreshHeartbeat()
	}
	return
}
