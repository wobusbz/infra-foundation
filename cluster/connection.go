package cluster

import (
	"context"
	"errors"
	"fmt"
	"infra-foundation/logx"
	"infra-foundation/packet"
	"infra-foundation/protomessage"
	"infra-foundation/queue"
	"infra-foundation/session"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cloudwego/netpoll"
	"google.golang.org/protobuf/proto"
)

type Connection struct {
	netpoll.Connection
	*session.NetworkEntities
	*packet.PackCodec
	closed            atomic.Bool
	writeQ            *queue.Queue[[]byte]
	writeQCond        *sync.Cond
	lastHeartBeatTime atomic.Int64
	ctx               context.Context
	cancel            context.CancelFunc
	wg                sync.WaitGroup
}

func NewConnection(conn netpoll.Connection, id, uid int64) *Connection {
	c := &Connection{
		Connection:      conn,
		NetworkEntities: session.NewNetworkEntities(id, uid),
		PackCodec:       packet.NewPackCodec(),
		writeQ:          queue.New[[]byte](),
		writeQCond:      sync.NewCond(&sync.Mutex{}),
	}
	c.ctx, c.cancel = context.WithCancel(context.TODO())
	c.wg.Go(c.writeLoop)
	return c
}

func (c *Connection) SetClosed() bool {
	return c.closed.CompareAndSwap(false, true)
}

func (c *Connection) IsClosed() bool {
	return c.closed.Load()
}

func (c *Connection) GetLastHeartBeatTime() int64 {
	return c.lastHeartBeatTime.Load()
}

func (c *Connection) SetLastHeartBeatTime(now int64) {
	c.lastHeartBeatTime.Store(now)
}

func (c *Connection) UdtHeartBeatTime() {
	c.SetLastHeartBeatTime(time.Now().Unix())
}

func (c *Connection) Send(pb protomessage.ProtoMessage) error {
	if c.IsClosed() {
		return errors.New("[Connection/Send] connection closed")
	}
	if defaultNodeAgent.node.Name == pb.NodeName() {
		return c.SendTypePb(packet.Data, pb)
	}
	conn, err := defaultNodeAgent.getNodeByName(c, pb.NodeName())
	if err != nil {
		return err
	}
	pbdata, err := proto.Marshal(pb)
	if err != nil {
		return fmt.Errorf("[Connection/Send] Marshal %w", err)
	}
	return conn.SendPack(packet.NewInternal(packet.InternalData, pb.MessageID(), c.ID(), pbdata))
}

func (c *Connection) SendPb(typ packet.Type, id int32, pb protomessage.ProtoMessage) error {
	if c.IsClosed() {
		return errors.New("[Connection/SendPb] connection closed")
	}
	pbdata, err := proto.Marshal(pb)
	if err != nil {
		return fmt.Errorf("[Connection/SendPb] Marshal %w", err)
	}
	return c.SendPack(packet.New(typ, id, pbdata))
}

func (c *Connection) SendTypePb(typ packet.Type, pb protomessage.ProtoMessage) error {
	if c.IsClosed() {
		return errors.New("[Connection/Send] connection closed")
	}
	pbdata, err := proto.Marshal(pb)
	if err != nil {
		return fmt.Errorf("[Connection/Send] Marshal %w", err)
	}
	return c.SendPack(packet.New(typ, pb.MessageID(), pbdata))
}

func (c *Connection) SendData(data []byte) error {
	if c.IsClosed() {
		return errors.New("[Connection/SendData] connection closed")
	}
	c.writeQCond.L.Lock()
	c.writeQ.Push(data)
	c.writeQCond.Signal()
	c.writeQCond.L.Unlock()
	return nil
}

func (c *Connection) SendPack(pack *packet.Packet) error {
	if c.IsClosed() {
		pack.Free()
		return errors.New("[Connection/SendPack] connection closed")
	}
	bdata, err := c.PackCodec.Pack(pack.Type(), pack.ID(), pack.SID(), pack.Data())
	pack.Free()
	if err != nil {
		return fmt.Errorf("[Connection/Send] Pack %w", err)
	}
	return c.SendData(bdata)
}

func (c *Connection) Close() error {
	if !c.SetClosed() {
		return errors.New("[Connection/Close] connection closed")
	}
	c.cancel()

	c.writeQCond.L.Lock()
	c.writeQCond.Signal()
	c.writeQCond.L.Unlock()

	c.wg.Wait()
	if c.Connection != nil {
		if err := c.Connection.Close(); err != nil {
			return err
		}
	}
	return nil
}

func (c *Connection) writeLoop() {
	defer func() { c.Close() }()
	for {
		select {
		case <-c.ctx.Done():
			return
		default:
		}
		c.writeQCond.L.Lock()
		if c.writeQ.Empty() {
			runtime.Gosched()
			c.writeQCond.Wait()
		}
		bdaba, _ := c.writeQ.PopSingleThread()
		c.writeQCond.L.Unlock()
		if len(bdaba) == 0 {
			continue
		}
		_, err := c.Connection.Write(bdaba)
		if err != nil {
			logx.Err.Println(err)
			return
		}
		c.UdtHeartBeatTime()
	}
}
