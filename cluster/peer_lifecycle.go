package cluster

import (
	"infra-foundation/logx"
	"infra-foundation/scheduler"
	"infra-foundation/session"
)

type PeerLifecycle struct {
	connMgr      PeerStore
	peerMgr      PeerStore
	executor     TaskExecutor
	healthMarker HealthMarker
	idPool       sessionIDPool
}

func NewPeerLifecycle(
	connMgr PeerStore,
	peerMgr PeerStore,
	executor TaskExecutor,
	healthMarker HealthMarker,
	idPool sessionIDPool,
) *PeerLifecycle {
	return &PeerLifecycle{
		connMgr:      connMgr,
		peerMgr:      peerMgr,
		executor:     executor,
		healthMarker: healthMarker,
		idPool:       idPool,
	}
}

func (pl *PeerLifecycle) Register(pc *PeerConn) {
	pl.connMgr.Store(pc)
}

func (pl *PeerLifecycle) StartHeartbeat(pc *PeerConn, fn scheduler.TaskFunc) {
	var err error
	pc.timerID, err = pl.executor.PushEvery(pc.heartbeatInterval, fn)
	if err != nil {
		logx.Err.Printf("[PeerLifecycle] failed to start heartbeat timer: %v", err)
	}
}

func (pl *PeerLifecycle) OnHandshake(oldID session.SessionID) {
	pl.idPool.Remove(oldID)
	pl.peerMgr.RemoveByID(oldID)
}

func (pl *PeerLifecycle) OnClose(pc *PeerConn) error {
	pl.connMgr.RemoveByID(pc.ID())
	if pc.isOutbound && pl.healthMarker != nil {
		pl.healthMarker.MarkHealthy(string(pc.ID()), false)
	}
	pl.idPool.Remove(pc.ID())
	pl.executor.CancelTimer(pc.timerID)
	if !pc.Conn.IsClosed() {
		return pc.Conn.Close()
	}
	return nil
}
