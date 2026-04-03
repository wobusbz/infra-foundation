package cluster

import (
	"errors"
	"fmt"
	"infra-foundation/config"
	"infra-foundation/logx"
	"infra-foundation/packet"
	"infra-foundation/protomessage"
	"infra-foundation/scheduler"
	"infra-foundation/session"
	"infra-foundation/transport"
	"sync/atomic"
	"time"

	"github.com/cloudwego/netpoll"
	"google.golang.org/protobuf/proto"
)

var _ session.Session = (*ServerConn)(nil)

// ServerConn 是基于 netpoll 的服务端连接封装。
// 通过 session.Base + netpollMessenger 将 Send/Notify/Close 逻辑统一委托，避免重复代码。
type ServerConn struct {
	*session.Base
	Conn              *transport.Conn
	request           *ServerHandler
	heartbeatInterval time.Duration
	timerID           scheduler.TimerID
	closed            atomic.Bool
	heartbeatTimeout  atomic.Int32
}

// netpollMessenger 实现了 session.Messenger，承载 ServerConn 特有的消息路由与关闭逻辑。
type netpollMessenger struct {
	n *ServerConn
}

func (m *netpollMessenger) Send(pb protomessage.ProtoMessage) error {
	if m.n.closed.Load() {
		return errors.New("[ServerConn/Send] connection closed")
	}
	localNode := m.n.request.node.LocalNode()
	if localNode != nil && localNode.Name == pb.NodeName() {
		return m.n.Conn.SendTypePb(packet.Data, pb)
	}
	pbdata, err := proto.Marshal(pb)
	if err != nil {
		return fmt.Errorf("[ServerConn/Send] Marshal %w", err)
	}
	return remoteCallWithAgent(m.n.request.node, m.n, m.n.Conn.Codec, packet.NewInternal(packet.InternalData, pb.MessageID(), m.n.ID(), pbdata), pb.NodeName())
}

func (m *netpollMessenger) Notify(targets []session.Session, pb protomessage.ProtoMessage) error {
	if len(targets) == 0 {
		return m.n.request.connManager.Range(func(s session.Session) error { return s.Send(pb) })
	}
	var errs []error
	for _, sv := range targets {
		errs = append(errs, sv.Send(pb))
	}
	return errors.Join(errs...)
}

func (m *netpollMessenger) Close() error {
	if !m.n.closed.CompareAndSwap(false, true) {
		return nil
	}
	if m.n.UID() == -1 {
		m.n.request.node.NodeConnManager().RemoveByID(m.n.ID())
	} else {
		m.n.request.modelManager.OnDisconnection(m.n)
		m.n.request.connManager.RemoveByID(m.n.ID())
	}
	m.n.request.scheduler.CancelTimer(m.n.timerID)
	return m.n.Conn.Close()
}

func NewServerConn(svrHandler *ServerHandler, connection netpoll.Connection, id int64) *ServerConn {
	n := &ServerConn{
		request:           svrHandler,
		heartbeatInterval: config.Default.NetPollHeartbeatInterval,
	}
	n.Base = &session.Base{
		SessionEntity: session.NewSessionEntity(id, -1),
		Messenger:       &netpollMessenger{n: n},
	}
	n.Conn = transport.NewConn(connection, id, -1)

	n.request.connManager.StoreSession(n)
	n.timerID, _ = n.request.scheduler.PushEvery(n.heartbeatInterval, n.checkHeartbeat)
	return n
}

func (n *ServerConn) checkHeartbeat() {
	now := time.Now().Unix()
	if n.Conn.HeartbeatAt() == 0 {
		n.Conn.SetHeartbeatAt(now)
		n.heartbeatTimeout.Store(0)
		return
	}

	if n.Conn.HeartbeatAt()+int64(n.heartbeatInterval.Seconds()*2) > now {
		n.heartbeatTimeout.Store(0)
		return
	}

	timeoutCount := n.heartbeatTimeout.Add(1)
	logx.War.Printf("[ServerConn/checkHeartbeat] timeout %d times for session %d", timeoutCount, n.ID())

	if timeoutCount >= config.Default.NetPollHeartbeatTimeoutCount {
		logx.Err.Printf("[ServerConn/checkHeartbeat] closing session %d after %d timeouts", n.ID(), timeoutCount)
		n.Close()
	}
}
