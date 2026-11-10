package metrics

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAIAccountingMetricsBoundedCountersAndGlobalGauges(t *testing.T) {
	m := NewAIAccountingMetrics()
	m.Inc("operations", "outcome", "success")
	m.Inc("operations", "outcome", "unknown")
	m.Inc("operations", "outcome", "success")
	m.Inc("quota_denials", "scope", "weekly")
	// Arbitrary labels are rejected; IDs and provider error text cannot create series.
	m.Inc("operations", "outcome", "user-123")
	m.Inc("operations", "provider_request_id", "secret-request")
	require.EqualValues(t, 2, m.CounterValue("operations", "outcome", "success"))
	require.EqualValues(t, 1, m.CounterValue("operations", "outcome", "unknown"))
	require.Zero(t, m.CounterValue("operations", "outcome", "user-123"))

	m.SetDatabaseSnapshot(7, 3, 2, 21*time.Minute, true)
	rendered := m.render()
	require.Contains(t, rendered, "messenger_ai_accounting_unresolved_reservations 7")
	require.Contains(t, rendered, "messenger_ai_accounting_unknown_usage_reservations 3")
	require.Contains(t, rendered, "messenger_ai_accounting_stale_reservations 2")
	require.Contains(t, rendered, "messenger_ai_accounting_oldest_reservation_age_seconds 1260")
	require.NotContains(t, rendered, "user-123")
	require.NotContains(t, rendered, "secret-request")

	// A failed sample changes health only and never clears the last known DB snapshot.
	m.SetDatabaseSnapshot(0, 0, 0, 0, false)
	require.EqualValues(t, 2, m.CounterValue("operations", "outcome", "success"))
	require.Contains(t, m.render(), "messenger_ai_accounting_db_sample_success 0")
	require.True(t, strings.Contains(m.render(), "messenger_ai_accounting_unresolved_reservations 7"))
}

func TestAIAccountingGlobalGaugeSnapshotsAreNotSummedAcrossInstances(t *testing.T) {
	first, second := NewAIAccountingMetrics(), NewAIAccountingMetrics()
	first.SetDatabaseSnapshot(9, 4, 2, 20*time.Minute, true)
	second.SetDatabaseSnapshot(9, 4, 2, 20*time.Minute, true)
	require.Contains(t, first.render(), "messenger_ai_accounting_unresolved_reservations 9")
	require.Contains(t, second.render(), "messenger_ai_accounting_unresolved_reservations 9")
	// The same global DB view is exposed by both scrapes; max remains 9 while sum would be wrong (18).
	require.NotContains(t, first.render(), "messenger_ai_accounting_unresolved_reservations 18")
}
