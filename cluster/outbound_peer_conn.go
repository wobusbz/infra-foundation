package cluster

import (
	"errors"
	"infra-foundation/config"
	"infra-foundation/logx"
	"infra-foundation/message"
	"infra-foundation/protocol"
	"infra-foundation/scheduler"
	"infra-foundation/session"
	"infra-foundation/transport"
	"sync/atomic"
	"time"

	"github.com/cloudwego/netpoll"
)

type OutboundPeerConn struct {
	*session.SessionBase
	Conn            *transport.Conn
	netpollConn     netpoll.Connection
	request         *OutboundHandler
	heartbeatTime   time.Duration
	timerID         scheduler.TimerID
	closed          atomic.Bool
	heartbeatFailed atomic.Int32
}

type clientMessenger struct {
	c *OutboundPeerConn
}

func (m *clientMessenger) Send(pb message.Message) error {
	if m.c.closed.Load() {
		return errors.New("[OutboundPeerConn/Send] connection closed")
	}
	return sendProtoMessage(&m.c.closed, "OutboundPeerConn", m.c.request.Node, m.c, m.c.Conn, string(m.c.ID()), pb)
}

func (m *clientMessenger) Notify(targets []session.Session, pb message.Message) error {
	if len(targets) == 0 {
		return m.c.request.ConnMgr.Range(func(s session.Session) error { return s.Send(pb) })
	}
	var errs []error
	for _, sv := range targets {
		errs = append(errs, sv.Send(pb))
	}
	return errors.Join(errs...)
}

func (m *clientMessenger) Close() error {
	if !m.c.closed.CompareAndSwap(false, true) {
		return nil
	}
	if m.c.IsPeerConn() {
		m.c.request.Node.PeerMgr().RemoveByID(m.c.ID())
		m.c.request.Node.LoadBalancer().MarkHealthy(string(m.c.ID()), false)
	}
	m.c.request.Scheduler.CancelTimer(m.c.timerID)
	return m.c.Conn.Close()
}

func NewOutboundPeerConn(svr Server) *OutboundPeerConn {
	c := &OutboundPeerConn{heartbeatTime: config.Default.ClientHeartbeatInterval}
	c.request = NewOutboundHandler(svr)
	c.request.OutboundPeerConn = c
	return c
}

func (c *OutboundPeerConn) DialConnection(addr string) error {
	logx.Dbg.Printf("[OutboundPeerConn/DialConnection] dialing %s", addr)
	conn, err := netpoll.NewDialer().DialConnection("tcp", addr, time.Second)
	if err != nil {
		logx.Err.Printf("[OutboundPeerConn/DialConnection] failed to dial %s: %v", addr, err)
		return err
	}
	logx.Dbg.Printf("[OutboundPeerConn/DialConnection] connected to %s (local=%s, remote=%s)", addr, conn.LocalAddr(), conn.RemoteAddr())
	c.netpollConn = conn
	c.Conn = transport.NewConn(conn, session.GenerateSessionID("cl"), -1)
	c.SessionBase = &session.SessionBase{
		SessionEntity: session.NewSessionEntity(c.Conn.ID(), -1),
		Messenger:     &clientMessenger{c: c},
	}
	c.netpollConn.SetOnRequest(c.request.OnRequest)
	c.timerID, _ = c.request.Scheduler.PushEvery(c.heartbeatTime, c.sendHeartbeat)
	logx.Dbg.Printf("[OutboundPeerConn/DialConnection] setup complete for %s, sending Conn packet", addr)
	return nil
}

func (c *OutboundPeerConn) sendHeartbeat() {
	now := time.Now().Unix()
	if c.Conn.HeartbeatAt()+int64(c.heartbeatTime.Seconds()) > now {
		return
	}
	pdata := protocol.New(protocol.Heartbeat, 0, nil)
	if err := c.Conn.SendPack(pdata); err != nil {
		failCount := c.heartbeatFailed.Add(1)
		logx.War.Printf("[OutboundPeerConn/sendHeartbeat] failed %d times: %v", failCount, err)

		if failCount >= config.Default.ClientHeartbeatFailCount {
			logx.Err.Printf("[OutboundPeerConn/sendHeartbeat] closing connection after %d failures", failCount)
			c.Close()
		}
		return
	}

	c.heartbeatFailed.Store(0)
	c.Conn.SetHeartbeatAt(now)
}

func (c *OutboundPeerConn) SendData(data []byte) error {
	if c.closed.Load() {
		return errors.New("[OutboundPeerConn/SendData] connection closed")
	}
	return c.Conn.SendData(data)
}

func (c *OutboundPeerConn) SendTypePb(typ int8, pb message.Message) error {
	if c.closed.Load() {
		return errors.New("[OutboundPeerConn/SendTypePb] connection closed")
	}
	return c.Conn.SendTypePb(typ, pb)
}

func (c *OutboundPeerConn) BindID(id session.SessionID) {
	c.SessionEntity.BindID(id)
	if c.Conn != nil {
		c.Conn.SessionEntity.BindID(id)
	}
}
