package cluster

import (
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
	peerMgr      *session.Manager
	router       *Node
	msgh         *MessageHandler
	closed       atomic.Bool
}

func NewProxySession(s *session.SessionEntity, router *Node, modelManager *model.ModelManager, connMgr, peerMgr *session.Manager, msgh *MessageHandler) *ProxySession {
	p := &ProxySession{
		Codec:        protocol.NewCodec(),
		modelManager: modelManager,
		connMgr:      connMgr,
		peerMgr:      peerMgr,
		router:       router,
		msgh:         msgh,
	}
	p.SessionBase = &session.SessionBase{
		SessionEntity: s,
		Messenger:     &proxyMessenger{p: p},
	}
	return p
}

func (p *ProxySession) BindUid(uid string) error {
	p.SessionEntity.BindUid(uid)
	return nil
}

func (p *ProxySession) SendData(data []byte) error {
	pack := protocol.NewWithSID(protocol.ClusterResponse, 0, string(p.ID()), data)
	return p.router.forwardPkt(p, p.Codec, pack, "")
}
