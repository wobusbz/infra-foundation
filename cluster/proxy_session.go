package cluster

import (
	"errors"
	"fmt"
	"infra-foundation/clusterpb"
	"infra-foundation/connmanager"
	"infra-foundation/model"
	"infra-foundation/packet"
	"infra-foundation/protomessage"
	"infra-foundation/session"
	"sync/atomic"

	"google.golang.org/protobuf/proto"
)

type ProxySession struct {
	*session.Base
	Codec        *packet.Codec
	modelManager *model.ModelManager
	connManager  *connmanager.SessionManager
	node         *Node
	closed       atomic.Bool
}

type proxyMessenger struct {
	p *ProxySession
}

func (m *proxyMessenger) Send(pb protomessage.ProtoMessage) error {
	pdata, err := proto.Marshal(pb)
	if err != nil {
		return fmt.Errorf("[ProxySession/Send] marshal failed: %w", err)
	}

	buf, err := m.p.Codec.Pack(packet.Data, pb.MessageID(), m.p.ID(), pdata)
	if err != nil {
		return fmt.Errorf("[ProxySession/Send] pack failed: %w", err)
	}
	return remoteCallWithAgent(m.p.node, m.p, m.p.Codec, packet.NewInternal(packet.ClientData, 0, m.p.ID(), buf), "")
}

func (m *proxyMessenger) Notify(targets []session.Session, pb protomessage.ProtoMessage) error {
	pdata, err := proto.Marshal(pb)
	if err != nil {
		return fmt.Errorf("[ProxySession/Notify] marshal failed: %w", err)
	}

	buf, err := m.p.Codec.Pack(packet.Data, pb.MessageID(), m.p.ID(), pdata)
	if err != nil {
		return fmt.Errorf("[ProxySession/Notify] pack failed: %w", err)
	}

	var tempSession = make(map[int64][]int64)

	if len(targets) == 0 {
		if err = m.p.connManager.Range(func(s session.Session) error {
			gatewayConn, err := m.p.node.gatewayBySession(s)
			if err != nil {
				return err
			}
			tempSession[gatewayConn.ID()] = append(tempSession[gatewayConn.ID()], s.ID())
			return nil
		}); err != nil {
			return fmt.Errorf("[ProxySession/Notify] range failed: %w", err)
		}
	} else {
		for _, sv := range targets {
			gatewayConn, err := m.p.node.gatewayBySession(sv)
			if err != nil {
				return err
			}
			tempSession[gatewayConn.ID()] = append(tempSession[gatewayConn.ID()], sv.ID())
		}
	}

	var notifyPB = &clusterpb.N2MNotify{}
	var errs []error
	for gatewayID, sessionIDs := range tempSession {
		notifyPB.SessionID = sessionIDs
		notifyPB.Plyload = buf

		notifyDataBuf, err := proto.Marshal(notifyPB)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		gatewayConn, ok := m.p.node.NodeConnManager().GetByID(gatewayID)
		if !ok {
			errs = append(errs, fmt.Errorf("[ProxySession/Notify] gateway %d not found", gatewayID))
			continue
		}
		dataSender, ok := gatewayConn.(interface{ SendData([]byte) error })
		if !ok {
			errs = append(errs, fmt.Errorf("[ProxySession/Notify] gateway connection does not support SendData"))
			continue
		}
		notifyData, err := m.p.Codec.Pack(packet.NotifyData, 0, 0, notifyDataBuf)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		errs = append(errs, dataSender.SendData(notifyData))
	}

	if len(errs) > 0 {
		return fmt.Errorf("[ProxySession/Notify] errors: %v", errors.Join(errs...))
	}
	return nil
}

func (m *proxyMessenger) Close() error {
	if !m.p.closed.CompareAndSwap(false, true) {
		return nil
	}
	m.p.modelManager.OnDisconnection(m.p)
	m.p.connManager.RemoveByID(m.p.ID())
	return nil
}

func NewProxySession(s *session.SessionEntity, node *Node) *ProxySession {
	p := &ProxySession{
		Codec:        packet.NewCodec(),
		modelManager: node.ctx.ModelManager(),
		connManager:  node.ctx.ConnManager(),
		node:         node,
	}
	p.Base = &session.Base{
		SessionEntity: s,
		Messenger:     &proxyMessenger{p: p},
	}
	return p
}
