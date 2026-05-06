package cluster

import (
	"infra-foundation/message"
	"infra-foundation/protocol"
	"infra-foundation/session"
)

type proxyMessenger struct {
	p *ProxySession
}

func (m *proxyMessenger) Send(pb message.Message) error {
	return m.p.router.sendPkt(m.p, m.p.Codec, string(m.p.ID()), pb, protocol.ClusterResponse, pb.ServiceName())
}

func (m *proxyMessenger) Notify(targets []session.Session, pb message.Message) error {
	return m.p.msgh.SendPush(m.p, m.p.Codec, targets, pb)
}

func (m *proxyMessenger) Close() error {
	if !m.p.closed.CompareAndSwap(false, true) {
		return nil
	}
	m.p.modelManager.OnDisconnection(m.p)
	m.p.connMgr.RemoveByID(m.p.ID())
	return nil
}
