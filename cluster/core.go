package cluster

import (
	"net/http"
	"time"

	"infra-foundation/message"
	"infra-foundation/protocol"
	"infra-foundation/scheduler"
	"infra-foundation/session"
)

type ClientStore = session.Store[session.Session]
type PeerStore = session.Store[NodeConn]

type TaskExecutor interface {
	PushTask(fn scheduler.TaskFunc)
	PushEvery(interval time.Duration, fn scheduler.TaskFunc) (scheduler.TimerID, error)
	CancelTimer(id scheduler.TimerID) bool
	Stop()
}

type ModelDispatcher interface {
	IsLocalHandler(msgID int32) bool
	Dispatch(s session.Session, msgID int32, data []byte) error
	DispatchHTTP(route string, w http.ResponseWriter, r *http.Request)
	OnSessionInitialization(s session.Identity)
	OnDisconnection(s session.Identity)
	Stop() error
}

type PacketForwarder interface {
	ForwardPkt(s session.Identity, codec *protocol.ClusterCodec, pack *protocol.Pkt, nodeName string) error
	SendPb(s session.Identity, codec *protocol.ClusterCodec, sid string, pb message.Message) error
}

type PushBroker interface {
	SendPush(targets []session.Session, pb message.Message) error
}

type NodeConn interface {
	session.Identity
	session.AppSender
	session.PacketSender
	session.Closer
	SendTypePb(typ int8, pb message.Message) error
}

type FrontendWriter interface {
	session.PacketSender
	WriteClientPacket(msgID int32, data []byte) error
}

type HealthMarker interface {
	MarkHealthy(nodeID string, healthy bool)
}
