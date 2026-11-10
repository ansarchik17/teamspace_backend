package metrics

import (
	"fmt"
	"net/http"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/telemetry"
)

var defaultDurationBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

type WebSocketStatsProvider interface {
	GetConnectedUsers() []int64
	GetClientCount() int
}

type Collector struct {
	namespace string
	appName   string
	version   string
	startedAt time.Time

	wsHub         WebSocketStatsProvider
	callTelemetry *telemetry.CallTelemetry

	httpInflight int64

	mu            sync.RWMutex
	httpCounters  map[string]*counterMetric
	httpDurations map[string]*histogramMetric
}

type counterMetric struct {
	name   string
	labels map[string]string
	value  uint64
}

type histogramMetric struct {
	name        string
	labels      map[string]string
	buckets     []float64
	bucketCount []uint64
	count       uint64
	sum         float64
}

func NewCollector(appName, version string, wsHub WebSocketStatsProvider, callTelemetry *telemetry.CallTelemetry) *Collector {
	return &Collector{
		namespace:     "messenger",
		appName:       strings.TrimSpace(appName),
		version:       strings.TrimSpace(version),
		startedAt:     time.Now().UTC(),
		wsHub:         wsHub,
		callTelemetry: callTelemetry,
		httpCounters:  make(map[string]*counterMetric),
		httpDurations: make(map[string]*histogramMetric),
	}
}

func (c *Collector) Middleware() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		atomic.AddInt64(&c.httpInflight, 1)
		startedAt := time.Now()
		ctx.Next()
		atomic.AddInt64(&c.httpInflight, -1)

		path := ctx.FullPath()
		if path == "" {
			path = unmatchedPath(ctx.Request.URL.Path)
		}

		labels := map[string]string{
			"method": ctx.Request.Method,
			"path":   path,
			"status": strconv.Itoa(ctx.Writer.Status()),
		}
		c.observeHTTPRequest(labels, time.Since(startedAt).Seconds())
	}
}

func (c *Collector) Handler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		ctx.Header("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		ctx.String(http.StatusOK, c.Render())
	}
}

func (c *Collector) Render() string {
	var builder strings.Builder

	builder.WriteString(c.renderStaticMetrics())
	builder.WriteString(c.renderHTTPMetrics())
	builder.WriteString(c.renderRuntimeMetrics())
	builder.WriteString(c.renderWebSocketMetrics())
	builder.WriteString(c.renderCallTelemetryMetrics())
	builder.WriteString(DefaultAIAccounting().render())

	return builder.String()
}

func (c *Collector) observeHTTPRequest(labels map[string]string, durationSeconds float64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	counterKey := metricKey(c.namespace+"_http_requests_total", labels)
	counter, exists := c.httpCounters[counterKey]
	if !exists {
		counter = &counterMetric{
			name:   c.namespace + "_http_requests_total",
			labels: cloneLabels(labels),
		}
		c.httpCounters[counterKey] = counter
	}
	counter.value++

	durationLabels := map[string]string{
		"method": labels["method"],
		"path":   labels["path"],
	}
	histogramKey := metricKey(c.namespace+"_http_request_duration_seconds", durationLabels)
	histogram, exists := c.httpDurations[histogramKey]
	if !exists {
		histogram = &histogramMetric{
			name:        c.namespace + "_http_request_duration_seconds",
			labels:      cloneLabels(durationLabels),
			buckets:     append([]float64(nil), defaultDurationBuckets...),
			bucketCount: make([]uint64, len(defaultDurationBuckets)+1),
		}
		c.httpDurations[histogramKey] = histogram
	}
	histogram.observe(durationSeconds)
}

