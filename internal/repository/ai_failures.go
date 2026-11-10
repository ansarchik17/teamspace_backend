package repository

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/west6ide/workspace-backend/internal/domain"
)

type aiAttemptOwner struct {
	operation uuid.UUID
	user      *int64
	payer     string
}

func lockAIAttemptOwner(ctx context.Context, tx pgx.Tx, org, attempt uuid.UUID) (aiAttemptOwner, error) {
	var owner aiAttemptOwner
	err := tx.QueryRow(ctx, `SELECT a.operation_id,o.user_id,o.payer_kind FROM ai_usage_attempts a JOIN ai_usage_operations o ON o.id=a.operation_id WHERE a.id=$1 AND a.organization_id IS NOT DISTINCT FROM NULLIF($2::uuid,'00000000-0000-0000-0000-000000000000'::uuid)`, attempt, org).Scan(&owner.operation, &owner.user, &owner.payer)
	if errors.Is(err, pgx.ErrNoRows) {
		return owner, domain.ErrAISettlementNotFound
	}
	if err != nil {
		return owner, err
	}
	var locked uuid.UUID
	if org != uuid.Nil {
		if err = tx.QueryRow(ctx, `SELECT id FROM organizations WHERE id=$1 FOR UPDATE`, org).Scan(&locked); err != nil {
			return owner, err
		}
	}
	if owner.user != nil {
		var id int64
		if err = tx.QueryRow(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, *owner.user).Scan(&id); err != nil {
			return owner, err
		}
	}
	err = tx.QueryRow(ctx, `SELECT organization_id FROM organization_ai_budgets WHERE organization_id=$1 FOR UPDATE`, org).Scan(&locked)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return owner, err
	}
	err = tx.QueryRow(ctx, `SELECT id FROM ai_usage_operations WHERE id=$1 FOR UPDATE`, owner.operation).Scan(&locked)
	return owner, err
}

