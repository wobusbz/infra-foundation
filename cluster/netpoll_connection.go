package cluster

import (
	"infra-foundation/logx"
	"infra-foundation/scheduler"
	"infra-foundation/session"
	"sync/atomic"
	"time"

	"github.com/cloudwego/netpoll"
)

var _ session.Session = (*NetPollConnection)(nil)

type NetPollConnection struct {
	*Connection
	*ServerRequest
	heartbeatInterval time.Duration
	timerID           scheduler.TimerID
	closed            atomic.Bool
}

func NewNetPollConnection(svrrequest *ServerRequest, connection netpoll.Connection, id int64) *NetPollConnection {
	n := &NetPollConnection{
		Connection:        NewConnection(connection, id, -1),
		ServerRequest:     svrrequest,
		heartbeatInterval: time.Second * 5,
	}

	n.ServerRequest.connManager.StoreSession(n)
	n.timerID, _ = n.scheduler.PushEvery(n.heartbeatInterval, n.checkHeartbeat)
	return n
}

func (n *NetPollConnection) checkHeartbeat() {
	now := time.Now().Unix()
	if n.HeartbeatAt() == 0 {
		n.SetHeartbeatAt(now)
		return
	}
	if n.HeartbeatAt()+int64(n.heartbeatInterval.Seconds()*2) > now {
		return
	}
	n.Close()
	logx.Dbg.Println("[NetPollConnection/checkHeartbeat] 心跳超时 ", n.ID())
}

func (n *NetPollConnection) Close() error {
	if !n.closed.CompareAndSwap(false, true) {
		return nil
	}
	if n.UID() == -1 {
		defaultNodeAgent.connManager.RemoveByID(n.ID())
	} else {
		n.modelManager.OnDisconnection(n)
		n.ServerRequest.connManager.RemoveByID(n.ID())
	}
	n.scheduler.CancelTimer(n.timerID)
	return n.Connection.Close()
}
