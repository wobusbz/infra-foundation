package cluster

import (
	"errors"
	"fmt"
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
	pbp "google.golang.org/protobuf/proto"
)

var _ session.Session = (*PeerConn)(nil)

type PeerConn struct {
	*session.SessionBase
	Conn              *transport.Conn
	codec             *protocol.Codec
	netpollConn       netpoll.Connection
	outHandler        *OutboundHandler
	node              *Node
	connMgr           *session.Manager
	scheduler         *scheduler.Scheduler
	heartbeatInterval time.Duration
	timerID           scheduler.TimerID
	heartbeatCounter  atomic.Int32
	isOutbound        bool
	queueKey          string
}

type peerMessenger struct {
	pc *PeerConn
}

func (m *peerMessenger) Send(pb message.Message) error {
	if m.pc.Conn.IsClosed() {
		return errors.New("[PeerConn/Send] connection closed")
	}
	return m.pc.sendProtoMessage(pb)
}

func (m *peerMessenger) Notify(targets []session.Session, pb message.Message) error {
	if len(targets) == 0 {
		return errors.New("[PeerConn/Notify] broadcast not supported on peer connections; specify explicit targets")
	}
	var errs []error
	for _, sv := range targets {
		errs = append(errs, sv.Send(pb))
	}
	return errors.Join(errs...)
}

func (m *peerMessenger) Close() error {
	if m.pc.Conn.IsClosed() {
		return nil
	}
	m.pc.node.PeerMgr().RemoveByID(m.pc.ID())
	if m.pc.isOutbound {
		m.pc.node.LoadBalancer().MarkHealthy(string(m.pc.ID()), false)
	}
	session.DefaultIDPool.Remove(m.pc.ID())
	m.pc.scheduler.CancelTimer(m.pc.timerID)
	return m.pc.Conn.Close()
}

func newPeerConn(node *Node, connMgr *session.Manager, scheduler *scheduler.Scheduler, isOutbound bool) *PeerConn {
	interval := config.Default.NetPollHeartbeatInterval
	if isOutbound {
		interval = config.Default.ClientHeartbeatInterval
	}
	return &PeerConn{
		node:              node,
		connMgr:           connMgr,
		scheduler:         scheduler,
		codec:             protocol.NewCodec(),
		isOutbound:        isOutbound,
		heartbeatInterval: interval,
	}
}

func (pc *PeerConn) bindConnection(conn netpoll.Connection, id session.SessionID) {
	pc.netpollConn = conn
	pc.Conn = transport.NewConn(conn, id)
	pc.Conn.SetHeartbeatAt(time.Now().Unix())
	pc.SessionBase = &session.SessionBase{
		SessionEntity: session.NewSessionEntity(pc.Conn.ID()),
		Messenger:     &peerMessenger{pc: pc},
	}
	pc.queueKey = string(id)
}

func NewPeerConn(node *Node, connMgr *session.Manager, scheduler *scheduler.Scheduler, connection netpoll.Connection, id session.SessionID) *PeerConn {
	pc := newPeerConn(node, connMgr, scheduler, false)
	pc.bindConnection(connection, id)
	connMgr.Store(pc)

	var err error
	pc.timerID, err = scheduler.PushEvery(pc.heartbeatInterval, pc.checkHeartbeat)
	if err != nil {
		logx.Err.Printf("[PeerConn] failed to start heartbeat timer: %v", err)
	}
	return pc
}

func NewOutboundPeerConn(peerMgr *session.Manager, scheduler *scheduler.Scheduler, node *Node, addr string, msgh *MessageHandler) (*PeerConn, error) {
	pc := newPeerConn(node, peerMgr, scheduler, true)
	pc.outHandler = NewOutboundHandler(msgh, pc)
	if err := pc.dialConnection(addr); err != nil {
		return nil, err
	}
	return pc, nil
}

