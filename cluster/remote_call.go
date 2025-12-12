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
	default:
		if defaultNodeAgent.node.Frontend {
			conn1 := s.(interface{ SendData(data []byte) error })
			bdata, _ := p.Pack(pack.Type(), pack.ID(), pack.SID(), pack.Data())
			return conn1.SendData(bdata)
		}
		agent, err = defaultNodeAgent.getGateNode(s)
		if err != nil {
			return err
		}
	}
	conn1 := agent.(interface{ SendData(data []byte) error })
	bdata, _ := p.Pack(pack.Type(), pack.ID(), pack.SID(), pack.Data())
	return conn1.SendData(bdata)
}
