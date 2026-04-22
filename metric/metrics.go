package metric

import (
	"encoding/json"
	"math"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

type Counter struct {
	v atomic.Uint64
}

func (c *Counter) Inc()          { c.v.Add(1) }
func (c *Counter) Add(n uint64)  { c.v.Add(n) }
func (c *Counter) Value() uint64 { return c.v.Load() }

type Gauge struct {
	v atomic.Int64
}

func (g *Gauge) Set(n int64)  { g.v.Store(n) }
func (g *Gauge) Add(n int64)  { g.v.Add(n) }
func (g *Gauge) Sub(n int64)  { g.v.Add(-n) }
func (g *Gauge) Value() int64 { return g.v.Load() }

type Histogram struct {
	buckets []float64
	counts  []atomic.Uint64
	upper   atomic.Uint64
	sum     atomic.Uint64
	count   atomic.Uint64
}

var defaultBuckets = []float64{
	0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10,
}

func NewHistogram(buckets []float64) *Histogram {
	if len(buckets) == 0 {
		buckets = defaultBuckets
	}
	sorted := make([]float64, len(buckets))
	copy(sorted, buckets)
	sort.Float64s(sorted)
	h := &Histogram{buckets: sorted, counts: make([]atomic.Uint64, len(sorted))}
	return h
}

func (h *Histogram) Observe(v float64) {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return
	}
	us := uint64(v * 1e6)
	if us > 0 {
		h.sum.Add(us)
	}
	h.count.Add(1)
	for i, b := range h.buckets {
		if v <= b {
			h.counts[i].Add(1)
			return
		}
	}
	h.upper.Add(1)
}

func (h *Histogram) ObserveDuration(d time.Duration) {
	h.Observe(d.Seconds())
}

func (h *Histogram) Value() map[string]any {
	m := map[string]any{
		"count": h.count.Load(),
		"sum":   float64(h.sum.Load()) / 1e6,
	}
	buckets := make([]map[string]any, 0, len(h.buckets))
	var cumulative uint64
	for i, b := range h.buckets {
		cumulative += h.counts[i].Load()
		buckets = append(buckets, map[string]any{
			"le":    b,
			"count": cumulative,
		})
	}
	m["buckets"] = buckets
	m["+Inf"] = cumulative + h.upper.Load()
	return m
}

var registry = struct {
	sync.RWMutex
	counters   map[string]*Counter
	gauges     map[string]*Gauge
	histograms map[string]*Histogram
}{
	counters:   make(map[string]*Counter),
	gauges:     make(map[string]*Gauge),
	histograms: make(map[string]*Histogram),
}

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

func HistogramOf(name string) *Histogram {
	registry.RLock()
	h, ok := registry.histograms[name]
	registry.RUnlock()
	if ok {
		return h
	}
	registry.Lock()
	defer registry.Unlock()
	if h, ok = registry.histograms[name]; ok {
		return h
	}
	h = NewHistogram(nil)
	registry.histograms[name] = h
	return h
}

func Snapshot() map[string]any {
	m := make(map[string]any)
	registry.RLock()
	for k, v := range registry.counters {
		m[k] = v.Value()
	}
	for k, v := range registry.gauges {
		m[k] = v.Value()
	}
	for k, v := range registry.histograms {
		m[k] = v.Value()
	}
	registry.RUnlock()
	return m
}

func ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(Snapshot())
}
