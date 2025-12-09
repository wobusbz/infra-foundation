package cluster

import (
	"fmt"
	"infra-foundation/connmannger"
	"infra-foundation/model"
	"infra-foundation/packet"
	"infra-foundation/protomessage"
	"infra-foundation/session"

	"google.golang.org/protobuf/proto"
)

type acceptor struct {
	*session.NetworkEntities
	codec        *packet.PackCodec
	modelManager *model.ModelManager
	connManager  *connmannger.ConnManager
}

func newAcceptor(s *session.NetworkEntities, svr Server) *acceptor {
	return &acceptor{NetworkEntities: s, codec: packet.NewPackCodec(), modelManager: svr.ModelManager(), connManager: svr.ConnManager()}
}

func (a *acceptor) Send(pb protomessage.ProtoMessage) error {
	pdata, err := proto.Marshal(pb)
	if err != nil {
		return fmt.Errorf("[acceptor/Send] proto Marshal %w", err)
	}
	bdata, err := a.codec.Pack(packet.New(packet.Data, pb.MessageID(), pdata))
	if err != nil {
		return fmt.Errorf("[acceptor/Send] codec Pack %w", err)
	}
	return a.remoteCall(packet.NewInternal(packet.ClientData, 0, a.ID(), bdata), pb.NodeName())
}

func (a *acceptor) SendPb(typ packet.Type, id int32, pb protomessage.ProtoMessage) error { return nil }

func (a *acceptor) SendTypePb(typ packet.Type, pb protomessage.ProtoMessage) error { return nil }

func (a *acceptor) SendPack(pack *packet.Packet) error { return nil }

func (a *acceptor) Close() error {
	a.connManager.RemoveByID(a.ID())
	a.modelManager.OnDisconnection(a)
	return nil
}

func (a *acceptor) remoteCall(pack *packet.Packet, nodeName string) error {
	var (
		agent session.Session
		err   error
	)
	switch {
	case defaultNodeAgent.hasGroutes(pack.ID()):
		agent, err = defaultNodeAgent.getNodeByName(a, nodeName)
		if err != nil {
			return fmt.Errorf("[acceptor/Send] %w", err)
		}
	default:
		agent, err = defaultNodeAgent.getGateNode(a)
		if err != nil {
			return fmt.Errorf("[acceptor/Send] %w", err)
		}
	}
	return agent.SendPack(pack)
}
