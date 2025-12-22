package cluster

import (
	"context"
	"errors"
	"fmt"
	"infra-foundation/connmannger"
	"infra-foundation/logx"
	"infra-foundation/model"
	"infra-foundation/packet"
	"infra-foundation/scheduler"
	"infra-foundation/session"

	"github.com/cloudwego/netpoll"
	"google.golang.org/protobuf/proto"
)

type ClientRequest struct {
	*ClientConnection
	modelManager *model.ModelManager
	connManager  *connmannger.ConnManager
	scheduler    *scheduler.Scheduler
	workMessage  *WorkMessage
}

func NewClientRequest(svr Server) *ClientRequest {
	c := &ClientRequest{
		modelManager: svr.ModelManager(),
		connManager:  svr.ConnManager(),
		scheduler:    svr.Scheduler(),
		workMessage:  svr.WorkMessage(),
	}
	return c
}

func (c *ClientRequest) OnRequest(ctx context.Context, connection netpoll.Connection) error {
	r2, err := c.PackCodec.NextPacket(connection.Reader())
	if err != nil {
		logx.Err.Printf("[ClientRequest/OnRequest] NextPacket error %v", err)
		return fmt.Errorf("[ClientRequest/OnRequest] Peek error %v", err)
	}
	if r2 == nil {
		return nil
	}
	if err = c.workMessage.Put(c.ID(), func() {
		pk, err := c.PackCodec.Unpack1(r2)
		if err != nil {
			logx.Err.Printf("[ClientRequest/OnRequest] Unpack error %v", err)
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

func (c *ClientRequest) onMessage(typ packet.Type, id int32, sid int64, bdata []byte) (err error) {
	switch typ {
	case packet.Connection:
		var pb = &M2NOnConnection{}
		if err := proto.Unmarshal(bdata, pb); err != nil {
			return fmt.Errorf("[ClientRequest/onMessage] Type[%d] ConnID[%d] Unmarshal %w", typ, c.ID(), err)
		}
		logx.Dbg.Println("[ClientRequest/OnRequest] ", pb, c.ClientConnection == nil)
		defaultNodeAgent.storeNodeConn(pb.ID, c)
	case packet.DisConnection:
		var pb N2MOnSessionClose
		if err := proto.Unmarshal(bdata, &pb); err != nil {
			return fmt.Errorf("[ClientRequest/onMessage] Type[%d] ConnID[%d] Unmarshal %w", typ, c.ID(), err)
		}
		conn, ok := c.connManager.GetByID(pb.SessionID)
		if !ok {
			return fmt.Errorf("[ClientRequest/onMessage] Type[%d] ConnID[%d] SessionID: %d not found", typ, c.ID(), pb.SessionID)
		}
		err = conn.Close()
	case packet.BindConnection:
		var pb N2MOnSessionBindServer
		if err := proto.Unmarshal(bdata, &pb); err != nil {
			return fmt.Errorf("[ClientRequest/onMessage] Type[%d] ConnID[%d] Unmarshal %w", typ, c.ID(), err)
		}
		conn, ok := c.connManager.GetByID(pb.SessionID)
		if !ok {
			conn = newAcceptor(session.NewNetworkEntities(pb.SessionID, pb.UID), defaultNodeAgent.svr)
			c.connManager.StoreSession(conn)
		}
		for name, id := range pb.GetServers() {
			conn.BindServers(name, id)
		}
		logx.Dbg.Printf("[ClientRequest/onMessage] Type[%d] ConnID[%d] SessionID: %d %v", typ, c.ID(), pb.SessionID, conn.Servers())
	case packet.InternalData:
		if !model.IsLocalHandler(id) {
			return fmt.Errorf("[ClientRequest/onMessage] Type[%d] ConnID[%d] MessageID: %d not found", typ, c.ID(), id)
		}
		conn, ok := c.connManager.GetByID(sid)
		if !ok {
			return fmt.Errorf("[ClientRequest/onMessage] Type[%d] ConnID[%d] SessionID: %d not found", typ, c.ID(), sid)
		}
		err = c.modelManager.DispatchLocalAsync(conn, id, bdata)
	case packet.ClientData:
		conn, ok := c.connManager.GetByID(sid)
		if !ok {
			return fmt.Errorf("[ClientConnection/onMessage] Type[%d] ConnID[%d] SessionID: %d not found", typ, c.ID(), sid)
		}
		conn1, ok := conn.(sender)
		if !ok {
			return fmt.Errorf("[ClientConnection/onMessage] Type[%d] 反射 SendData", typ)
		}
		err = conn1.SendData(bdata)
	case packet.NotifyData:
		var pb N2MNotify
		if err := proto.Unmarshal(bdata, &pb); err != nil {
			return fmt.Errorf("[ClientRequest/onMessage] Type[%d] ConnID[%d] Unmarshal %w", typ, c.ID(), err)
		}
		if len(pb.SessionID) == 0 {
			return c.connManager.Range(func(s session.Session) error {
				conn1, ok := s.(sender)
				if !ok {
					return fmt.Errorf("[ClientConnection/onMessage] Range Type[%d] 反射 SendData", typ)
				}
				return conn1.SendData(pb.Plyload)
			})
		}
		var errs []error
		for _, sid := range pb.SessionID {
			conn, ok := c.connManager.GetByID(sid)
			if !ok {
				errs = append(errs, fmt.Errorf("[ClientConnection/onMessage] Type[%d] ConnID[%d] SessionID: %d not found", typ, c.ID(), sid))
				continue
			}
			conn1, ok := conn.(sender)
			if !ok {
				errs = append(errs, fmt.Errorf("[ClientConnection/onMessage] Type[%d] 反射 SendData", typ))
				continue
			}
			errs = append(errs, conn1.SendData(bdata))
		}
		if err = errors.Join(errs...); err != nil {
			return fmt.Errorf("[ClientConnection/onMessage] Type[%d] Notify error: %w", typ, err)
		}
	}
	if err == nil {
		c.RefreshHeartbeat()
	}
	return
}
