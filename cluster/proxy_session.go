package cluster

import (
	"infra-foundation/message"
	"infra-foundation/protocol"
	"infra-foundation/session"
	"sync/atomic"
)

var _ session.Session = (*ProxySession)(nil)
var _ ServerBinding = (*ProxySession)(nil)

type ProxySession struct {
	*ClusterSessionBase
	Codec      *protocol.ClusterCodec
	forwarder  PacketForwarder
	pusher     PushBroker
	dispatcher ModelDispatcher
	connMgr    ClientStore
	closed     atomic.Bool
}

func NewProxySession(
	s *session.SessionCore,
	forwarder PacketForwarder,
	pusher PushBroker,
	dispatcher ModelDispatcher,
	connMgr ClientStore,
) *ProxySession {
	p := &ProxySession{
		Codec:      protocol.NewClusterCodec(),
		forwarder:  forwarder,
		pusher:     pusher,
		dispatcher: dispatcher,
		connMgr:    connMgr,
	}
	p.ClusterSessionBase = NewClusterSessionBase(s.ID())
	if uid := s.UID(); uid != "" {
		p.ClusterSessionBase.BindUid(uid)
	}
	p.connMgr.Store(p)
	return p
}

func (p *ProxySession) BindUid(uid string) error {
	p.ClusterSessionBase.BindUid(uid)
	return nil
}

func (p *ProxySession) Send(pb message.Message) error {
	return p.forwarder.SendPb(p, p.Codec, string(p.ID()), pb)
}

func (p *ProxySession) Notify(targets []session.Session, pb message.Message) error {
	return p.pusher.SendPush(targets, pb)
}

func (p *ProxySession) Close() error {
	if !p.closed.CompareAndSwap(false, true) {
		return nil
	}
	prevState := p.State()
	if p.TransitionToClosing() {
		if prevState == session.SessionUIDBound {
			p.dispatcher.OnDisconnection(p)
		}
		p.MarkClosed()
	}
	p.connMgr.RemoveByID(p.ID())
	return nil
}

func (p *ProxySession) SendData(data []byte) error {
	pack := protocol.NewWithSID(protocol.ClusterResponse, 0, 0, string(p.ID()), data)
	return p.forwarder.ForwardPkt(p, p.Codec, pack, "")
}
