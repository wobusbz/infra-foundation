package cluster

import (
	"encoding/json"
	"errors"
	"fmt"
	"infra-foundation/connmannger"
	"infra-foundation/logx"
	"infra-foundation/packet"
	"infra-foundation/session"
	"maps"
	"math/rand"
	"strconv"
	"sync"
	"sync/atomic"
)

type node struct {
	Id       string
	Name     string
	Addr     string
	Frontend bool
	Routes   []int32
}

func (n *node) connection(id, name string) error {
	conn := NewClientConnection(defaultNodeAgent.svr)
	if err := conn.DialConnection(n.Addr); err != nil {
		return err
	}
	return conn.SendTypePb(packet.Connection, &N2MOnConnection{ID: id, Name: name, Frontend: n.Frontend})
}

type NodeAgent struct {
	svr         Server
	node        *node
	nodes       map[string][]*node
	idNodes     map[string]*node
	m           sync.RWMutex
	tryState    atomic.Bool
	groutes     map[int32]string
	groutesrw   sync.RWMutex
	connManager *connmannger.ConnManager
}

var (
	_                = (*NodeAgent).removeByNameOrAddr
	_                = (*NodeAgent).getGroutes
	_                = (*NodeAgent).getNodeByName
	_                = (*NodeAgent).getGateNode
	_                = (*NodeAgent).storeNodeConn
	_                = (*NodeAgent).hasGroutes
	defaultNodeAgent = newNodeAgent()
)

func newNodeAgent() *NodeAgent {
	return &NodeAgent{
		nodes:       map[string][]*node{},
		idNodes:     map[string]*node{},
		groutes:     map[int32]string{},
		connManager: connmannger.NewConnManager(),
	}
}

func (n *NodeAgent) storeServer(svr Server) {
	n.svr = svr
}

func (n *NodeAgent) setNode(name, id, addr string, frontend bool) {
	n.node = &node{Id: id, Name: name, Addr: addr, Frontend: frontend}
}

func (n *NodeAgent) getNodeByName(s session.Session, name string) (session.Session, error) {
	nodeInstances := s.GetServers(name)
	if nodeInstances == "" {
		return n.pick(name, s)
	}
	id, _ := strconv.Atoi(nodeInstances)
	conn, ok := n.connManager.GetByID(int64(id))
	if !ok {
		return nil, fmt.Errorf("NodeName: %s NodeId: %s not found", name, nodeInstances)
	}
	return conn, nil
}

func (n *NodeAgent) notifyCloseSession(s session.Session) error {
	var errs []error
	for name, ids := range s.Servers() {
		if n.node.Name == name {
			continue
		}

		id, _ := strconv.Atoi(ids)
		conn, ok := n.connManager.GetByID(int64(id))
		if !ok {
			continue
		}
		errs = append(errs, conn.SendPb(packet.DisConnection, 0, &N2MOnSessionClose{SessionID: s.ID()}))
	}
	return errors.Join(errs...)
}

func (n *NodeAgent) getGateNode(s session.Session) (session.Session, error) {
	for _, v := range s.Servers() {
		node, ok := n.idNodes[v]
		if !ok {
			continue
		}
		if !node.Frontend {
			continue
		}
		id, _ := strconv.Atoi(v)
		conn, ok := n.connManager.GetByID(int64(id))
		if !ok {
			return nil, fmt.Errorf("NodeName: %s NodeId: %s not found", node.Name, node.Id)
		}
		return conn, nil
	}
	return nil, fmt.Errorf("Session[%d] gate not found", s.ID())
}

func (n *NodeAgent) pick(name string, s session.Session) (session.Session, error) {
	n.m.RLock()
	defer n.m.RUnlock()
	nodes, ok := n.nodes[name]
	if !ok {
		return nil, fmt.Errorf("%s not found", name)
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("%s len == 0", name)
	}
	node := nodes[rand.Int()%len(n.nodes[name])]
	id, _ := strconv.Atoi(node.Id)
	conn, ok := n.connManager.GetByID(int64(id))
	if !ok {
		return nil, fmt.Errorf("NodeName: %s NodeId: %s not found", name, node.Id)
	}
	s.BindServers(name, node.Id)
	s.BindServers(defaultNodeAgent.node.Name, defaultNodeAgent.node.Id)
	return conn, conn.SendTypePb(packet.BindConnection, &N2MOnSessionBindServer{
		SessionID: s.ID(),
		UID:       s.UID(),
		Servers:   s.Servers(),
	})
}

