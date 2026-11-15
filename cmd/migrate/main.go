package main

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/west6ide/workspace-backend/internal/config"
	"go.uber.org/zap"
)

const migrationTimeout = 10 * time.Minute

func main() {
	logger, err := config.NewLogger(config.LoggerConfig{
		Level:  envOrDefault("LOG_LEVEL", "info"),
		Format: envOrDefault("LOG_FORMAT", "console"),
		Output: envOrDefault("LOG_OUTPUT", "stdout"),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "initialize migration logger: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = logger.Sync() }()
	defer zap.RedirectStdLog(logger)()

	if !migrationsEnabled(os.Getenv("RUN_DB_MIGRATIONS")) {
		logger.Info("database migrations are disabled")
		return
	}

	dsn := buildPostgresDSN(
		envOrDefault("POSTGRES_HOST", "localhost"),
		envOrDefault("POSTGRES_PORT", "5432"),
		envOrDefault("POSTGRES_USER", "postgres"),
		envOrDefault("POSTGRES_PASSWORD", "postgres"),
		envOrDefault("POSTGRES_DB", "workspace"),
		envOrDefault("POSTGRES_SSL_MODE", "disable"),
	)

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		logger.Fatal("open database for migrations", zap.Error(err))
	}
	defer func() { _ = db.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), migrationTimeout)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		logger.Fatal("connect to database for migrations", zap.Error(err))
	}
	if err := goose.SetDialect("postgres"); err != nil {
		logger.Fatal("configure migration dialect", zap.Error(err))
	}

	dir := envOrDefault("MIGRATIONS_DIR", "/app/db/migrations")
	if err := applyMigrations(ctx, db, dir); err != nil {
		logger.Fatal("apply database migrations", zap.Error(err))
	}

	logger.Info("database migrations are up to date")
}

// News was released after audio despite having earlier migration versions.
// Only these reviewed, independent news migrations may fill that ledger gap.
var delayedNewsVersions = []int64{20260929001000, 20260929001100, 20260929001200, 20260929001300}

// Research jobs (20261023000100) were released before the independent audio
// measurement migration. Admit only that reviewed gap as well as the news gap.
// Employment admission was added after company-service defaults (20261027000300).
// Its new tables and login functions do not rewrite existing employee records.
var reviewedDelayedVersions = append([]int64{20261022100600, 20261022120000}, delayedNewsVersions...)

func applyMigrations(ctx context.Context, db *sql.DB, dir string) error {
	provider, err := goose.NewProvider(goose.DialectPostgres, db, os.DirFS(dir), goose.WithAllowOutofOrder(true))
	if err != nil {
		return err
	}
	current, err := provider.GetDBVersion(ctx)
	if err != nil {
		return err
	}
	statuses, err := provider.Status(ctx)
	if err != nil {
		return err
	}
	for _, status := range statuses {
		if status.State != goose.StatePending || status.Source.Version >= current {
			continue
		}
		allowed := false
		for _, version := range reviewedDelayedVersions {
			if status.Source.Version == version {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("unexpected missing migration %d before current version %d", status.Source.Version, current)
		}
	}
	_, err = provider.Up(ctx)
	return err
}

func buildPostgresDSN(host, port, user, password, database, sslMode string) string {
	query := url.Values{}
	query.Set("sslmode", sslMode)

	return (&url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(user, password),
		Host:     net.JoinHostPort(host, port),
		Path:     "/" + strings.TrimPrefix(database, "/"),
		RawQuery: query.Encode(),
	}).String()
}

func envOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func migrationsEnabled(value string) bool {
	return config.DatabaseMigrationsEnabled(value)
}
