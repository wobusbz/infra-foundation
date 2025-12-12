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

func (a *acceptor) SendPb(typ packet.Type, id int32, pb protomessage.ProtoMessage) error {
	pdata, err := proto.Marshal(pb)
	if err != nil {
		return fmt.Errorf("[acceptor/SendPb] proto Marshal %w", err)
	}
	bdata, err := a.codec.Pack(typ, id, a.ID(), pdata)
	if err != nil {
		return fmt.Errorf("[acceptor/SendPb] codec Pack %w", err)
	}
	return remoteCall(a, a.codec, packet.NewInternal(packet.ClientData, id, a.ID(), bdata), pb.NodeName())
}

func (a *acceptor) SendTypePb(typ packet.Type, pb protomessage.ProtoMessage) error {
	pdata, err := proto.Marshal(pb)
	if err != nil {
		return fmt.Errorf("[acceptor/SendTypePb] proto Marshal %w", err)
	}
	bdata, err := a.codec.Pack(typ, pb.MessageID(), a.ID(), pdata)
	if err != nil {
		return fmt.Errorf("[acceptor/SendTypePb] codec Pack %w", err)
	}
	return remoteCall(a, a.codec, packet.NewInternal(packet.ClientData, pb.MessageID(), a.ID(), bdata), pb.NodeName())
}

func (a *acceptor) SendPack(pack *packet.Packet) error {
	bdata, err := a.codec.Pack(pack.Type(), pack.ID(), pack.SID(), pack.Data())
	if err != nil {
		return fmt.Errorf("[acceptor/SendPack] codec Pack %w", err)
	}
	return remoteCall(a, a.codec, packet.NewInternal(packet.ClientData, pack.ID(), a.ID(), bdata), "")
}

func (a *acceptor) Close() error {
	if !a.closed.CompareAndSwap(false, true) {
		return nil
	}
	a.modelManager.OnDisconnection(a)
	a.connManager.RemoveByID(a.ID())
	return nil
}
