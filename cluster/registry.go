package cluster

import (
	"encoding/json"
	"infra-foundation/logx"
	"slices"
	"strings"
	"sync"
)

type NodeInfo struct {
	Id       string
	Name     string
	Addr     string
	Frontend bool
	Routes   []int32
}

type ServiceRegistry struct {
	m        sync.RWMutex
	nodes    map[string][]*NodeInfo
	idNodes  map[string]*NodeInfo
	routes   map[int32]string
	routesMu sync.RWMutex
}

func NewServiceRegistry() *ServiceRegistry {
	return &ServiceRegistry{nodes: make(map[string][]*NodeInfo), idNodes: make(map[string]*NodeInfo), routes: make(map[int32]string)}
}

func (r *ServiceRegistry) AddNode(nodes *NodeInfo) {
	name := strings.ToLower(nodes.Name)
	r.m.Lock()
	defer r.m.Unlock()

	cloned := &NodeInfo{
		Id:       nodes.Id,
		Name:     name,
		Addr:     nodes.Addr,
		Frontend: nodes.Frontend,
		Routes:   append([]int32{}, nodes.Routes...),
	}

	for i, n := range r.nodes[name] {
		if n.Id == nodes.Id {
			r.nodes[name][i] = cloned
			r.idNodes[nodes.Id] = cloned
			r.rebuildRoutesLocked(name, cloned.Routes)
			return
		}
	}

	r.nodes[name] = append(r.nodes[name], cloned)
	r.idNodes[nodes.Id] = cloned

	r.rebuildRoutesLocked(name, cloned.Routes)
}

func (r *ServiceRegistry) rebuildRoutesLocked(name string, newRoutes []int32) {
	r.routesMu.Lock()
	defer r.routesMu.Unlock()
	for routeID, svcName := range r.routes {
		if svcName != name {
			continue
		}
		found := false
		for _, n := range r.nodes[name] {
			if slices.ContainsFunc(n.Routes, func(rid int32) bool { return rid == routeID }) {
				found = true
				break
			}
		}
		if !found {
			delete(r.routes, routeID)
		}
	}
	for _, routeID := range newRoutes {
		r.routes[routeID] = name
	}
}

func (r *ServiceRegistry) GetNodes(name string) []*NodeInfo {
	name = strings.ToLower(name)
	r.m.RLock()
	defer r.m.RUnlock()

	nodes := r.nodes[name]
	return append([]*NodeInfo{}, nodes...)
}

func (r *ServiceRegistry) GetServiceByRoute(routeID int32) string {
	r.routesMu.RLock()
	defer r.routesMu.RUnlock()
	return r.routes[routeID]
}

func (r *ServiceRegistry) HasRoute(routeID int32) bool {
	r.routesMu.RLock()
	defer r.routesMu.RUnlock()
	_, ok := r.routes[routeID]
	return ok
}

func (r *ServiceRegistry) GetNodeByID(id string) (*NodeInfo, bool) {
	r.m.RLock()
	defer r.m.RUnlock()

	node, ok := r.idNodes[id]
	return node, ok
}

func (r *ServiceRegistry) GetAllNodes() map[string][]*NodeInfo {
	r.m.RLock()
	defer r.m.RUnlock()

	nodesCopy := make(map[string][]*NodeInfo, len(r.nodes))
	for k, v := range r.nodes {
		nodesCopy[k] = append([]*NodeInfo{}, v...)
	}
	return nodesCopy
}

func (r *ServiceRegistry) Marshal(name string) (string, error) {
	nodes := r.GetNodes(name)
	rb, err := json.Marshal(nodes)
	if err != nil {
		return "", err
	}
	return string(rb), nil
}

func (r *ServiceRegistry) Unmarshal(name string, data []byte) ([]*NodeInfo, error) {
	name = strings.ToLower(name)
	var nodes *NodeInfo
	if err := json.Unmarshal(data, &nodes); err != nil {
		return nil, err
	}
	r.m.Lock()
	r.nodes[name] = slices.DeleteFunc(r.nodes[name], func(node *NodeInfo) bool { return node.Addr == nodes.Addr })
	r.m.Unlock()

	r.AddNode(nodes)

	r.routesMu.Lock()

	logx.Dbg.Println(string(data))
	for _, rid := range nodes.Routes {
		r.routes[rid] = name
	}
	r.routesMu.Unlock()
	return r.GetNodes(name), nil
}

func (r *ServiceRegistry) Size() int {
	r.m.RLock()
	defer r.m.RUnlock()
	return len(r.nodes)
}

func (r *ServiceRegistry) HasNode(name, id string) bool {
	r.m.RLock()
	defer r.m.RUnlock()

	nodes, ok := r.nodes[name]
	if !ok {
		return false
	}

	for _, node := range nodes {
		if node.Id == id {
			return true
		}
	}
	return false
}

func (r *ServiceRegistry) RemoveNode(name, id string) {
	name = strings.ToLower(name)
	r.m.Lock()
	defer r.m.Unlock()

	if _, ok := r.nodes[name]; !ok {
		return
	}
	var newNodes []*NodeInfo
	for _, v := range r.nodes[name] {
		if v.Id == id {
			continue
		}
		newNodes = append(newNodes, v)
	}

	r.nodes[name] = newNodes
	if len(r.nodes[name]) == 0 {
		delete(r.nodes, name)
	}
	delete(r.idNodes, id)

	r.routesMu.Lock()
	defer r.routesMu.Unlock()
	for routeID, svcName := range r.routes {
		if svcName == name {
			delete(r.routes, routeID)
		}
	}
	if nodes, ok := r.nodes[name]; ok {
		for _, n := range nodes {
			for _, rid := range n.Routes {
				r.routes[rid] = name
			}
		}
	}
}
