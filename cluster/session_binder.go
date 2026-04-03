package cluster

import (
	"fmt"
	"infra-foundation/clusterpb"
	"infra-foundation/connmanager"
	"infra-foundation/packet"
	"infra-foundation/protomessage"
	"infra-foundation/session"
	"strconv"
)

type SessionBinder struct {
	connManager *connmanager.SessionManager
	localNode   *NodeInfo
}

func NewSessionBinder(connManager *connmanager.SessionManager) *SessionBinder {
	return &SessionBinder{connManager: connManager}
}

func (sb *SessionBinder) SetLocalNode(node *NodeInfo) {
	sb.localNode = node
}

func (sb *SessionBinder) BindSessionToNode(sess session.Session, node *NodeInfo) error {
	id, err := strconv.ParseInt(node.Id, 10, 64)
	if err != nil {
		return fmt.Errorf("[SessionBinder/BindSessionToNode] invalid node ID %s: %w", node.Id, err)
	}

	conn, ok := sb.connManager.GetByID(id)
	if !ok {
		return fmt.Errorf("[SessionBinder/BindSessionToNode] node connection %s not found", node.Id)
	}

	sess.BindServers(node.Name, node.Id)

	if sb.localNode != nil {
		sess.BindServers(sb.localNode.Name, sb.localNode.Id)
	}

	pb := &clusterpb.N2MOnSessionBindServer{
		SessionID: sess.ID(),
		UID:       sess.UID(),
		Servers:   sess.Servers(),
	}

	senderConn, ok := conn.(interface {
		SendTypePb(typ packet.Type, pb protomessage.ProtoMessage) error
	})
	if !ok {
		return fmt.Errorf("[SessionBinder/BindSessionToNode] connection does not support SendTypePb")
	}

	return senderConn.SendTypePb(packet.BindConnection, pb)
}

func (sb *SessionBinder) GetNodeConnection(nodeID string) (session.Session, bool) {
	id, err := strconv.ParseInt(nodeID, 10, 64)
	if err != nil {
		return nil, false
	}
	return sb.connManager.GetByID(id)
}

func (sb *SessionBinder) StoreNodeConnection(nodeID string, conn session.Session) error {
	id, err := strconv.ParseInt(nodeID, 10, 64)
	if err != nil {
		return fmt.Errorf("[SessionBinder/StoreNodeConnection] invalid node ID %s: %w", nodeID, err)
	}

	conn.BindID(id)
	conn.BindUID(-1)
	sb.connManager.StoreSession(conn)
	return nil
}

func (sb *SessionBinder) GetGateNode(sess session.Session, registry *ServiceRegistry) (session.Session, error) {
	for _, nodeID := range sess.Servers() {
		node, ok := registry.GetNodeByID(nodeID)
		if !ok {
			continue
		}
		if !node.Frontend {
			continue
		}

		id, err := strconv.ParseInt(nodeID, 10, 64)
		if err != nil {
			continue
		}

		conn, ok := sb.connManager.GetByID(id)
		if !ok {
			return nil, fmt.Errorf("[SessionBinder/GetGateNode] gate node %s connection not found", nodeID)
		}
		return conn, nil
	}
	return nil, fmt.Errorf("[SessionBinder] session[%d] has no gate node", sess.ID())
}

func (sb *SessionBinder) GetNodeByName(sess session.Session, serviceName string) (session.Session, error) {
	nodeID := sess.GetServers(serviceName)
	if nodeID == "" {
		return nil, fmt.Errorf("[SessionBinder/GetNodeByName] session not bound to service %s", serviceName)
	}

	id, err := strconv.ParseInt(nodeID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("[SessionBinder/GetNodeByName] invalid node ID %s: %w", nodeID, err)
	}

	conn, ok := sb.connManager.GetByID(id)
	if !ok {
		return nil, fmt.Errorf("[SessionBinder/GetNodeByName] node %s connection not found", nodeID)
	}

	return conn, nil
}
