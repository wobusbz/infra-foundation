package metrics

import (
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"
)

// Counter 是基于原子操作的无锁计数器。
type Counter struct {
	v atomic.Uint64
}

func (c *Counter) Inc()        { c.v.Add(1) }
func (c *Counter) Add(n uint64) { c.v.Add(n) }
func (c *Counter) Value() uint64 { return c.v.Load() }

// Gauge 是基于原子操作的无锁仪表盘。
type Gauge struct {
	v atomic.Int64
}

func (g *Gauge) Set(n int64)  { g.v.Store(n) }
func (g *Gauge) Add(n int64)  { g.v.Add(n) }
func (g *Gauge) Sub(n int64)  { g.v.Add(-n) }
func (g *Gauge) Value() int64 { return g.v.Load() }

var registry = struct {
	sync.RWMutex
	counters map[string]*Counter
	gauges   map[string]*Gauge
}{
	counters: make(map[string]*Counter),
	gauges:   make(map[string]*Gauge),
}

// CounterOf 获取或创建一个命名计数器。
func CounterOf(name string) *Counter {
	registry.RLock()
	c, ok := registry.counters[name]
	registry.RUnlock()
	if ok {
		return c
	}
	registry.Lock()
	defer registry.Unlock()
	if c, ok = registry.counters[name]; ok {
		return c
	}
	c = &Counter{}
	registry.counters[name] = c
	return c
}

// GaugeOf 获取或创建一个命名仪表盘。
func GaugeOf(name string) *Gauge {
	registry.RLock()
	g, ok := registry.gauges[name]
	registry.RUnlock()
	if ok {
		return g
	}
	registry.Lock()
	defer registry.Unlock()
	if g, ok = registry.gauges[name]; ok {
		return g
	}
	g = &Gauge{}
	registry.gauges[name] = g
	return g
}

// Snapshot 返回当前所有指标的快照，可用于 HTTP 暴露或日志输出。
func Snapshot() map[string]any {
	m := make(map[string]any)
	registry.RLock()
	for k, v := range registry.counters {
		m[k] = v.Value()
	}
	for k, v := range registry.gauges {
		m[k] = v.Value()
	}
	registry.RUnlock()
	return m
}

// ServeHTTP 实现了 http.Handler，可直接挂载到 /debug/metrics。
func ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(Snapshot())
}
