package cluster

import (
	"context"
	"errors"
	"fmt"
	"infra-foundation/logx"
	"strconv"
	"strings"
	"sync"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
)

type EtcdServiceDiscovery struct {
	nodeAgent *NodeAgent
	client    *clientv3.Client
	ctx       context.Context
	cancel    context.CancelFunc
	preKey    string
	closeOnce sync.Once
	ttl       int64
}

func NewEtcdServiceDiscovery(preKey string, addr string) (*EtcdServiceDiscovery, error) {
	client, err := clientv3.New(clientv3.Config{Endpoints: []string{addr}, DialTimeout: 5 * time.Second})
	if err != nil {
		return nil, err
	}
	e := &EtcdServiceDiscovery{preKey: preKey, nodeAgent: defaultNodeAgent, client: client, ttl: 5}
	e.ctx, e.cancel = context.WithCancel(context.TODO())
	go e.watch()
	return e, nil
}

func (e *EtcdServiceDiscovery) RegisterService(name, advertiseAddr string, frontend bool, rids []int32) error {
	defer func() { e.list() }()

	for _, vn := range e.nodeAgent.mapList()[name] {
		if vn.Addr != advertiseAddr {
			continue
		}
		logx.Dbg.Printf("[EtcdServiceDiscovery/RegisterService] ID: %v Name: %v Addr: %v", vn.Id, name, advertiseAddr)
		e.nodeAgent.setNode(name, vn.Id, advertiseAddr, vn.Frontend)
		return nil
	}
	grsp, err := e.client.Grant(context.Background(), e.ttl)
	if err != nil {
		return err
	}
	id := strconv.Itoa(int(grsp.ID))
	k := fmt.Sprintf("%s/%s/%s", e.preKey, name, id)
	bdata := e.nodeAgent.Marshal(name, id, advertiseAddr, frontend, rids)
	if _, err = e.client.Put(e.ctx, k, bdata, clientv3.WithLease(grsp.ID)); err != nil {
		return err
	}
	e.nodeAgent.setNode(name, id, advertiseAddr, frontend)
	logx.Dbg.Printf("[EtcdServiceDiscovery/RegisterService] ID: %v Name: %v Addr: %v", grsp.ID, name, advertiseAddr)
	krsp, err := e.client.KeepAlive(e.ctx, grsp.ID)
	if err != nil {
		return err
	}
	go func() {
		for {
			if v := <-krsp; v == nil {
				return
			}
		}
	}()
	return nil
}

func (e *EtcdServiceDiscovery) MapList() map[string][]*node {
	return e.nodeAgent.mapList()
}

func (e *EtcdServiceDiscovery) List(name string) []*node {
	return e.nodeAgent.list(name)
}

func (e *EtcdServiceDiscovery) Close() {
	e.closeOnce.Do(func() {
		e.client.Close()
		e.cancel()
	})
}

func (e *EtcdServiceDiscovery) parseKey(k []byte) (kname, nid string, err error) {
	snames := strings.Split(string(k), "/")
	if len(snames) != 3 {
		err = errors.New("Invalid k")
		return
	}
	return snames[1], snames[2], nil
}

func (e *EtcdServiceDiscovery) list() error {
	grsp, err := e.client.Get(e.ctx, e.preKey, clientv3.WithPrefix())
	if err != nil {
		return fmt.Errorf("[EtcdServiceDiscovery/list] %w", err)
	}
	for _, v := range grsp.Kvs {
		name, _, err := e.parseKey(v.Key)
		if err != nil {
			return fmt.Errorf("[EtcdServiceDiscovery/list] %w", err)
		}
		if err = e.nodeAgent.Unmarshal(name, v.Value); err != nil {
			return fmt.Errorf("[EtcdServiceDiscovery/list] %w", err)
		}
	}
	return nil
}

func (e *EtcdServiceDiscovery) watch() {
	wch := e.client.Watch(e.ctx, e.preKey, clientv3.WithPrefix())
	for {
		select {
		case <-e.ctx.Done():
			return
		case wresp, ok := <-wch:
			if !ok {
				return
			}
			if wresp.Canceled {
				logx.Err.Printf("[EtcdServiceDiscovery/watch] canceled: %v", wresp.Err())
				return
			}
			for _, ev := range wresp.Events {
				name, id, err := e.parseKey(ev.Kv.Key)
				if err != nil {
					continue
				}
				switch ev.Type {
				case clientv3.EventTypeDelete:
					e.nodeAgent.removeByNameOrId(name, id)
				case clientv3.EventTypePut:
					if err = e.nodeAgent.Unmarshal(name, ev.Kv.Value); err != nil {
						logx.Err.Println("[EtcdServiceDiscovery/watch] ", err)
					}
				}
			}
		}
	}
}
