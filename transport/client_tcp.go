package transport

import (
	"context"
	"errors"
	"fmt"
	"infra-foundation/logx"
	"infra-foundation/message"
	"infra-foundation/protocol"
	"math/rand"
	"net"
	"sync"
	"time"

	pbp "google.golang.org/protobuf/proto"
)

type clientMsgHandler func(*ClientTCP, message.Message)

type ClientTCP struct {
	id             string
	conn           net.Conn
	handlers       map[int32]clientMsgHandler
	msgs           map[int32]message.Message
	handlersrw     sync.RWMutex
	wg             sync.WaitGroup
	ctx            context.Context
	cancel         context.CancelFunc
	clientProtocol protocol.ClientProtocol
	closed         bool
	mu             sync.Mutex
	pendingCalls   map[int32]chan message.Message
	pendingMu      sync.Mutex
}

func NewClientTCP() *ClientTCP {
	return NewClientTCPWithProtocol(protocol.NewClientCodec())
}

func NewClientTCPWithProtocol(p protocol.ClientProtocol) *ClientTCP {
	c := &ClientTCP{
		id:             fmt.Sprintf("%d-%d", time.Now().UnixNano(), rand.Int63()),
		handlers:       map[int32]clientMsgHandler{},
		msgs:           map[int32]message.Message{},
		clientProtocol: p,
		pendingCalls:   map[int32]chan message.Message{},
	}
	c.ctx, c.cancel = context.WithCancel(context.Background())
	return c
}

func (c *ClientTCP) ID() string { return c.id }

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
	c.pendingMu.Lock()
	for _, ch := range c.pendingCalls {
		close(ch)
	}
	c.pendingCalls = map[int32]chan message.Message{}
	c.pendingMu.Unlock()
	c.wg.Wait()
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
	if !ok1 {
		logx.Err.Printf("[ClientTCP/dispatch] message[%d] proto template not found", msgID)
		return
	}
	bpb := pbp.Clone(pb)
	if err := pbp.Unmarshal(payload, bpb); err != nil {
		logx.Err.Printf("[ClientTCP/dispatch] unmarshal message[%d] error: %v", msgID, err)
		return
	}
	msg := bpb.(message.Message)

	c.pendingMu.Lock()
	if ch, ok := c.pendingCalls[msgID]; ok {
		delete(c.pendingCalls, msgID)
		c.pendingMu.Unlock()
		select {
		case ch <- msg:
			return
		default:
		}
	} else {
		c.pendingMu.Unlock()
	}

	if ok2 {
		go hd(c, msg)
	}
}

func (c *ClientTCP) Call(ctx context.Context, req message.Message, respProto message.Message) (message.Message, error) {
	respID := respProto.MessageID()

	c.handlersrw.Lock()
	if _, ok := c.msgs[respID]; !ok {
		c.msgs[respID] = respProto
	}
	c.handlersrw.Unlock()

	respCh := make(chan message.Message, 1)
	c.pendingMu.Lock()
	c.pendingCalls[respID] = respCh
	c.pendingMu.Unlock()

	defer func() {
		c.pendingMu.Lock()
		delete(c.pendingCalls, respID)
		c.pendingMu.Unlock()
	}()

	if err := c.Send(req); err != nil {
		return nil, err
	}

	select {
	case resp := <-respCh:
		if resp == nil {
			return nil, errors.New("[ClientTCP/Call] connection closed while waiting")
		}
		return resp, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
