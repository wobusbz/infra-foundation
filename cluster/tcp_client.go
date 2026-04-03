package cluster

import (
	"context"
	"errors"
	"infra-foundation/config"
	"infra-foundation/logx"
	"infra-foundation/packet"
	"infra-foundation/protomessage"
	"infra-foundation/scheduler"
	"infra-foundation/session"
	"infra-foundation/transport"
	"net"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"
)

type handler func(*TCPClient, protomessage.ProtoMessage)

// TCPClient 是基于标准 net.Conn 的客户端实现。
type TCPClient struct {
	*session.Base
	Conn          *transport.Conn
	conn          net.Conn
	handlers      map[int32]handler
	msgs          map[int32]protomessage.ProtoMessage
	handlersrw    sync.RWMutex
	timerID       scheduler.TimerID
	scheduler     *scheduler.Scheduler
	heartbeatTime time.Duration
	wg            sync.WaitGroup
	ctx           context.Context
	cancel        context.CancelFunc
}

type tcpClientMessenger struct {
	t *TCPClient
}

func (m *tcpClientMessenger) Send(pb protomessage.ProtoMessage) error {
	if m.t.Conn.IsClosed() {
		return errors.New("[TCPClient/Send] connection closed")
	}
	return m.t.Conn.SendTypePb(packet.Data, pb)
}

func (m *tcpClientMessenger) Notify(targets []session.Session, pb protomessage.ProtoMessage) error {
	var errs []error
	for _, sv := range targets {
		errs = append(errs, sv.Send(pb))
	}
	return errors.Join(errs...)
}

func (m *tcpClientMessenger) Close() error {
	m.t.cancel()
	m.t.scheduler.CancelTimer(m.t.timerID)
	err := m.t.Conn.Close()
	m.t.wg.Wait()
	m.t.scheduler.Stop()
	return err
}

func NewTCPClient() *TCPClient {
	t := &TCPClient{
		handlers:      map[int32]handler{},
		msgs:          map[int32]protomessage.ProtoMessage{},
		scheduler:     scheduler.NewScheduler(),
		heartbeatTime: config.Default.TCPClientHeartbeatInterval,
	}
	t.ctx, t.cancel = context.WithCancel(context.TODO())
	return t
}

func (t *TCPClient) RegisterHandler(pb protomessage.ProtoMessage, handl handler) {
	t.handlersrw.Lock()
	t.handlers[pb.MessageID()] = handl
	t.msgs[pb.MessageID()] = pb
	t.handlersrw.Unlock()
}

func (t *TCPClient) DialConnection(addr string) error {
	var err error
	t.conn, err = net.Dial("tcp", addr)
	if err != nil {
		return err
	}
	t.Conn = transport.NewConn(t.conn, 1, -1)
	t.Base = &session.Base{
		SessionEntity: session.NewSessionEntity(1, -1),
		Messenger:     &tcpClientMessenger{t: t},
	}
	t.timerID, _ = t.scheduler.PushEvery(t.heartbeatTime, t.sendHeartbeat)
	t.wg.Add(1)
	t.wg.Go(t.readerLoop)
	return err
}

func (t *TCPClient) Send(pb protomessage.ProtoMessage) error {
	if t.Conn.IsClosed() {
		return errors.New("[TCPClient/Send] connection closed")
	}
	return t.Conn.SendTypePb(packet.Data, pb)
}

func (t *TCPClient) SendTypePb(typ packet.Type, pb protomessage.ProtoMessage) error {
	if t.Conn.IsClosed() {
		return errors.New("[TCPClient/SendTypePb] connection closed")
	}
	return t.Conn.SendTypePb(typ, pb)
}

func (t *TCPClient) SendData(data []byte) error {
	if t.Conn.IsClosed() {
		return errors.New("[TCPClient/SendData] connection closed")
	}
	return t.Conn.SendData(data)
}

func (t *TCPClient) SendPack(pack *packet.Packet) error {
	if t.Conn.IsClosed() {
		pack.Free()
		return errors.New("[TCPClient/SendPack] connection closed")
	}
	return t.Conn.SendPack(pack)
}

func (t *TCPClient) readerLoop() {
	defer t.wg.Done()
	var buf = make([]byte, 2048)
	for {
		select {
		case <-t.ctx.Done():
			return
		default:
		}
		n, err := t.conn.Read(buf)
		if err != nil {
			logx.Err.Println(err)
			return
		}
		pks, err := t.Conn.Codec.Unpack(buf[:n])
		if err != nil {
			logx.Dbg.Println(err)
			return
		}
		for _, pk := range pks {
			switch pk.Type() {
			case packet.Data:
				t.handlersrw.RLock()
				pb, ok1 := t.msgs[pk.ID()]
				hd, ok2 := t.handlers[pk.ID()]
				t.handlersrw.RUnlock()
				if !ok1 || !ok2 {
					pk.Free()
					logx.Err.Printf("333 [TCPClient/ReaderLoop] message[%d] not found", pk.ID())
					return
				}
				bpb := proto.Clone(pb)
				if err = proto.Unmarshal(pk.Data(), bpb); err != nil {
					logx.Err.Printf("444 [TCPClient/ReaderLoop] message[%d] proto Unmarshal error: %v", pk.ID(), err)
					pk.Free()
					return
				}
				t.scheduler.PushTask(func() { hd(t, bpb.(protomessage.ProtoMessage)) })
			}
			pk.Free()
		}
		t.Conn.RefreshHeartbeat()
	}
}

func (t *TCPClient) Close() error {
	if !t.Conn.SetClosed() {
		return nil
	}
	return t.Messenger.Close()
}

func (t *TCPClient) sendHeartbeat() {
	now := time.Now().Unix()
	if t.Conn.HeartbeatAt()+int64(t.heartbeatTime.Seconds()) > now {
		return
	}
	if err := t.Conn.SendPack(packet.New(packet.Heartbeat, 0, nil)); err != nil {
		t.Conn.Close()
		return
	}
	t.Conn.SetHeartbeatAt(now)
}
