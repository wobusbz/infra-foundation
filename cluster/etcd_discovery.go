package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"infra-foundation/config"
	"infra-foundation/logx"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
)

type EtcdServiceDiscovery struct {
	node   *Node
	client *clientv3.Client
	ctx    context.Context
	cancel context.CancelFunc
	preKey string
	closed atomic.Bool
	ttl    int64
	wg     sync.WaitGroup
}

func NewEtcdServiceDiscovery(preKey string, addr string, node *Node) (*EtcdServiceDiscovery, error) {
	etcdCfg := clientv3.Config{Endpoints: []string{addr}, DialTimeout: config.Default.EtcdDialTimeout}
	client, err := clientv3.New(etcdCfg)
	if err != nil {
		return nil, fmt.Errorf("create etcd client: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), config.Default.EtcdOpTimeout)
	defer cancel()
	if _, err = client.Status(ctx, addr); err != nil {
		client.Close()
		return nil, fmt.Errorf("etcd ping %s: %w", addr, err)
	}
	e := &EtcdServiceDiscovery{preKey: preKey, node: node, client: client, ttl: config.Default.EtcdTTL}
	e.ctx, e.cancel = context.WithCancel(context.TODO())

	if err = e.list(); err != nil {
		client.Close()
		cancel()
		return nil, fmt.Errorf("list etcd services: %w", err)
	}

	e.wg.Go(e.watch)
	return e, nil
}

func (e *EtcdServiceDiscovery) RegisterService(name, advertiseAddr string, frontend bool, rids []int32) error {
	ctx, cancel := context.WithTimeout(context.Background(), config.Default.EtcdOpTimeout)
	defer cancel()

	logx.Dbg.Printf("[EtcdServiceDiscovery/RegisterService] Requesting lease from etcd, ttl=%d", e.ttl)
	grsp, err := e.client.Grant(ctx, e.ttl)
	if err != nil {
		return fmt.Errorf("grant lease: %w", err)
	}

	id := strconv.Itoa(int(grsp.ID))
	k := fmt.Sprintf("/%s/%s/%s", strings.Trim(e.preKey, "/"), name, id)
	e.node.SetLocalNode(name, id, advertiseAddr, frontend, rids)
	data, err := json.Marshal(e.node.LocalNode())
	if err != nil {
		return fmt.Errorf("marshal service info: %w", err)
	}
	logx.Dbg.Printf("[EtcdServiceDiscovery/RegisterService] Putting key=%s to etcd value: %s", k, data)
	if _, err = e.client.Put(e.ctx, k, string(data), clientv3.WithLease(grsp.ID)); err != nil {
		return fmt.Errorf("etcd put: %w", err)
	}

	logx.Dbg.Printf("[EtcdServiceDiscovery/RegisterService] Registered - ID: %v Name: %v Addr: %v", grsp.ID, name, advertiseAddr)

	krsp, err := e.client.KeepAlive(e.ctx, grsp.ID)
	if err != nil {
		return fmt.Errorf("etcd keepalive: %w", err)
	}

	e.wg.Go(func() {
		e.keepAliveLoop(krsp, grsp.ID, name, advertiseAddr, frontend, rids)
	})
	return nil
}

func (e *EtcdServiceDiscovery) keepAliveLoop(krsp <-chan *clientv3.LeaseKeepAliveResponse, leaseID clientv3.LeaseID, name, advertiseAddr string, frontend bool, rids []int32) {
	logx.Dbg.Printf("[EtcdServiceDiscovery/keepAlive] started for lease %d", leaseID)
	for {
		select {
		case v, ok := <-krsp:
			if !ok {
				logx.War.Printf("[EtcdServiceDiscovery/keepAlive] channel closed for lease %d, will reregister", leaseID)
				e.tryReregister(name, advertiseAddr, frontend, rids)
				return
			}
			if v == nil {
				logx.War.Printf("[EtcdServiceDiscovery/keepAlive] nil response for lease %d, will reregister", leaseID)
				e.tryReregister(name, advertiseAddr, frontend, rids)
				return
			}
		case <-e.ctx.Done():
			logx.Dbg.Printf("[EtcdServiceDiscovery/keepAlive] context done for lease %d", leaseID)
			return
		}
	}
}

func (e *EtcdServiceDiscovery) tryReregister(name, advertiseAddr string, frontend bool, rids []int32) {
	backoff := time.Second
	maxBackoff := 30 * time.Second
	for {
		select {
		case <-e.ctx.Done():
			return
		case <-time.After(backoff):
		}
		logx.Inf.Printf("[EtcdServiceDiscovery/tryReregister] re-registering %s@%s", name, advertiseAddr)
		if err := e.RegisterService(name, advertiseAddr, frontend, rids); err != nil {
			logx.Err.Printf("[EtcdServiceDiscovery/tryReregister] failed: %v, retry in %v", err, backoff)
			if backoff < maxBackoff {
				backoff *= 2
			}
			continue
		}
		logx.Inf.Printf("[EtcdServiceDiscovery/tryReregister] success")
		return
	}
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
	parts := make([]string, 0, len(snames))
	for _, s := range snames {
		if s == "" {
			continue
		}
		parts = append(parts, s)
	}
	if len(parts) != 3 {
		err = errors.New("Invalid k")
		return
	}
	return parts[1], parts[2], nil
}

func (e *EtcdServiceDiscovery) list() error {
	prefix := "/" + strings.Trim(e.preKey, "/")
	grsp, err := e.client.Get(e.ctx, prefix, clientv3.WithPrefix())
	if err != nil {
		return fmt.Errorf("list services: %w", err)
	}
	for _, v := range grsp.Kvs {
		name, _, err := e.parseKey(v.Key)
		if err != nil {
			logx.Err.Printf("[EtcdServiceDiscovery/list] skipping invalid key: %s", string(v.Key))
			continue
		}
		if err = e.node.decodeRegistryAndConnect(name, v.Value); err != nil {
			logx.Err.Printf("[EtcdServiceDiscovery/list] failed to decode registry for %s: %v", name, err)
			continue
		}
	}
	return nil
}

func (e *EtcdServiceDiscovery) watch() {
	prefix := "/" + strings.Trim(e.preKey, "/")
	wch := e.client.Watch(e.ctx, prefix, clientv3.WithPrefix())
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
					if err = e.node.decodeRegistryAndConnect(name, ev.Kv.Value); err != nil {
						logx.Err.Println("[EtcdServiceDiscovery/watch] ", err)
					}
				}
			}
		}
	}
}
