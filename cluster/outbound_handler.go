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
		if err = c.onMessage(pk.ClusterType(), pk.ID(), pk.SID(), pk.Data()); err != nil {
			logx.Err.Println(err)
		}
		pk.Free()
	}); err != nil {
		logx.Err.Println(err)
	}
	return err
}

func (c *OutboundHandler) onMessage(typ protocol.ClusterType, id int32, sid string, data []byte) (err error) {
	switch typ {
	case protocol.ClusterHandshake:
		var pb = &clusterpb.M2NOnConnection{}
		if err := pbp.Unmarshal(data, pb); err != nil {
			return fmt.Errorf("unmarshal connection: %w", err)
		}
		if err = c.Node.bindNodeConn(pb.ID, c); err != nil {
			return fmt.Errorf("bind node connection %s: %w", pb.ID, err)
		}
		c.Node.LoadBalancer().MarkHealthy(pb.ID, true)
	case protocol.ClusterDisconnect:
		err = c.handleDisconnect(data)
	case protocol.ClusterBindSession:
		err = c.handleBindSession(data)
	case protocol.ClusterServiceCall:
		err = c.handleServiceCall(id, sid, data)
	case protocol.ClusterResponse:
		err = c.handleResponse(sid, id, data)
	case protocol.ClusterPush:
		err = c.handlePush(data)
	}

	if err == nil {
		c.Conn.RefreshHeartbeat()
	}
	return
}
