package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"infra-foundation/config"
	"infra-foundation/logx"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
)

type EtcdServiceDiscovery struct {
	handler        DiscoveryHandler
	onLocalNodeSet func(name, id, addr string, frontend bool, rids []int32)
	client         *clientv3.Client
	ctx            context.Context
	cancel         context.CancelFunc
	preKey         string
	closed         atomic.Bool
	ttl            int64
	wg             sync.WaitGroup
	leaseID        clientv3.LeaseID
	serviceKey     string
	mu             sync.Mutex
}

func NewEtcdServiceDiscovery(preKey string, addr string, handler DiscoveryHandler, onLocalNodeSet func(name, id, addr string, frontend bool, rids []int32)) (*EtcdServiceDiscovery, error) {
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
	e := &EtcdServiceDiscovery{preKey: preKey, handler: handler, onLocalNodeSet: onLocalNodeSet, client: client, ttl: config.Default.EtcdTTL}
	e.ctx, e.cancel = context.WithCancel(context.Background())

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

	e.mu.Lock()
	defer e.mu.Unlock()

	if e.leaseID != 0 {
		revokeCtx, revokeCancel := context.WithTimeout(context.Background(), config.Default.EtcdOpTimeout)
		if _, err := e.client.Revoke(revokeCtx, e.leaseID); err != nil {
			logx.War.Printf("[EtcdServiceDiscovery/RegisterService] failed to revoke old lease %d: %v", e.leaseID, err)
		}
		revokeCancel()
	}

	logx.Dbg.Printf("[EtcdServiceDiscovery/RegisterService] Requesting lease from etcd, ttl=%d", e.ttl)
	grsp, err := e.client.Grant(ctx, e.ttl)
	if err != nil {
		return fmt.Errorf("grant lease: %w", err)
	}

	id := strconv.Itoa(int(grsp.ID))
	preKey := strings.Trim(e.preKey, "/")
	var k string
	if preKey == "" {
		k = fmt.Sprintf("/%s/%s", name, id)
	} else {
		k = fmt.Sprintf("/%s/%s/%s", preKey, name, id)
	}
	if e.onLocalNodeSet != nil {
		e.onLocalNodeSet(name, id, advertiseAddr, frontend, rids)
	}
	localNode := &NodeInfo{Id: id, Name: name, Addr: advertiseAddr, Frontend: frontend, Routes: rids}
	data, err := json.Marshal(localNode)
	if err != nil {
		return fmt.Errorf("marshal service info: %w", err)
	}
	logx.Dbg.Printf("[EtcdServiceDiscovery/RegisterService] Putting key=%s to etcd value: %s", k, data)
	if _, err = e.client.Put(e.ctx, k, string(data), clientv3.WithLease(grsp.ID)); err != nil {
		return fmt.Errorf("etcd put: %w", err)
	}

	e.leaseID = grsp.ID
	e.serviceKey = k

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
		jitter := time.Duration(rand.Int63n(int64(backoff)))
		timer := time.NewTimer(backoff + jitter)
		select {
		case <-e.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		logx.Inf.Printf("[EtcdServiceDiscovery/tryReregister] re-registering %s@%s", name, advertiseAddr)
		if err := e.RegisterService(name, advertiseAddr, frontend, rids); err != nil {
			logx.Err.Printf("[EtcdServiceDiscovery/tryReregister] failed: %v, retry in %v", err, backoff+jitter)
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
	if e.leaseID != 0 {
		ctx, cancel := context.WithTimeout(context.Background(), config.Default.EtcdOpTimeout)
		defer cancel()
		if _, err := e.client.Revoke(ctx, e.leaseID); err != nil {
			logx.War.Printf("[EtcdServiceDiscovery/Close] failed to revoke lease %d: %v", e.leaseID, err)
		} else {
			logx.Inf.Printf("[EtcdServiceDiscovery/Close] lease %d revoked, key %s removed", e.leaseID, e.serviceKey)
		}
	}
	return e.client.Close()
}

func (e *EtcdServiceDiscovery) parseKey(k []byte) (kname, nid string, err error) {
	s := string(k)
	prefix := "/" + strings.Trim(e.preKey, "/")
	if !strings.HasPrefix(s, prefix) {
		return "", "", errors.New("key does not match prefix")
	}
	remainder := strings.TrimPrefix(s, prefix)
	remainder = strings.TrimPrefix(remainder, "/")
	parts := strings.Split(remainder, "/")
	if len(parts) != 2 {
		return "", "", errors.New("invalid key")
	}
	return parts[0], parts[1], nil
}

func (e *EtcdServiceDiscovery) handleRegistry(name string, data []byte) {
	e.handler.HandleDiscoveryEvent(DiscoveryEvent{Type: NodeUpdated, Name: name, Data: data})
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
		e.handleRegistry(name, v.Value)
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
					e.handler.HandleDiscoveryEvent(DiscoveryEvent{Type: NodeRemoved, Name: name, ID: id})
				case clientv3.EventTypePut:
					e.handleRegistry(name, ev.Kv.Value)
				}
			}
		}
	}
}
