package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

const initialWeeklyQuota int64 = 100_000_000
const initialFiveHourLimit int64 = 20_000_000

// Defaults initialize only missing employee settings, using the same rights,
// revisions, budget locks and audit transaction as the administrative API.
// Existing settings (including explicit zero) and all usage remain untouched.
func runDefaults(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("defaults", flag.ContinueOnError)
	flags.SetOutput(out)
	database := flags.String("database-name", "", "exact database name")
	organization := flags.String("organization", "", "company UUID")
	actor := flags.Int64("actor-id", 0, "existing platform administrator; checked by repository")
	apply := flags.Bool("apply", false, "initialize missing quotas; default is preview")
	if err := flags.Parse(args); err != nil {
		return err
	}
	org, err := uuid.Parse(*organization)
	if err != nil || org == uuid.Nil || *actor <= 0 || *database == "" || flags.NArg() != 0 {
		return errors.New("defaults requires exact database-name, organization UUID and positive actor-id")
	}
	dsn := os.Getenv("AI_OPS_DATABASE_URL")
	if dsn == "" {
		return errors.New("AI_OPS_DATABASE_URL must be explicitly supplied by the operator")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil || cfg.ConnConfig.Database != *database {
		return errors.New("invalid connection settings or database-name mismatch")
	}
	cfg.MaxConns = 2
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return errors.New("cannot initialize operator database connection")
	}
	defer db.Close()
	var actual string
	if err := db.QueryRow(ctx, "SELECT current_database()").Scan(&actual); err != nil || actual != *database {
		return errors.New("cannot verify target database identity")
	}
	return initializeDefaults(ctx, db, *actor, org, *apply, out)
}

func initializeDefaults(ctx context.Context, db *pgxpool.Pool, actor int64, org uuid.UUID, apply bool, out io.Writer) error {
	repo := repository.NewOrganizationRepository(db, zap.NewNop())
	budget, err := repo.AdministrationAIBudget(ctx, actor, &org)
	if err != nil {
		return errors.New("company access denied or budget unavailable")
	}
	if !budget.Configured || !budget.CanSetUserQuotas {
		return errors.New("configure an active company budget before allocating employee quotas")
	}
	if budget.MaxFiveHour == nil || *budget.MaxFiveHour < initialFiveHourLimit {
		return errors.New("company five-hour maximum must permit the initial 20-credit limit")
	}
	rows, err := db.Query(ctx, `SELECT u.id,COALESCE(q.revision,0)
FROM users u LEFT JOIN organization_ai_user_quotas q ON q.organization_id=u.organization_id AND q.user_id=u.id
WHERE u.organization_id=$1 AND u.deleted_at IS NULL AND u.is_active
AND NOT EXISTS(SELECT 1 FROM organization_user_blocks b WHERE b.user_id=u.id)
AND q.weekly_quota_microcredits IS NULL AND q.five_hour_limit_microcredits IS NULL ORDER BY u.id`, org)
	if err != nil {
		return errors.New("cannot read missing employee settings")
	}
	type employee struct{ id, revision int64 }
	var employees []employee
	for rows.Next() {
		var item employee
		if err := rows.Scan(&item.id, &item.revision); err != nil {
			rows.Close()
			return err
		}
		employees = append(employees, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if budget.Unallocated == nil || int64(len(employees)) > *budget.Unallocated/initialWeeklyQuota {
		return errors.New("company unallocated budget is insufficient for all missing employee defaults; no changes made")
	}
	if err := json.NewEncoder(out).Encode(map[string]any{"organization_id": org, "eligible_employees": len(employees), "weekly_credits": 100, "five_hour_credits": 20, "apply": apply}); err != nil {
		return err
	}
	if !apply {
		return nil
	}
	for i, item := range employees {
		fresh, err := repo.AdministrationAIBudget(ctx, actor, &org)
		if err != nil {
			return fmt.Errorf("stopped after %d committed assignments; re-run preview before retry", i)
		}
		weekly, five := initialWeeklyQuota, initialFiveHourLimit
		err = repo.SetAdministrationAIUserQuota(ctx, actor, item.id, &org, domain.SetAIUserQuotaRequest{WeeklyQuota: &weekly, FiveHourLimit: &five, Revision: &item.revision, CompanyRevision: &fresh.Revision})
		if err != nil {
			return fmt.Errorf("stopped after %d committed assignments due to a concurrent change or quota validation; earlier assignments remain audited", i)
		}
	}
	return json.NewEncoder(out).Encode(map[string]any{"assigned": len(employees), "existing_settings_unchanged": true})
}