func (c *Collector) renderStaticMetrics() string {
	var builder strings.Builder

	builder.WriteString(helpLine(c.namespace+"_build_info", "Application build information"))
	builder.WriteString(typeLine(c.namespace+"_build_info", "gauge"))
	builder.WriteString(metricLine(c.namespace+"_build_info", map[string]string{
		"app":     emptyFallback(c.appName, "messenger"),
		"version": emptyFallback(c.version, "unknown"),
	}, 1))

	builder.WriteString(helpLine(c.namespace+"_uptime_seconds", "Process uptime in seconds"))
	builder.WriteString(typeLine(c.namespace+"_uptime_seconds", "gauge"))
	builder.WriteString(metricLine(c.namespace+"_uptime_seconds", nil, time.Since(c.startedAt).Seconds()))

	builder.WriteString(helpLine(c.namespace+"_started_at_seconds", "Process start time since unix epoch in seconds"))
	builder.WriteString(typeLine(c.namespace+"_started_at_seconds", "gauge"))
	builder.WriteString(metricLine(c.namespace+"_started_at_seconds", nil, float64(c.startedAt.Unix())))

	return builder.String()
}

func (c *Collector) renderHTTPMetrics() string {
	var builder strings.Builder

	c.mu.RLock()
	counters := make([]*counterMetric, 0, len(c.httpCounters))
	for _, counter := range c.httpCounters {
		counters = append(counters, &counterMetric{
			name:   counter.name,
			labels: cloneLabels(counter.labels),
			value:  counter.value,
		})
	}

	histograms := make([]*histogramMetric, 0, len(c.httpDurations))
	for _, histogram := range c.httpDurations {
		cloned := &histogramMetric{
			name:        histogram.name,
			labels:      cloneLabels(histogram.labels),
			buckets:     append([]float64(nil), histogram.buckets...),
			bucketCount: append([]uint64(nil), histogram.bucketCount...),
			count:       histogram.count,
			sum:         histogram.sum,
		}
		histograms = append(histograms, cloned)
	}
	c.mu.RUnlock()

	sort.Slice(counters, func(i, j int) bool {
		return metricKey(counters[i].name, counters[i].labels) < metricKey(counters[j].name, counters[j].labels)
	})
	sort.Slice(histograms, func(i, j int) bool {
		return metricKey(histograms[i].name, histograms[i].labels) < metricKey(histograms[j].name, histograms[j].labels)
	})

	builder.WriteString(helpLine(c.namespace+"_http_inflight_requests", "Current in-flight HTTP requests"))
	builder.WriteString(typeLine(c.namespace+"_http_inflight_requests", "gauge"))
	builder.WriteString(metricLine(c.namespace+"_http_inflight_requests", nil, float64(atomic.LoadInt64(&c.httpInflight))))

	builder.WriteString(helpLine(c.namespace+"_http_requests_total", "Total number of HTTP requests"))
	builder.WriteString(typeLine(c.namespace+"_http_requests_total", "counter"))
	for _, counter := range counters {
		builder.WriteString(metricLine(counter.name, counter.labels, float64(counter.value)))
	}

	builder.WriteString(helpLine(c.namespace+"_http_request_duration_seconds", "HTTP request duration in seconds"))
	builder.WriteString(typeLine(c.namespace+"_http_request_duration_seconds", "histogram"))
	for _, histogram := range histograms {
		builder.WriteString(histogram.render())
	}

	return builder.String()
}

func (c *Collector) renderRuntimeMetrics() string {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)

	var builder strings.Builder
	runtimeMetrics := []struct {
		name string
		help string
		val  float64
	}{
		{name: c.namespace + "_go_goroutines", help: "Number of current goroutines", val: float64(runtime.NumGoroutine())},
		{name: c.namespace + "_go_memory_alloc_bytes", help: "Bytes of allocated heap objects", val: float64(stats.Alloc)},
		{name: c.namespace + "_go_heap_inuse_bytes", help: "Bytes in in-use heap spans", val: float64(stats.HeapInuse)},
		{name: c.namespace + "_go_heap_idle_bytes", help: "Bytes in idle heap spans", val: float64(stats.HeapIdle)},
		{name: c.namespace + "_go_gc_cycles_total", help: "Total completed GC cycles", val: float64(stats.NumGC)},
		{name: c.namespace + "_go_gc_pause_total_seconds", help: "Total GC pause time in seconds", val: float64(stats.PauseTotalNs) / float64(time.Second)},
	}

	for _, metric := range runtimeMetrics {
		builder.WriteString(helpLine(metric.name, metric.help))
		builder.WriteString(typeLine(metric.name, "gauge"))
		builder.WriteString(metricLine(metric.name, nil, metric.val))
	}

	return builder.String()
}

