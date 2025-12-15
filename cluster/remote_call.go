package cluster

import (
	"infra-foundation/packet"
	"infra-foundation/session"
)

func remoteCall(s session.Session, p *packet.PackCodec, pack *packet.Packet, nodeName string) error {
	var (
		agent session.Session
		err   error
	)
	switch {
	case defaultNodeAgent.hasGroutes(pack.ID()):
		agent, err = defaultNodeAgent.getNodeByName(s, nodeName)
		if err != nil {
			return err
		}
	case defaultNodeAgent.node.Frontend:
		agent = s
	default:
		agent, err = defaultNodeAgent.getGateNode(s)
		if err != nil {
			return err
		}
	}
	bdata, err := p.Pack(pack.Type(), pack.ID(), pack.SID(), pack.Data())
	if err != nil {
		return err
	}
	return agent.(sender).SendData(bdata)
}
