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
}

func NewClientRequest(svr Server) *ClientRequest {
	c := &ClientRequest{modelManager: svr.ModelManager(), connManager: svr.ConnManager(), scheduler: svr.Scheduler()}
	return c
}

func (c *ClientRequest) OnRequest(ctx context.Context, connection netpoll.Connection) error {
	reader := connection.Reader()
	buf, err := reader.Next(reader.Len())
	if err != nil {
		logx.Err.Println(err)
		return fmt.Errorf("[ClientRequest/OnRequest] Peek error %v", err)
	}
	pks, err := c.Unpack(buf)
	if err != nil {
		logx.Err.Println(err)
		return fmt.Errorf("[ClientRequest/OnRequest] Unpack error %v", err)
	}
	var errs = make([]error, 0, len(pks))
	for _, pk := range pks {
		errs = append(errs, c.onMessage(pk.Type(), pk.ID(), int64(pk.SID()), pk.Data()))
		pk.Free()
	}
	err = errors.Join(errs...)
	if err != nil {
		logx.Err.Println(err)
	}
	return err
}

func (c *ClientRequest) onMessage(typ packet.Type, id int32, sid int64, bdata []byte) (err error) {
	switch typ {
	case packet.Connection:
		var pb = &M2NOnConnection{}
		if err := proto.Unmarshal(bdata, pb); err != nil {
			return fmt.Errorf("[ClientRequest/onMessage] Type: %d proto Unmarshal %w", typ, err)
		}
		logx.Dbg.Println("[ClientRequest/OnRequest] ", pb, c.ClientConnection == nil)
		defaultNodeAgent.storeNodeConn(pb.Name, pb.ID, c)
	case packet.InternalData:
		if !model.IsLocalHandler(id) {
			return fmt.Errorf("[ClientRequest/onMessage] MessageID: %d not found", id)
		}
		conn, ok := c.connManager.GetByID(sid)
		if !ok {
			return fmt.Errorf("[ClientRequest/onMessage] SessionID: %d not found", sid)
		}
		err = c.modelManager.DispatchLocalAsync(conn, id, bdata)
	case packet.ClientData:
		conn, ok := c.connManager.GetByID(sid)
		if !ok {
			return fmt.Errorf("[ClientConnection/onMessage] SessionID: %d not found", id)
		}
		conn1, ok := conn.(interface{ SendData(data []byte) error })
		if !ok {
			return errors.New("[ServerRequest/onMessage] 反射 SendData")
		}
		err = conn1.SendData(bdata)
	case packet.BindConnection:
		var pb N2MOnSessionBindServer
		if err := proto.Unmarshal(bdata, &pb); err != nil {
			return fmt.Errorf("[ClientRequest/onMessage] Type: %d proto Unmarshal %w", typ, err)
		}
		conn, ok := c.connManager.GetByID(pb.SessionID)
		if !ok {
			conn = newAcceptor(session.NewNetworkEntities(pb.SessionID, pb.UID), defaultNodeAgent.svr)
			c.connManager.StoreSession(conn)
		}
		for name, id := range pb.GetServers() {
			conn.BindServers(name, id)
		}
		logx.Dbg.Printf("[ClientRequest/onMessage] BindConnection SessionID: %d %v", pb.SessionID, conn.Servers())
	case packet.DisConnection:
		var pb N2MOnSessionClose
		if err := proto.Unmarshal(bdata, &pb); err != nil {
			return fmt.Errorf("[ClientRequest/onMessage] Type: %d proto Unmarshal %w", typ, err)
		}
		conn, ok := c.connManager.GetByID(pb.SessionID)
		if !ok {
			return fmt.Errorf("[ClientRequest/onMessage] MessageID: %d not found", pb.SessionID)
		}
		err = conn.Close()
	}
	if err == nil {
		c.UdtHeartBeatTime()
	}
	return
}
