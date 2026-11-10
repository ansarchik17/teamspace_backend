package metrics

import (
	"strings"
	"testing"

	"github.com/west6ide/workspace-backend/internal/telemetry"
)

type wsHubStub struct {
	users   []int64
	clients int
}

func (w wsHubStub) GetConnectedUsers() []int64 {
	return w.users
}

func (w wsHubStub) GetClientCount() int {
	return w.clients
}

func TestCollectorRenderIncludesCoreMetrics(t *testing.T) {
	t.Parallel()

	callTelemetry := telemetry.NewCallTelemetry(true)
	callTelemetry.IncrementCounter("livekit_api_calls_total", map[string]string{
		"operation": "list_participants",
		"status":    "ok",
	})

	collector := NewCollector("messenger", "1.0.0", wsHubStub{
		users:   []int64{1, 2},
		clients: 3,
	}, callTelemetry)

	output := collector.Render()

	requiredFragments := []string{
		"messenger_build_info",
		"messenger_websocket_connected_users 2",
		"messenger_websocket_connected_clients 3",
		`messenger_livekit_api_calls_total{operation="list_participants",status="ok"}`,
	}

	for _, fragment := range requiredFragments {
		if !strings.Contains(output, fragment) {
			t.Fatalf("metrics output does not contain %q\n%s", fragment, output)
		}
	}
}
