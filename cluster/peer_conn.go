package cluster

import (
	"context"
	"errors"
	"fmt"
	"infra-foundation/config"
	"infra-foundation/logx"
	"infra-foundation/message"
	"infra-foundation/protocol"
	"infra-foundation/queue"
	"infra-foundation/scheduler"
	"infra-foundation/session"
	"infra-foundation/transport"
	"sync/atomic"
	"time"

	"github.com/cloudwego/netpoll"
	pbp "google.golang.org/protobuf/proto"
)

var _ NodeConn = (*PeerConn)(nil)

type PeerConn struct {
	*session.SessionCore
	Conn              *transport.Conn
	codec             *protocol.ClusterCodec
	netpollConn       netpoll.Connection
	outHandler        *OutboundHandler
	lifecycle         *PeerLifecycle
	heartbeatInterval time.Duration
	timerID           scheduler.TimerID
	heartbeatCounter  atomic.Int32
	isOutbound        bool
	queueKey          string
	closed            atomic.Bool
}

func (pc *PeerConn) Send(pb message.Message) error {
	if pc.Conn.IsClosed() {
		return errors.New("[PeerConn/Send] connection closed")
	}
	return pc.sendProtoMessage(pb)
}

func (pc *PeerConn) Notify(targets []session.Session, pb message.Message) error {
	if len(targets) == 0 {
		return errors.New("[PeerConn/Notify] broadcast not supported on peer connections; specify explicit targets")
	}
	var errs []error
	for _, sv := range targets {
		errs = append(errs, sv.Send(pb))
	}
	return errors.Join(errs...)
}

func (pc *PeerConn) Close() error {
	if !pc.closed.CompareAndSwap(false, true) {
		return nil
	}
	return pc.lifecycle.OnClose(pc)
}

func (pc *PeerConn) SendData(data []byte) error {
	return pc.Conn.SendData(data)
}

func newPeerConn(isOutbound bool) *PeerConn {
	interval := config.Default.NetPollHeartbeatInterval
	if isOutbound {
		interval = config.Default.ClientHeartbeatInterval
	}
	return &PeerConn{
		codec:             protocol.NewClusterCodec(),
		isOutbound:        isOutbound,
		heartbeatInterval: interval,
	}
}

func (pc *PeerConn) bindConnection(conn netpoll.Connection, id session.SessionID) {
	pc.netpollConn = conn
	pc.Conn = transport.NewConn(conn, string(id))
	pc.Conn.SetBufReleaser(protocolBufReleaser{})
	pc.Conn.SetHeartbeatAt(time.Now().Unix())
	pc.SessionCore = session.NewSessionCore(session.SessionID(pc.Conn.SessionID()))
	pc.queueKey = string(id)
}

func NewPeerConn(lifecycle *PeerLifecycle, connection netpoll.Connection, id session.SessionID) *PeerConn {
	pc := newPeerConn(false)
	pc.bindConnection(connection, id)
	pc.lifecycle = lifecycle
	lifecycle.Register(pc)
	lifecycle.StartHeartbeat(pc, pc.checkHeartbeat)
	return pc
}

func NewOutboundPeerConn(lifecycle *PeerLifecycle, addr string, router *clusterMsgRouter, taskQueue *queue.TaskQueue) (*PeerConn, error) {
	pc := newPeerConn(true)
	pc.outHandler = NewOutboundHandler(pc, router, taskQueue)
	pc.lifecycle = lifecycle
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
	pc.lifecycle.StartHeartbeat(pc, pc.sendHeartbeat)
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
	buf, err := pc.codec.Pack(protocol.ClusterHeartbeat, 0, 0, string(pc.ID()), nil)
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
	data, err := pbp.Marshal(pb)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	buf, err := pc.codec.Pack(protocol.ClusterRequest, pb.MessageID(), 0, string(pc.ID()), data)
	if err != nil {
		return fmt.Errorf("pack: %w", err)
	}
	if err := pc.Conn.SendData(buf); err != nil {
		protocol.PutBuf(buf)
		return err
	}
	return nil
}

func (pc *PeerConn) SendTypePb(typ int8, pb message.Message) error {
	if pc.Conn.IsClosed() {
		return errors.New("[PeerConn/SendTypePb] connection closed")
	}
	data, err := pbp.Marshal(pb)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	buf, err := pc.codec.Pack(protocol.ClusterType(typ), pb.MessageID(), 0, string(pc.ID()), data)
	if err != nil {
		return fmt.Errorf("pack: %w", err)
	}
	if err := pc.Conn.SendData(buf); err != nil {
		protocol.PutBuf(buf)
		return err
	}
	return nil
}

type OutboundHandler struct {
	peer      *PeerConn
	router    *clusterMsgRouter
	taskQueue *queue.TaskQueue
}

func NewOutboundHandler(peer *PeerConn, router *clusterMsgRouter, taskQueue *queue.TaskQueue) *OutboundHandler {
	return &OutboundHandler{peer: peer, router: router, taskQueue: taskQueue}
}

func (h *OutboundHandler) OnRequest(ctx context.Context, connection netpoll.Connection) error {
	return processClusterMessage(h.peer, h.router, h.taskQueue, connection, "OutboundHandler")
}
