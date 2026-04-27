package cluster

import (
	"fmt"
	"infra-foundation/logx"
	"math/rand"
	"slices"
	"sync"
	"time"
)

type LoadBalancer struct {
	registry *ServiceRegistry
	health   map[string]bool
	mu       sync.RWMutex
	rnd      *rand.Rand
}

func NewLoadBalancer(registry *ServiceRegistry) *LoadBalancer {
	return &LoadBalancer{
		registry: registry,
		health:   make(map[string]bool),
		rnd:      rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

func (lb *LoadBalancer) MarkHealthy(nodeID string, healthy bool) {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	lb.health[nodeID] = healthy
}

func (lb *LoadBalancer) IsHealthy(nodeID string) bool {
	lb.mu.RLock()
	defer lb.mu.RUnlock()
	return lb.health[nodeID]
}

func (lb *LoadBalancer) Pick(serviceName string) (*NodeInfo, error) {
	nodes := lb.registry.GetNodes(serviceName)

	lb.mu.RLock()
	healthyNodes := make([]*NodeInfo, 0, len(nodes))
	for _, node := range nodes {
		if lb.health[node.Id] {
			healthyNodes = append(healthyNodes, node)
		}
	}
	lb.mu.RUnlock()

	if len(healthyNodes) == 0 && len(nodes) > 0 {
		healthyNodes = nodes
	}

	if len(healthyNodes) == 0 {
		logx.War.Printf("no available nodes for service: %s", serviceName)
		return nil, fmt.Errorf("no available nodes for service: %s", serviceName)
	}

	lb.mu.Lock()
	idx := lb.rnd.Intn(len(healthyNodes))
	lb.mu.Unlock()
	return healthyNodes[idx], nil
}

func (lb *LoadBalancer) PickFrontend(serviceName string) (*NodeInfo, error) {
	nodes := lb.registry.GetNodes(serviceName)

	lb.mu.RLock()
	var frontendNodes []*NodeInfo
	for _, node := range nodes {
		if node.Frontend && lb.health[node.Id] {
			frontendNodes = append(frontendNodes, node)
		}
	}
	lb.mu.RUnlock()

	if len(frontendNodes) == 0 && len(nodes) > 0 {
		for _, node := range nodes {
			if node.Frontend {
				frontendNodes = append(frontendNodes, node)
			}
		}
	}

	if len(frontendNodes) == 0 {
		logx.War.Printf("no frontend nodes for service: %s", serviceName)
		return nil, fmt.Errorf("no frontend nodes for service: %s", serviceName)
	}

	lb.mu.Lock()
	idx := lb.rnd.Intn(len(frontendNodes))
	lb.mu.Unlock()
	return frontendNodes[idx], nil
}

func (lb *LoadBalancer) PickByID(id string) (*NodeInfo, error) {
	node, ok := lb.registry.GetNodeByID(id)
	if !ok {
		return nil, fmt.Errorf("node %s not found", id)
	}
	return node, nil
}

func (lb *LoadBalancer) GetNodesByRoute(routeID int32) []*NodeInfo {
	allNodes := lb.registry.GetAllNodes()

	var result []*NodeInfo
	for _, nodes := range allNodes {
		for _, node := range nodes {
			if slices.Contains(node.Routes, routeID) {
				result = append(result, node)
			}
		}
	}

	return result
}
