package cluster

import (
	"context"
	"errors"
	"fmt"
	"infra-foundation/config"
	"infra-foundation/logx"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	clientv3 "go.etcd.io/etcd/client/v3"
)

type EtcdServiceDiscovery struct {
	node *Node
	client    *clientv3.Client
	ctx       context.Context
	cancel    context.CancelFunc
	preKey    string
	closed    atomic.Bool
	ttl       int64
	wg        sync.WaitGroup
}

func NewEtcdServiceDiscovery(preKey string, addr string, node *Node) (*EtcdServiceDiscovery, error) {
	etcdCfg := clientv3.Config{Endpoints: []string{addr}, DialTimeout: config.Default.EtcdDialTimeout}
	client, err := clientv3.New(etcdCfg)
	if err != nil {
		return nil, fmt.Errorf("创建 etcd 客户端失败: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), config.Default.EtcdOpTimeout)
	defer cancel()
	if _, err = client.Status(ctx, addr); err != nil {
		client.Close()
		return nil, fmt.Errorf("etcd 连接验证失败 (地址: %s): %w", addr, err)
	}
	e := &EtcdServiceDiscovery{preKey: preKey, node: node, client: client, ttl: config.Default.EtcdTTL}
	e.ctx, e.cancel = context.WithCancel(context.TODO())
	e.wg.Go(e.watch)
	return e, nil
}

func (e *EtcdServiceDiscovery) RegisterService(name, advertiseAddr string, frontend bool, rids []int32) error {
	defer func() { e.list() }()

	for _, vn := range e.node.ServiceRegistry().GetNodes(name) {
		if vn.Addr != advertiseAddr {
			continue
		}
		logx.Dbg.Printf("[EtcdServiceDiscovery/RegisterService] ID: %v Name: %v Addr: %v", vn.Id, name, advertiseAddr)
		e.node.SetLocalNode(name, vn.Id, advertiseAddr, vn.Frontend)
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), config.Default.EtcdOpTimeout)
	defer cancel()

	logx.Dbg.Printf("[EtcdServiceDiscovery/RegisterService] Requesting lease from etcd, ttl=%d", e.ttl)
	grsp, err := e.client.Grant(ctx, e.ttl)
	if err != nil {
		return fmt.Errorf("[EtcdServiceDiscovery/RegisterService] Grant lease failed: %w", err)
	}

	id := strconv.Itoa(int(grsp.ID))
	k := fmt.Sprintf("%s/%s/%s", e.preKey, name, id)

	data, err := e.node.encodeRegistry(name, id, advertiseAddr, frontend, rids)
	if err != nil {
		return fmt.Errorf("[EtcdServiceDiscovery/RegisterService] Marshal failed: %w", err)
	}

	logx.Dbg.Printf("[EtcdServiceDiscovery/RegisterService] Putting key=%s to etcd", k)
	if _, err = e.client.Put(e.ctx, k, data, clientv3.WithLease(grsp.ID)); err != nil {
		return fmt.Errorf("[EtcdServiceDiscovery/RegisterService] Put failed: %w", err)
	}

	e.node.SetLocalNode(name, id, advertiseAddr, frontend)
	logx.Dbg.Printf("[EtcdServiceDiscovery/RegisterService] Registered - ID: %v Name: %v Addr: %v", grsp.ID, name, advertiseAddr)

	krsp, err := e.client.KeepAlive(e.ctx, grsp.ID)
	if err != nil {
		return fmt.Errorf("[EtcdServiceDiscovery/RegisterService] KeepAlive failed: %w", err)
	}

	go func() {
		logx.Dbg.Printf("[EtcdServiceDiscovery/RegisterService] KeepAlive started for lease %d", grsp.ID)
		for {
			select {
			case v, ok := <-krsp:
				if !ok {
					logx.War.Printf("[EtcdServiceDiscovery/RegisterService] KeepAlive channel closed for lease %d", grsp.ID)
					return
				}
				if v == nil {
					logx.War.Printf("[EtcdServiceDiscovery/RegisterService] KeepAlive response nil for lease %d", grsp.ID)
					return
				}
			case <-e.ctx.Done():
				logx.Dbg.Printf("[EtcdServiceDiscovery/RegisterService] Context done, stopping KeepAlive for lease %d", grsp.ID)
				return
			}
		}
	}()
	return nil
}

func (e *EtcdServiceDiscovery) Close() error {
	if !e.closed.CompareAndSwap(false, true) {
		return nil
	}
	e.cancel()
	e.wg.Wait()
	return e.client.Close()
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
		if err = e.node.decodeRegistry(name, v.Value); err != nil {
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
					e.node.ServiceRegistry().RemoveNode(name, id)
				case clientv3.EventTypePut:
					if err = e.node.decodeRegistry(name, ev.Kv.Value); err != nil {
						logx.Err.Println("[EtcdServiceDiscovery/watch] ", err)
					}
				}
			}
		}
	}
}

