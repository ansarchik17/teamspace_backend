package telemetry

import (
	"testing"
	"time"
)

func TestCallTelemetrySnapshotFiltersByCallID(t *testing.T) {
	t.Parallel()

	collector := NewCallTelemetry(true)
	collector.SetMaxEvents(10)
	collector.IncrementCounter("livekit_api_calls_total", map[string]string{
		"operation": "list_participants",
		"status":    "ok",
	})
	collector.ObserveDuration("livekit_api_latency_ms", "list_participants", 150*time.Millisecond, nil)
	collector.RecordEvent("call_rejoin", TraceContext{CallID: "call-1", RoomName: "room-1"}, "ok", map[string]any{
		"participants_count": 2,
	})
	collector.RecordEvent("call_rejoin", TraceContext{CallID: "call-2", RoomName: "room-2"}, "ok", nil)

	snapshot := collector.Snapshot(SnapshotFilter{
		CallID: "call-1",
		Limit:  5,
	})

	if !snapshot.Enabled {
		t.Fatalf("expected enabled snapshot")
	}
	if len(snapshot.Counters) == 0 {
		t.Fatalf("expected counters in snapshot")
	}
	if len(snapshot.Durations) != 1 {
		t.Fatalf("expected 1 duration metric, got %d", len(snapshot.Durations))
	}
	if len(snapshot.RecentEvents) != 1 {
		t.Fatalf("expected 1 filtered event, got %d", len(snapshot.RecentEvents))
	}
	if snapshot.RecentEvents[0].Trace.CallID != "call-1" {
		t.Fatalf("unexpected call id in snapshot: %s", snapshot.RecentEvents[0].Trace.CallID)
	}
}
