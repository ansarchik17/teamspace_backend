package metrics

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// AIAccountingMetrics contains bounded process counters and database-wide gauges.
// Counters represent committed lifecycle events; gauges are sampled snapshots.
type AIAccountingMetrics struct {
	mu               sync.RWMutex
	counters         map[string]uint64
	unresolved       int64
	unknown          int64
	stale            int64
	oldestAgeSeconds float64
	sampleOK         bool
}

func NewAIAccountingMetrics() *AIAccountingMetrics {
	return &AIAccountingMetrics{counters: make(map[string]uint64)}
}

var aiAccounting = NewAIAccountingMetrics()

func DefaultAIAccounting() *AIAccountingMetrics { return aiAccounting }

func (m *AIAccountingMetrics) Inc(metric, label, value string) {
	if m == nil || !validAIMetricMetric(metric, label) || !validAIMetricLabel(label, value) {
		return
	}
	m.mu.Lock()
	m.counters[metric+"\x00"+label+"\x00"+value]++
	m.mu.Unlock()
}

func validAIMetricMetric(metric, label string) bool {
	switch metric {
	case "operations", "reconciliation":
		return label == "outcome"
	case "quota_denials":
		return label == "scope"
	case "reservation_errors", "settlement_errors":
		return label == "kind"
	}
	return false
}

func validAIMetricLabel(name, value string) bool {
	switch name {
	case "outcome":
		switch value {
		case "success", "confirmed_not_executed", "unknown", "reconciled_usage", "reconciled_not_executed", "reconciliation_deferred", "reconciliation_manual", "reconciliation_error":
			return true
		}
	case "scope":
		switch value {
		case "company", "weekly", "five_hour", "service", "account":
			return true
		}
	case "kind":
		switch value {
		case "database", "invalid", "conflict", "calculation", "unknown":
			return true
		}
	}
	return false
}

func (m *AIAccountingMetrics) SetDatabaseSnapshot(unresolved, unknown, stale int64, oldestAge time.Duration, ok bool) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sampleOK = ok
	if ok {
		m.unresolved, m.unknown, m.stale, m.oldestAgeSeconds = unresolved, unknown, stale, oldestAge.Seconds()
	}
}

func (m *AIAccountingMetrics) CounterValue(metric, label, value string) uint64 {
	if m == nil || !validAIMetricMetric(metric, label) || !validAIMetricLabel(label, value) {
		return 0
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.counters[metric+"\x00"+label+"\x00"+value]
}

// StartAIDatabaseSampler refreshes DB-wide gauges even when no requests arrive.
// Every API instance may sample the same totals; dashboards must use max, never sum.
func (m *AIAccountingMetrics) StartAIDatabaseSampler(ctx context.Context, db *pgxpool.Pool, interval, staleAfter time.Duration) {
	if m == nil || db == nil {
		return
	}
	if interval <= 0 {
		interval = time.Minute
	}
	if staleAfter <= 0 {
		staleAfter = 15 * time.Minute
	}
	refresh := func() {
		qctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		var unresolved, unknown, stale int64
		var oldestSeconds float64
		err := db.QueryRow(qctx, `SELECT count(*) FILTER (WHERE r.status='active'), count(*) FILTER (WHERE r.status='active' AND a.status='unknown'), count(*) FILTER (WHERE r.status='active' AND r.created_at <= clock_timestamp()-$1 * interval '1 second'), COALESCE(EXTRACT(EPOCH FROM clock_timestamp()-min(r.created_at) FILTER (WHERE r.status='active')),0) FROM ai_usage_reservations r JOIN ai_usage_attempts a ON a.id=r.attempt_id`, int64(staleAfter.Seconds())).Scan(&unresolved, &unknown, &stale, &oldestSeconds)
		if err != nil {
			m.SetDatabaseSnapshot(0, 0, 0, 0, false)
			return
		}
		m.SetDatabaseSnapshot(unresolved, unknown, stale, time.Duration(oldestSeconds*float64(time.Second)), true)
	}
	refresh()
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				refresh()
			}
		}
	}()
}

func (m *AIAccountingMetrics) render() string {
	if m == nil {
		return ""
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	var b strings.Builder
	for _, spec := range []struct{ metric, help, label string }{
		{"operations", "Committed AI accounting lifecycle outcomes", "outcome"},
		{"quota_denials", "AI requests denied by a configured quota", "scope"},
		{"reservation_errors", "AI reservation failures", "kind"},
		{"settlement_errors", "AI settlement failures", "kind"},
		{"reconciliation", "Committed AI reconciliation outcomes", "outcome"},
	} {
		name := "messenger_ai_accounting_" + spec.metric + "_total"
		b.WriteString(helpLine(name, spec.help))
		b.WriteString(typeLine(name, "counter"))
		for key, count := range m.counters {
			parts := strings.SplitN(key, "\x00", 3)
			if len(parts) == 3 && parts[0] == spec.metric && parts[1] == spec.label {
				b.WriteString(metricLine(name, map[string]string{spec.label: parts[2]}, float64(count)))
			}
		}
	}
	for _, metric := range []struct {
		name, help string
		value      float64
	}{
		{"messenger_ai_accounting_unresolved_reservations", "Current global count of active AI reservations", float64(m.unresolved)},
		{"messenger_ai_accounting_unknown_usage_reservations", "Current global count of active reservations whose provider usage is unknown", float64(m.unknown)},
		{"messenger_ai_accounting_stale_reservations", "Current global count of active AI reservations older than the configured threshold", float64(m.stale)},
		{"messenger_ai_accounting_oldest_reservation_age_seconds", "Age of the oldest active AI reservation in seconds", m.oldestAgeSeconds},
	} {
		b.WriteString(helpLine(metric.name, metric.help))
		b.WriteString(typeLine(metric.name, "gauge"))
		b.WriteString(metricLine(metric.name, nil, metric.value))
	}
	name := "messenger_ai_accounting_db_sample_success"
	b.WriteString(helpLine(name, "Whether the most recent global AI accounting gauge sample succeeded"))
	b.WriteString(typeLine(name, "gauge"))
	value := 0.0
	if m.sampleOK {
		value = 1
	}
	b.WriteString(metricLine(name, nil, value))
	return b.String()
}
