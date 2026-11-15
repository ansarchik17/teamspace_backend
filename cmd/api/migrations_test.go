package main

import (
	"context"
	"errors"
	"testing"

	"github.com/west6ide/workspace-backend/internal/config"
	"go.uber.org/zap"
)

func TestDisabledStartupMigrationsDoNotOpenDatabaseOrReadFiles(t *testing.T) {
	for _, value := range []string{"false", "0", "no", "off", " FALSE ", " Off "} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("RUN_DB_MIGRATIONS", value)
			t.Setenv("MIGRATIONS_DIR", t.TempDir()+"/does-not-exist")
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			// This invalid config cannot open a migration database. Disabled
			// migrations must still succeed, even with no migration files.
			cfg := config.DatabaseConfig{Port: "invalid-port"}
			if err := runStartupMigrations(ctx, cfg, zap.NewNop()); err != nil {
				t.Fatalf("disabled startup migrations attempted work: %v", err)
			}
		})
	}
}

func TestEnabledStartupMigrationsPropagateCancellation(t *testing.T) {
	t.Setenv("RUN_DB_MIGRATIONS", "true")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cfg := config.DatabaseConfig{Host: "127.0.0.1", Port: "1", User: "test", Password: "test", Database: "test", SSLMode: "disable"}
	if err := runStartupMigrations(ctx, cfg, zap.NewNop()); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation to stop startup migrations, got %v", err)
	}
}
