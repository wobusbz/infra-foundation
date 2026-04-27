package cluster

import (
	"errors"
	"fmt"
	"infra-foundation/message"
	"infra-foundation/protocol"
	"infra-foundation/session"
	"infra-foundation/transport"
	"strings"
	"sync/atomic"

	pbp "google.golang.org/protobuf/proto"
)

func remoteCallWithAgent(node *Node, s session.Session, p *protocol.Codec, pack *protocol.Pkt, nodeName string) error {
	return node.RemoteCallWithAgent(s, p, pack, nodeName)
}

func sendProtoMessage(
	closed *atomic.Bool,
	name string,
	node *Node,
	sess session.Session,
	conn *transport.Conn,
	sid string,
	pb message.Message,
) error {
	if closed.Load() {
		return errors.New("[" + name + "/Send] connection closed")
	}
	localNode := node.LocalNode()
	if localNode != nil && strings.EqualFold(localNode.Name, pb.ServiceName()) {
		return conn.SendTypePb(int8(protocol.ClusterRequest), pb)
	}
	pbdata, err := pbp.Marshal(pb)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	pack := protocol.NewWithSID(protocol.ClusterServiceCall, pb.MessageID(), sid, pbdata)
	return node.RemoteCallWithAgent(sess, conn.Codec, pack, pb.ServiceName())
}
