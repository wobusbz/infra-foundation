package cluster

import (
	"errors"
	"fmt"
	"infra-foundation/clusterpb"
	"infra-foundation/protocol"
	"infra-foundation/session"
)

type MessageRouter struct {
	registry      *ServiceRegistry
	loadBalancer  *LoadBalancer
	sessionBinder *SessionBinder
}

func (mr *MessageRouter) nodeBySession(s session.Session, name string) (session.Session, error) {
	nodeID := s.GetServers(name)
	if nodeID == "" {
		return mr.selectNode(name, s)
	}
	return mr.sessionBinder.GetNodeByName(s, name)
}

func (mr *MessageRouter) broadcastSessionClose(s session.Session) error {
	var errs []error
	s.RangeServers(func(name, nodeID string) bool {
		if mr.sessionBinder.localNode != nil && mr.sessionBinder.localNode.Name == name {
			return true
		}
		conn, ok := mr.sessionBinder.GetNodeConnection(nodeID)
		if !ok {
			return true
		}
		errs = append(errs, conn.SendTypePb(int8(protocol.ClusterDisconnect), &clusterpb.N2MOnSessionClose{SessionID: string(s.ID())}))
		return true
	})
	return errors.Join(errs...)
}

func (mr *MessageRouter) selectNode(name string, s session.Session) (session.Session, error) {
	node, err := mr.loadBalancer.Pick(name)
	if err != nil {
		return nil, err
	}

	if err = mr.sessionBinder.BindSessionToNode(s, node); err != nil {
		return nil, err
	}

	conn, ok := mr.sessionBinder.GetNodeConnection(node.Id)
	if !ok {
		return nil, fmt.Errorf("node %s connection not found", node.Id)
	}
	return conn, nil
}

func (mr *MessageRouter) bindNodeConn(id string, conn session.Session) error {
	return mr.sessionBinder.StoreNodeConnection(id, conn)
}

func (mr *MessageRouter) serviceByRoute(id int32) string {
	return mr.registry.GetServiceByRoute(id)
}

func (mr *MessageRouter) hasRoute(id int32) bool {
	return mr.registry.HasRoute(id)
}
