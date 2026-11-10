package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/west6ide/workspace-backend/internal/domain"
)

// AIReadiness never creates anchors, periods or default budgets.
func (r *AIReservationRepository) AIReadiness(ctx context.Context, staleAfter time.Duration) (domain.AIReadiness, error) {
	out := domain.AIReadiness{MissingMigrations: []int64{}}
	if staleAfter < time.Second {
		return out, domain.ErrAIReservationInvalid
	}
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	for _, version := range domain.AIAccountingMigrationVersions {
		var applied bool
		err = tx.QueryRow(ctx, `SELECT COALESCE((SELECT is_applied FROM goose_db_version WHERE version_id=$1 ORDER BY id DESC LIMIT 1),false)`, version).Scan(&applied)
		if err != nil {
			return out, err
		}
		if !applied {
			out.MissingMigrations = append(out.MissingMigrations, version)
		}
	}
	if len(out.MissingMigrations) > 0 {
		return out, tx.Commit(ctx)
	}
	rows, err := tx.Query(ctx, `SELECT id FROM organizations WHERE status='active' ORDER BY id`)
	if err != nil {
		return out, err
	}
	var organizations []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return out, err
		}
		organizations = append(organizations, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	out.ActiveCompanies = len(organizations)
	inner := &OrganizationRepository{db: administrationTx{tx}}
	for _, org := range organizations {
		summary, e := inner.aiBudgetSummary(ctx, org, true, r.periods.now().UTC())
		if e != nil {
			return out, e
		}
		if !summary.Configured {
			out.UnconfiguredCompanies++
		}
		if summary.Overcommitted != nil && *summary.Overcommitted > 0 {
			out.OvercommittedCompanies++
		}
	}
	// Every active user has personal defaults; no assignment is required.
	err = tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE a.status='unknown'),count(*) FILTER(WHERE r.created_at<=clock_timestamp()-$1::bigint*interval '1 microsecond') FROM ai_usage_reservations r JOIN ai_usage_attempts a ON a.id=r.attempt_id WHERE r.status='active'`, staleAfter.Microseconds()).Scan(&out.ActiveReservations, &out.UnknownReservations, &out.StaleReservations)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}

// InspectAIAttempt returns only accounting metadata; message content is never read.
func (r *AIReservationRepository) InspectAIAttempt(ctx context.Context, org, attempt uuid.UUID) (domain.AIReconciliationJob, error) {
	out := domain.AIReconciliationJob{OrganizationID: org, AttemptID: attempt}
	err := r.db.QueryRow(ctx, `SELECT a.status,r.status,a.provider_id,a.endpoint_kind,a.requested_model,a.credit_policy_version,a.usage_adapter_version,a.provider_request_id,a.request_fingerprint IS NOT NULL FROM ai_usage_attempts a JOIN ai_usage_reservations r ON r.attempt_id=a.id WHERE a.organization_id IS NOT DISTINCT FROM $1::uuid AND a.id=$2`, nullableAIUUID(org), attempt).Scan(&out.AttemptStatus, &out.ReservationStatus, &out.ProviderID, &out.EndpointKind, &out.RequestedModel, &out.PolicyVersion, &out.UsageAdapterVersion, &out.ProviderRequestID, &out.PayloadIdentityKnown)
	return out, err
}
