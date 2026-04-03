package cluster

import (
	"context"
	"fmt"
	"infra-foundation/clusterpb"
	"infra-foundation/connmanager"
	"infra-foundation/logx"
	"infra-foundation/model"
	"infra-foundation/packet"
	"infra-foundation/processor"
	"infra-foundation/scheduler"

	"github.com/cloudwego/netpoll"
	"google.golang.org/protobuf/proto"
)

type ClientHandler struct {
	*ClientConn
	modelManager *model.ModelManager
	connManager  *connmanager.SessionManager
	scheduler    *scheduler.Scheduler
	workMessage  *processor.MsgQueue
	node           *Node
	clusterHandler *ClusterHandler
}

func NewClientHandler(svr Server) *ClientHandler {
	c := &ClientHandler{
		modelManager: svr.ModelManager(),
		connManager:  svr.ConnManager(),
		scheduler:    svr.Scheduler(),
		workMessage:  svr.WorkMessage(),
		node:         svr.ClusterNode(),
	}

	c.clusterHandler = &ClusterHandler{connManager: c.connManager, modelManager: c.modelManager, node: c.node}

	return c
}

func (c *ClientHandler) OnRequest(ctx context.Context, connection netpoll.Connection) error {
	r2, err := c.Conn.Codec.NextPacket(connection.Reader())
	if err != nil {
		logx.Err.Printf("[ClientHandler/OnRequest] NextPacket error %v", err)
		return fmt.Errorf("[ClientHandler/OnRequest] Peek error %v", err)
	}
	if r2 == nil {
		return nil
	}
	if err = c.workMessage.Put(c.ID(), func() {
		pk, err := c.Conn.Codec.Unpack1(r2)
		if err != nil {
			logx.Err.Printf("[ClientHandler/OnRequest] Unpack error %v", err)
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

func (c *ClientHandler) onMessage(typ packet.Type, id int32, sid int64, data []byte) (err error) {
	switch typ {
	case packet.Connection:
		var pb = &clusterpb.M2NOnConnection{}
		if err := proto.Unmarshal(data, pb); err != nil {
			return fmt.Errorf("[ClientHandler/onMessage] Type[%d] ConnID[%d] Unmarshal %w", typ, c.ID(), err)
		}
		logx.Dbg.Println("[ClientHandler/OnRequest] ", pb, c.ClientConn == nil)
		c.node.bindNodeConn(pb.ID, c)
	default:
		switch typ {
		case packet.DisConnection:
			err = c.clusterHandler.handleSessionClose(data, c.ID())
		case packet.BindConnection:
			err = c.clusterHandler.handleBindConnection(data, c.ID())
		case packet.InternalData:
			err = c.clusterHandler.handleInternalData(id, sid, c.ID(), data)
		case packet.ClientData:
			err = c.clusterHandler.handleClientData(sid, c.ID(), data)
		case packet.NotifyData:
			err = c.clusterHandler.handleNotifyData(data, c.ID())
		}
	}

	if err == nil {
		c.Conn.RefreshHeartbeat()
	}
	return
}

