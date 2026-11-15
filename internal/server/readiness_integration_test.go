package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestReadinessPostgresOutageAndRecoveryIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_READINESS_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_READINESS_DATABASE_URL to a disposable local unitespace_readiness_test database")
	}
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Path != "/unitespace_readiness_test" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost") || (u.RawQuery != "" && u.RawQuery != "sslmode=disable") {
		t.Fatal("requires the dedicated local unitespace_readiness_test database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	controlConfig := cfg.ConnConfig.Copy()
	controlConfig.Database = "postgres"
	control, err := pgx.ConnectConfig(ctx, controlConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close(context.Background())
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_, _ = control.Exec(cleanup, `ALTER DATABASE unitespace_readiness_test ALLOW_CONNECTIONS true`)
	}()
	router := newTestRouter(t, pool.Ping)
	check := func(path string, status int) {
		t.Helper()
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != status {
			t.Fatalf("%s returned %d want %d: %s", path, response.Code, status, response.Body.String())
		}
	}
	check("/ready", http.StatusOK)

	// Disable only this disposable DB and terminate its pool connection. The
	// control connection remains in postgres; no application database is touched.
	if _, err := control.Exec(ctx, `ALTER DATABASE unitespace_readiness_test ALLOW_CONNECTIONS false`); err != nil {
		t.Fatal(err)
	}
	if _, err := control.Exec(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname='unitespace_readiness_test'`); err != nil {
		t.Fatal(err)
	}
	check("/ready", http.StatusServiceUnavailable)
	check("/health", http.StatusOK)

	if _, err := control.Exec(ctx, `ALTER DATABASE unitespace_readiness_test ALLOW_CONNECTIONS true`); err != nil {
		t.Fatal(err)
	}
	check("/ready", http.StatusOK)
}
