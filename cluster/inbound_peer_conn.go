package cluster

import (
	"errors"
	"infra-foundation/config"
	"infra-foundation/logx"
	"infra-foundation/message"
	"infra-foundation/scheduler"
	"infra-foundation/session"
	"infra-foundation/transport"
	"sync/atomic"
	"time"

	"github.com/cloudwego/netpoll"
)

var _ session.Session = (*InboundPeerConn)(nil)

type InboundPeerConn struct {
	*session.SessionBase
	Conn              *transport.Conn
	request           *ServerHandler
	heartbeatInterval time.Duration
	timerID           scheduler.TimerID
	closed            atomic.Bool
	heartbeatTimeout  atomic.Int32
}

type inboundMessenger struct {
	n   *InboundPeerConn
	sid session.SessionID
}

func (m *inboundMessenger) Send(pb message.Message) error {
	if m.n.closed.Load() {
		return errors.New("[inboundMessenger/Send] connection closed")
	}
	return sendProtoMessage(&m.n.closed, "inboundMessenger", m.n.request.Node, m.n, m.n.Conn, string(m.sid), pb)
}

func (m *inboundMessenger) Notify(targets []session.Session, pb message.Message) error {
	if len(targets) == 0 {
		return m.n.request.ConnMgr.Range(func(s session.Session) error { return s.Send(pb) })
	}
	var errs []error
	for _, sv := range targets {
		errs = append(errs, sv.Send(pb))
	}
	return errors.Join(errs...)
}

func (m *inboundMessenger) Close() error {
	if !m.n.closed.CompareAndSwap(false, true) {
		return nil
	}
	if m.n.IsPeerConn() {
		m.n.request.Node.PeerMgr().RemoveByID(m.n.ID())
		m.n.request.Node.LoadBalancer().MarkHealthy(string(m.n.ID()), false)
	} else {
		m.n.request.ModelManager.OnDisconnection(m.n)
		m.n.request.ConnMgr.RemoveByID(m.n.ID())
	}
	session.DefaultIDPool.Remove(m.n.ID())
	m.n.request.Scheduler.CancelTimer(m.n.timerID)
	return m.n.Conn.Close()
}

func NewInboundPeerConn(svrHandler *ServerHandler, connection netpoll.Connection, id session.SessionID) *InboundPeerConn {
	n := &InboundPeerConn{
		request:           svrHandler,
		heartbeatInterval: config.Default.NetPollHeartbeatInterval,
	}
	n.SessionBase = &session.SessionBase{
		SessionEntity: session.NewSessionEntity(id, -1),
		Messenger:     &inboundMessenger{n: n, sid: id},
	}
	n.Conn = transport.NewConn(connection, id, -1)

	n.request.ConnMgr.Store(n)
	n.timerID, _ = n.request.Scheduler.PushEvery(n.heartbeatInterval, n.checkHeartbeat)
	return n
}

func (n *InboundPeerConn) checkHeartbeat() {
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

	if timeoutCount >= config.Default.NetPollHeartbeatTimeoutCount {
		logx.Err.Printf("[InboundPeerConn/checkHeartbeat] closing session %s after %d timeouts", n.ID(), timeoutCount)
		n.Close()
	}
}

func (n *InboundPeerConn) SendData(data []byte) error {
	if n.closed.Load() {
		return errors.New("[InboundPeerConn/SendData] connection closed")
	}
	return n.Conn.SendData(data)
}

func (n *InboundPeerConn) SendTypePb(typ int8, pb message.Message) error {
	if n.closed.Load() {
		return errors.New("[InboundPeerConn/SendTypePb] connection closed")
	}
	return n.Conn.SendTypePb(typ, pb)
}

func (n *InboundPeerConn) BindID(id session.SessionID) {
	n.SessionEntity.BindID(id)
	if n.Conn != nil {
		n.Conn.SessionEntity.BindID(id)
	}
}