func (pc *PeerConn) dialConnection(addr string) error {
	logx.Dbg.Printf("[PeerConn/dialConnection] dialing %s", addr)
	conn, err := netpoll.NewDialer().DialConnection("tcp", addr, config.Default.PeerDialTimeout)
	if err != nil {
		logx.Err.Printf("[PeerConn/dialConnection] failed to dial %s: %v", addr, err)
		return err
	}
	logx.Dbg.Printf("[PeerConn/dialConnection] connected to %s (local=%s, remote=%s)", addr, conn.LocalAddr(), conn.RemoteAddr())
	pc.bindConnection(conn, session.GenerateSessionID())
	pc.netpollConn.SetOnRequest(pc.outHandler.OnRequest)
	pc.timerID, err = pc.scheduler.PushEvery(pc.heartbeatInterval, pc.sendHeartbeat)
	if err != nil {
		logx.Err.Printf("[PeerConn/dialConnection] failed to start heartbeat timer: %v", err)
	}
	logx.Dbg.Printf("[PeerConn/dialConnection] setup complete for %s", addr)
	return nil
}

func (pc *PeerConn) checkHeartbeat() {
	lastHb := pc.Conn.HeartbeatAt()
	if time.Since(time.Unix(lastHb, 0)) < pc.heartbeatInterval*2 {
		pc.heartbeatCounter.Store(0)
		return
	}
	timeoutCount := pc.heartbeatCounter.Add(1)
	if timeoutCount >= config.Default.NetPollHeartbeatTimeoutCount {
		logx.Err.Printf("[PeerConn/checkHeartbeat] closing after %d timeouts", timeoutCount)
		pc.Close()
	}
}

func (pc *PeerConn) sendHeartbeat() {
	lastHb := pc.Conn.HeartbeatAt()
	if lastHb != 0 && time.Since(time.Unix(lastHb, 0)) < pc.heartbeatInterval {
		return
	}
	buf, err := pc.codec.Pack(protocol.ClusterHeartbeat, 0, string(pc.ID()), nil)
	if err != nil {
		failCount := pc.heartbeatCounter.Add(1)
		logx.War.Printf("[PeerConn/sendHeartbeat] failed %d times: %v", failCount, err)
		if failCount >= config.Default.ClientHeartbeatFailCount {
			logx.Err.Printf("[PeerConn/sendHeartbeat] closing after %d failures", failCount)
			pc.Close()
		}
		return
	}
	if err := pc.Conn.SendData(buf); err != nil {
		protocol.PutBuf(buf)
		failCount := pc.heartbeatCounter.Add(1)
		logx.War.Printf("[PeerConn/sendHeartbeat] failed %d times: %v", failCount, err)
		if failCount >= config.Default.ClientHeartbeatFailCount {
			logx.Err.Printf("[PeerConn/sendHeartbeat] closing after %d failures", failCount)
			pc.Close()
		}
		return
	}
	pc.heartbeatCounter.Store(0)
	pc.Conn.SetHeartbeatAt(time.Now().Unix())
}

func (pc *PeerConn) sendProtoMessage(pb message.Message) error {
	return pc.node.SendPb(pc, pc.codec, string(pc.ID()), pb)
}

func (pc *PeerConn) SendData(data []byte) error {
	return pc.Conn.SendData(data)
}

func (pc *PeerConn) SendTypePb(typ int8, pb message.Message) error {
	if pc.Conn.IsClosed() {
		return errors.New("[PeerConn/SendTypePb] connection closed")
	}
	data, err := pbp.Marshal(pb)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	buf, err := pc.codec.Pack(protocol.ClusterType(typ), pb.MessageID(), string(pc.ID()), data)
	if err != nil {
		return fmt.Errorf("pack: %w", err)
	}
	if err := pc.Conn.SendData(buf); err != nil {
		protocol.PutBuf(buf)
		return err
	}
	return nil
}

func (pc *PeerConn) BindID(id session.SessionID) {
	pc.SessionEntity.BindID(id)
	if pc.Conn != nil {
		pc.Conn.SessionEntity.BindID(id)
	}
}
