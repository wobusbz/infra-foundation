package cluster

import (
	"fmt"
	"infra-foundation/logx"
	"math/rand"
	"sync"
	"time"
)

type LoadBalancer struct {
	registry *ServiceRegistry
	health   map[string]bool
	mu       sync.RWMutex
	rnd      *rand.Rand
	rndMu    sync.Mutex
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

	lb.rndMu.Lock()
	idx := lb.rnd.Intn(len(healthyNodes))
	lb.rndMu.Unlock()
	return healthyNodes[idx], nil
}
