package cluster

import (
	"errors"
	"fmt"
	"infra-foundation/connmannger"
	"infra-foundation/model"
	"infra-foundation/packet"
	"infra-foundation/protomessage"
	"infra-foundation/session"
	"sync/atomic"

	"google.golang.org/protobuf/proto"
)

type acceptor struct {
	*session.NetworkEntities
	codec        *packet.PackCodec
	modelManager *model.ModelManager
	connManager  *connmannger.ConnManager
	closed       atomic.Bool
}

func newAcceptor(s *session.NetworkEntities, svr Server) *acceptor {
	return &acceptor{NetworkEntities: s, codec: packet.NewPackCodec(), modelManager: svr.ModelManager(), connManager: svr.ConnManager()}
}

func (a *acceptor) Send(pb protomessage.ProtoMessage) error {
	pdata, err := proto.Marshal(pb)
	if err != nil {
		return fmt.Errorf("[acceptor/Send] proto Marshal %w", err)
	}
	bdata, err := a.codec.Pack(packet.Data, pb.MessageID(), a.ID(), pdata)
	if err != nil {
		return fmt.Errorf("[acceptor/Send] codec Pack %w", err)
	}
	return remoteCall(a, a.codec, packet.NewInternal(packet.ClientData, 0, a.ID(), bdata), pb.NodeName())
}

func (a *acceptor) Notify(s []session.Session, pb protomessage.ProtoMessage) error {
	pdata, err := proto.Marshal(pb)
	if err != nil {
		return fmt.Errorf("[acceptor/Notify] proto Marshal %w", err)
	}
	bdata, err := a.codec.Pack(packet.Data, pb.MessageID(), a.ID(), pdata)
	if err != nil {
		return fmt.Errorf("[acceptor/Notify] codec Pack %w", err)
	}
	var tempSession = map[int64][]int64{}
	for _, sv := range s {
		agent, err := defaultNodeAgent.getGateNode(sv)
		if err != nil {
			return err
		}
		tempSession[agent.ID()] = append(tempSession[agent.ID()], sv.ID())
	}
	var pb2 = &N2MNotify{}
	var errs []error
	for sid, usids := range tempSession {
		pb2.SessionID = usids
		pb2.Plyload = bdata
		bdata2, err := proto.Marshal(pb2)
		if err != nil {
			return err
		}
		asion, ok := defaultNodeAgent.connManager.GetByID(sid)
		if !ok {
			return fmt.Errorf("[acceptor/Notify] %d not found", sid)
		}
		errs = append(errs, remoteCall(asion, a.codec, packet.New(packet.NotifyData, 0, bdata2), pb.NodeName()))
	}
	if err = errors.Join(errs...); err != nil {
		return fmt.Errorf("[acceptor/Notify] remoteCall %w", err)
	}
	return nil
}

func (a *acceptor) Close() error {
	if !a.closed.CompareAndSwap(false, true) {
		return nil
	}
	a.modelManager.OnDisconnection(a)
	a.connManager.RemoveByID(a.ID())
	return nil
}
