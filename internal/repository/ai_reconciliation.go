package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/west6ide/workspace-backend/internal/domain"
)

// ScheduleReconciliation discovers old active holds, including reservations
// without reconcile_after. Age selects work; it never proves a zero charge.
func (r *AIReservationRepository) ScheduleReconciliation(ctx context.Context, staleAfter time.Duration, limit int) (int64, error) {
	if staleAfter < time.Second || staleAfter > 30*24*time.Hour || limit < 1 || limit > 1000 {
		return 0, domain.ErrAISettlementInvalid
	}
	_, err := r.db.Exec(ctx, `UPDATE ai_usage_reconciliation_jobs j SET status='completed',lease_token=NULL,lease_until=NULL,last_reason='already_final',completed_at=clock_timestamp() FROM ai_usage_reservations r WHERE r.attempt_id=j.attempt_id AND r.status<>'active' AND j.status<>'completed' AND (j.status<>'processing' OR j.lease_until<=clock_timestamp())`)
	if err != nil {
		return 0, err
	}
	tag, err := r.db.Exec(ctx, `INSERT INTO ai_usage_reconciliation_jobs(attempt_id) SELECT r.attempt_id FROM ai_usage_reservations r WHERE r.status='active' AND COALESCE(r.reconcile_after,r.created_at+$1::bigint*interval '1 microsecond')<=clock_timestamp() AND NOT EXISTS(SELECT 1 FROM ai_usage_reconciliation_jobs j WHERE j.attempt_id=r.attempt_id) ORDER BY COALESCE(r.reconcile_after,r.created_at+$1::bigint*interval '1 microsecond'),r.attempt_id LIMIT $2 ON CONFLICT DO NOTHING`, staleAfter.Microseconds(), limit)
	return tag.RowsAffected(), err
}

