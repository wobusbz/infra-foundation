package cluster

import (
	"errors"
	"fmt"
	"infra-foundation/clusterpb"
	"infra-foundation/logx"
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
		return nil, fmt.Errorf("session %s has no binding for service %s", s.ID(), name)
	}
	conn, ok := mr.sessionBinder.GetNodeConnection(nodeID)
	if !ok {
		return nil, fmt.Errorf("node connection %s not found for service %s", nodeID, name)
	}
	return conn, nil
}

func (mr *MessageRouter) broadcastSessionBind(s session.Session) error {
	allNodes := mr.registry.GetAllNodes()
	var errs []error
	for name := range allNodes {
		if mr.sessionBinder.IsLocalNodeName(name) {
			continue
		}
		node, err := mr.loadBalancer.Pick(name)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if err := mr.sessionBinder.BindSessionToNode(s, node); err != nil {
			errs = append(errs, err)
			continue
		}
	}
	if len(errs) > 0 {
		logx.War.Printf("[MessageRouter/BindToAllServices] session %s partial bind failures: %v", s.ID(), errors.Join(errs...))
	}
	return errors.Join(errs...)
}

func (mr *MessageRouter) broadcastSessionClose(s session.Session) error {
	var errs []error
	s.RangeServers(func(name, nodeID string) bool {
		if mr.sessionBinder.IsLocalNodeName(name) {
			return true
		}
		conn, ok := mr.sessionBinder.GetNodeConnection(nodeID)
		if !ok {
			return true
		}
		errs = append(errs, conn.SendTypePb(int8(protocol.ClusterSessionDisconnect), &clusterpb.N2MOnSessionDisconnected{SessionID: string(s.ID())}))
		return true
	})
	return errors.Join(errs...)
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
