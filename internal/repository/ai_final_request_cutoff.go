package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/west6ide/workspace-backend/internal/domain"
)

type AIFinalQuotaCutoff struct {
	MessageID    string
	Microcredits int64
	ResetsAt     time.Time
}

// CompleteFinalQuotaTurn accepts a terminal chat turn and consumes its remaining
// personal allowance. This is an explicit product debit, not invented LLM usage.
// The assistant notice, debit and both personal counters commit together.
func (r *AIRepository) CompleteFinalQuotaTurn(ctx context.Context, caller domain.AIPaidCaller, chat, turn, scope, content string, expectedAvailable int64, allowExhausted bool, tokens int) (*AIFinalQuotaCutoff, error) {
	if caller.Validate() != nil || caller.IsService || expectedAvailable <= 0 || (scope != "weekly" && scope != "five_hour") || tokens < 0 {
		return nil, domain.ErrAIReservationInvalid
	}
	if _, err := uuid.Parse(chat); err != nil {
		return nil, domain.ErrAIReservationInvalid
	}
	if _, err := uuid.Parse(turn); err != nil {
		return nil, domain.ErrAIReservationInvalid
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if caller.OrganizationID != uuid.Nil {
		var status string
		if err = tx.QueryRow(ctx, `SELECT status FROM organizations WHERE id=$1 FOR UPDATE`, caller.OrganizationID).Scan(&status); err != nil {
			return nil, err
		}
		if status != "active" {
			return nil, nil
		}
	}
	var active bool
	if err = tx.QueryRow(ctx, `SELECT is_active AND deleted_at IS NULL AND organization_id IS NOT DISTINCT FROM $2::uuid FROM users WHERE id=$1 FOR UPDATE`, *caller.UserID, nullableAIUUID(caller.OrganizationID)).Scan(&active); err != nil {
		return nil, err
	}
	if !active {
		return nil, nil
	}
	var owned bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM ai_messages m JOIN ai_conversations c ON c.id=m.conversation_id WHERE m.id=$1 AND c.id=$2 AND c.user_id=$3 AND m.role='user')`, turn, chat, *caller.UserID).Scan(&owned); err != nil {
		return nil, err
	}
	if !owned {
		return nil, domain.ErrAIPaidCallInvalidCaller
	}
	var previous AIFinalQuotaCutoff
	err = tx.QueryRow(ctx, `SELECT d.assistant_message_id,d.microcredits,CASE WHEN scope='weekly' THEN w.ends_at ELSE h.ends_at END FROM ai_final_request_cutoffs d JOIN ai_user_weekly_periods w ON w.id=d.weekly_period_id JOIN user_ai_five_hour_periods h ON h.id=d.five_hour_period_id WHERE d.user_id=$1 AND d.source_message_id=$2`, *caller.UserID, turn).Scan(&previous.MessageID, &previous.Microcredits, &previous.ResetsAt)
	if err == nil {
		return &previous, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	q, h, err := readAIPersonalQuota(ctx, tx, caller.OrganizationID, *caller.UserID)
	if err != nil {
		return nil, err
	}
	h = min(h, q)
	if q == 0 || h == 0 {
		return nil, nil
	}
	periods, err := NewAIPeriodRepository(nil).OpenForReservation(ctx, tx, caller.OrganizationID, caller.UserID)
	if err != nil {
		return nil, err
	}
	// Old five-hour holds still protect weekly capacity, but must not prevent
	// exhaustion of a fresh five-hour allowance using its unreserved balance.
	var weeklyHeld, fiveHeld int64
	if err = tx.QueryRow(ctx, `SELECT
 COALESCE(sum(r.reserved_microcredits) FILTER (WHERE o.organization_id IS NOT DISTINCT FROM $2::uuid),0)::bigint,
 COALESCE(sum(r.reserved_microcredits) FILTER (WHERE a.five_hour_period_id=$3),0)::bigint
 FROM ai_usage_reservations r JOIN ai_usage_attempts a ON a.id=r.attempt_id JOIN ai_usage_operations o ON o.id=a.operation_id
 WHERE o.user_id=$1 AND o.payer_kind='user' AND r.status='active'`, *caller.UserID, nullableAIUUID(caller.OrganizationID), periods.FiveHour.ID).Scan(&weeklyHeld, &fiveHeld); err != nil {
		return nil, err
	}
	// Never label money still reserved in the exhausted window as spent.
	if fiveHeld != 0 || (scope == "weekly" && weeklyHeld != 0) {
		return nil, nil
	}
	var weeklyUsed, fiveUsed int64
	if err = tx.QueryRow(ctx, `SELECT used_microcredits FROM ai_user_weekly_periods WHERE id=$1`, periods.UserWeekly.ID).Scan(&weeklyUsed); err != nil {
		return nil, err
	}
	if err = tx.QueryRow(ctx, `SELECT used_microcredits FROM user_ai_five_hour_periods WHERE id=$1`, periods.FiveHour.ID).Scan(&fiveUsed); err != nil {
		return nil, err
	}
	weekly, five := aiBudgetRemaining(q, weeklyUsed, weeklyHeld), aiBudgetRemaining(h, fiveUsed, fiveHeld)
	amount := min(weekly, five)
	// A reset or quota increase invalidates the old admission decision. Never
	// burn a newly replenished allowance using a stale denial from an old window.
	if amount > expectedAvailable || (amount == 0 && !allowExhausted) {
		return nil, nil
	}
	if scope == "weekly" && weekly > five || scope == "five_hour" && five > weekly {
		return nil, nil
	}
	result := &AIFinalQuotaCutoff{Microcredits: amount, ResetsAt: periods.FiveHour.EndsAt}
	if scope == "weekly" {
		result.ResetsAt = periods.UserWeekly.EndsAt
	}
	if err = tx.QueryRow(ctx, `INSERT INTO ai_messages(conversation_id,role,content,tokens_usage) SELECT id,'assistant',$3,$4 FROM ai_conversations WHERE id=$1 AND user_id=$2 RETURNING id`, chat, *caller.UserID, content, tokens).Scan(&result.MessageID); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO ai_final_request_cutoffs(user_id,organization_id,source_message_id,conversation_id,assistant_message_id,weekly_period_id,five_hour_period_id,microcredits,scope) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, *caller.UserID, nullableAIUUID(caller.OrganizationID), turn, chat, result.MessageID, periods.UserWeekly.ID, periods.FiveHour.ID, amount, scope); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE ai_user_weekly_periods SET used_microcredits=used_microcredits+$2 WHERE id=$1`, periods.UserWeekly.ID, amount); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE user_ai_five_hour_periods SET used_microcredits=used_microcredits+$2 WHERE id=$1`, periods.FiveHour.ID, amount); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE ai_conversations SET updated_at=now() WHERE id=$1`, chat); err != nil {
		return nil, err
	}
	return result, tx.Commit(ctx)
}
