package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/config"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestMinIOCapacityMetrics(t *testing.T) {
	for _, item := range []struct {
		name, body string
		status     int
		ok         bool
	}{
		{"valid", "minio_cluster_capacity_usable_total_bytes{server=\"node\"} 1000\nminio_cluster_capacity_usable_free_bytes 800\n", 200, true},
		{"missing", "minio_cluster_capacity_raw_total_bytes 1000\n", 200, false},
		{"invalid", "minio_cluster_capacity_usable_total_bytes 1000\nminio_cluster_capacity_usable_free_bytes NaN\n", 200, false},
		{"inconsistent", "minio_cluster_capacity_usable_total_bytes 1000\nminio_cluster_capacity_usable_free_bytes 1001\n", 200, false},
		{"denied", "private diagnostics", 403, false},
		{"redirect", "", 302, false},
	} {
		t.Run(item.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/minio/v2/metrics/cluster", r.URL.Path)
				require.Equal(t, "Bearer test-secret", r.Header.Get("Authorization"))
				w.WriteHeader(item.status)
				_, _ = w.Write([]byte(item.body))
			}))
			defer server.Close()
			provider, err := NewMinIOCapacity(config.MinIOConfig{Endpoint: server.URL, MetricsToken: "test-secret", CapacityReservePercent: 10})
			require.NoError(t, err)
			result, err := provider.Capacity(context.Background())
			if item.ok {
				require.NoError(t, err)
				require.Equal(t, int64(1000), result.TotalBytes)
				require.Equal(t, int64(800), result.FreeBytes)
				require.Equal(t, int64(100), result.ReserveBytes)
			} else {
				require.Error(t, err)
				require.NotContains(t, err.Error(), "private")
			}
		})
	}
}
func TestMinIOCapacityLive(t *testing.T) {
	endpoint := os.Getenv("MINIO_CAPACITY_TEST_ENDPOINT")
	if endpoint == "" {
		t.Skip("optional live MinIO metrics verification")
	}
	provider, err := NewMinIOCapacity(config.MinIOConfig{Endpoint: endpoint, MetricsToken: os.Getenv("MINIO_CAPACITY_TEST_TOKEN"), CapacityReservePercent: 10})
	require.NoError(t, err)
	result, err := provider.Capacity(context.Background())
	require.NoError(t, err)
	require.Positive(t, result.TotalBytes)
	require.LessOrEqual(t, result.FreeBytes, result.TotalBytes)
	t.Logf("MinIO total=%d free=%d reserve=%d", result.TotalBytes, result.FreeBytes, result.ReserveBytes)
}
