package telemetry

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
)

// DefBuckets defines standard Prometheus latency histogram buckets in seconds.
var DefBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0}

// Metric represents a measurable time-series item.
type Metric interface {
	Name() string
	Help() string
	Type() string
	WritePrometheus(buf *strings.Builder)
}

// Collector allows external dynamic subsystems (such as database pools or message queues)
// to sample and emit metrics on-demand during a Prometheus scrape.
type Collector interface {
	Collect()
}

// Registry manages registered metrics and collectors.
type Registry struct {
	mu         sync.RWMutex
	metrics    []Metric
	collectors []Collector
}

// NewRegistry creates a new metrics registry.
func NewRegistry() *Registry {
	return &Registry{
		metrics:    make([]Metric, 0, 32),
		collectors: make([]Collector, 0, 8),
	}
}

// Register adds a metric to the registry.
func (r *Registry) Register(m Metric) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.metrics = append(r.metrics, m)
}

// RegisterCollector registers a dynamic collector sampled on scrape.
func (r *Registry) RegisterCollector(c Collector) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.collectors = append(r.collectors, c)
}

// Gather executes all dynamic collectors and renders all registered metrics
// into standard Prometheus 0.0.4 text exposition format.
func (r *Registry) Gather() string {
	r.mu.RLock()
	// Run dynamic collectors
	for _, c := range r.collectors {
		c.Collect()
	}

	var buf strings.Builder
	buf.Grow(4096)

	for _, m := range r.metrics {
		buf.WriteString("# HELP ")
		buf.WriteString(m.Name())
		buf.WriteString(" ")
		buf.WriteString(m.Help())
		buf.WriteString("\n# TYPE ")
		buf.WriteString(m.Name())
		buf.WriteString(" ")
		buf.WriteString(m.Type())
		buf.WriteString("\n")
		m.WritePrometheus(&buf)
	}
	r.mu.RUnlock()
	return buf.String()
}

// --- Counter ---

// Counter represents a monotonically increasing cumulative metric.
type Counter struct {
	name      string
	help      string
	labelKeys []string
	mu        sync.RWMutex
	values    map[string]float64
}

// NewCounter registers a counter with the registry.
func (r *Registry) NewCounter(name, help string, labelKeys []string) *Counter {
	c := &Counter{
		name:      name,
		help:      help,
		labelKeys: labelKeys,
		values:    make(map[string]float64),
	}
	r.Register(c)
	return c
}

func (c *Counter) Name() string { return c.name }
func (c *Counter) Help() string { return c.help }
func (c *Counter) Type() string { return "counter" }

func (c *Counter) Inc(labelValues ...string) {
	c.Add(1, labelValues...)
}

func (c *Counter) Add(val float64, labelValues ...string) {
	if val <= 0 {
		return
	}
	key := encodeLabels(c.labelKeys, labelValues)
	c.mu.Lock()
	c.values[key] += val
	c.mu.Unlock()
}

func (c *Counter) WritePrometheus(buf *strings.Builder) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// Sort keys for deterministic output
	keys := make([]string, 0, len(c.values))
	for k := range c.values {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	if len(keys) == 0 && len(c.labelKeys) == 0 {
		buf.WriteString(c.name)
		buf.WriteString(" 0\n")
		return
	}

	for _, k := range keys {
		buf.WriteString(c.name)
		if k != "" {
			buf.WriteString("{")
			buf.WriteString(k)
			buf.WriteString("}")
		}
		buf.WriteString(" ")
		buf.WriteString(formatFloat(c.values[k]))
		buf.WriteString("\n")
	}
}

// --- Gauge ---

// Gauge represents a single numerical value that can arbitrarily go up or down.
type Gauge struct {
	name      string
	help      string
	labelKeys []string
	mu        sync.RWMutex
	values    map[string]float64
}

// NewGauge registers a gauge with the registry.
func (r *Registry) NewGauge(name, help string, labelKeys []string) *Gauge {
	g := &Gauge{
		name:      name,
		help:      help,
		labelKeys: labelKeys,
		values:    make(map[string]float64),
	}
	r.Register(g)
	return g
}

func (g *Gauge) Name() string { return g.name }
func (g *Gauge) Help() string { return g.help }
func (g *Gauge) Type() string { return "gauge" }

