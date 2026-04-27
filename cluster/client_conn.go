package cluster

import (
	"errors"
	"fmt"
	"infra-foundation/message"
	"infra-foundation/protocol"
	"infra-foundation/session"
	"infra-foundation/transport"
	"sync/atomic"

	pbp "google.golang.org/protobuf/proto"
)

var _ session.Session = (*ClientConn)(nil)

type ClientConn struct {
	*session.SessionBase
	Conn           *transport.Conn
	clientProtocol protocol.ClientProtocol
	node           *Node
	closed         atomic.Bool
}

func NewClientConn(conn *transport.Conn, clientProtocol protocol.ClientProtocol, node *Node) *ClientConn {
	c := &ClientConn{
		Conn:           conn,
		clientProtocol: clientProtocol,
		node:           node,
	}
	c.SessionBase = &session.SessionBase{
		SessionEntity: conn.SessionEntity,
		Messenger:     &clientConnMessenger{c: c},
	}
	return c
}

func (c *ClientConn) SendData(data []byte) error {
	if c.closed.Load() {
		return errors.New("[ClientConn/SendData] connection closed")
	}
	return c.Conn.SendData(data)
}

func (c *ClientConn) SendTypePb(typ int8, pb message.Message) error {
	if c.closed.Load() {
		return errors.New("[ClientConn/SendTypePb] connection closed")
	}
	data, err := pbp.Marshal(pb)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	pack := c.clientProtocol.Pack(pb.MessageID(), data)
	return c.Conn.SendData(pack)
}

type clientConnMessenger struct {
	c *ClientConn
}

func (m *clientConnMessenger) Send(pb message.Message) error {
	return sendProtoMessage(&m.c.closed, "clientConnMessenger", m.c.node, m.c, m.c.Conn, string(m.c.ID()), pb)
}

func (m *clientConnMessenger) Notify(targets []session.Session, pb message.Message) error {
	var errs []error
	for _, sv := range targets {
		errs = append(errs, sv.Send(pb))
	}
	return errors.Join(errs...)
}

func (m *clientConnMessenger) Close() error {
	return m.c.Conn.Close()
}
