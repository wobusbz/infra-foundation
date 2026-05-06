package cluster

import (
	"fmt"
	"infra-foundation/clusterpb"
	"infra-foundation/logx"
	"infra-foundation/protocol"
	"infra-foundation/session"
	"sync/atomic"
)

type SessionBinder struct {
	connMgr   *session.Manager
	localNode atomic.Pointer[NodeInfo]
}

func NewSessionBinder(connMgr *session.Manager) *SessionBinder {
	return &SessionBinder{connMgr: connMgr}
}

func (sb *SessionBinder) SetLocalNode(node *NodeInfo) {
	sb.localNode.Store(node)
}

func (sb *SessionBinder) IsLocalNodeName(name string) bool {
	n := sb.localNode.Load()
	return n != nil && n.Name == name
}

func (sb *SessionBinder) BindSessionToNode(sess session.Session, node *NodeInfo) error {
	conn, ok := sb.connMgr.GetByID(session.SessionID(node.Id))
	if !ok {
		return fmt.Errorf("node connection %s not found", node.Id)
	}

	sess.BindServers(node.Name, node.Id)

	if ln := sb.localNode.Load(); ln != nil {
		sess.BindServers(ln.Name, ln.Id)
	}

	pb := &clusterpb.N2MOnSessionBind{
		SessionID: string(sess.ID()),
		UID:       sess.UID(),
		Servers:   sess.Servers(),
	}

	return conn.SendTypePb(int8(protocol.ClusterSessionBind), pb)
}

func (sb *SessionBinder) GetNodeConnection(nodeID string) (session.Session, bool) {
	return sb.connMgr.GetByID(session.SessionID(nodeID))
}

func (sb *SessionBinder) StoreNodeConnection(nodeID string, conn session.Session) error {
	if conn == nil {
		return fmt.Errorf("cannot store nil connection for node %s", nodeID)
	}
	conn.BindID(session.SessionID(nodeID))
	sb.connMgr.Store(conn)
	return nil
}

func (sb *SessionBinder) GetFrontendNode(sess session.Session, registry *ServiceRegistry) (session.Session, error) {
	var result session.Session
	var found bool
	sess.RangeServers(func(name, nodeID string) bool {
		node, ok := registry.GetNodeByID(nodeID)
		if !ok {
			return true
		}
		if !node.Frontend {
			return true
		}

		conn, ok := sb.connMgr.GetByID(session.SessionID(nodeID))
		if !ok {
			return true
		}
		result = conn
		found = true
		return false
	})
	if found {
		return result, nil
	}
	logx.War.Printf("session %s has no gate node", sess.ID())
	return nil, fmt.Errorf("session %s has no gate node", sess.ID())
}
