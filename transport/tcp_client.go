package transport

import (
	"context"
	"errors"
	"infra-foundation/config"
	"infra-foundation/logx"
	"infra-foundation/message"
	"infra-foundation/protocol"
	"infra-foundation/scheduler"
	"infra-foundation/session"

	"net"
	"sync"
	"time"

	pbp "google.golang.org/protobuf/proto"
)

type msgHandler func(*TCPClient, message.Message)

type TCPClient struct {
	*session.SessionBase
	Conn          *Conn
	conn          net.Conn
	handlers      map[int32]msgHandler
	msgs          map[int32]message.Message
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

func (m *tcpClientMessenger) Send(pb message.Message) error {
	if m.t.Conn.IsClosed() {
		return errors.New("[TCPClient/Send] connection closed")
	}
	return m.t.Conn.SendTypePb(int8(protocol.Request), pb)
}

func (m *tcpClientMessenger) Notify(targets []session.Session, pb message.Message) error {
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
		handlers:      map[int32]msgHandler{},
		msgs:          map[int32]message.Message{},
		scheduler:     scheduler.NewScheduler(),
		heartbeatTime: config.Default.TCPClientHeartbeatInterval,
	}
	t.ctx, t.cancel = context.WithCancel(context.TODO())
	return t
}

func (t *TCPClient) RegisterHandler(pb message.Message, handl msgHandler) {
	t.handlersrw.Lock()
	t.handlers[pb.MessageID()] = handl
	t.msgs[pb.MessageID()] = pb
	t.handlersrw.Unlock()
}

func (t *TCPClient) Dial(addr string) error {
	var err error
	t.conn, err = net.Dial("tcp", addr)
	if err != nil {
		return err
	}
	id := session.GenerateSessionID("tc")
	t.Conn = NewConn(t.conn, id, -1)
	t.SessionBase = &session.SessionBase{
		SessionEntity: session.NewSessionEntity(id, -1),
		Messenger:     &tcpClientMessenger{t: t},
	}
	t.timerID, _ = t.scheduler.PushEvery(t.heartbeatTime, t.sendHeartbeat)
	t.wg.Add(1)
	t.wg.Go(t.readerLoop)
	return err
}

func (t *TCPClient) Send(pb message.Message) error {
	if t.Conn.IsClosed() {
		return errors.New("[TCPClient/Send] connection closed")
	}
	return t.Conn.SendTypePb(int8(protocol.Request), pb)
}

func (t *TCPClient) SendTypePb(typ int8, pb message.Message) error {
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

func (t *TCPClient) SendPack(pack *protocol.Pkt) error {
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
			for _, pk := range pks {
				pk.Free()
			}
			logx.Dbg.Println(err)
			return
		}
		for _, pk := range pks {
			switch pk.Type() {
			case protocol.Request, protocol.Response, protocol.Push:
				t.handlersrw.RLock()
				pb, ok1 := t.msgs[pk.ID()]
				hd, ok2 := t.handlers[pk.ID()]
				t.handlersrw.RUnlock()
				if !ok1 || !ok2 {
					logx.Err.Printf("333 [TCPClient/ReaderLoop] message[%d] not found", pk.ID())
					logx.Err.Println(pk.String())
					pk.Free()
					continue
				}
				bpb := pbp.Clone(pb)
				if err = pbp.Unmarshal(pk.Data(), bpb); err != nil {
					logx.Err.Printf("444 [TCPClient/ReaderLoop] message[%d] pbp.Unmarshal error: %v", pk.ID(), err)
					pk.Free()
					continue
				}
				t.scheduler.PushTask(func() { hd(t, bpb.(message.Message)) })
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
	pdata := protocol.New(protocol.Heartbeat, 0, nil)
	defer pdata.Free()
	if err := t.Conn.SendPack(pdata); err != nil {
		t.Conn.Close()
		return
	}
	t.Conn.SetHeartbeatAt(now)
}