// MarkSent commits the single dispatch claim BEFORE any provider call. A false
// result forbids dispatch, including on a replay or after a failure/settlement.
func (r *AIReservationRepository) MarkSent(ctx context.Context, org, attempt uuid.UUID) (bool, error) {
	if attempt == uuid.Nil {
		return false, domain.ErrAIReservationInvalid
	}
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	owner, err := lockAIAttemptOwner(ctx, tx, org, attempt)
	if err != nil {
		return false, err
	}
	// Revalidate admission after a possible wait or lifecycle change. Existing
	// reservations remain held if dispatch is no longer authorized.
	var active bool
	if org != uuid.Nil {
		if err = tx.QueryRow(ctx, `SELECT status='active' FROM organizations WHERE id=$1`, org).Scan(&active); err != nil {
			return false, err
		}
		if !active {
			return false, reservationDenied("COMPANY_SUSPENDED", "company", nil)
		}
	}
	if owner.payer == "user" {
		if err = tx.QueryRow(ctx, `SELECT COALESCE(organization_id IS NOT DISTINCT FROM NULLIF($2::uuid,'00000000-0000-0000-0000-000000000000'::uuid) AND deleted_at IS NULL AND is_active,false) FROM users WHERE id=$1`, owner.user, org).Scan(&active); err != nil {
			return false, err
		}
		if !active {
			return false, reservationDenied("AI_ACCESS_DENIED", "account", nil)
		}
	}
	tag, err := tx.Exec(ctx, `UPDATE ai_usage_attempts a SET status='sent' FROM ai_usage_operations o,ai_usage_reservations r WHERE a.id=$1 AND o.id=a.operation_id AND r.attempt_id=a.id AND a.status='reserved' AND a.request_fingerprint IS NOT NULL AND r.status='active' AND o.status IN ('pending','running')`, attempt)
	if err != nil {
		return false, err
	}
	claimed := tag.RowsAffected() == 1
	if claimed {
		_, err = tx.Exec(ctx, `UPDATE ai_usage_operations SET status='running' WHERE id=$1`, owner.operation)
		if err != nil {
			return false, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	return claimed, nil
}

// FinishFailure accounts for one failed attempt, preserving every other paid
// attempt in the chain. The event and any charge/release commit atomically.
func (r *AIReservationRepository) FinishFailure(ctx context.Context, req domain.AIFailureRequest) (domain.AIFailureResult, error) {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return domain.AIFailureResult{}, err
	}
	defer tx.Rollback(ctx)
	out, err := r.finishFailure(ctx, tx, req)
	if err != nil {
		return domain.AIFailureResult{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.AIFailureResult{}, err
	}
	return out, nil
}

// Caller owns the transaction so a fenced reconciliation outcome can commit
// together with the job acknowledgement and all accounting changes.
func (r *AIReservationRepository) finishFailure(ctx context.Context, tx pgx.Tx, req domain.AIFailureRequest) (domain.AIFailureResult, error) {
	if !validAIFailure(req) {
		return domain.AIFailureResult{}, domain.ErrAISettlementInvalid
	}
	encoded, err := json.Marshal(req)
	if err != nil {
		return domain.AIFailureResult{}, err
	}
	fingerprint := sha256.Sum256(encoded)
	owner, err := lockAIAttemptOwner(ctx, tx, req.OrganizationID, req.AttemptID)
	if err != nil {
		return domain.AIFailureResult{}, err
	}
	var recorded []byte
	err = tx.QueryRow(ctx, `SELECT result_fingerprint FROM ai_usage_failure_events WHERE event_id=$1`, req.EventID).Scan(&recorded)
	if err == nil {
		if !bytes.Equal(recorded, fingerprint[:]) {
			return domain.AIFailureResult{}, domain.ErrAISettlementConflict
		}
		out, err := readAIFailureResult(ctx, tx, req)
		if err != nil {
			return out, err
		}
		return out, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.AIFailureResult{}, err
	}
	var status, reservationStatus string
	var week uuid.UUID
	var five *uuid.UUID
	var created, reservationCreated time.Time
	var providerID *string
	var hasTokens bool
	err = tx.QueryRow(ctx, `SELECT a.status,r.status,COALESCE(a.company_period_id,'00000000-0000-0000-0000-000000000000'::uuid),a.five_hour_period_id,a.created_at,r.created_at,a.provider_request_id,(COALESCE(a.input_tokens,0)>0 OR COALESCE(a.output_tokens,0)>0 OR COALESCE(a.total_tokens,0)>0 OR COALESCE(a.reported_input_tokens,0)>0 OR COALESCE(a.reported_output_tokens,0)>0 OR COALESCE(a.reported_total_tokens,0)>0) FROM ai_usage_attempts a JOIN ai_usage_reservations r ON r.attempt_id=a.id WHERE a.id=$1 FOR UPDATE OF a,r`, req.AttemptID).Scan(&status, &reservationStatus, &week, &five, &created, &reservationCreated, &providerID, &hasTokens)
	if err != nil {
		return domain.AIFailureResult{}, err
	}
	now := r.periods.now().UTC().Truncate(time.Microsecond)
	if now.Before(created) {
		now = created
	}
	if now.Before(reservationCreated) {
		now = reservationCreated
	}
	switch req.Outcome {
	case domain.AIFailureCharged:
		if _, err = r.settleSuccess(ctx, tx, *req.Usage); err != nil {
			return domain.AIFailureResult{}, err
		}
	case domain.AIFailureNotDispatched, domain.AIFailureNotExecuted:
		if reservationStatus != "active" || hasTokens || (status != "reserved" && status != "sent" && status != "unknown") || (req.Outcome == domain.AIFailureNotDispatched && (status != "reserved" || providerID != nil)) {
			return domain.AIFailureResult{}, domain.ErrAISettlementConflict
		}
		_, err = tx.Exec(ctx, `UPDATE ai_usage_attempts SET status='rejected',usage_status='confirmed_not_executed',usage_source=$2,charged_microcredits=0,completed_at=$3 WHERE id=$1`, req.AttemptID, req.EvidenceSource, now)
		if err != nil {
			return domain.AIFailureResult{}, err
		}
		_, err = tx.Exec(ctx, `UPDATE ai_usage_reservations SET status='released',closed_at=$2 WHERE attempt_id=$1`, req.AttemptID, now)
		if err != nil {
			return domain.AIFailureResult{}, err
		}
		if err = refreshAISettlementHolds(ctx, tx, req.OrganizationID, owner.user, owner.payer, week, five); err != nil {
			return domain.AIFailureResult{}, aiSettlementWriteError(err)
		}
	case domain.AIFailureUnknown:
		// A late cancellation/error cannot undo a known final result. Otherwise
		// keep tokens/charge nullable and retain the active hold indefinitely.
		if status == "reserved" || status == "sent" {
			_, err = tx.Exec(ctx, `UPDATE ai_usage_attempts SET status='unknown' WHERE id=$1`, req.AttemptID)
			if err != nil {
				return domain.AIFailureResult{}, err
			}
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO ai_usage_failure_events(event_id,attempt_id,outcome,reason,evidence_source,evidence_reference,result_fingerprint,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, req.EventID, req.AttemptID, string(req.Outcome), req.Reason, req.EvidenceSource, req.EvidenceReference, fingerprint[:], now)
	if err != nil {
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "23505" {
			return domain.AIFailureResult{}, domain.ErrAISettlementConflict
		}
		return domain.AIFailureResult{}, err
	}
	if _, err = refreshAIOperationStatus(ctx, tx, owner.operation, now); err != nil {
		return domain.AIFailureResult{}, err
	}
	out, err := readAIFailureResult(ctx, tx, req)
	if err != nil {
		return out, err
	}
	out.Applied = true
	return out, nil
}

func readAIFailureResult(ctx context.Context, tx pgx.Tx, req domain.AIFailureRequest) (domain.AIFailureResult, error) {
	out := domain.AIFailureResult{AttemptID: req.AttemptID}
	err := tx.QueryRow(ctx, `SELECT a.operation_id,a.status,o.status,a.charged_microcredits,r.reserved_microcredits FROM ai_usage_attempts a JOIN ai_usage_operations o ON o.id=a.operation_id JOIN ai_usage_reservations r ON r.attempt_id=a.id WHERE a.id=$1`, req.AttemptID).Scan(&out.OperationID, &out.AttemptStatus, &out.OperationStatus, &out.ChargedMicrocredits, &out.ReservedMicrocredits)
	if out.ChargedMicrocredits != nil && (req.Outcome == domain.AIFailureCharged || req.Outcome == domain.AIFailureNotDispatched || req.Outcome == domain.AIFailureNotExecuted) {
		out.ReleasedMicrocredits = aiBudgetRemaining(out.ReservedMicrocredits, *out.ChargedMicrocredits, 0)
	}
	return out, err
}

func validAIFailure(req domain.AIFailureRequest) bool {
	text := func(s string, n int) bool {
		return utf8.ValidString(s) && !strings.ContainsRune(s, 0) && strings.TrimSpace(s) != "" && utf8.RuneCountInString(s) <= n
	}
	if req.AttemptID == uuid.Nil || req.EventID == uuid.Nil || !text(req.EvidenceSource, 100) || (req.EvidenceReference != nil && !text(*req.EvidenceReference, 200)) {
		return false
	}
	switch req.Reason {
	case "local_error", "provider_error", "timeout", "client_cancelled", "invalid_usage":
	default:
		return false
	}
	switch req.Outcome {
	case domain.AIFailureNotDispatched:
		return req.Usage == nil && req.EvidenceSource == "local_dispatch_guard"
	case domain.AIFailureNotExecuted:
		return req.Usage == nil && req.EvidenceSource == "provider_confirmation" && req.EvidenceReference != nil
	case domain.AIFailureCharged:
		return req.Usage != nil && req.Usage.OrganizationID == req.OrganizationID && req.Usage.AttemptID == req.AttemptID && validAISettlement(*req.Usage)
	case domain.AIFailureUnknown:
		return req.Usage == nil
	default:
		return false
	}
}

// Failure is a business outcome, not proof of zero charge. Any unfinished
// attempt keeps the failed chain in reconciliation until all expenses resolve.
func refreshAIOperationStatus(ctx context.Context, tx pgx.Tx, operation uuid.UUID, now time.Time) (string, error) {
	var status string
	err := tx.QueryRow(ctx, `WITH failures AS (SELECT e.reason FROM ai_usage_failure_events e JOIN ai_usage_attempts a ON a.id=e.attempt_id WHERE a.operation_id=$1),next AS (SELECT CASE WHEN o.status IN ('failed','cancelled') THEN o.status WHEN EXISTS(SELECT 1 FROM ai_usage_attempts WHERE operation_id=$1 AND status NOT IN ('settled','rejected','waived')) THEN CASE WHEN EXISTS(SELECT 1 FROM failures) OR EXISTS(SELECT 1 FROM ai_usage_attempts WHERE operation_id=$1 AND status='unknown') THEN 'needs_reconciliation' ELSE 'running' END WHEN EXISTS(SELECT 1 FROM failures) THEN CASE WHEN EXISTS(SELECT 1 FROM failures WHERE reason<>'client_cancelled') THEN 'failed' ELSE 'cancelled' END ELSE 'completed' END status FROM ai_usage_operations o WHERE o.id=$1) UPDATE ai_usage_operations o SET status=n.status,completed_at=CASE WHEN o.status IN ('failed','cancelled') THEN o.completed_at WHEN n.status IN ('completed','failed','cancelled') THEN $2::timestamptz ELSE NULL END FROM next n WHERE o.id=$1 RETURNING o.status`, operation, now).Scan(&status)
	return status, err
}
