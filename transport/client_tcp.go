package transport

import (
	"context"
	"errors"
	"infra-foundation/logx"
	"infra-foundation/message"
	"infra-foundation/protocol"
	"infra-foundation/scheduler"
	"infra-foundation/session"
	"net"
	"sync"

	pbp "google.golang.org/protobuf/proto"
)

type clientMsgHandler func(*ClientTCP, message.Message)

type ClientTCP struct {
	*session.SessionBase
	conn           net.Conn
	handlers       map[int32]clientMsgHandler
	msgs           map[int32]message.Message
	handlersrw     sync.RWMutex
	scheduler      *scheduler.Scheduler
	wg             sync.WaitGroup
	ctx            context.Context
	cancel         context.CancelFunc
	clientProtocol protocol.ClientProtocol
	closed         bool
	mu             sync.Mutex
}

func NewClientTCP() *ClientTCP {
	return NewClientTCPWithProtocol(protocol.NewClientCodec())
}

func NewClientTCPWithProtocol(p protocol.ClientProtocol) *ClientTCP {
	c := &ClientTCP{
		handlers:       map[int32]clientMsgHandler{},
		msgs:           map[int32]message.Message{},
		scheduler:      scheduler.NewScheduler(),
		clientProtocol: p,
	}
	c.ctx, c.cancel = context.WithCancel(context.Background())
	return c
}

func (c *ClientTCP) RegisterHandler(pb message.Message, h clientMsgHandler) {
	c.handlersrw.Lock()
	c.handlers[pb.MessageID()] = h
	c.msgs[pb.MessageID()] = pb
	c.handlersrw.Unlock()
}

func (c *ClientTCP) Dial(addr string) error {
	var err error
	c.conn, err = net.Dial("tcp", addr)
	if err != nil {
		return err
	}
	id := session.GenerateSessionID("cc")
	c.SessionBase = &session.SessionBase{
		SessionEntity: session.NewSessionEntity(id, -1),
		Messenger:     &clientTCPMessenger{c: c},
	}
	c.wg.Add(1)
	go c.readerLoop()
	return nil
}

func (c *ClientTCP) Send(pb message.Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errors.New("[ClientTCP/Send] connection closed")
	}
	data, err := pbp.Marshal(pb)
	if err != nil {
		return err
	}
	pack := c.clientProtocol.Pack(pb.MessageID(), data)
	_, err = c.conn.Write(pack)
	return err
}

func (c *ClientTCP) SendData(data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errors.New("[ClientTCP/SendData] connection closed")
	}
	_, err := c.conn.Write(data)
	return err
}

func (c *ClientTCP) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.mu.Unlock()
	c.cancel()
	if c.conn != nil {
		_ = c.conn.Close()
	}
	c.wg.Wait()
	c.scheduler.Stop()
	return nil
}

func (c *ClientTCP) readerLoop() {
	defer c.wg.Done()
	var buf = make([]byte, 4096)
	var pending []byte
	for {
		select {
		case <-c.ctx.Done():
			return
		default:
		}
		n, err := c.conn.Read(buf)
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				logx.Err.Println("[ClientTCP/readerLoop] read error:", err)
			}
			return
		}
		pending = append(pending, buf[:n]...)
		msgIDs, payloads, err := c.clientProtocol.UnpackAll(pending)
		if err != nil {
			logx.Err.Println("[ClientTCP/readerLoop] unpack error:", err)
			return
		}
		// 计算已消费的字节数
		consumed := 0
		for i := range msgIDs {
			consumed += 8 + len(payloads[i])
			c.dispatch(msgIDs[i], payloads[i])
		}
		pending = pending[consumed:]
	}
}

func (c *ClientTCP) dispatch(msgID int32, payload []byte) {
	c.handlersrw.RLock()
	pb, ok1 := c.msgs[msgID]
	hd, ok2 := c.handlers[msgID]
	c.handlersrw.RUnlock()
	if !ok1 || !ok2 {
		logx.Err.Printf("[ClientTCP/dispatch] message[%d] not found", msgID)
		return
	}
	bpb := pbp.Clone(pb)
	if err := pbp.Unmarshal(payload, bpb); err != nil {
		logx.Err.Printf("[ClientTCP/dispatch] unmarshal message[%d] error: %v", msgID, err)
		return
	}
	c.scheduler.PushTask(func() { hd(c, bpb.(message.Message)) })
}

type clientTCPMessenger struct {
	c *ClientTCP
}

func (m *clientTCPMessenger) Send(pb message.Message) error {
	return m.c.Send(pb)
}

func (m *clientTCPMessenger) Notify(targets []session.Session, pb message.Message) error {
	var errs []error
	for _, sv := range targets {
		errs = append(errs, sv.Send(pb))
	}
	return errors.Join(errs...)
}

func (m *clientTCPMessenger) Close() error {
	return m.c.Close()
}