func (g *Gauge) Set(val float64, labelValues ...string) {
	key := encodeLabels(g.labelKeys, labelValues)
	g.mu.Lock()
	g.values[key] = val
	g.mu.Unlock()
}

func (g *Gauge) Inc(labelValues ...string) {
	g.Add(1, labelValues...)
}

func (g *Gauge) Dec(labelValues ...string) {
	g.Add(-1, labelValues...)
}

func (g *Gauge) Add(val float64, labelValues ...string) {
	key := encodeLabels(g.labelKeys, labelValues)
	g.mu.Lock()
	g.values[key] += val
	g.mu.Unlock()
}

func (g *Gauge) WritePrometheus(buf *strings.Builder) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	keys := make([]string, 0, len(g.values))
	for k := range g.values {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	if len(keys) == 0 && len(g.labelKeys) == 0 {
		buf.WriteString(g.name)
		buf.WriteString(" 0\n")
		return
	}

	for _, k := range keys {
		buf.WriteString(g.name)
		if k != "" {
			buf.WriteString("{")
			buf.WriteString(k)
			buf.WriteString("}")
		}
		buf.WriteString(" ")
		buf.WriteString(formatFloat(g.values[k]))
		buf.WriteString("\n")
	}
}

// --- Histogram ---

type histogramVal struct {
	buckets []uint64
	count   uint64
	sum     float64
}

// Histogram tracks the size and distribution of events.
type Histogram struct {
	name      string
	help      string
	labelKeys []string
	buckets   []float64
	mu        sync.RWMutex
	values    map[string]*histogramVal
}

// NewHistogram registers a histogram with the registry.
func (r *Registry) NewHistogram(name, help string, labelKeys []string, buckets []float64) *Histogram {
	if len(buckets) == 0 {
		buckets = DefBuckets
	}
	sortedBuckets := make([]float64, len(buckets))
	copy(sortedBuckets, buckets)
	sort.Float64s(sortedBuckets)

	h := &Histogram{
		name:      name,
		help:      help,
		labelKeys: labelKeys,
		buckets:   sortedBuckets,
		values:    make(map[string]*histogramVal),
	}
	r.Register(h)
	return h
}

func (h *Histogram) Name() string { return h.name }
func (h *Histogram) Help() string { return h.help }
func (h *Histogram) Type() string { return "histogram" }

func (h *Histogram) Observe(val float64, labelValues ...string) {
	key := encodeLabels(h.labelKeys, labelValues)
	h.mu.Lock()
	defer h.mu.Unlock()

	hv, ok := h.values[key]
	if !ok {
		hv = &histogramVal{
			buckets: make([]uint64, len(h.buckets)),
		}
		h.values[key] = hv
	}

	hv.count++
	hv.sum += val
	for i, b := range h.buckets {
		if val <= b {
			hv.buckets[i]++
		}
	}
}

func (h *Histogram) WritePrometheus(buf *strings.Builder) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	keys := make([]string, 0, len(h.values))
	for k := range h.values {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		hv := h.values[k]

		// Cumulative bucket counts
		for i, b := range h.buckets {
			buf.WriteString(h.name)
			buf.WriteString("_bucket{")
			if k != "" {
				buf.WriteString(k)
				buf.WriteString(",")
			}
			buf.WriteString("le=\"")
			buf.WriteString(formatFloat(b))
			buf.WriteString("\"} ")
			buf.WriteString(fmt.Sprintf("%d\n", hv.buckets[i]))
		}

		// +Inf bucket
		buf.WriteString(h.name)
		buf.WriteString("_bucket{")
		if k != "" {
			buf.WriteString(k)
			buf.WriteString(",")
		}
		buf.WriteString("le=\"+Inf\"} ")
		buf.WriteString(fmt.Sprintf("%d\n", hv.count))

		// _sum and _count
		buf.WriteString(h.name)
		buf.WriteString("_sum")
		if k != "" {
			buf.WriteString("{")
			buf.WriteString(k)
			buf.WriteString("}")
		}
		buf.WriteString(" ")
		buf.WriteString(formatFloat(hv.sum))
		buf.WriteString("\n")

		buf.WriteString(h.name)
		buf.WriteString("_count")
		if k != "" {
			buf.WriteString("{")
			buf.WriteString(k)
			buf.WriteString("}")
		}
		buf.WriteString(" ")
		buf.WriteString(fmt.Sprintf("%d\n", hv.count))
	}
}

// --- Helpers ---

