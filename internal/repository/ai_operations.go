package repository

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/west6ide/workspace-backend/internal/domain"
)

// FindOperation reads the logical operation without opening periods/reserving
// credit or consulting today's model registry. It returns nil when absent.
// The caller still must authorize access to the source document/chat.
func (r *AIReservationRepository) FindOperation(ctx context.Context, req domain.AIOperationLookup) (*domain.AIOperationView, error) {
	if !validAIOperationLookup(req) {
		return nil, domain.ErrAIReservationInvalid
	}
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var active bool
	if req.OrganizationID != uuid.Nil {
		err = tx.QueryRow(ctx, `SELECT status='active' FROM organizations WHERE id=$1`, req.OrganizationID).Scan(&active)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && !active) {
			return nil, reservationDenied("AI_ACCESS_DENIED", "account", nil)
		}
		if err != nil {
			return nil, err
		}
	}
	if req.UserID != nil {
		err = tx.QueryRow(ctx, `SELECT COALESCE(organization_id IS NOT DISTINCT FROM NULLIF($2::uuid,'00000000-0000-0000-0000-000000000000'::uuid) AND deleted_at IS NULL AND is_active,false) FROM users WHERE id=$1`, *req.UserID, req.OrganizationID).Scan(&active)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && !active) {
			return nil, reservationDenied("AI_ACCESS_DENIED", "account", nil)
		}
		if err != nil {
			return nil, err
		}
	}
	out := &domain.AIOperationView{}
	var org uuid.UUID
	var fingerprint []byte
	var kind string
	err = tx.QueryRow(ctx, `SELECT id,COALESCE(organization_id,'00000000-0000-0000-0000-000000000000'::uuid),status,request_fingerprint,operation_kind,source_kind,source_id FROM ai_usage_operations WHERE (payer_kind='user' AND user_id=$1 AND idempotency_key=$3) OR (payer_kind='service' AND $1::bigint IS NULL AND organization_id=$2 AND idempotency_key=$3)`, req.UserID, req.OrganizationID, req.IdempotencyKey).Scan(&out.OperationID, &org, &out.Status, &fingerprint, &kind, &out.SourceKind, &out.SourceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if org != req.OrganizationID || !bytes.Equal(fingerprint, req.Fingerprint[:]) || kind != req.OperationKind || !sameAIOptional(req.SourceKind, out.SourceKind) || !sameAIOptional(req.SourceID, out.SourceID) {
		return nil, domain.ErrAIReservationConflict
	}
	latest := domain.AIReservation{OperationID: out.OperationID}
	var charged *int64
	err = tx.QueryRow(ctx, `SELECT a.id,r.id,COALESCE(a.company_period_id,'00000000-0000-0000-0000-000000000000'::uuid),a.five_hour_period_id,r.reserved_microcredits,a.status,r.status,a.attempt_number,a.charged_microcredits,COUNT(*) OVER()::int FROM ai_usage_attempts a JOIN ai_usage_reservations r ON r.attempt_id=a.id WHERE a.operation_id=$1 ORDER BY a.attempt_number DESC LIMIT 1`, out.OperationID).Scan(&latest.AttemptID, &latest.ReservationID, &latest.CompanyPeriodID, &latest.FiveHourPeriodID, &latest.Microcredits, &latest.AttemptStatus, &latest.ReservationStatus, &latest.AttemptNumber, &charged, &out.AttemptCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrAIReservationConflict
	}
	if err != nil {
		return nil, err
	}
	fillAIReplay(&latest, out.Status, charged)
	out.LatestAttempt = latest
	out.Disposition = latest.Disposition
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

func fillAIReplay(out *domain.AIReservation, status string, charged *int64) {
	out.OperationStatus = status
	out.Disposition = domain.AIReplayDisposition(status, out.AttemptStatus)
	if out.AttemptStatus == "settled" && charged != nil {
		out.Settlement = &domain.AISettlement{OperationID: out.OperationID, AttemptID: out.AttemptID, ChargedMicrocredits: *charged, ReservedMicrocredits: out.Microcredits, ReleasedMicrocredits: aiBudgetRemaining(out.Microcredits, *charged, 0), OverrunMicrocredits: aiBudgetRemaining(*charged, out.Microcredits, 0), OperationStatus: status}
	}
}

func validAIOperationLookup(req domain.AIOperationLookup) bool {
	text := func(s string, n int) bool {
		return utf8.ValidString(s) && !strings.ContainsRune(s, 0) && strings.TrimSpace(s) != "" && utf8.RuneCountInString(s) <= n
	}
	return (req.OrganizationID != uuid.Nil || req.UserID != nil) && (req.UserID == nil || *req.UserID > 0) && req.Fingerprint != ([32]byte{}) && text(req.IdempotencyKey, 200) && text(req.OperationKind, 100) && (req.SourceKind == nil) == (req.SourceID == nil) && (req.SourceKind == nil || (text(*req.SourceKind, 100) && *req.SourceID != uuid.Nil))
}
