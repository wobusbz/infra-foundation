package cluster

import (
	"infra-foundation/message"
	"infra-foundation/model"
	"infra-foundation/protocol"
	"infra-foundation/session"
	"sync/atomic"
)

type ProxySession struct {
	*session.SessionBase
	Codec        *protocol.Codec
	modelManager *model.ModelManager
	connMgr      *session.Manager
	router       Router
	peerMgr      *session.Manager
	closed       atomic.Bool
}

func NewProxySession(s *session.SessionEntity, router Router, modelManager *model.ModelManager, connMgr *session.Manager, peerMgr *session.Manager) *ProxySession {
	p := &ProxySession{
		Codec:        protocol.NewCodec(),
		modelManager: modelManager,
		connMgr:      connMgr,
		router:       router,
		peerMgr:      peerMgr,
	}
	p.SessionBase = &session.SessionBase{
		SessionEntity: s,
		Messenger:     &proxyMessenger{p: p},
	}
	return p
}

func (p *ProxySession) SendData(data []byte) error {
	pack := protocol.NewWithSID(protocol.ClusterResponse, 0, string(p.ID()), data)
	return p.router.RemoteCallWithAgent(p, p.Codec, pack, "")
}

func (p *ProxySession) SendTypePb(typ int8, pb message.Message) error {
	return p.Send(pb)
}