func encodeLabels(keys, values []string) string {
	if len(keys) == 0 || len(values) == 0 {
		return ""
	}
	var pairs []string
	for i := 0; i < len(keys) && i < len(values); i++ {
		pairs = append(pairs, fmt.Sprintf("%s=\"%s\"", keys[i], escapeLabelValue(values[i])))
	}
	return strings.Join(pairs, ",")
}

func escapeLabelValue(val string) string {
	val = strings.ReplaceAll(val, `\`, `\\`)
	val = strings.ReplaceAll(val, `"`, `\"`)
	val = strings.ReplaceAll(val, "\n", `\n`)
	return val
}

func formatFloat(v float64) string {
	if math.IsNaN(v) {
		return "NaN"
	}
	if math.IsInf(v, 1) {
		return "+Inf"
	}
	if math.IsInf(v, -1) {
		return "-Inf"
	}
	// If it's an integer value, display without decimal
	if v == float64(int64(v)) {
		return fmt.Sprintf("%d", int64(v))
	}
	return fmt.Sprintf("%.6g", v)
}

// --- Global Default Registry & Standard Metrics ---

// DefaultRegistry is the default metrics registry for the Bliss application.
var DefaultRegistry = NewRegistry()

var (
	// HTTP
	HTTPRequestsTotal    = DefaultRegistry.NewCounter("http_requests_total", "Total number of HTTP requests processed.", []string{"method", "route", "status"})
	HTTPRequestDuration  = DefaultRegistry.NewHistogram("http_request_duration_seconds", "HTTP request execution latency in seconds.", []string{"method", "route"}, DefBuckets)
	HTTPRequestsInFlight = DefaultRegistry.NewGauge("http_requests_in_flight", "Current number of in-flight HTTP requests.", nil)

	// Database
	DBPoolConnections    = DefaultRegistry.NewGauge("db_pool_connections", "Number of database connections by state.", []string{"state"})
	DBPoolMaxConnections = DefaultRegistry.NewGauge("db_pool_max_connections", "Configured maximum database connection pool size.", nil)
	DBPoolAcquireWait    = DefaultRegistry.NewCounter("db_pool_acquire_wait_duration_seconds_total", "Cumulative wait duration acquiring database connections.", nil)

	// Redis Cache & Limits
	RedisCacheOperationsTotal = DefaultRegistry.NewCounter("redis_cache_operations_total", "Total Redis cache operations.", []string{"cache", "operation"})
	RateLimitEventsTotal      = DefaultRegistry.NewCounter("rate_limit_events_total", "Total rate limiting events.", []string{"bucket", "status"})

	// Payments & Financial Reconciliation
	PaymentAttemptsTotal    = DefaultRegistry.NewCounter("payment_attempts_total", "Total payment attempts recorded.", []string{"provider", "status"})
	PaymentExceptionsTotal  = DefaultRegistry.NewCounter("payment_exceptions_total", "Total payment reconciliation exceptions detected.", []string{"type"})
	UnresolvedPaymentsGauge = DefaultRegistry.NewGauge("unresolved_payments_gauge", "Number of currently unresolved payment attempts.", nil)
	ReconciliationRunsTotal = DefaultRegistry.NewCounter("reconciliation_runs_total", "Total query reconciliation sweeps executed.", []string{"status"})

	// Outbox & Queue
	OutboxPendingEventsGauge      = DefaultRegistry.NewGauge("outbox_pending_events_gauge", "Number of pending events in transactional outbox.", nil)
	OutboxEventsPublishedTotal    = DefaultRegistry.NewCounter("outbox_events_published_total", "Total events published from transactional outbox.", []string{"destination"})
	RabbitMQMessagesConsumedTotal = DefaultRegistry.NewCounter("rabbitmq_messages_consumed_total", "Total RabbitMQ messages consumed by background workers.", []string{"queue", "status"})
	RabbitMQDLQMessagesGauge      = DefaultRegistry.NewGauge("rabbitmq_dlq_messages_gauge", "Dead letter queue messages detected.", []string{"queue"})

	// Stock Reservations
	StockReservationsActiveGauge  = DefaultRegistry.NewGauge("stock_reservations_active_gauge", "Currently active 15-minute stock reservations.", nil)
	StockReservationsExpiredTotal = DefaultRegistry.NewCounter("stock_reservations_expired_total", "Total stock reservations that expired before payment.", nil)
)
