package cluster

import (
	"context"
	"errors"
	"fmt"
	"infra-foundation/logx"
	"infra-foundation/packet"
	"infra-foundation/pcall"
	"infra-foundation/protomessage"
	"infra-foundation/scheduler"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cloudwego/netpoll"
	"google.golang.org/protobuf/proto"
)

type handler func(*TCPClient, protomessage.ProtoMessage)

type TCPClient struct {
	conn              netpoll.Connection
	codec             *packet.PackCodec
	handlers          map[int32]handler
	msgs              map[int32]protomessage.ProtoMessage
	handlersrw        sync.RWMutex
	closed            atomic.Bool
	writeC            chan []byte
	timerID           scheduler.TimerID
	scheduler         *scheduler.Scheduler
	heartbeatTime     time.Duration
	lastHeartbeatTime atomic.Int64
	wg                sync.WaitGroup
	ctx               context.Context
	cancel            context.CancelFunc
}

func NewTCPClient() *TCPClient {
	t := &TCPClient{
		codec:     packet.NewPackCodec(),
		handlers:  map[int32]handler{},
		msgs:      map[int32]protomessage.ProtoMessage{},
		writeC:    make(chan []byte, 1<<8),
		scheduler: scheduler.NewScheduler(),
	}
	t.ctx, t.cancel = context.WithCancel(context.TODO())
	return t
}

func (t *TCPClient) SetClosed() bool { return t.closed.CompareAndSwap(false, true) }

func (t *TCPClient) IsClosed() bool { return t.closed.Load() }

func (t *TCPClient) GetLastHeartBeatTime() int64 { return t.lastHeartbeatTime.Load() }

func (t *TCPClient) SetLastHeartBeatTime(now int64) { t.lastHeartbeatTime.Store(now) }

func (t *TCPClient) UdtLastHeartBeatTime() { t.SetLastHeartBeatTime(time.Now().Unix()) }

func (t *TCPClient) RegisterHandler(pb protomessage.ProtoMessage, handl handler) {
	t.handlersrw.Lock()
	t.handlers[pb.MessageID()] = handl
	t.msgs[pb.MessageID()] = pb
	t.handlersrw.Unlock()
}

func (t *TCPClient) DialConnection(addr string) error {
	var err error
	t.conn, err = netpoll.NewDialer().DialConnection("tcp", addr, time.Second)
	if err != nil {
		return err
	}
	t.conn.SetOnRequest(t.OnRequest)
	t.timerID, _ = t.scheduler.PushEvery(t.heartbeatTime, t.sendHeartBeat)
	t.wg.Go(t.writeLoop)
	return err
}

func (t *TCPClient) Send(pb protomessage.ProtoMessage) error {
	if t.IsClosed() {
		return errors.New("[TCPClient/Send] connection closed")
	}
	return t.SendTypePb(packet.Data, pb)
}

func (t *TCPClient) SendTypePb(typ packet.Type, pb protomessage.ProtoMessage) error {
	if t.IsClosed() {
		return errors.New("[TCPClient/SendTypePb] connection closed")
	}
	pbdata, err := proto.Marshal(pb)
	if err != nil {
		return fmt.Errorf("[TCPClient/SendTypePb] Marshal %w", err)
	}
	return t.SendPack(packet.New(typ, pb.MessageID(), pbdata))
}

func (t *TCPClient) SendData(data []byte) error {
	if t.IsClosed() {
		return errors.New("[TCPClient/SendData] connection closed")
	}
	ctx, cancel := context.WithTimeout(t.ctx, time.Second)
	defer cancel()
	select {
	case t.writeC <- data:
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

func (t *TCPClient) SendPack(pack *packet.Packet) error {
	if t.IsClosed() {
		return errors.New("[TCPClient/SendPack] connection closed")
	}
	bdata, err := t.codec.Pack(pack)
	if err != nil {
		return fmt.Errorf("[TCPClient/Send] Pack %w", err)
	}
	return t.SendData(bdata)
}

func (t *TCPClient) OnRequest(ctx context.Context, connection netpoll.Connection) error {
	reader := connection.Reader()
	buf, err := reader.Next(reader.Len())
	if err != nil {
		logx.Err.Println(err)
		return fmt.Errorf("[TCPClient/OnRequest] Peek error %v", err)
	}
	pks, err := t.codec.Unpack(buf)
	if err != nil {
		logx.Err.Println(err)
		return fmt.Errorf("[TCPClient/OnRequest] Unpack error %v", err)
	}
	var errs []error
	for _, pk := range pks {
		switch pk.Type() {
		case packet.Connection:
		case packet.Data:
			t.handlersrw.RLock()
			pb, ok1 := t.msgs[pk.ID()]
			hd, ok2 := t.handlers[pk.ID()]
			t.handlersrw.RUnlock()
			if !ok1 || !ok2 {
				errs = append(errs, fmt.Errorf("[TCPClient/OnRequest] message[%d] not found", pk.ID()))
				pk.Free()
				continue
			}
			bpb := proto.Clone(pb)
			if err = proto.Unmarshal(pk.Data(), bpb); err != nil {
				errs = append(errs, fmt.Errorf("[TCPClient/OnRequest] message[%d] proto Unmarshal error: %w", pk.ID(), err))
				pk.Free()
				continue
			}
			pcall.PcallF0(func() { hd(t, bpb.(protomessage.ProtoMessage)) })
		case packet.BindConnection:
		case packet.DisConnection:
		}
		pk.Free()
	}
	err = errors.Join(errs...)
	if err != nil {
		logx.Err.Println(err)
	}
	return nil
}

func (t *TCPClient) Close() error {
	if !t.SetClosed() {
		return nil
	}
	t.cancel()
	close(t.writeC)
	t.wg.Wait()
	t.scheduler.CancelTimer(t.timerID)
	t.scheduler.Stop()
	return t.conn.Close()
}

func (t *TCPClient) sendHeartBeat() {
	now := time.Now().Unix()
	if t.GetLastHeartBeatTime()+int64(t.heartbeatTime.Seconds()) > now {
		return
	}
	if err := t.SendPack(packet.New(packet.Heartbeat, 0, nil)); err != nil {
		t.Close()
		return
	}
	t.SetLastHeartBeatTime(now)
}

func (t *TCPClient) writeLoop() {
	defer t.Close()
	for {
		select {
		case <-t.ctx.Done():
			return
		case bdata := <-t.writeC:
			_, err := t.conn.Write(bdata)
			if err != nil {
				logx.Err.Println(err)
			}
		}
	}
}
