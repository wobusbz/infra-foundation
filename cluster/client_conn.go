package cluster

import (
	"errors"
	"infra-foundation/message"
	"infra-foundation/protocol"
	"infra-foundation/session"
	"infra-foundation/transport"
	"sync/atomic"
)

var _ session.Session = (*ClientConn)(nil)
var _ ServerBinding = (*ClientConn)(nil)

type ClientConn struct {
	*ClusterSessionBase
	Conn           *transport.Conn
	codec          *protocol.ClusterCodec
	clientProtocol protocol.ClientProtocol
	forwarder      PacketForwarder
	lifecycle      *FrontendLifecycle
	closed         atomic.Bool
}

func NewClientConn(
	conn *transport.Conn,
	clientProtocol protocol.ClientProtocol,
	codec *protocol.ClusterCodec,
	forwarder PacketForwarder,
	lifecycle *FrontendLifecycle,
) *ClientConn {
	c := &ClientConn{
		Conn:           conn,
		codec:          codec,
		clientProtocol: clientProtocol,
		forwarder:      forwarder,
		lifecycle:      lifecycle,
	}
	c.ClusterSessionBase = NewClusterSessionBase(session.SessionID(conn.SessionID()))
	lifecycle.Register(c)
	return c
}

func (c *ClientConn) Send(pb message.Message) error {
	if c.Conn.IsClosed() {
		return errors.New("[ClientConn/Send] connection closed")
	}
	return c.forwarder.SendPb(c, c.codec, string(c.ID()), pb)
}

func (c *ClientConn) Notify(targets []session.Session, pb message.Message) error {
	var errs []error
	for _, sv := range targets {
		errs = append(errs, sv.Send(pb))
	}
	return errors.Join(errs...)
}

func (c *ClientConn) Close() error {
	if !c.closed.CompareAndSwap(false, true) {
		return nil
	}
	return c.lifecycle.OnClose(c)
}

func (c *ClientConn) SendData(data []byte) error {
	return c.Conn.SendData(data)
}

func (c *ClientConn) ClientProtocol() protocol.ClientProtocol {
	return c.clientProtocol
}

func (c *ClientConn) WriteClientPacket(msgID int32, data []byte) error {
	pack := c.clientProtocol.PackPooled(msgID, data)
	if err := c.Conn.SendData(pack); err != nil {
		protocol.PutBuf(pack)
		return err
	}
	return nil
}

func (c *ClientConn) BindUid(uid string) error {
	c.ClusterSessionBase.BindUid(uid)
	return c.lifecycle.OnBindUid(c)
}
