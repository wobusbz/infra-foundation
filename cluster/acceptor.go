package cluster

import (
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

func (a *acceptor) Notify(s []*session.Session, pb protomessage.ProtoMessage) error {
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
