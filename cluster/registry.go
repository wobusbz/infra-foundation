package cluster

import (
	"encoding/json"
	"slices"
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

func (r *ServiceRegistry) AddNode(name, id, addr string, frontend bool, routes []int32) {
	r.m.Lock()
	defer r.m.Unlock()

	// 去重：如果节点已存在，更新信息而不是重复添加
	for i, n := range r.nodes[name] {
		if n.Id == id {
			r.nodes[name][i].Addr = addr
			r.nodes[name][i].Frontend = frontend
			r.nodes[name][i].Routes = routes
			r.idNodes[id] = r.nodes[name][i]
			r.rebuildRoutesLocked(name, id, routes)
			return
		}
	}

	node := &NodeInfo{
		Id:       id,
		Name:     name,
		Addr:     addr,
		Frontend: frontend,
		Routes:   routes,
	}

	r.nodes[name] = append(r.nodes[name], node)
	r.idNodes[id] = node

	r.rebuildRoutesLocked(name, id, routes)
}

// rebuildRoutesLocked 在持有 r.m 锁的情况下重建路由表。
func (r *ServiceRegistry) rebuildRoutesLocked(name, updatedID string, newRoutes []int32) {
	r.routesMu.Lock()
	defer r.routesMu.Unlock()
	// 清理旧路由中不再被该服务任何节点持有的条目
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
	var nodes []*NodeInfo
	if err := json.Unmarshal(data, &nodes); err != nil {
		return nil, err
	}
	r.m.Lock()
	r.nodes[name] = nodes
	for _, node := range nodes {
		r.idNodes[node.Id] = node
	}
	r.m.Unlock()

	r.routesMu.Lock()
	for _, node := range nodes {
		for _, routeID := range node.Routes {
			r.routes[routeID] = node.Name
		}
	}
	r.routesMu.Unlock()

	return nodes, nil
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
}
