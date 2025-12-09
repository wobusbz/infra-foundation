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

type ServerRequest struct {
	connManager  *connmannger.ConnManager
	modelManager *model.ModelManager
	scheduler    *scheduler.Scheduler
}

func NewServerRequest(svr Server) *ServerRequest {
	return &ServerRequest{connManager: svr.ConnManager(), modelManager: svr.ModelManager(), scheduler: svr.Scheduler()}
}

func (s *ServerRequest) OnPrepare(connection netpoll.Connection) context.Context {
	sid := session.DefaultConnSession.SessionID()
	return context.WithValue(context.TODO(), "connection", NewNetPollConnection(s, connection, sid))
}

func (s *ServerRequest) OnDisconnect(ctx context.Context, connection netpoll.Connection) {
	conn, ok := ctx.Value("connection").(*NetPollConnection)
	if !ok {
		return
	}
	conn.Close()
	defaultNodeAgent.notifyCloseSession(conn)
}

func (s *ServerRequest) OnRequest(ctx context.Context, connection netpoll.Connection) error {
	sconn, ok := ctx.Value("connection").(*NetPollConnection)
	if !ok {
		return fmt.Errorf("[ServerRequest/OnReuest] 反射当前连接实体失败")
	}
	reader := connection.Reader()
	buf, err := reader.Next(reader.Len())
	if err != nil {
		logx.Err.Println(err)
		return fmt.Errorf("[ClientRequest/OnRequest] Peek error %v", err)
	}
	pks, err := sconn.Unpack(buf)
	if err != nil {
		logx.Err.Println(err)
		return fmt.Errorf("[ClientRequest/OnRequest] Unpack error %v", err)
	}

	var errs = make([]error, 0, len(pks))
	for _, pk := range pks {
		errs = append(errs, s.onMessage(sconn, pk.Type(), pk.ID(), int64(pk.SID()), pk.Data()))
		pk.Free()
	}
	err = errors.Join(errs...)
	if err != nil {
		logx.Err.Println(err)
	}
	return err
}

func (s *ServerRequest) onMessage(sconn *NetPollConnection, typ packet.Type, id int32, sid int64, bdata []byte) (err error) {
	switch typ {
	case packet.Heartbeat:
	case packet.Data:
		if !model.IsLocalHandler(id) {
			return fmt.Errorf("[ServerRequest/onMessage] SessionId[%d] MessageID[%d] not found", sconn.ID(), id)
		}
		err = s.modelManager.DispatchLocalAsync(sconn, id, bdata)
	case packet.Connection:
		var pb = &N2MOnConnection{}
		if err := proto.Unmarshal(bdata, pb); err != nil {
			return fmt.Errorf("[ServerRequest/onMessage] Type[%d] proto Unmarshal %w", typ, err)
		}
		s.connManager.RemoveByID(sconn.ID())
		defaultNodeAgent.storeNodeConn(pb.Name, pb.ID, sconn)
		logx.Dbg.Println("[ServerRequest/OnRequest] ", pb)
		err = sconn.SendTypePb(packet.Connection, &M2NOnConnection{
			ID:       defaultNodeAgent.node.Id,
			Name:     defaultNodeAgent.node.Name,
			Frontend: defaultNodeAgent.node.Frontend,
		})
	case packet.DisConnection:
		var pb N2MOnSessionClose
		if err := proto.Unmarshal(bdata, &pb); err != nil {
			return fmt.Errorf("[ServerRequest/onMessage] Type[%d] proto Unmarshal %w", typ, err)
		}
		conn, ok := s.connManager.GetByID(pb.SessionID)
		if !ok {
			return fmt.Errorf("[ServerRequest/onMessage] MessageID: %d not found", pb.SessionID)
		}
		err = conn.Close()
	case packet.BindConnection:
		var pb N2MOnSessionBindServer
		if err := proto.Unmarshal(bdata, &pb); err != nil {
			return fmt.Errorf("[ServerRequest/onMessage] Type[%d] proto Unmarshal %w", typ, err)
		}
		conn, ok := s.connManager.GetByID(pb.SessionID)
		if !ok {
			conn = newAcceptor(session.NewNetworkEntities(pb.SessionID, pb.UID), defaultNodeAgent.svr)
			s.connManager.StoreSession(conn)
		}
		for name, id := range pb.GetServers() {
			conn.BindServers(name, id)
		}
		logx.Dbg.Printf("[ServerRequest/onMessage] BindConnection SessionID: %d %v", pb.SessionID, conn.Servers())
	case packet.InternalData:
		if !model.IsLocalHandler(id) {
			return fmt.Errorf("[ServerRequest/onMessage] MessageID: %d not found", id)
		}
		conn, ok := s.connManager.GetByID(sid)
		if !ok {
			return fmt.Errorf("[ServerRequest/onMessage] SessionID: %d not found", id)
		}
		err = s.modelManager.DispatchLocalAsync(conn, id, bdata)
	case packet.ClientData:
		conn, ok := s.connManager.GetByID(sid)
		if !ok {
			return fmt.Errorf("[ServerRequest/onMessage] SessionID: %d not found", id)
		}
		conn1, ok := conn.(interface{ SendData(data []byte) error })
		if !ok {
			return errors.New("[ServerRequest/onMessage] 反射 SendData")
		}
		err = conn1.SendData(bdata)
	}
	if err == nil {
		sconn.UdtHeartBeatTime()
	}
	return
}
