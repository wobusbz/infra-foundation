package cluster

import (
	"fmt"
	"infra-foundation/message"
	"infra-foundation/protocol"
	"infra-foundation/session"

	pbp "google.golang.org/protobuf/proto"
)

type ClusterForwarder struct {
	router     *MessageRouter
	isFrontend func() bool
}

func newClusterForwarder(router *MessageRouter, isFrontend func() bool) *ClusterForwarder {
	return &ClusterForwarder{router: router, isFrontend: isFrontend}
}

func (cf *ClusterForwarder) ForwardPkt(s session.Identity, codec *protocol.ClusterCodec, pack *protocol.Pkt, nodeName string) error {
	defer pack.Free()

	decision := cf.router.Decide(pack.ID(), s, DirClientInbound)
	switch decision.Kind {
	case RouteLocalModel:
		return fmt.Errorf("unexpected local model route in forwarder")
	case RouteFrontendClient:
		if fw, ok := s.(FrontendWriter); ok {
			return fw.WriteClientPacket(pack.ID(), pack.Data())
		}
		return fmt.Errorf("session %s does not support frontend writing", s.ID())
	case RouteBackendNode:
		sb, ok := s.(BoundSession)
		if !ok {
			return fmt.Errorf("session %s does not support binding", s.ID())
		}
		agent, err := cf.router.nodeBySession(sb, nodeName)
		if err != nil {
			return err
		}
		return sendToAgent(agent, codec, pack)
	case RouteGatewayNode:
		sb, ok := s.(BoundSession)
		if !ok {
			return fmt.Errorf("session %s does not support binding", s.ID())
		}
		agent, err := cf.router.sessionBinder.GetFrontendNode(sb, cf.router.registry)
		if err != nil {
			return err
		}
		return sendToAgent(agent, codec, pack)
	case RouteDrop:
		return fmt.Errorf("no route for message %d", pack.ID())
	}
	return nil
}

func sendToAgent(agent session.PacketSender, codec *protocol.ClusterCodec, pack *protocol.Pkt) error {
	buf, err := codec.Pack(pack.ClusterType(), pack.ID(), pack.RequestID(), pack.SID(), pack.Data())
	if err != nil {
		return err
	}
	if err := agent.SendData(buf); err != nil {
		protocol.PutBuf(buf)
		return err
	}
	return nil
}

func (cf *ClusterForwarder) sendPkt(s session.Identity, codec *protocol.ClusterCodec, sid string, pb message.Message, typ protocol.ClusterType, nodeName string) error {
	pbdata, err := pbp.Marshal(pb)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	pack := protocol.NewWithSID(typ, pb.MessageID(), 0, sid, pbdata)
	return cf.ForwardPkt(s, codec, pack, nodeName)
}

func (cf *ClusterForwarder) SendPb(s session.Identity, codec *protocol.ClusterCodec, sid string, pb message.Message) error {
	return cf.sendPkt(s, codec, sid, pb, protocol.ClusterRequest, pb.ServiceName())
}
