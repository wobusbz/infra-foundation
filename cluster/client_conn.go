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

// ClientConn 是基于 netpoll 的客户端连接封装（用于节点间互联）。
type ClientConn struct {
	*session.Base
	Conn            *transport.Conn
	netpollConn     netpoll.Connection
	request         *ClientHandler
	heartbeatTime   time.Duration
	timerID         scheduler.TimerID
	closed          atomic.Bool
	heartbeatFailed atomic.Int32
}

type clientMessenger struct {
	c *ClientConn
}

func (m *clientMessenger) Send(pb protomessage.ProtoMessage) error {
	if m.c.closed.Load() {
		return errors.New("[ClientConn/Send] connection closed")
	}
	localNode := m.c.request.node.LocalNode()
	if localNode != nil && localNode.Name == pb.NodeName() {
		return m.c.Conn.SendTypePb(packet.Data, pb)
	}
	pbdata, err := proto.Marshal(pb)
	if err != nil {
		return fmt.Errorf("[ClientConn/Send] Marshal %w", err)
	}
	return remoteCallWithAgent(m.c.request.node, m.c, m.c.Conn.Codec, packet.NewInternal(packet.InternalData, pb.MessageID(), m.c.ID(), pbdata), pb.NodeName())
}

func (m *clientMessenger) Notify(targets []session.Session, pb protomessage.ProtoMessage) error {
	if len(targets) == 0 {
		return m.c.request.node.ctx.ConnManager().Range(func(s session.Session) error { return s.Send(pb) })
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
	if m.c.UID() == -1 {
		m.c.request.node.NodeConnManager().RemoveByID(m.c.ID())
	}
	m.c.request.scheduler.CancelTimer(m.c.timerID)
	return m.c.Conn.Close()
}

func NewClientConn(svr Server) *ClientConn {
	c := &ClientConn{heartbeatTime: config.Default.ClientHeartbeatInterval}
	c.request = NewClientHandler(svr)
	c.request.ClientConn = c
	return c
}

func (c *ClientConn) DialConnection(addr string) error {
	conn, err := netpoll.NewDialer().DialConnection("tcp", addr, time.Second)
	if err != nil {
		logx.Err.Println(err)
		return err
	}
	c.netpollConn = conn
	c.Conn = transport.NewConn(conn, 1, -1)
	c.Base = &session.Base{
		SessionEntity: session.NewSessionEntity(1, -1),
		Messenger:       &clientMessenger{c: c},
	}
	c.netpollConn.SetOnRequest(c.request.OnRequest)
	c.timerID, _ = c.request.scheduler.PushEvery(c.heartbeatTime, c.sendHeartbeat)
	return nil
}

func (c *ClientConn) sendHeartbeat() {
	now := time.Now().Unix()
	if c.Conn.HeartbeatAt()+int64(c.heartbeatTime.Seconds()) > now {
		return
	}
	if err := c.Conn.SendPack(packet.New(packet.Heartbeat, 0, nil)); err != nil {
		failCount := c.heartbeatFailed.Add(1)
		logx.War.Printf("[ClientConn/sendHeartbeat] failed %d times: %v", failCount, err)

		if failCount >= config.Default.ClientHeartbeatFailCount {
			logx.Err.Printf("[ClientConn/sendHeartbeat] closing connection after %d failures", failCount)
			c.Close()
		}
		return
	}

	c.heartbeatFailed.Store(0)
	c.Conn.SetHeartbeatAt(now)
}