// ClaimReconciliation locks only a queue row in a short transaction. It never
// takes accounting-owner locks or holds a DB transaction across provider lookup.
// SKIP LOCKED and a new random token make expiry/reclaim safe across processes.
func (r *AIReservationRepository) ClaimReconciliation(ctx context.Context, lease time.Duration) (*domain.AIReconciliationJob, error) {
	if lease < time.Second || lease > time.Hour {
		return nil, domain.ErrAISettlementInvalid
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	out := &domain.AIReconciliationJob{LeaseToken: uuid.New()}
	err = tx.QueryRow(ctx, `WITH candidate AS (SELECT attempt_id FROM ai_usage_reconciliation_jobs WHERE (status='pending' AND next_attempt_at<=clock_timestamp()) OR (status='processing' AND lease_until<=clock_timestamp()) ORDER BY next_attempt_at,attempt_id FOR UPDATE SKIP LOCKED LIMIT 1) UPDATE ai_usage_reconciliation_jobs j SET status='processing',lease_token=$1,lease_until=clock_timestamp()+$2::bigint*interval '1 microsecond',claim_count=claim_count+1 FROM candidate c WHERE j.attempt_id=c.attempt_id RETURNING j.attempt_id,j.lease_until,j.claim_count`, out.LeaseToken, lease.Microseconds()).Scan(&out.AttemptID, &out.LeaseUntil, &out.ClaimNumber)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	err = tx.QueryRow(ctx, `SELECT COALESCE(a.organization_id,'00000000-0000-0000-0000-000000000000'::uuid),a.status,r.status,a.provider_id,a.endpoint_kind,a.requested_model,a.credit_policy_version,a.usage_adapter_version,a.provider_request_id,a.request_fingerprint IS NOT NULL FROM ai_usage_attempts a JOIN ai_usage_reservations r ON r.attempt_id=a.id WHERE a.id=$1`, out.AttemptID).Scan(&out.OrganizationID, &out.AttemptStatus, &out.ReservationStatus, &out.ProviderID, &out.EndpointKind, &out.RequestedModel, &out.PolicyVersion, &out.UsageAdapterVersion, &out.ProviderRequestID, &out.PayloadIdentityKnown)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

// ApplyReconciliation fences the lease AFTER the normal accounting-owner locks.
// Expense, reserve release and job acknowledgement commit together. An expired
// process cannot apply after another instance has reclaimed the job.
func (r *AIReservationRepository) ApplyReconciliation(ctx context.Context, job domain.AIReconciliationJob, evidence domain.AIReconciliationEvidence, retry time.Duration, maxClaims int) (domain.AIReconciliationResult, error) {
	if job.AttemptID == uuid.Nil || job.LeaseToken == uuid.Nil || retry < time.Second || retry > 24*time.Hour || maxClaims < 1 || maxClaims > 1000 {
		return domain.AIReconciliationResult{}, domain.ErrAISettlementInvalid
	}
	switch evidence.Outcome {
	case domain.AIReconcileUsage, domain.AIReconcileNotExecuted, domain.AIReconcileNotDispatched, domain.AIReconcilePending, domain.AIReconcileManual:
	default:
		return domain.AIReconciliationResult{}, domain.ErrAISettlementInvalid
	}
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return domain.AIReconciliationResult{}, err
	}
	defer tx.Rollback(ctx)
	owner, err := lockAIAttemptOwner(ctx, tx, job.OrganizationID, job.AttemptID)
	if err != nil {
		return domain.AIReconciliationResult{}, err
	}
	var status, reservationStatus string
	var identityKnown bool
	err = tx.QueryRow(ctx, `SELECT a.status,r.status,a.request_fingerprint IS NOT NULL FROM ai_usage_attempts a JOIN ai_usage_reservations r ON r.attempt_id=a.id WHERE a.id=$1 FOR UPDATE OF a,r`, job.AttemptID).Scan(&status, &reservationStatus, &identityKnown)
	if err != nil {
		return domain.AIReconciliationResult{}, err
	}
	var owned bool
	var claims int64
	err = tx.QueryRow(ctx, `SELECT COALESCE(status='processing' AND lease_token=$2 AND lease_until>clock_timestamp(),false),claim_count FROM ai_usage_reconciliation_jobs WHERE attempt_id=$1 FOR UPDATE`, job.AttemptID, job.LeaseToken).Scan(&owned, &claims)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !owned) {
		return domain.AIReconciliationResult{}, domain.ErrAIReconciliationLeaseLost
	}
	if err != nil {
		return domain.AIReconciliationResult{}, err
	}
	out := domain.AIReconciliationResult{JobStatus: "completed", Reason: "already_final"}
	if reservationStatus == "active" {
		out = domain.AIReconciliationResult{JobStatus: "pending", Reason: "provider_pending"}
		if evidence.Outcome == domain.AIReconcileNotDispatched && (status != "reserved" || !identityKnown) {
			evidence = domain.AIReconciliationEvidence{Outcome: domain.AIReconcilePending, Reason: "state_changed"}
		}
		if evidence.Outcome == domain.AIReconcileUsage || evidence.Outcome == domain.AIReconcileNotExecuted || evidence.Outcome == domain.AIReconcileNotDispatched {
			// A savepoint lets invalid evidence leave a manual job without any
			// partial journal/counter changes or an aborted PostgreSQL transaction.
			var sub pgx.Tx
			sub, err = tx.Begin(ctx)
			if err != nil {
				return out, err
			}
			if evidence.Outcome == domain.AIReconcileUsage {
				if evidence.Usage == nil || evidence.Usage.OrganizationID != job.OrganizationID || evidence.Usage.AttemptID != job.AttemptID {
					err = domain.ErrAISettlementInvalid
				} else {
					_, err = r.settleSuccess(ctx, sub, *evidence.Usage)
				}
			} else {
				failure := domain.AIFailureRequest{OrganizationID: job.OrganizationID, AttemptID: job.AttemptID, EventID: uuid.NewSHA1(job.AttemptID, []byte("reconcile-not-dispatched")), Outcome: domain.AIFailureNotDispatched, Reason: "local_error", EvidenceSource: "local_dispatch_guard"}
				if evidence.Outcome == domain.AIReconcileNotExecuted {
					failure.Outcome = domain.AIFailureNotExecuted
					failure.Reason = "provider_error"
					failure.EvidenceSource = "provider_confirmation"
					failure.EvidenceReference = evidence.ProviderConfirmation
					if evidence.ProviderConfirmation != nil {
						failure.EventID = uuid.NewSHA1(job.AttemptID, []byte("reconcile-confirmation:"+*evidence.ProviderConfirmation))
					}
				}
				_, err = r.finishFailure(ctx, sub, failure)
			}
			if err != nil {
				rollbackErr := sub.Rollback(ctx)
				if rollbackErr != nil {
					return out, rollbackErr
				}
				if !isAIReconciliationEvidenceError(err) {
					return out, err
				}
				evidence = domain.AIReconciliationEvidence{Outcome: domain.AIReconcileManual, Reason: "invalid_evidence"}
			} else {
				if err = sub.Commit(ctx); err != nil {
					return out, err
				}
				out = domain.AIReconciliationResult{JobStatus: "completed", Reason: string(evidence.Outcome)}
			}
		}
		if evidence.Outcome == domain.AIReconcilePending || evidence.Outcome == domain.AIReconcileManual {
			out.Reason = aiReconciliationReason(evidence.Reason)
			if evidence.Outcome == domain.AIReconcileManual {
				out.JobStatus = "manual"
			} else if claims >= int64(maxClaims) {
				out.JobStatus = "manual"
				out.Reason = "attempts_exhausted"
			}
			_, err = tx.Exec(ctx, `UPDATE ai_usage_attempts SET status='unknown' WHERE id=$1 AND status IN ('reserved','sent')`, job.AttemptID)
			if err != nil {
				return out, err
			}
			if _, err = refreshAIOperationStatus(ctx, tx, owner.operation, r.periods.now().UTC()); err != nil {
				return out, err
			}
		}
	}
	tag, err := tx.Exec(ctx, `UPDATE ai_usage_reconciliation_jobs SET status=$3,last_reason=$4,lease_token=NULL,lease_until=NULL,next_attempt_at=clock_timestamp()+$5::bigint*interval '1 microsecond',completed_at=CASE WHEN $3='completed' THEN clock_timestamp() ELSE NULL END WHERE attempt_id=$1 AND lease_token=$2`, job.AttemptID, job.LeaseToken, out.JobStatus, out.Reason, retry.Microseconds())
	if err != nil {
		return out, err
	}
	if tag.RowsAffected() != 1 {
		return out, domain.ErrAIReconciliationLeaseLost
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.AIReconciliationResult{}, err
	}
	out.Applied = true
	return out, nil
}

func isAIReconciliationEvidenceError(err error) bool {
	for _, known := range []error{domain.ErrAISettlementInvalid, domain.ErrAISettlementConflict, domain.ErrAICreditUnknownPolicy, domain.ErrAICreditUnknownModel, domain.ErrAICreditUnknownUsage, domain.ErrAICreditInvalidUsage, domain.ErrAICreditOverflow} {
		if errors.Is(err, known) {
			return true
		}
	}
	return false
}

func aiReconciliationReason(reason string) string {
	switch reason {
	case "provider_pending", "lookup_failed", "lookup_unavailable", "invalid_evidence", "state_changed", "attempts_exhausted":
		return reason
	default:
		return "lookup_unavailable"
	}
}
