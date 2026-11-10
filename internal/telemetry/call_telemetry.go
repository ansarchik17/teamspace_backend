package telemetry

import (
	"sort"
	"strings"
	"sync"
	"time"
)

type TraceContext struct {
	CallID    string `json:"call_id,omitempty"`
	RoomName  string `json:"room_name,omitempty"`
	RequestID string `json:"request_id,omitempty"`
	UserID    int64  `json:"user_id,omitempty"`
	Identity  string `json:"identity,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	DeviceID  string `json:"device_id,omitempty"`
}

type Event struct {
	Timestamp time.Time      `json:"timestamp"`
	Kind      string         `json:"kind"`
	Status    string         `json:"status,omitempty"`
	Trace     TraceContext   `json:"trace"`
	Details   map[string]any `json:"details,omitempty"`
}

type DurationMetric struct {
	Name       string  `json:"name"`
	Operation  string  `json:"operation"`
	Count      uint64  `json:"count"`
	ErrorCount uint64  `json:"error_count"`
	TotalMS    int64   `json:"total_ms"`
	MaxMS      int64   `json:"max_ms"`
	AvgMS      float64 `json:"avg_ms"`
}

type SnapshotFilter struct {
	CallID   string
	RoomName string
	Kind     string
	Limit    int
}

type Snapshot struct {
	Enabled      bool              `json:"enabled"`
	GeneratedAt  time.Time         `json:"generated_at"`
	MaxEvents    int               `json:"max_events"`
	Counters     map[string]uint64 `json:"counters"`
	Durations    []DurationMetric  `json:"durations"`
	RecentEvents []Event           `json:"recent_events"`
	Filters      map[string]string `json:"filters,omitempty"`
}

type durationAggregate struct {
	Name       string
	Operation  string
	Count      uint64
	ErrorCount uint64
	TotalMS    int64
	MaxMS      int64
}

type CallTelemetry struct {
	enabled   bool
	maxEvents int

	mu        sync.RWMutex
	counters  map[string]uint64
	durations map[string]*durationAggregate
	events    []Event
}

func NewCallTelemetry(enabled bool) *CallTelemetry {
	return &CallTelemetry{
		enabled:   enabled,
		maxEvents: 500,
		counters:  make(map[string]uint64),
		durations: make(map[string]*durationAggregate),
	}
}

func (t *CallTelemetry) Enabled() bool {
	return t != nil && t.enabled
}

func (t *CallTelemetry) SetMaxEvents(limit int) {
	if t == nil || limit <= 0 {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	t.maxEvents = limit
	if len(t.events) > t.maxEvents {
		t.events = append([]Event(nil), t.events[len(t.events)-t.maxEvents:]...)
	}
}

func (t *CallTelemetry) IncrementCounter(name string, labels map[string]string) {
	if t == nil || !t.enabled || strings.TrimSpace(name) == "" {
		return
	}

	key := formatKey(name, labels)

	t.mu.Lock()
	defer t.mu.Unlock()
	t.counters[key]++
}

func (t *CallTelemetry) ObserveDuration(name, operation string, duration time.Duration, err error) {
	if t == nil || !t.enabled || strings.TrimSpace(name) == "" {
		return
	}

	key := formatKey(name, map[string]string{
		"operation": strings.TrimSpace(operation),
	})
	durationMS := duration.Milliseconds()

	t.mu.Lock()
	defer t.mu.Unlock()

	agg, exists := t.durations[key]
	if !exists {
		agg = &durationAggregate{
			Name:      name,
			Operation: strings.TrimSpace(operation),
		}
		t.durations[key] = agg
	}

	agg.Count++
	agg.TotalMS += durationMS
	if durationMS > agg.MaxMS {
		agg.MaxMS = durationMS
	}
	if err != nil {
		agg.ErrorCount++
	}
}

func (t *CallTelemetry) RecordEvent(kind string, trace TraceContext, status string, details map[string]any) {
	if t == nil || !t.enabled || strings.TrimSpace(kind) == "" {
		return
	}

	event := Event{
		Timestamp: time.Now().UTC(),
		Kind:      strings.TrimSpace(kind),
		Status:    strings.TrimSpace(status),
		Trace:     trace,
		Details:   cloneDetails(details),
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	t.events = append(t.events, event)
	if len(t.events) > t.maxEvents {
		t.events = append([]Event(nil), t.events[len(t.events)-t.maxEvents:]...)
	}
}

func (t *CallTelemetry) Snapshot(filter SnapshotFilter) Snapshot {
	snapshot := Snapshot{
		Enabled:     t != nil && t.enabled,
		GeneratedAt: time.Now().UTC(),
	}
	if t == nil {
		return snapshot
	}

	t.mu.RLock()
	defer t.mu.RUnlock()

	snapshot.MaxEvents = t.maxEvents
	snapshot.Counters = make(map[string]uint64, len(t.counters))
	for key, value := range t.counters {
		snapshot.Counters[key] = value
	}

	snapshot.Durations = make([]DurationMetric, 0, len(t.durations))
	for _, agg := range t.durations {
		avgMS := 0.0
		if agg.Count > 0 {
			avgMS = float64(agg.TotalMS) / float64(agg.Count)
		}
		snapshot.Durations = append(snapshot.Durations, DurationMetric{
			Name:       agg.Name,
			Operation:  agg.Operation,
			Count:      agg.Count,
			ErrorCount: agg.ErrorCount,
			TotalMS:    agg.TotalMS,
			MaxMS:      agg.MaxMS,
			AvgMS:      avgMS,
		})
	}
	sort.Slice(snapshot.Durations, func(i, j int) bool {
		if snapshot.Durations[i].Name == snapshot.Durations[j].Name {
			return snapshot.Durations[i].Operation < snapshot.Durations[j].Operation
		}
		return snapshot.Durations[i].Name < snapshot.Durations[j].Name
	})

	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}

	callID := strings.TrimSpace(filter.CallID)
	roomName := strings.TrimSpace(filter.RoomName)
	kind := strings.TrimSpace(filter.Kind)

	if callID != "" || roomName != "" || kind != "" {
		snapshot.Filters = make(map[string]string)
		if callID != "" {
			snapshot.Filters["call_id"] = callID
		}
		if roomName != "" {
			snapshot.Filters["room_name"] = roomName
		}
		if kind != "" {
			snapshot.Filters["kind"] = kind
		}
	}

	snapshot.RecentEvents = make([]Event, 0, min(limit, len(t.events)))
	for i := len(t.events) - 1; i >= 0 && len(snapshot.RecentEvents) < limit; i-- {
		event := t.events[i]
		if callID != "" && event.Trace.CallID != callID {
			continue
		}
		if roomName != "" && event.Trace.RoomName != roomName {
			continue
		}
		if kind != "" && event.Kind != kind {
			continue
		}
		snapshot.RecentEvents = append(snapshot.RecentEvents, event)
	}

	return snapshot
}

func formatKey(name string, labels map[string]string) string {
	if len(labels) == 0 {
		return strings.TrimSpace(name)
	}

	keys := make([]string, 0, len(labels))
	for key, value := range labels {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)

	if len(keys) == 0 {
		return strings.TrimSpace(name)
	}

	parts := make([]string, 0, len(keys)+1)
	parts = append(parts, strings.TrimSpace(name))
	for _, key := range keys {
		parts = append(parts, key+"="+strings.TrimSpace(labels[key]))
	}
	return strings.Join(parts, "|")
}

func cloneDetails(details map[string]any) map[string]any {
	if len(details) == 0 {
		return nil
	}

	cloned := make(map[string]any, len(details))
	for key, value := range details {
		cloned[key] = value
	}
	return cloned
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
