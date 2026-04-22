package cluster

import (
	"fmt"
	"infra-foundation/clusterpb"
	"infra-foundation/logx"
	"infra-foundation/protocol"
	"infra-foundation/session"
)

type SessionBinder struct {
	connMgr   *session.Manager
	localNode *NodeInfo
}

func NewSessionBinder(connMgr *session.Manager) *SessionBinder {
	return &SessionBinder{connMgr: connMgr}
}

func (sb *SessionBinder) SetLocalNode(node *NodeInfo) {
	sb.localNode = node
}

func (sb *SessionBinder) BindSessionToNode(sess session.Session, node *NodeInfo) error {
	conn, ok := sb.connMgr.GetByID(session.SessionID(node.Id))
	if !ok {
		return fmt.Errorf("node connection %s not found", node.Id)
	}

	sess.BindServers(node.Name, node.Id)

	if sb.localNode != nil {
		sess.BindServers(sb.localNode.Name, sb.localNode.Id)
	}

	pb := &clusterpb.N2MOnSessionBindServer{
		SessionID: string(sess.ID()),
		UID:       sess.UID(),
		Servers:   sess.Servers(),
	}

	return conn.SendTypePb(int8(protocol.BindSession), pb)
}

func (sb *SessionBinder) GetNodeConnection(nodeID string) (session.Session, bool) {
	return sb.connMgr.GetByID(session.SessionID(nodeID))
}

func (sb *SessionBinder) StoreNodeConnection(nodeID string, conn session.Session) error {
	conn.BindID(session.SessionID(nodeID))
	conn.BindUID(-1)
	if se, ok := conn.(interface{ SetPeerConn(bool) }); ok {
		se.SetPeerConn(true)
	}
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

func (sb *SessionBinder) GetNodeByName(sess session.Session, serviceName string) (session.Session, error) {
	nodeID := sess.GetServers(serviceName)
	if nodeID == "" {
		return nil, fmt.Errorf("session not bound to service %s", serviceName)
	}

	conn, ok := sb.connMgr.GetByID(session.SessionID(nodeID))
	if !ok {
		return nil, fmt.Errorf("node %s connection not found", nodeID)
	}

	return conn, nil
}
