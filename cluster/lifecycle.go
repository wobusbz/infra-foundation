package cluster

import (
	"errors"

	"infra-foundation/session"
)

type ModelLifecycleNotifier interface {
	OnSessionInitialization(s session.Identity)
	OnDisconnection(s session.Identity)
}

type SessionEventBroadcaster interface {
	BroadcastSessionBind(s BoundSession) error
	BroadcastSessionClose(s BoundSession) error
}

type sessionIDPool interface {
	NextID() session.SessionID
	Remove(id session.SessionID)
}

type FrontendLifecycle struct {
	modelNotifier ModelLifecycleNotifier
	connMgr       ClientStore
	router        SessionEventBroadcaster
	idPool        sessionIDPool
}

func NewFrontendLifecycle(
	modelNotifier ModelLifecycleNotifier,
	connMgr ClientStore,
	router SessionEventBroadcaster,
	idPool sessionIDPool,
) *FrontendLifecycle {
	return &FrontendLifecycle{modelNotifier: modelNotifier, connMgr: connMgr, router: router, idPool: idPool}
}

func (fl *FrontendLifecycle) Register(conn *ClientConn) {
	fl.connMgr.Store(conn)
}

func (fl *FrontendLifecycle) OnBindUid(conn *ClientConn) error {
	if !conn.TransitionToUIDBound() {
		return nil
	}
	fl.modelNotifier.OnSessionInitialization(conn)
	if err := fl.router.BroadcastSessionBind(conn); err != nil {
		return err
	}
	return nil
}

func (fl *FrontendLifecycle) OnClose(conn *ClientConn) error {
	prevState := conn.State()
	if !conn.TransitionToClosing() {
		return nil
	}
	defer conn.MarkClosed()

	var errs []error
	if prevState == session.SessionUIDBound {
		fl.modelNotifier.OnDisconnection(conn)
	}
	fl.connMgr.RemoveByID(conn.ID())
	fl.idPool.Remove(conn.ID())
	errs = append(errs, fl.router.BroadcastSessionClose(conn))
	if !conn.Conn.IsClosed() {
		errs = append(errs, conn.Conn.Close())
	}
	return errors.Join(errs...)
}
