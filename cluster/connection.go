package cluster

import (
	"errors"
	"fmt"
	"infra-foundation/logx"
	"infra-foundation/packet"
	"infra-foundation/protomessage"
	"infra-foundation/queue"
	"infra-foundation/session"
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
	writeQ            *queue.Queue[[]byte]
	writeCond         *sync.Cond
	closed            atomic.Bool
	lastHeartBeatTime atomic.Int64
	wg                sync.WaitGroup
}

func NewConnection(conn netpoll.Connection, id, uid int64) *Connection {
	c := &Connection{
		Connection:      conn,
		writeQ:          queue.New[[]byte](),
		writeCond:       sync.NewCond(&sync.Mutex{}),
		NetworkEntities: session.NewNetworkEntities(id, uid),
		PackCodec:       packet.NewPackCodec(),
	}
	c.wg.Go(c.writeLoop)
	return c
}

func (c *Connection) SetClosed() bool {
	return c.closed.CompareAndSwap(false, true)
}

func (c *Connection) IsClosed() bool {
	return c.closed.Load()
}

func (c *Connection) HeartbeatAt() int64 {
	return c.lastHeartBeatTime.Load()
}

func (c *Connection) SetHeartbeatAt(now int64) {
	c.lastHeartBeatTime.Store(now)
}

func (c *Connection) RefreshHeartbeat() {
	c.SetHeartbeatAt(time.Now().Unix())
}

func (c *Connection) Send(pb protomessage.ProtoMessage) error {
	if c.IsClosed() {
		return errors.New("[Connection/Send] connection closed")
	}
	if defaultNodeAgent.node.Name == pb.NodeName() {
		return c.SendTypePb(packet.Data, pb)
	}
	pbdata, err := proto.Marshal(pb)
	if err != nil {
		return fmt.Errorf("[Connection/Send] Marshal %w", err)
	}
	return remoteCall(c, c.PackCodec, packet.NewInternal(packet.InternalData, pb.MessageID(), c.ID(), pbdata), pb.NodeName())
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

func (c *Connection) SendData(bdata []byte) error {
	if c.IsClosed() {
		return errors.New("[Connection/SendData] connection closed")
	}
	c.writeCond.L.Lock()
	c.writeQ.Push(bdata)
	c.writeCond.Signal()
	c.writeCond.L.Unlock()
	return nil
}

func (c *Connection) SendPack(pack *packet.Packet) error {
	if c.IsClosed() {
		return errors.New("[Connection/SendPack] connection closed")
	}
	bdata, err := c.PackCodec.Pack(pack.Type(), pack.ID(), pack.SID(), pack.Data())
	if err != nil {
		return fmt.Errorf("[Connection/Send] Pack %w", err)
	}
	return c.SendData(bdata)
}

func (c *Connection) Notify(s []*session.Session, pb protomessage.ProtoMessage) error {
	return nil
}

func (c *Connection) Close() error {
	if !c.SetClosed() {
		return nil
	}

	c.writeCond.L.Lock()
	c.writeCond.Signal()
	c.writeCond.L.Unlock()

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
		c.writeCond.L.Lock()
		for c.writeQ.Empty() && !c.IsClosed() {
			c.writeCond.Wait()
		}
		if c.IsClosed() {
			c.writeCond.L.Unlock()
			return
		}
		bdaba, _ := c.writeQ.PopSingleThread()
		c.writeCond.L.Unlock()

		if len(bdaba) == 0 {
			continue
		}
		_, err := c.Connection.Write(bdaba)
		if err != nil {
			logx.Err.Println(err)
			return
		}
		c.RefreshHeartbeat()
	}
}
