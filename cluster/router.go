package cluster

import (
	"infra-foundation/packet"
	"infra-foundation/session"
)

func remoteCallWithAgent(node *Node, s session.Session, p *packet.Codec, pack *packet.Packet, nodeName string) error {
	var (
		agent session.Session
		err   error
	)
	switch {
	case node.hasRoute(pack.ID()):
		agent, err = node.nodeBySession(s, nodeName)
		if err != nil {
			return err
		}
	case node.localNode != nil && node.localNode.Frontend:
		agent = s
	default:
		agent, err = node.gatewayBySession(s)
		if err != nil {
			return err
		}
	}
	buf, err := p.Pack(pack.Type(), pack.ID(), pack.SID(), pack.Data())
	if err != nil {
		return err
	}
	return agent.(interface{ SendData([]byte) error }).SendData(buf)
}