func (c *Collector) renderWebSocketMetrics() string {
	var builder strings.Builder

	connectedUsers := 0
	connectedClients := 0
	if c.wsHub != nil {
		connectedUsers = len(c.wsHub.GetConnectedUsers())
		connectedClients = c.wsHub.GetClientCount()
	}

	builder.WriteString(helpLine(c.namespace+"_websocket_connected_users", "Number of users with at least one active websocket connection"))
	builder.WriteString(typeLine(c.namespace+"_websocket_connected_users", "gauge"))
	builder.WriteString(metricLine(c.namespace+"_websocket_connected_users", nil, float64(connectedUsers)))

	builder.WriteString(helpLine(c.namespace+"_websocket_connected_clients", "Number of active websocket client sessions"))
	builder.WriteString(typeLine(c.namespace+"_websocket_connected_clients", "gauge"))
	builder.WriteString(metricLine(c.namespace+"_websocket_connected_clients", nil, float64(connectedClients)))

	return builder.String()
}

func (c *Collector) renderCallTelemetryMetrics() string {
	var builder strings.Builder

	enabledValue := 0.0
	if c.callTelemetry != nil && c.callTelemetry.Enabled() {
		enabledValue = 1
	}

	builder.WriteString(helpLine(c.namespace+"_call_telemetry_enabled", "Whether call telemetry collection is enabled"))
	builder.WriteString(typeLine(c.namespace+"_call_telemetry_enabled", "gauge"))
	builder.WriteString(metricLine(c.namespace+"_call_telemetry_enabled", nil, enabledValue))

	if c.callTelemetry == nil || !c.callTelemetry.Enabled() {
		return builder.String()
	}

	snapshot := c.callTelemetry.Snapshot(telemetry.SnapshotFilter{Limit: 500})
	counterNames := make(map[string]bool)
	for key, value := range snapshot.Counters {
		name, labels := parseTelemetryKey(key)
		if name == "" {
			continue
		}
		fullName := c.namespace + "_" + normalizeMetricName(name)
		if !counterNames[fullName] {
			builder.WriteString(helpLine(fullName, "Counter exported from call telemetry"))
			builder.WriteString(typeLine(fullName, "counter"))
			counterNames[fullName] = true
		}
		builder.WriteString(metricLine(fullName, labels, float64(value)))
	}

	durationNames := map[string]string{
		c.namespace + "_call_telemetry_duration_count":       "counter",
		c.namespace + "_call_telemetry_duration_error_count": "counter",
		c.namespace + "_call_telemetry_duration_total_ms":    "counter",
		c.namespace + "_call_telemetry_duration_max_ms":      "gauge",
		c.namespace + "_call_telemetry_duration_avg_ms":      "gauge",
	}
	for name, metricType := range durationNames {
		builder.WriteString(helpLine(name, "Aggregated duration metric exported from call telemetry"))
		builder.WriteString(typeLine(name, metricType))
	}
	for _, duration := range snapshot.Durations {
		labels := map[string]string{
			"metric": duration.Name,
		}
		if duration.Operation != "" {
			labels["operation"] = duration.Operation
		}
		builder.WriteString(metricLine(c.namespace+"_call_telemetry_duration_count", labels, float64(duration.Count)))
		builder.WriteString(metricLine(c.namespace+"_call_telemetry_duration_error_count", labels, float64(duration.ErrorCount)))
		builder.WriteString(metricLine(c.namespace+"_call_telemetry_duration_total_ms", labels, float64(duration.TotalMS)))
		builder.WriteString(metricLine(c.namespace+"_call_telemetry_duration_max_ms", labels, float64(duration.MaxMS)))
		builder.WriteString(metricLine(c.namespace+"_call_telemetry_duration_avg_ms", labels, duration.AvgMS))
	}

	builder.WriteString(helpLine(c.namespace+"_call_telemetry_recent_events", "Number of recent telemetry events stored in memory"))
	builder.WriteString(typeLine(c.namespace+"_call_telemetry_recent_events", "gauge"))
	builder.WriteString(metricLine(c.namespace+"_call_telemetry_recent_events", nil, float64(len(snapshot.RecentEvents))))

	return builder.String()
}

