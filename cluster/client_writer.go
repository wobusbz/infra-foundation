package cluster

import (
	"infra-foundation/protocol"
	"infra-foundation/session"
)

type clientSender interface {
	session.PacketSender
	ClientProtocol() protocol.ClientProtocol
}

func sendToClientConn(conn session.Session, msgID int32, data []byte) error {
	if cs, ok := conn.(clientSender); ok {
		pack := cs.ClientProtocol().PackPooled(msgID, data)
		return cs.SendData(pack)
	}
	return conn.SendData(data)
}
