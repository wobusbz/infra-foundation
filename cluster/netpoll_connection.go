package cluster

import (
	"infra-foundation/connmannger"
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
	connManager  *connmannger.ConnManager
	chkHeartbeat time.Duration
	timerId      scheduler.TimerID
	closed       atomic.Bool
}

func NewNetPollConnection(svrrequest *ServerRequest, connection netpoll.Connection, id int64) *NetPollConnection {
	n := &NetPollConnection{
		Connection:    NewConnection(connection, id, -1),
		connManager:   defaultNodeAgent.connManager,
		ServerRequest: svrrequest,
		chkHeartbeat:  time.Second * 5,
	}

	n.ServerRequest.connManager.StoreSession(n)
	n.timerId, _ = n.scheduler.PushEvery(n.chkHeartbeat, n.checkHeartbeat)
	return n
}

func (n *NetPollConnection) checkHeartbeat() {
	now := time.Now().Unix()
	if n.GetLastHeartBeatTime() == 0 {
		n.SetLastHeartBeatTime(now)
		return
	}
	if n.GetLastHeartBeatTime()+int64(n.chkHeartbeat.Seconds()*2) > now {
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
		n.connManager.RemoveByID(n.ID())
	} else {
		n.modelManager.OnDisconnection(n)
		n.ServerRequest.connManager.RemoveByID(n.ID())
	}
	n.scheduler.CancelTimer(n.timerId)
	return n.Close()
}
