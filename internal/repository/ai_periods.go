package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/west6ide/workspace-backend/internal/domain"
)

var ErrAIPeriodMembership = errors.New("AI period requires current active company membership")

type AIPeriod struct {
	ID uuid.UUID
	domain.AIPeriodWindow
}

type AIReservationPeriods struct {
	ObservedAt time.Time
	Weekly     AIPeriod
	UserWeekly *AIPeriod
	FiveHour   *AIPeriod // nil for the company service payer
}

type AIPeriodRepository struct{ now func() time.Time }

func NewAIPeriodRepository(now func() time.Time) *AIPeriodRepository {
	if now == nil {
		now = time.Now
	}
	return &AIPeriodRepository{now: now}
}

// OpenForReservation must run inside the caller's reservation transaction.
// It never commits: rejected/failed reservations must roll back newly opened
// anchors/windows too. Lock order is user (if any), then company budget.
// This is not an authorization or quota check and must not be used by GETs.
func (r *AIPeriodRepository) OpenForReservation(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID, userID *int64) (AIReservationPeriods, error) {
	if userID != nil {
		var member bool
		err := tx.QueryRow(ctx, `SELECT COALESCE(organization_id IS NOT DISTINCT FROM NULLIF($2::uuid,'00000000-0000-0000-0000-000000000000'::uuid) AND deleted_at IS NULL AND is_active,false) FROM users WHERE id=$1 FOR UPDATE`, *userID, organizationID).Scan(&member)
		if err != nil {
			return AIReservationPeriods{}, err
		}
		if !member {
			return AIReservationPeriods{}, ErrAIPeriodMembership
		}
	}
	now := r.now().UTC().Truncate(time.Microsecond)
	result := AIReservationPeriods{ObservedAt: now}
	var anchor time.Time
	var window domain.AIPeriodWindow
	if organizationID != uuid.Nil {
		var lockedID uuid.UUID
		err := tx.QueryRow(ctx, `SELECT organization_id FROM organization_ai_budgets WHERE organization_id=$1 FOR UPDATE`, organizationID).Scan(&lockedID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return AIReservationPeriods{}, err
		}
		if errors.Is(err, pgx.ErrNoRows) {
			if err = tx.QueryRow(ctx, `SELECT id FROM organizations WHERE id=$1 FOR UPDATE`, organizationID).Scan(&lockedID); err != nil {
				return AIReservationPeriods{}, err
			}
		}
		// Capture once after serialization, so lock waits cannot open stale windows.
		now = r.now().UTC().Truncate(time.Microsecond)
		if _, err := domain.AIPeriodAt(now, now, domain.AIWeeklyDuration); err != nil {
			return AIReservationPeriods{}, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO organization_ai_weekly_anchors(organization_id,anchor_at) VALUES($1,$2) ON CONFLICT (organization_id) DO NOTHING`, organizationID, now); err != nil {
			return AIReservationPeriods{}, err
		}
		if err := tx.QueryRow(ctx, `SELECT anchor_at FROM organization_ai_weekly_anchors WHERE organization_id=$1`, organizationID).Scan(&anchor); err != nil {
			return AIReservationPeriods{}, err
		}
		window, err = domain.AIPeriodAt(anchor, now, domain.AIWeeklyDuration)
		if err != nil {
			return AIReservationPeriods{}, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO organization_ai_weekly_periods(organization_id,anchor_at,starts_at,ends_at) VALUES($1,$2,$3,$4) ON CONFLICT (organization_id,starts_at) DO NOTHING`, organizationID, anchor, window.StartsAt, window.EndsAt); err != nil {
			return AIReservationPeriods{}, err
		}
		result = AIReservationPeriods{ObservedAt: now, Weekly: AIPeriod{AIPeriodWindow: window}}
		if err = tx.QueryRow(ctx, `SELECT id FROM organization_ai_weekly_periods WHERE organization_id=$1 AND starts_at=$2`, organizationID, window.StartsAt).Scan(&result.Weekly.ID); err != nil {
			return AIReservationPeriods{}, err
		}
	}
	var err error
	if userID == nil {
		return result, nil
	}
	if _, err = tx.Exec(ctx, `INSERT INTO user_ai_five_hour_anchors(user_id,anchor_at) VALUES($1,$2) ON CONFLICT (user_id) DO NOTHING`, *userID, now); err != nil {
		return AIReservationPeriods{}, err
	}
	if err = tx.QueryRow(ctx, `SELECT anchor_at FROM user_ai_five_hour_anchors WHERE user_id=$1`, *userID).Scan(&anchor); err != nil {
		return AIReservationPeriods{}, err
	}
	// Personal windows restart on the first accepted reservation after expiry.
	// Reuse a live persisted window; GETs never synthesize the next one.
	err = tx.QueryRow(ctx, `SELECT starts_at,ends_at FROM user_ai_five_hour_periods
		WHERE user_id=$1 AND starts_at<=$2 AND ends_at>$2 ORDER BY starts_at DESC LIMIT 1`,
		*userID, now).Scan(&window.StartsAt, &window.EndsAt)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return AIReservationPeriods{}, err
	}
	if errors.Is(err, pgx.ErrNoRows) {
		window = domain.AIPeriodWindow{StartsAt: now, EndsAt: now.Add(domain.AIFiveHourDuration)}
	}
	window.StartsAt, window.EndsAt = window.StartsAt.UTC(), window.EndsAt.UTC()
	if _, err = tx.Exec(ctx, `INSERT INTO user_ai_five_hour_periods(user_id,anchor_at,starts_at,ends_at) VALUES($1,$2,$3,$4) ON CONFLICT (user_id,starts_at) DO NOTHING`, *userID, anchor, window.StartsAt, window.EndsAt); err != nil {
		return AIReservationPeriods{}, err
	}
	result.FiveHour = &AIPeriod{AIPeriodWindow: window}
	if err = tx.QueryRow(ctx, `SELECT id FROM user_ai_five_hour_periods WHERE user_id=$1 AND starts_at=$2`, *userID, window.StartsAt).Scan(&result.FiveHour.ID); err != nil {
		return AIReservationPeriods{}, err
	}
	var personal domain.AIPeriodWindow
	err = tx.QueryRow(ctx, `SELECT starts_at,ends_at FROM ai_user_weekly_periods WHERE organization_id IS NOT DISTINCT FROM NULLIF($1::uuid,'00000000-0000-0000-0000-000000000000'::uuid) AND user_id=$2 AND starts_at<=$3 AND ends_at>$3 ORDER BY starts_at DESC LIMIT 1`, organizationID, *userID, now).Scan(&personal.StartsAt, &personal.EndsAt)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return AIReservationPeriods{}, err
	}
	if errors.Is(err, pgx.ErrNoRows) {
		personal = domain.AIPeriodWindow{StartsAt: now, EndsAt: now.Add(domain.AIWeeklyDuration)}
	}
	personal.StartsAt, personal.EndsAt = personal.StartsAt.UTC(), personal.EndsAt.UTC()
	result.UserWeekly = &AIPeriod{AIPeriodWindow: personal}
	err = tx.QueryRow(ctx, `SELECT id FROM ai_user_weekly_periods WHERE organization_id IS NOT DISTINCT FROM $1::uuid AND user_id=$2 AND starts_at=$3`, nullableAIUUID(organizationID), *userID, personal.StartsAt).Scan(&result.UserWeekly.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `INSERT INTO ai_user_weekly_periods(organization_id,user_id,starts_at,ends_at) VALUES($1,$2,$3,$4) RETURNING id`, nullableAIUUID(organizationID), *userID, personal.StartsAt, personal.EndsAt).Scan(&result.UserWeekly.ID)
	}
	if err != nil {
		return AIReservationPeriods{}, err
	}
	return result, nil
}
