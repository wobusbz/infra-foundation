package cluster

import (
	"errors"
	"fmt"
	"infra-foundation/clusterpb"
	"infra-foundation/message"
	"infra-foundation/model"
	"infra-foundation/protocol"
	"infra-foundation/session"
	"sync/atomic"

	pbp "google.golang.org/protobuf/proto"
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

type proxyMessenger struct {
	p *ProxySession
}

func (m *proxyMessenger) Send(pb message.Message) error {
	pdata, err := pbp.Marshal(pb)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	buf, err := m.p.Codec.Pack(protocol.Request, pb.MessageID(), string(m.p.ID()), pdata)
	if err != nil {
		return fmt.Errorf("pack: %w", err)
	}
	pack := protocol.NewWithSID(protocol.Response, 0, string(m.p.ID()), buf)
	return m.p.router.RemoteCallWithAgent(m.p, m.p.Codec, pack, "")
}

func (m *proxyMessenger) Notify(targets []session.Session, pb message.Message) error {
	pdata, err := pbp.Marshal(pb)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	buf, err := m.p.Codec.Pack(protocol.Request, pb.MessageID(), string(m.p.ID()), pdata)
	if err != nil {
		return fmt.Errorf("pack: %w", err)
	}

	tempSession := make(map[string][]string)

	if len(targets) == 0 {
		if err = m.p.connMgr.Range(func(s session.Session) error {
			gatewayConn, err := m.p.router.GatewayBySession(s)
			if err != nil {
				return err
			}
			tempSession[string(gatewayConn.ID())] = append(tempSession[string(gatewayConn.ID())], string(s.ID()))
			return nil
		}); err != nil {
			return fmt.Errorf("range sessions: %w", err)
		}
	} else {
		for _, sv := range targets {
			gatewayConn, err := m.p.router.GatewayBySession(sv)
			if err != nil {
				return err
			}
			tempSession[string(gatewayConn.ID())] = append(tempSession[string(gatewayConn.ID())], string(sv.ID()))
		}
	}

	notifyPB := &clusterpb.N2MNotify{}
	var errs []error
	for gatewayID, sessionIDs := range tempSession {
		notifyPB.SessionID = sessionIDs
		notifyPB.Plyload = buf

		notifyDataBuf, err := pbp.Marshal(notifyPB)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		gatewayConn, ok := m.p.peerMgr.GetByID(session.SessionID(gatewayID))
		if !ok {
			errs = append(errs, fmt.Errorf("gateway %s not found", gatewayID))
			continue
		}
		notifyData, err := m.p.Codec.Pack(protocol.Push, 0, "", notifyDataBuf)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		errs = append(errs, gatewayConn.SendData(notifyData))
	}

	if len(errs) > 0 {
		return fmt.Errorf("notify: %w", errors.Join(errs...))
	}
	return nil
}

func (m *proxyMessenger) Close() error {
	if !m.p.closed.CompareAndSwap(false, true) {
		return nil
	}
	m.p.modelManager.OnDisconnection(m.p)
	m.p.connMgr.RemoveByID(m.p.ID())
	return nil
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
	pack := protocol.NewWithSID(protocol.Response, 0, string(p.ID()), data)
	return p.router.RemoteCallWithAgent(p, p.Codec, pack, "")
}

func (p *ProxySession) SendTypePb(typ int8, pb message.Message) error {
	return p.Send(pb)
}