func (n *NodeAgent) addNode(name, id, addr string, frontend bool, rids []int32) {
	n.m.Lock()
	node := &node{Id: id, Name: name, Addr: addr, Frontend: frontend, Routes: rids}
	n.nodes[name] = append(n.nodes[name], node)
	n.idNodes[id] = node
	n.m.Unlock()
}

func (n *NodeAgent) storeNodeConn(name, id string, conn session.Session) {
	iid, _ := strconv.Atoi(id)
	conn.BindID(int64(iid))
	conn.BindUID(-1)
	n.connManager.StoreSession(conn)
}

func (n *NodeAgent) Marshal(name, id, addr string, frontend bool, rids []int32) string {
	n.addNode(name, id, addr, frontend, rids)
	rb, _ := json.Marshal(n.list(name))
	return string(rb)
}

func (n *NodeAgent) removeByNameOrId(name, id string) {
	n.m.Lock()
	defer n.m.Unlock()
	if _, ok := n.nodes[name]; !ok {
		return
	}
	var newNodes []*node
	for _, v := range n.nodes[name] {
		if v.Id == id {
			continue
		}
		newNodes = append(newNodes, v)
	}
	n.nodes[name] = newNodes
	if len(n.nodes[name]) == 0 {
		delete(n.nodes, name)
	}
	delete(n.idNodes, id)
}

func (n *NodeAgent) removeByNameOrAddr(name, addr string) {
	n.m.Lock()
	defer n.m.Unlock()
	if _, ok := n.nodes[name]; !ok {
		return
	}
	var newNodes []*node
	for _, v := range n.nodes[name] {
		if v.Id == addr {
			continue
		}
		newNodes = append(newNodes, v)
	}
	n.nodes[name] = newNodes
	if len(n.nodes[name]) == 0 {
		delete(n.nodes, name)
	}
}

func (n *NodeAgent) mapList() map[string][]*node {
	n.m.RLock()
	defer n.m.RUnlock()
	return n.nodes
}

func (n *NodeAgent) list(name string) []*node {
	n.m.RLock()
	defer n.m.RUnlock()
	return n.nodes[name]
}

func (n *NodeAgent) Unmarshal(k string, sb []byte) error {
	var v = make(map[string]*node, len(n.nodes))
	for _, vv := range n.list(k) {
		v[vv.Id] = vv
	}
	var vns []*node
	if err := json.Unmarshal(sb, &vns); err != nil {
		return err
	}
	var mns = make(map[string]*node, len(vns))
	for _, vv := range vns {
		mns[vv.Id] = vv
		n.groutesrw.Lock()
		for _, id := range vv.Routes {
			n.groutes[id] = vv.Name
		}
		n.groutesrw.Unlock()
		if _, ok := v[vv.Id]; ok {
			continue
		}
		if vv.Id == n.node.Id {
			continue
		}
		if n.node.Id > vv.Id {
			continue
		}
		if err := vv.connection(n.node.Id, n.node.Name); err != nil {
			return err
		}
	}
	n.m.Lock()
	n.nodes[k] = vns
	maps.Copy(n.idNodes, mns)
	logx.Dbg.Println(k, string(sb), n.idNodes)
	n.m.Unlock()
	return nil
}

func (n *NodeAgent) getGroutes(id int32) string {
	n.groutesrw.RLock()
	defer n.groutesrw.RUnlock()
	return n.groutes[id]
}

func (n *NodeAgent) hasGroutes(id int32) bool {
	n.groutesrw.RLock()
	defer n.groutesrw.RUnlock()
	_, ok := n.groutes[id]
	return ok
}
