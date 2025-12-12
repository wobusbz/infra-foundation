package cluster

import (
	"infra-foundation/logx"
	"infra-foundation/packet"
	"infra-foundation/scheduler"
	"sync/atomic"
	"time"

	"github.com/cloudwego/netpoll"
)

type ClientConnection struct {
	*Connection
	*ClientRequest
	heartbeatTime time.Duration
	timerID       scheduler.TimerID
	closed        atomic.Bool
}

func NewClientConnection(svr Server) *ClientConnection {
	c := &ClientConnection{heartbeatTime: time.Second * 3}
	c.ClientRequest = NewClientRequest(svr)
	c.ClientRequest.ClientConnection = c
	return c
}

func (c *ClientConnection) DialConnection(addr string) error {
	conn, err := netpoll.NewDialer().DialConnection("tcp", addr, time.Second)
	if err != nil {
		logx.Err.Println(err)
		return err
	}
	c.Connection = NewConnection(conn, 1, -1)
	c.SetOnRequest(c.ClientRequest.OnRequest)
	c.timerID, _ = c.scheduler.PushEvery(c.heartbeatTime, c.sendHeartbeat)
	return nil
}

func (c *ClientConnection) Close() error {
	if !c.closed.CompareAndSwap(false, true) {
		return nil
	}
	if c.UID() == -1 {
		defaultNodeAgent.connManager.RemoveByID(c.ID())
	}
	c.scheduler.CancelTimer(c.timerID)
	return c.Connection.Close()
}

func (c *ClientConnection) sendHeartbeat() {
	now := time.Now().Unix()
	if c.HeartbeatAt()+int64(c.heartbeatTime.Seconds()) > now {
		return
	}
	if err := c.SendPack(packet.New(packet.Heartbeat, 0, nil)); err != nil {
		c.Close()
		return
	}
	c.SetHeartbeatAt(now)
}