func (h *histogramMetric) observe(value float64) {
	for idx, bucket := range h.buckets {
		if value <= bucket {
			h.bucketCount[idx]++
		}
	}
	h.bucketCount[len(h.bucketCount)-1]++
	h.count++
	h.sum += value
}

func (h *histogramMetric) render() string {
	var builder strings.Builder

	for idx, bucket := range h.buckets {
		labels := cloneLabels(h.labels)
		labels["le"] = formatFloat(bucket)
		builder.WriteString(metricLine(h.name+"_bucket", labels, float64(h.bucketCount[idx])))
	}

	infLabels := cloneLabels(h.labels)
	infLabels["le"] = "+Inf"
	builder.WriteString(metricLine(h.name+"_bucket", infLabels, float64(h.bucketCount[len(h.bucketCount)-1])))
	builder.WriteString(metricLine(h.name+"_sum", h.labels, h.sum))
	builder.WriteString(metricLine(h.name+"_count", h.labels, float64(h.count)))

	return builder.String()
}

func parseTelemetryKey(key string) (string, map[string]string) {
	parts := strings.Split(key, "|")
	if len(parts) == 0 {
		return "", nil
	}

	name := strings.TrimSpace(parts[0])
	if name == "" {
		return "", nil
	}

	labels := make(map[string]string)
	for _, part := range parts[1:] {
		pair := strings.SplitN(part, "=", 2)
		if len(pair) != 2 {
			continue
		}
		labelName := strings.TrimSpace(pair[0])
		labelValue := strings.TrimSpace(pair[1])
		if labelName == "" || labelValue == "" {
			continue
		}
		labels[labelName] = labelValue
	}
	return name, labels
}

func metricKey(name string, labels map[string]string) string {
	if len(labels) == 0 {
		return name
	}

	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys)+1)
	parts = append(parts, name)
	for _, key := range keys {
		parts = append(parts, key+"="+labels[key])
	}
	return strings.Join(parts, "|")
}

func cloneLabels(labels map[string]string) map[string]string {
	if len(labels) == 0 {
		return nil
	}

	cloned := make(map[string]string, len(labels))
	for key, value := range labels {
		cloned[key] = value
	}
	return cloned
}

func metricLine(name string, labels map[string]string, value float64) string {
	if len(labels) == 0 {
		return fmt.Sprintf("%s %s\n", name, formatFloat(value))
	}

	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	labelParts := make([]string, 0, len(keys))
	for _, key := range keys {
		labelParts = append(labelParts, fmt.Sprintf(`%s="%s"`, normalizeLabelName(key), escapeLabelValue(labels[key])))
	}

	return fmt.Sprintf("%s{%s} %s\n", name, strings.Join(labelParts, ","), formatFloat(value))
}

func helpLine(name, help string) string {
	return fmt.Sprintf("# HELP %s %s\n", name, escapeHelp(help))
}

func typeLine(name, metricType string) string {
	return fmt.Sprintf("# TYPE %s %s\n", name, metricType)
}

func normalizeMetricName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "unknown"
	}

	var builder strings.Builder
	for _, char := range name {
		switch {
		case char >= 'a' && char <= 'z':
			builder.WriteRune(char)
		case char >= 'A' && char <= 'Z':
			builder.WriteRune(char + ('a' - 'A'))
		case char >= '0' && char <= '9':
			builder.WriteRune(char)
		default:
			builder.WriteByte('_')
		}
	}
	return strings.Trim(builder.String(), "_")
}

func normalizeLabelName(name string) string {
	return normalizeMetricName(name)
}

func formatFloat(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func escapeLabelValue(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, "\n", `\n`, `"`, `\"`)
	return replacer.Replace(value)
}

func escapeHelp(help string) string {
	replacer := strings.NewReplacer("\n", " ", `\`, `\\`)
	return replacer.Replace(help)
}

func unmatchedPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "_unmatched"
	}
	if path == "/" {
		return "/"
	}
	return "_unmatched"
}

func emptyFallback(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}
