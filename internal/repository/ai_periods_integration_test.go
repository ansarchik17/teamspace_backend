package repository

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestAIPeriodRepository(t *testing.T) {
	dsn := os.Getenv("TEST_MESSENGER_DATABASE_URL")
	if dsn == "" {
		t.Skip("set disposable TEST_MESSENGER_DATABASE_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	admin, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(admin.Close)
	schema := "ai_period_repo_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, cleanupErr := admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		require.NoError(t, cleanupErr)
	})
	cfg, err := pgxpool.ParseConfig(dsn)
	require.NoError(t, err)
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(db.Close)
	_, err = db.Exec(ctx, `CREATE TABLE organizations(id UUID PRIMARY KEY);
CREATE TABLE users(id BIGINT PRIMARY KEY,organization_id UUID REFERENCES organizations(id),deleted_at TIMESTAMPTZ,is_active BOOLEAN NOT NULL DEFAULT true);`)
	require.NoError(t, err)
	for _, file := range []string{"20261020100100_ai_budget_settings.sql", "20261020100200_ai_accounting_periods.sql", "20261020100300_ai_operations_and_reservations.sql", "20261022100100_ai_five_hour_request_windows.sql", "20261022100200_ai_personal_weekly_windows.sql", "20261020100900_ai_enforcement_mode_unconfigured.sql", "20261022100400_ai_personal_access_policy.sql", "20261022100500_ai_final_request_cutoffs.sql"} {
		data, readErr := os.ReadFile("../../db/migrations/" + file)
		require.NoError(t, readErr)
		_, err = db.Exec(ctx, strings.Split(string(data), "-- +goose Down")[0])
		require.NoError(t, err)
	}
	a, b := uuid.New(), uuid.New()
	_, err = db.Exec(ctx, `INSERT INTO organizations VALUES($1),($2)`, a, b)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO users(id,organization_id) VALUES(1,$1),(2,$2)`, a, b)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO organization_ai_budgets(organization_id,weekly_budget_microcredits,max_five_hour_microcredits) VALUES($1,100,100),($2,100,100)`, a, b)
	require.NoError(t, err)
	anchor := time.Date(2026, 10, 16, 5, 20, 0, 123456000, time.UTC)
	clock := anchor
	repo := NewAIPeriodRepository(func() time.Time { return clock })
	uid := int64(1)
	tx, err := db.Begin(ctx)
	require.NoError(t, err)
	_, err = repo.OpenForReservation(ctx, tx, a, &uid)
	require.NoError(t, err)
	require.NoError(t, tx.Rollback(ctx))
	var count int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM organization_ai_weekly_anchors`).Scan(&count))
	require.Zero(t, count, "rejected reservation must not open an anchor")

	var wg sync.WaitGroup
	results := make(chan AIReservationPeriods, 8)
	errors := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tx, openErr := db.Begin(ctx)
			if openErr != nil {
				errors <- openErr
				return
			}
			defer tx.Rollback(ctx)
			periods, openErr := repo.OpenForReservation(ctx, tx, a, &uid)
			if openErr == nil {
				openErr = tx.Commit(ctx)
			}
			errors <- openErr
			results <- periods
		}()
	}
	wg.Wait()
	close(results)
	close(errors)
	for openErr := range errors {
		require.NoError(t, openErr)
	}
	var initial AIReservationPeriods
	for periods := range results {
		if initial.Weekly.ID == uuid.Nil {
			initial = periods
		}
		require.Equal(t, initial, periods)
	}
	require.Equal(t, anchor, initial.ObservedAt)
	open := func(org uuid.UUID, user *int64) AIReservationPeriods {
		t.Helper()
		tx, beginErr := db.Begin(ctx)
		require.NoError(t, beginErr)
		defer tx.Rollback(ctx)
		periods, openErr := repo.OpenForReservation(ctx, tx, org, user)
		require.NoError(t, openErr)
		require.NoError(t, tx.Commit(ctx))
		return periods
	}
	_, err = db.Exec(ctx, `UPDATE user_ai_five_hour_periods SET used_microcredits=7,reserved_microcredits=3 WHERE id=$1`, initial.FiveHour.ID)
	require.NoError(t, err)
	clock = anchor.Add(5*time.Hour - time.Microsecond)
	require.Equal(t, initial.FiveHour.ID, open(a, &uid).FiveHour.ID)
	clock = anchor.Add(5 * time.Hour)
	next := open(a, &uid)
	require.NotEqual(t, initial.FiveHour.ID, next.FiveHour.ID)
	require.Equal(t, initial.Weekly.ID, next.Weekly.ID)
	require.Equal(t, initial.FiveHour.EndsAt, next.FiveHour.StartsAt)
	clock = anchor.Add(domain.AIWeeklyDuration)
	boundary := open(a, &uid)
	require.NotEqual(t, initial.Weekly.ID, boundary.Weekly.ID)
	require.Equal(t, initial.Weekly.EndsAt, boundary.Weekly.StartsAt)
	clock = anchor.Add(30*24*time.Hour + time.Minute)
	afterIdle := open(a, &uid)
	require.Equal(t, anchor.Add(28*24*time.Hour), afterIdle.Weekly.StartsAt)
	require.True(t, afterIdle.FiveHour.Contains(clock))
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM organization_ai_weekly_periods WHERE organization_id=$1`, a).Scan(&count))
	require.Equal(t, 3, count, "idle windows are not materialized")
	var used, reserved int64
	require.NoError(t, db.QueryRow(ctx, `SELECT used_microcredits,reserved_microcredits FROM user_ai_five_hour_periods WHERE id=$1`, initial.FiveHour.ID).Scan(&used, &reserved))
	require.Equal(t, int64(7), used)
	require.Equal(t, int64(3), reserved, "ending a period must not release unknown reservations")
	_, err = db.Exec(ctx, `UPDATE users SET organization_id=$1 WHERE id=1`, b)
	require.NoError(t, err)
	transferred := open(b, &uid)
	require.Equal(t, afterIdle.FiveHour.ID, transferred.FiveHour.ID)
	require.Equal(t, clock, transferred.Weekly.StartsAt)
	require.Nil(t, open(b, nil).FiveHour, "service payer has no personal window")
	tx, err = db.Begin(ctx)
	require.NoError(t, err)
	_, err = repo.OpenForReservation(ctx, tx, a, &uid)
	require.ErrorIs(t, err, ErrAIPeriodMembership)
	require.NoError(t, tx.Rollback(ctx))
}
