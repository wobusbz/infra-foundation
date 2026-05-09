package cluster

import (
	"errors"
	"fmt"
	"infra-foundation/clusterpb"
	"infra-foundation/logx"
	"infra-foundation/protocol"
)

type MessageRouter struct {
	registry      *ServiceRegistry
	loadBalancer  *LoadBalancer
	sessionBinder *SessionBinder
	dispatcher    ModelDispatcher
}

func (mr *MessageRouter) ResolveNode(s BoundSession, name string) (*NodeInfo, error) {
	nodeID := s.GetServers(name)
	if nodeID != "" {
		if _, ok := mr.sessionBinder.GetNodeConnection(nodeID); ok {
			if node, ok := mr.registry.GetNodeByID(nodeID); ok {
				return node, nil
			}
		}
		s.UnbindServers(name)
	}
	return mr.loadBalancer.Pick(name)
}

func (mr *MessageRouter) ApplyBinding(s BoundSession, node *NodeInfo) (NodeConn, error) {
	conn, ok := mr.sessionBinder.GetNodeConnection(node.Id)
	if !ok {
		return nil, fmt.Errorf("node connection %s not found", node.Id)
	}

	if err := conn.SendTypePb(int8(protocol.ClusterSessionBind), &clusterpb.N2MOnSessionBind{
		SessionID: string(s.ID()),
		UID:       s.UID(),
		Servers:   s.Servers(),
	}); err != nil {
		return nil, fmt.Errorf("send bind to node %s: %w", node.Id, err)
	}

	s.BindServers(node.Name, node.Id)
	if ln := mr.sessionBinder.localNode(); ln != nil {
		s.BindServers(ln.Name, ln.Id)
	}

	return conn, nil
}

func (mr *MessageRouter) nodeBySession(s BoundSession, name string) (NodeConn, error) {
	node, err := mr.ResolveNode(s, name)
	if err != nil {
		return nil, fmt.Errorf("session %s failed to resolve node for service %s: %w", s.ID(), name, err)
	}
	conn, err := mr.ApplyBinding(s, node)
	if err != nil {
		return nil, fmt.Errorf("session %s failed to apply binding to node %s: %w", s.ID(), node.Id, err)
	}
	return conn, nil
}

func (mr *MessageRouter) broadcastSessionBind(s BoundSession) error {
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

func (mr *MessageRouter) broadcastSessionClose(s BoundSession) error {
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

func (mr *MessageRouter) bindNodeConn(id string, conn NodeConn) error {
	return mr.sessionBinder.StoreNodeConnection(id, conn)
}
