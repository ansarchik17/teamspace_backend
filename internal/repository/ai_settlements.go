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

// SettleSuccess records trusted usage even after transfer, blocking or company
// suspension. It uses historical journal identity, not current membership. The
// owner lock order matches Reserve: organization -> user -> budget -> journal.
func (r *AIReservationRepository) SettleSuccess(ctx context.Context, req domain.AISettleRequest) (domain.AISettlement, error) {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return domain.AISettlement{}, err
	}
	defer tx.Rollback(ctx)
	out, err := r.settleSuccess(ctx, tx, req)
	if err != nil {
		return domain.AISettlement{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.AISettlement{}, err
	}
	return out, nil
}

// Caller owns the transaction, allowing paid failure evidence and usage to be
// recorded atomically. Never dispatch a provider from this method.
func (r *AIReservationRepository) settleSuccess(ctx context.Context, tx pgx.Tx, req domain.AISettleRequest) (domain.AISettlement, error) {
	if !validAISettlement(req) {
		return domain.AISettlement{}, domain.ErrAISettlementInvalid
	}
	encoded, err := json.Marshal(req)
	if err != nil {
		return domain.AISettlement{}, err
	}
	fingerprint := sha256.Sum256(encoded)
	owner, err := lockAIAttemptOwner(ctx, tx, req.OrganizationID, req.AttemptID)
	if err != nil {
		return domain.AISettlement{}, err
	}
	operation, user, payer := owner.operation, owner.user, owner.payer
	var week uuid.UUID
	var five *uuid.UUID
	var key domain.AICreditModelKey
	var adapter, status, reservationStatus string
	var input int64
	var output, cached, reasoning, charged *int64
	var providerRequest *string
	var reserved int64
	var created, reservationCreated time.Time
	err = tx.QueryRow(ctx, `SELECT COALESCE(a.company_period_id,'00000000-0000-0000-0000-000000000000'::uuid),a.five_hour_period_id,a.provider_id,a.endpoint_kind,a.requested_model,a.credit_policy_version,a.usage_adapter_version,a.input_weight_microcredits,a.output_weight_microcredits,a.cached_input_weight_microcredits,a.reasoning_output_weight_microcredits,a.status,a.charged_microcredits,a.provider_request_id,a.created_at,r.status,r.reserved_microcredits,r.created_at FROM ai_usage_attempts a JOIN ai_usage_reservations r ON r.attempt_id=a.id WHERE a.id=$1 FOR UPDATE OF a,r`, req.AttemptID).Scan(&week, &five, &key.ProviderID, &key.EndpointKind, &key.ModelID, &key.PolicyVersion, &adapter, &input, &output, &cached, &reasoning, &status, &charged, &providerRequest, &created, &reservationStatus, &reserved, &reservationCreated)
	if err != nil {
		return domain.AISettlement{}, err
	}
	out := domain.AISettlement{OperationID: operation, AttemptID: req.AttemptID, ReservedMicrocredits: reserved}
	var recorded []byte
	err = tx.QueryRow(ctx, `SELECT result_fingerprint,overrun_microcredits FROM ai_usage_settlements WHERE attempt_id=$1`, req.AttemptID).Scan(&recorded, &out.OverrunMicrocredits)
	if err == nil {
		if !bytes.Equal(recorded, fingerprint[:]) || charged == nil || status != "settled" || reservationStatus != "settled" {
			return domain.AISettlement{}, domain.ErrAISettlementConflict
		}
		out.ChargedMicrocredits = *charged
		out.ReleasedMicrocredits = aiBudgetRemaining(reserved, *charged, 0)
		if err = tx.QueryRow(ctx, `SELECT status FROM ai_usage_operations WHERE id=$1`, operation).Scan(&out.OperationStatus); err != nil {
			return domain.AISettlement{}, err
		}
		return out, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.AISettlement{}, err
	}
	if reservationStatus != "active" || (status != "reserved" && status != "sent" && status != "unknown") || adapter != req.UsageAdapterVersion || (providerRequest != nil && req.ProviderRequestID != nil && *providerRequest != *req.ProviderRequestID) {
		return domain.AISettlement{}, domain.ErrAISettlementConflict
	}
	rates := domain.AICreditRates{Input: input, InputOnly: output == nil}
	if output != nil {
		rates.Output = *output
	}
	if cached != nil {
		rates.Cached = *cached
	}
	if reasoning != nil {
		rates.Reasoning = *reasoning
	}
	if key.PolicyVersion == domain.AIAudioCreditPolicyV1 {
		unitRates, unitErr := domain.AICreditRatesForModel(key)
		if unitErr != nil {
			return domain.AISettlement{}, unitErr
		}
		rates.Unit = unitRates.Unit
	}
	if req.ActualModel != key.ModelID {
		// A confirmed different model uses its exact entry in the pinned policy;
		// the original reservation weights remain immutable in the attempt.
		key.ModelID = req.ActualModel
		rates, err = domain.AICreditRatesForModel(key)
		if err != nil {
			return domain.AISettlement{}, err
		}
	}
	out.ChargedMicrocredits, err = domain.CalculateAICreditsAtRates(rates, req.Usage)
	if err != nil {
		return domain.AISettlement{}, err
	}
	out.ReleasedMicrocredits = aiBudgetRemaining(reserved, out.ChargedMicrocredits, 0)
	out.OverrunMicrocredits = aiBudgetRemaining(out.ChargedMicrocredits, reserved, 0)
	now := r.periods.now().UTC().Truncate(time.Microsecond)
	if now.Before(created) {
		now = created
	}
	if now.Before(reservationCreated) {
		now = reservationCreated
	}
	if req.Usage.MeasuredUnit != "" {
		_, err = tx.Exec(ctx, `UPDATE ai_usage_attempts SET measured_unit=$2,measured_quantity=$3 WHERE id=$1`, req.AttemptID, req.Usage.MeasuredUnit, req.Usage.MeasuredQuantity)
		if err != nil {
			return domain.AISettlement{}, err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE ai_usage_attempts SET actual_model=$2,provider_request_id=COALESCE($3,provider_request_id),reported_input_tokens=$4,reported_output_tokens=$5,reported_total_tokens=$6,input_tokens=$7,output_tokens=$8,total_tokens=$9,cached_input_tokens=$10,reasoning_output_tokens=$11,usage_status=$12,usage_source=$13,charged_microcredits=$14,status='settled',completed_at=$15 WHERE id=$1`, req.AttemptID, req.ActualModel, req.ProviderRequestID, req.ReportedInputTokens, req.ReportedOutputTokens, req.ReportedTotalTokens, req.Usage.InputTokens, req.Usage.OutputTokens, req.Usage.TotalTokens, req.Usage.CachedInput, req.Usage.ReasoningOutput, req.Usage.Status, req.UsageSource, out.ChargedMicrocredits, now)
	if err != nil {
		return domain.AISettlement{}, aiSettlementWriteError(err)
	}
	_, err = tx.Exec(ctx, `UPDATE ai_usage_reservations SET status='settled',closed_at=$2 WHERE attempt_id=$1`, req.AttemptID, now)
	if err != nil {
		return domain.AISettlement{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO ai_usage_settlements(attempt_id,result_fingerprint,input_weight_microcredits,output_weight_microcredits,cached_input_weight_microcredits,reasoning_output_weight_microcredits,input_only,overrun_microcredits,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, req.AttemptID, fingerprint[:], rates.Input, nullableAIWeight(rates.Output), nullableAIWeight(rates.Cached), nullableAIWeight(rates.Reasoning), rates.InputOnly, out.OverrunMicrocredits, now)
	if err != nil {
		return domain.AISettlement{}, err
	}
	if req.OrganizationID != uuid.Nil {
		if payer == "user" {
			_, err = tx.Exec(ctx, `INSERT INTO organization_ai_user_weekly_usage(organization_id,period_id,user_id) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, req.OrganizationID, week, user)
		} else {
			_, err = tx.Exec(ctx, `INSERT INTO organization_ai_service_weekly_usage(organization_id,period_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, req.OrganizationID, week)
		}
		if err != nil {
			return domain.AISettlement{}, err
		}
	}
	// Refresh authoritative holds BEFORE adding usage: the old reservation no
	// longer occupies capacity, including mirrors of all later existing windows.
	if err = refreshAISettlementHolds(ctx, tx, req.OrganizationID, user, payer, week, five); err != nil {
		return domain.AISettlement{}, aiSettlementWriteError(err)
	}
	_, err = tx.Exec(ctx, `UPDATE organization_ai_weekly_periods SET used_microcredits=used_microcredits+$2 WHERE id=$1`, week, out.ChargedMicrocredits)
	if err != nil {
		return domain.AISettlement{}, aiSettlementWriteError(err)
	}
	if payer == "user" {
		_, err = tx.Exec(ctx, `UPDATE ai_user_weekly_periods SET used_microcredits=used_microcredits+$2 WHERE id=(SELECT period_id FROM ai_user_weekly_attempts WHERE attempt_id=$1)`, req.AttemptID, out.ChargedMicrocredits)
		if err != nil {
			return domain.AISettlement{}, aiSettlementWriteError(err)
		}
	}
	if payer == "user" {
		_, err = tx.Exec(ctx, `UPDATE organization_ai_user_weekly_usage SET used_microcredits=used_microcredits+$4 WHERE organization_id=$1 AND period_id=$2 AND user_id=$3`, req.OrganizationID, week, user, out.ChargedMicrocredits)
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE user_ai_five_hour_periods SET used_microcredits=used_microcredits+$2 WHERE id=$1`, five, out.ChargedMicrocredits)
		}
	} else {
		_, err = tx.Exec(ctx, `UPDATE organization_ai_service_weekly_usage SET used_microcredits=used_microcredits+$3 WHERE organization_id=$1 AND period_id=$2`, req.OrganizationID, week, out.ChargedMicrocredits)
	}
	if err != nil {
		return domain.AISettlement{}, aiSettlementWriteError(err)
	}
	// Complete only after every actual attempt is final. Unknown outcomes of
	// other retries retain their reservations and reconciliation status.
	out.OperationStatus, err = refreshAIOperationStatus(ctx, tx, operation, now)
	if err != nil {
		return domain.AISettlement{}, err
	}
	out.Applied = true
	return out, nil
}

