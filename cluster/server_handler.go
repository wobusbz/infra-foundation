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
	"infra-foundation/session"

	"github.com/cloudwego/netpoll"
	"google.golang.org/protobuf/proto"
)

type connContextKey struct{}

var connCtxKey connContextKey

type ServerHandler struct {
	connManager    *connmanager.SessionManager
	modelManager   *model.ModelManager
	scheduler      *scheduler.Scheduler
	workMessage    *processor.MsgQueue
	node           *Node
	clusterHandler *ClusterHandler
}

func NewServerHandler(svr Server) *ServerHandler {
	s := &ServerHandler{
		connManager:  svr.ConnManager(),
		modelManager: svr.ModelManager(),
		scheduler:    svr.Scheduler(),
		workMessage:  svr.WorkMessage(),
		node:         svr.ClusterNode(),
	}

	s.clusterHandler = &ClusterHandler{connManager: s.connManager, modelManager: s.modelManager, node: s.node}

	return s
}

func (s *ServerHandler) OnPrepare(connection netpoll.Connection) context.Context {
	sid := session.DefaultConnSession.NextID()
	return context.WithValue(context.TODO(), connCtxKey, NewServerConn(s, connection, sid))
}

func (s *ServerHandler) OnDisconnect(ctx context.Context, connection netpoll.Connection) {
	conn, ok := ctx.Value(connCtxKey).(*ServerConn)
	if !ok {
		return
	}
	conn.Close()
	s.node.broadcastSessionClose(conn)
}

func (s *ServerHandler) OnRequest(ctx context.Context, connection netpoll.Connection) error {
	sconn, ok := ctx.Value(connCtxKey).(*ServerConn)
	if !ok {
		logx.Err.Println("[ServerHandler/OnRequest] 反射当前连接实体失败")
		return fmt.Errorf("[ServerHandler/OnRequest] 反射当前连接实体失败")
	}
	r2, err := sconn.Conn.Codec.NextPacket(connection.Reader())
	if err != nil {
		logx.Err.Printf("[ServerHandler/OnRequest] NextPacket error %v", err)
		return fmt.Errorf("[ServerHandler/OnRequest] Peek error %v", err)
	}
	if r2 == nil {
		return nil
	}
	if err = s.workMessage.Put(sconn.ID(), func() {
		pk, err := sconn.Conn.Codec.Unpack1(r2)
		if err != nil {
			logx.Err.Printf("[ServerHandler/OnRequest] Unpack error %v", err)
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

func (s *ServerHandler) onMessage(sconn *ServerConn, typ packet.Type, id int32, sid int64, data []byte) (err error) {
	switch typ {
	case packet.Heartbeat:
		// 心跳消息不需要处理
	case packet.Data:
		if model.IsLocalHandler(id) {
			err = s.modelManager.Dispatch(sconn, id, data)
		} else {
			err = remoteCallWithAgent(s.node, sconn, sconn.Conn.Codec, packet.NewInternal(packet.InternalData, id, sid, data), s.node.serviceByRoute(id))
		}
	case packet.Connection:
		var pb = &clusterpb.N2MOnConnection{}
		if err := proto.Unmarshal(data, pb); err != nil {
			return fmt.Errorf("[ServerHandler/onMessage] Type[%d] ConnID[%d] proto Unmarshal %w", typ, sconn.ID(), err)
		}
		s.connManager.RemoveByID(sconn.ID())
		s.node.bindNodeConn(pb.ID, sconn)
		logx.Dbg.Printf("[ServerHandler/onMessage] Type[%d]  %v", typ, pb)
		localNode := s.node.LocalNode()
		if localNode == nil {
			return fmt.Errorf("[ServerHandler/onMessage] local node not set")
		}
		err = sconn.Conn.SendTypePb(packet.Connection, &clusterpb.M2NOnConnection{ID: localNode.Id, Name: localNode.Name, Frontend: localNode.Frontend})
	default:
		switch typ {
		case packet.DisConnection:
			err = s.clusterHandler.handleSessionClose(data, sconn.ID())
		case packet.BindConnection:
			err = s.clusterHandler.handleBindConnection(data, sconn.ID())
		case packet.InternalData:
			err = s.clusterHandler.handleInternalData(id, sid, sconn.ID(), data)
		case packet.ClientData:
			err = s.clusterHandler.handleClientData(sid, sconn.ID(), data)
		case packet.NotifyData:
			err = s.clusterHandler.handleNotifyData(data, sconn.ID())
		}
	}

	if err == nil {
		sconn.Conn.RefreshHeartbeat()
	}
	return
}
