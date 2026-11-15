package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/west6ide/workspace-backend/internal/config"
	"go.uber.org/zap"
)

func runStartupMigrations(ctx context.Context, cfg config.DatabaseConfig, logger *zap.Logger) error {
	// Check before opening a migration connection or inspecting migration files.
	// API instances can run with schema changes delegated to cmd/migrate.
	if !config.DatabaseMigrationsEnabled(os.Getenv("RUN_DB_MIGRATIONS")) {
		logger.Info("Database migrations are disabled")
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	dsn := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s timezone=UTC",
		cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.Database, cfg.SSLMode,
	)
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("open database for migrations: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			logger.Warn("Failed to close migration connection", zap.Error(err))
		}
	}()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("connect to database for migrations: %w", err)
	}
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("configure migration dialect: %w", err)
	}
	dir := strings.TrimSpace(os.Getenv("MIGRATIONS_DIR"))
	if dir == "" {
		dir = "db/migrations"
	}
	if err := goose.UpContext(ctx, db, dir); err != nil {
		return fmt.Errorf("apply database migrations: %w", err)
	}
	logger.Info("Database migrations are up to date")
	return nil
}
