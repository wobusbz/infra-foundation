package cluster

import (
	"context"
	"fmt"
	"infra-foundation/logx"

	"github.com/cloudwego/netpoll"
)

type OutboundHandler struct {
	peer *PeerConn
	msgh *MessageHandler
}

func NewOutboundHandler(msgh *MessageHandler, peer *PeerConn) *OutboundHandler {
	return &OutboundHandler{peer: peer, msgh: msgh}
}

func (c *OutboundHandler) OnRequest(ctx context.Context, connection netpoll.Connection) error {
	r2, err := c.peer.codec.NextPacket(connection.Reader())
	if err != nil {
		logx.Err.Printf("[OutboundHandler/OnRequest] NextPacket error %v", err)
		return fmt.Errorf("next packet: %w", err)
	}
	if r2 == nil {
		return nil
	}
	if err = c.msgh.taskQueue.Put(c.peer.queueKey, func() {
		pk, err := c.peer.codec.Unpack(r2)
		if err != nil {
			logx.Err.Printf("[OutboundHandler/OnRequest] Unpack error %v", err)
			return
		}
		if err = c.msgh.OnMessage(c.peer, pk.ClusterType(), pk.ID(), pk.SID(), pk.Data()); err != nil {
			logx.Err.Println(err)
		}
		pk.Free()
	}); err != nil {
		logx.War.Printf("[OutboundHandler/OnRequest] task queue full: %v", err)
	}
	return nil
}