func validAISettlement(req domain.AISettleRequest) bool {
	validText := func(s string, n int) bool {
		return utf8.ValidString(s) && !strings.ContainsRune(s, 0) && strings.TrimSpace(s) != "" && utf8.RuneCountInString(s) <= n
	}
	if req.AttemptID == uuid.Nil || !validText(req.ActualModel, 200) || !validText(req.UsageAdapterVersion, 100) || !validText(req.UsageSource, 100) || (req.ProviderRequestID != nil && !validText(*req.ProviderRequestID, 300)) {
		return false
	}
	for _, v := range []*int64{req.ReportedInputTokens, req.ReportedOutputTokens, req.ReportedTotalTokens} {
		if v != nil && *v < 0 {
			return false
		}
	}
	return true
}

func aiSettlementWriteError(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		if pg.Code == "22003" || (pg.Code == "23514" && (strings.HasPrefix(pg.ConstraintName, "organization_ai_") || strings.HasPrefix(pg.ConstraintName, "user_ai_"))) {
			return domain.ErrAICreditOverflow
		}
		if pg.Code == "23505" && pg.ConstraintName == "ai_usage_attempts_provider_request" {
			return domain.ErrAISettlementConflict
		}
	}
	return err
}

// Do not open new periods during completion. Recompute only original/later
// existing mirrors; never charge a late result to the current company/window.
func refreshAISettlementHolds(ctx context.Context, tx pgx.Tx, org uuid.UUID, user *int64, payer string, week uuid.UUID, five *uuid.UUID) error {
	_, err := tx.Exec(ctx, `WITH holds AS (SELECT src.starts_at,r.reserved_microcredits amount FROM ai_usage_reservations r JOIN ai_usage_attempts a ON a.id=r.attempt_id JOIN organization_ai_weekly_periods src ON src.id=a.company_period_id WHERE a.organization_id=$1 AND r.status='active') UPDATE organization_ai_weekly_periods p SET reserved_microcredits=COALESCE((SELECT SUM(amount) FROM holds WHERE starts_at<=p.starts_at),0)::bigint,carried_reserved_microcredits=COALESCE((SELECT SUM(amount) FROM holds WHERE starts_at<p.starts_at),0)::bigint WHERE p.organization_id=$1 AND p.starts_at>=(SELECT starts_at FROM organization_ai_weekly_periods WHERE id=$2)`, org, week)
	if err != nil {
		return err
	}
	if payer == "user" {
		_, err = tx.Exec(ctx, `WITH holds AS (SELECT src.starts_at,r.reserved_microcredits amount FROM ai_usage_reservations r JOIN ai_usage_attempts a ON a.id=r.attempt_id JOIN ai_usage_operations o ON o.id=a.operation_id JOIN organization_ai_weekly_periods src ON src.id=a.company_period_id WHERE o.organization_id=$1 AND o.user_id=$2 AND o.payer_kind='user' AND r.status='active') UPDATE organization_ai_user_weekly_usage u SET reserved_microcredits=COALESCE((SELECT SUM(amount) FROM holds WHERE starts_at<=p.starts_at),0)::bigint,carried_reserved_microcredits=COALESCE((SELECT SUM(amount) FROM holds WHERE starts_at<p.starts_at),0)::bigint FROM organization_ai_weekly_periods p WHERE u.organization_id=$1 AND u.user_id=$2 AND p.id=u.period_id AND p.starts_at>=(SELECT starts_at FROM organization_ai_weekly_periods WHERE id=$3)`, org, user, week)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE user_ai_five_hour_periods p
			SET reserved_microcredits=COALESCE((SELECT SUM(r.reserved_microcredits)
			 FROM ai_usage_reservations r JOIN ai_usage_attempts a ON a.id=r.attempt_id
			 WHERE a.five_hour_period_id=p.id AND r.status='active'),0)::bigint,
			 carried_reserved_microcredits=0 WHERE p.user_id=$1 AND p.id=$2`, user, five)
	} else {
		_, err = tx.Exec(ctx, `WITH holds AS (SELECT src.starts_at,r.reserved_microcredits amount FROM ai_usage_reservations r JOIN ai_usage_attempts a ON a.id=r.attempt_id JOIN ai_usage_operations o ON o.id=a.operation_id JOIN organization_ai_weekly_periods src ON src.id=a.company_period_id WHERE o.organization_id=$1 AND o.payer_kind='service' AND r.status='active') UPDATE organization_ai_service_weekly_usage u SET reserved_microcredits=COALESCE((SELECT SUM(amount) FROM holds WHERE starts_at<=p.starts_at),0)::bigint,carried_reserved_microcredits=COALESCE((SELECT SUM(amount) FROM holds WHERE starts_at<p.starts_at),0)::bigint FROM organization_ai_weekly_periods p WHERE u.organization_id=$1 AND p.id=u.period_id AND p.starts_at>=(SELECT starts_at FROM organization_ai_weekly_periods WHERE id=$2)`, org, week)
	}
	return err
}
