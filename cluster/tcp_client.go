package cluster

import (
	"context"
	"errors"
	"fmt"
	"infra-foundation/logx"
	"infra-foundation/packet"
	"infra-foundation/protomessage"
	"infra-foundation/scheduler"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/proto"
)

type handler func(*TCPClient, protomessage.ProtoMessage)

type TCPClient struct {
	conn              net.Conn
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
		codec:         packet.NewPackCodec(),
		handlers:      map[int32]handler{},
		msgs:          map[int32]protomessage.ProtoMessage{},
		writeC:        make(chan []byte, 1<<8),
		scheduler:     scheduler.NewScheduler(),
		heartbeatTime: time.Second * 3,
	}
	t.ctx, t.cancel = context.WithCancel(context.TODO())
	return t
}

func (t *TCPClient) SetClosed() bool { return t.closed.CompareAndSwap(false, true) }

func (t *TCPClient) IsClosed() bool { return t.closed.Load() }

func (t *TCPClient) HeartbeatAt() int64 { return t.lastHeartbeatTime.Load() }

func (t *TCPClient) SetHeartbeatAt(now int64) { t.lastHeartbeatTime.Store(now) }

func (t *TCPClient) RefreshHeartbeat() { t.SetHeartbeatAt(time.Now().Unix()) }

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
	t.timerID, _ = t.scheduler.PushEvery(t.heartbeatTime, t.sendHeartbeat)
	t.wg.Go(t.writeLoop)
	t.wg.Go(t.readerLoop)
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
		pack.Free()
		return errors.New("[TCPClient/SendPack] connection closed")
	}
	bdata, err := t.codec.Pack(pack.Type(), pack.ID(), pack.SID(), pack.Data())
	pack.Free()
	if err != nil {
		return fmt.Errorf("[TCPClient/Send] Pack %w", err)
	}
	return t.SendData(bdata)
}

func (t *TCPClient) readerLoop() {
	var bdata = make([]byte, 2048)
	for {
		n, err := t.conn.Read(bdata)
		if err != nil {
			logx.Err.Println(err)
			return
		}
		pks, err := t.codec.Unpack(bdata[:n])
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
		t.RefreshHeartbeat()
	}
}

func (t *TCPClient) Close() error {
	if !t.SetClosed() {
		return nil
	}
	t.cancel()
	t.wg.Wait()
	close(t.writeC)
	t.scheduler.CancelTimer(t.timerID)
	t.scheduler.Stop()
	return t.conn.Close()
}

func (t *TCPClient) sendHeartbeat() {
	now := time.Now().Unix()
	if t.HeartbeatAt()+int64(t.heartbeatTime.Seconds()) > now {
		return
	}
	if err := t.SendPack(packet.New(packet.Heartbeat, 0, nil)); err != nil {
		t.Close()
		return
	}
	t.SetHeartbeatAt(now)
}

func (t *TCPClient) writeLoop() {
	for {
		select {
		case <-t.ctx.Done():
			return
		case bdata, ok := <-t.writeC:
			if !ok {
				return
			}
			for off := 0; off < len(bdata); {
				n, err := t.conn.Write(bdata[off:])
				if err != nil {
					logx.Err.Printf("[TCPClient/writeLoop] write error: %v", err)
					return
				}
				off += n
			}
		}
	}
}
