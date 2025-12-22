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

type ctxKeyConn struct{}

var ctxKeyConnection ctxKeyConn

type ServerRequest struct {
	connManager  *connmannger.ConnManager
	modelManager *model.ModelManager
	scheduler    *scheduler.Scheduler
	workMessage  *WorkMessage
}

func NewServerRequest(svr Server) *ServerRequest {
	return &ServerRequest{
		connManager:  svr.ConnManager(),
		modelManager: svr.ModelManager(),
		scheduler:    svr.Scheduler(),
		workMessage:  svr.WorkMessage(),
	}
}

func (s *ServerRequest) OnPrepare(connection netpoll.Connection) context.Context {
	sid := session.DefaultConnSession.SessionID()
	return context.WithValue(context.TODO(), ctxKeyConnection, NewNetPollConnection(s, connection, sid))
}

func (s *ServerRequest) OnDisconnect(ctx context.Context, connection netpoll.Connection) {
	conn, ok := ctx.Value(ctxKeyConnection).(*NetPollConnection)
	if !ok {
		return
	}
	conn.Close()
	defaultNodeAgent.notifyCloseSession(conn)
}

func (s *ServerRequest) OnRequest(ctx context.Context, connection netpoll.Connection) error {
	sconn, ok := ctx.Value(ctxKeyConnection).(*NetPollConnection)
	if !ok {
		logx.Err.Println("[ServerRequest/OnRequest] 反射当前连接实体失败")
		return fmt.Errorf("[ServerRequest/OnRequest] 反射当前连接实体失败")
	}
	r2, err := sconn.PackCodec.NextPacket(connection.Reader())
	if err != nil {
		logx.Err.Printf("[ServerRequest/OnRequest] NextPacket error %v", err)
		return fmt.Errorf("[ServerRequest/OnRequest] Peek error %v", err)
	}
	if r2 == nil {
		return nil
	}
	if err = s.workMessage.Put(sconn.ID(), func() {
		pk, err := sconn.PackCodec.Unpack1(r2)
		if err != nil {
			logx.Err.Printf("[ServerRequest/OnRequest] Unpack error %v", err)
			return
		}
		if err = s.onMessage(sconn, pk.Type(), pk.ID(), pk.SID(), pk.Data()); err != nil {
			logx.Err.Println(err)
		}
		pk.Free()
	}); err != nil {
		logx.Err.Println(err)
	}
	return err
}

func (s *ServerRequest) onMessage(sconn *NetPollConnection, typ packet.Type, id int32, sid int64, bdata []byte) (err error) {
	switch typ {
	case packet.Heartbeat:
	case packet.Data:
		if model.IsLocalHandler(id) {
			err = s.modelManager.DispatchLocalAsync(sconn, id, bdata)
		} else {
			err = remoteCall(sconn, sconn.PackCodec, packet.NewInternal(packet.InternalData, id, sid, bdata), defaultNodeAgent.getGroutes(id))
		}
	case packet.Connection:
		var pb = &N2MOnConnection{}
		if err := proto.Unmarshal(bdata, pb); err != nil {
			return fmt.Errorf("[ServerRequest/onMessage] Type[%d] ConnID[%d] proto Unmarshal %w", typ, sconn.ID(), err)
		}
		s.connManager.RemoveByID(sconn.ID())
		defaultNodeAgent.storeNodeConn(pb.ID, sconn)
		logx.Dbg.Printf("[ServerRequest/onMessage] Type[%d]  %v", typ, pb)
		err = sconn.SendTypePb(packet.Connection, &M2NOnConnection{
			ID:       defaultNodeAgent.node.Id,
			Name:     defaultNodeAgent.node.Name,
			Frontend: defaultNodeAgent.node.Frontend,
		})
	case packet.DisConnection:
		var pb N2MOnSessionClose
		if err := proto.Unmarshal(bdata, &pb); err != nil {
			return fmt.Errorf("[ServerRequest/onMessage] Type[%d] ConnID[%d] proto Unmarshal %w", typ, sconn.ID(), err)
		}
		conn, ok := s.connManager.GetByID(pb.SessionID)
		if !ok {
			return fmt.Errorf("[ServerRequest/onMessage] Type[%d] ConnID[%d] SessionID: %d not found", typ, sconn.ID(), pb.SessionID)
		}
		err = conn.Close()
	case packet.BindConnection:
		var pb N2MOnSessionBindServer
		if err := proto.Unmarshal(bdata, &pb); err != nil {
			return fmt.Errorf("[ServerRequest/onMessage] Type[%d] ConnID[%d] proto Unmarshal %w", typ, sconn.ID(), err)
		}
		conn, ok := s.connManager.GetByID(pb.SessionID)
		if !ok {
			conn = newAcceptor(session.NewNetworkEntities(pb.SessionID, pb.UID), defaultNodeAgent.svr)
			s.connManager.StoreSession(conn)
		}
		for name, id := range pb.GetServers() {
			conn.BindServers(name, id)
		}
		logx.Dbg.Printf("[ServerRequest/onMessage] Type[%d] ConnID[%d] SessionID: %d %v", typ, sconn.ID(), pb.SessionID, conn.Servers())
	case packet.InternalData:
		if !model.IsLocalHandler(id) {
			return fmt.Errorf("[ServerRequest/onMessage] Type[%d] ConnID[%d] MessageID: %d not found", typ, sconn.ID(), id)
		}
		conn, ok := s.connManager.GetByID(sid)
		if !ok {
			return fmt.Errorf("[ServerRequest/onMessage] Type[%d] ConnID[%d] SessionID: %d not found", typ, sconn.ID(), sid)
		}
		err = s.modelManager.DispatchLocalAsync(conn, id, bdata)
	case packet.ClientData:
		conn, ok := s.connManager.GetByID(sid)
		if !ok {
			return fmt.Errorf("[ServerRequest/onMessage] Type[%d] ConnID[%d] SessionID: %d not found", typ, sconn.ID(), sid)
		}
		conn1, ok := conn.(sender)
		if !ok {
			return fmt.Errorf("[ServerRequest/onMessage] Type[%d] ConnID[%d] 反射 SendData", typ, sconn.ID())
		}
		err = conn1.SendData(bdata)
	case packet.NotifyData:
		var pb N2MNotify
		if err := proto.Unmarshal(bdata, &pb); err != nil {
			return fmt.Errorf("[ServerRequest/onMessage] Type[%d] ConnID[%d] Unmarshal %w", typ, sconn.ID(), err)
		}
		if len(pb.SessionID) == 0 {
			return s.connManager.Range(func(s session.Session) error {
				conn1, ok := s.(sender)
				if !ok {
					return fmt.Errorf("[ServerRequest/onMessage] Range Type[%d] 反射 SendData", typ)
				}
				return conn1.SendData(pb.Plyload)
			})
		}
		var errs []error
		for _, sid := range pb.SessionID {
			conn, ok := s.connManager.GetByID(sid)
			if !ok {
				errs = append(errs, fmt.Errorf("[ServerRequest/onMessage] Type[%d] ConnID[%d] SessionID: %d not found", typ, sconn.ID(), sid))
				continue
			}
			conn1, ok := conn.(sender)
			if !ok {
				errs = append(errs, fmt.Errorf("[ServerRequest/onMessage] Type[%d] 反射 SendData", typ))
				continue
			}
			errs = append(errs, conn1.SendData(bdata))
		}
		if err = errors.Join(errs...); err != nil {
			return fmt.Errorf("[ServerRequest/onMessage] Type[%d] Notify error: %w", typ, err)
		}
	}
	if err == nil {
		sconn.RefreshHeartbeat()
	}
	return
}
