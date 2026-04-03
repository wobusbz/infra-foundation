package transport

import (
	"errors"
	"fmt"
	"infra-foundation/config"
	"infra-foundation/logx"
	"infra-foundation/metrics"
	"infra-foundation/packet"
	"infra-foundation/protomessage"
	"infra-foundation/session"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/proto"
)

type WriteCloser interface {
	Write(b []byte) (n int, err error)
	Close() error
}

type Conn struct {
	wc WriteCloser
	*session.SessionEntity
	*packet.Codec
	writeQ            chan []byte
	closed            atomic.Bool
	lastHeartBeatTime atomic.Int64
	wg                sync.WaitGroup
}

func NewConn(wc WriteCloser, id, uid int64) *Conn {
	c := &Conn{
		wc:            wc,
		writeQ:        make(chan []byte, config.Default.TransportWriteQueueSize),
		SessionEntity: session.NewSessionEntity(id, uid),
		Codec:         packet.NewCodec(),
	}
	c.wg.Go(c.writeLoop)
	return c
}

func (c *Conn) SetClosed() bool {
	return c.closed.CompareAndSwap(false, true)
}

func (c *Conn) IsClosed() bool {
	return c.closed.Load()
}

func (c *Conn) HeartbeatAt() int64 {
	return c.lastHeartBeatTime.Load()
}

func (c *Conn) SetHeartbeatAt(now int64) {
	c.lastHeartBeatTime.Store(now)
}

func (c *Conn) RefreshHeartbeat() {
	c.SetHeartbeatAt(time.Now().Unix())
}

func (c *Conn) Send(pb protomessage.ProtoMessage) error {
	if c.IsClosed() {
		return errors.New("[transport.Conn/Send] connection closed")
	}
	return c.SendTypePb(packet.Data, pb)
}

func (c *Conn) SendTypePb(typ packet.Type, pb protomessage.ProtoMessage) error {
	if c.IsClosed() {
		return errors.New("[transport.Conn/SendTypePb] connection closed")
	}
	pbdata, err := proto.Marshal(pb)
	if err != nil {
		return fmt.Errorf("[transport.Conn/SendTypePb] Marshal %w", err)
	}
	return c.SendPack(packet.New(typ, pb.MessageID(), pbdata))
}

func (c *Conn) SendData(data []byte) error {
	if c.IsClosed() {
		return errors.New("[transport.Conn/SendData] connection closed")
	}
	select {
	case c.writeQ <- data:
		return nil
	default:
		return errors.New("[transport.Conn/SendData] write queue full")
	}
}

func (c *Conn) SendPack(pack *packet.Packet) error {
	if c.IsClosed() {
		pack.Free()
		return errors.New("[transport.Conn/SendPack] connection closed")
	}
	data, err := c.Codec.Pack(pack.Type(), pack.ID(), pack.SID(), pack.Data())
	pack.Free()
	if err != nil {
		return fmt.Errorf("[transport.Conn/SendPack] Pack %w", err)
	}
	return c.SendData(data)
}

func (c *Conn) Notify(s []session.Session, pb protomessage.ProtoMessage) error {
	var errs []error
	for _, sv := range s {
		errs = append(errs, sv.Send(pb))
	}
	return errors.Join(errs...)
}

func (c *Conn) Close() error {
	if !c.SetClosed() {
		return nil
	}
	close(c.writeQ)
	c.wg.Wait()
	if c.wc != nil {
		if err := c.wc.Close(); err != nil {
			return err
		}
	}
	return nil
}

func (c *Conn) writeLoop() {
	for buf := range c.writeQ {
		if len(buf) == 0 {
			continue
		}
		_, err := c.wc.Write(buf)
		packet.TryPutPackBuffer(buf)
		if err != nil {
			logx.Err.Println(err)
			return
		}
		metrics.CounterOf("transport_packets_sent_total").Inc()
		metrics.CounterOf("transport_bytes_sent_total").Add(uint64(len(buf)))
		c.RefreshHeartbeat()
	}
}
