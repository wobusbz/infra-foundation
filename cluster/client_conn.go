package cluster

import (
	"errors"
	"fmt"
	"infra-foundation/logx"
	"infra-foundation/message"
	"infra-foundation/model"
	"infra-foundation/protocol"
	"infra-foundation/session"
	"infra-foundation/transport"
)

var _ session.Session = (*ClientConn)(nil)

type ClientConn struct {
	*session.SessionBase
	Conn           *transport.Conn
	codec          *protocol.Codec
	clientProtocol protocol.ClientProtocol
	node           *Node
	connMgr        *session.Manager
	modelMgr       *model.ModelManager
}

func NewClientConn(
	conn *transport.Conn,
	clientProtocol protocol.ClientProtocol,
	codec *protocol.Codec,
	node *Node,
	connMgr *session.Manager,
	modelMgr *model.ModelManager,
) *ClientConn {
	c := &ClientConn{
		Conn:           conn,
		codec:          codec,
		clientProtocol: clientProtocol,
		node:           node,
		connMgr:        connMgr,
		modelMgr:       modelMgr,
	}
	c.SessionBase = &session.SessionBase{
		SessionEntity: conn.SessionEntity,
		Messenger:     &clientConnMessenger{c: c},
	}
	c.connMgr.Store(c)
	return c
}

func (c *ClientConn) sendProtoMessage(pb message.Message) error {
	return c.node.SendPb(c, c.codec, string(c.ID()), pb)
}

func (c *ClientConn) SendData(data []byte) error {
	return c.Conn.SendData(data)
}

func (c *ClientConn) ClientProtocol() protocol.ClientProtocol {
	return c.clientProtocol
}

func (c *ClientConn) BindUid(uid string) error {
	c.SessionEntity.BindUid(uid)
	c.modelMgr.OnSessionInitialization(c)
	if err := c.node.broadcastSessionBind(c); err != nil {
		logx.War.Printf("[ClientConn/BindUid] bind session %s to services: %v", c.ID(), err)
		return fmt.Errorf("bind session to services: %w", err)
	}
	return nil
}

type clientConnMessenger struct {
	c *ClientConn
}

func (m *clientConnMessenger) Send(pb message.Message) error {
	if m.c.Conn.IsClosed() {
		return errors.New("[ClientConn/Send] connection closed")
	}
	return m.c.sendProtoMessage(pb)
}

func (m *clientConnMessenger) Notify(targets []session.Session, pb message.Message) error {
	var errs []error
	if len(targets) == 0 {
		errs = append(errs, m.c.connMgr.Range(func(s session.Session) error { return s.Send(pb) }))
	} else {
		for _, sv := range targets {
			errs = append(errs, sv.Send(pb))
		}
	}
	return errors.Join(errs...)
}

func (m *clientConnMessenger) Close() error {
	var errs []error
	if !m.c.Conn.IsClosed() {
		errs = append(errs, m.c.Conn.Close())
	}
	m.c.modelMgr.OnDisconnection(m.c)
	m.c.connMgr.RemoveByID(m.c.ID())
	session.DefaultIDPool.Remove(m.c.ID())
	errs = append(errs, m.c.node.broadcastSessionClose(m.c))
	return errors.Join(errs...)
}
