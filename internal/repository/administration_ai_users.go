package repository

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func (r *OrganizationRepository) AdministrationAIUsers(ctx context.Context, actor int64, org *uuid.UUID, filter domain.AdministrationUserFilter) (*domain.AIEmployeeQuotaPage, error) {
	return r.administrationAIUsers(ctx, actor, org, filter, time.Now)
}

func (r *OrganizationRepository) administrationAIUsers(ctx context.Context, actor int64, org *uuid.UUID, filter domain.AdministrationUserFilter, clock func() time.Time) (*domain.AIEmployeeQuotaPage, error) {
	return r.readAdministrationAIUsers(ctx, actor, org, filter, clock, nil)
}

func (r *OrganizationRepository) AdministrationAIUser(ctx context.Context, actor, target int64, org *uuid.UUID) (*domain.AIEmployeeQuotaDetail, error) {
	if target <= 0 {
		return nil, apperrors.Validation("invalid user id")
	}
	page, err := r.readAdministrationAIUsers(ctx, actor, org, domain.AdministrationUserFilter{Limit: 1}, time.Now, &target)
	if err != nil {
		return nil, err
	}
	if len(page.Items) == 0 {
		return nil, apperrors.NotFound("employee")
	}
	return &domain.AIEmployeeQuotaDetail{Item: page.Items[0], CompanyRevision: page.CompanyRevision, ServerTime: page.ServerTime}, nil
}

func (r *OrganizationRepository) readAdministrationAIUsers(ctx context.Context, actor int64, org *uuid.UUID, filter domain.AdministrationUserFilter, clock func() time.Time, target *int64) (*domain.AIEmployeeQuotaPage, error) {
	query := strings.TrimSpace(filter.Query)
	if filter.Limit < 1 || filter.Limit > 100 || filter.Offset < 0 || filter.Offset > math.MaxInt32 || !utf8.ValidString(query) || utf8.RuneCountInString(query) > 255 {
		return nil, apperrors.Validation("invalid AI quota pagination")
	}
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	inner := &OrganizationRepository{db: administrationTx{tx}, logger: r.logger}
	scope, err := inner.accessScope(ctx, actor, org)
	if err != nil {
		return nil, err
	}
	out := &domain.AIEmployeeQuotaPage{Items: []domain.AIEmployeeQuota{}, Limit: filter.Limit, Offset: filter.Offset, ServerTime: clock().UTC().Truncate(time.Microsecond)}
	var maxFive *int64
	var anchor *time.Time
	err = tx.QueryRow(ctx, `SELECT COALESCE(b.revision,0),b.max_five_hour_microcredits,a.anchor_at FROM organizations o LEFT JOIN organization_ai_budgets b ON b.organization_id=o.id LEFT JOIN organization_ai_weekly_anchors a ON a.organization_id=o.id WHERE o.id=$1`, scope.ID).Scan(&out.CompanyRevision, &maxFive, &anchor)
	if err != nil {
		return nil, err
	}
	var weekStart, weekEnd *time.Time
	if anchor != nil {
		window, e := domain.AIPeriodAt(*anchor, out.ServerTime, domain.AIWeeklyDuration)
		if e != nil {
			return nil, e
		}
		out.Window = &window
		weekStart = &window.StartsAt
		weekEnd = &window.EndsAt
		err = tx.QueryRow(ctx, `SELECT id FROM organization_ai_weekly_periods WHERE organization_id=$1 AND starts_at=$2`, scope.ID, weekStart).Scan(&out.PeriodID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
	}
	const predicate = `u.organization_id=$1 AND u.deleted_at IS NULL AND ($2='' OR strpos(lower(concat_ws(' ',u.full_name_local,u.first_name,u.last_name,u.username,u.email)),lower($2))>0) AND ($3::bigint IS NULL OR u.id=$3)`
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM users u WHERE `+predicate, scope.ID, query, target).Scan(&out.Total); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT u.id,COALESCE(NULLIF(u.full_name_local,''),NULLIF(trim(concat_ws(' ',u.first_name,u.last_name)),''),u.username,''),COALESCE(u.username,''),COALESCE(u.email,''),u.is_active,EXISTS(SELECT 1 FROM organization_user_blocks b WHERE b.user_id=u.id),(o.owner_id=u.id) IS TRUE,COALESCE(q.revision,0),q.weekly_quota_microcredits,q.five_hour_limit_microcredits,a.anchor_at FROM users u JOIN organizations o ON o.id=u.organization_id LEFT JOIN organization_ai_user_quotas q ON q.organization_id=u.organization_id AND q.user_id=u.id LEFT JOIN LATERAL (SELECT starts_at AS anchor_at FROM user_ai_five_hour_periods WHERE user_id=u.id AND starts_at<=$6 AND ends_at>$6 ORDER BY starts_at DESC LIMIT 1) a ON true WHERE `+predicate+` ORDER BY u.id LIMIT $4 OFFSET $5`, scope.ID, query, target, filter.Limit, filter.Offset, out.ServerTime)
	if err != nil {
		return nil, err
	}
	type lookup struct {
		UserID   int64      `json:"user_id"`
		StartsAt *time.Time `json:"starts_at"`
		EndsAt   *time.Time `json:"ends_at"`
	}
	lookups := []lookup{}
	for rows.Next() {
		var item domain.AIEmployeeQuota
		var personalAnchor *time.Time
		if err = rows.Scan(&item.UserID, &item.FullName, &item.Username, &item.Email, &item.IsActive, &item.IsBlocked, &item.IsDirector, &item.Revision, &item.WeeklyQuota, &item.FiveHourLimit, &personalAnchor); err != nil {
			rows.Close()
			return nil, err
		}
		if item.WeeklyQuota == nil {
			q := domain.DefaultAIWeeklyMicrocredits
			item.WeeklyQuota = &q
		}
		if item.FiveHourLimit == nil {
			h := domain.DefaultAIFiveHourMicrocredits
			item.FiveHourLimit = &h
		}
		item.Configured = true
		effective := min(*item.FiveHourLimit, *item.WeeklyQuota)
		item.EffectiveFiveHour = &effective
		item.Weekly.Limit = item.WeeklyQuota
		item.Weekly.Window = out.Window
		item.Weekly.PeriodID = out.PeriodID
		item.FiveHour.Limit = item.EffectiveFiveHour
		lookup := lookup{UserID: item.UserID}
		if personalAnchor != nil {
			window := domain.AIPeriodWindow{StartsAt: personalAnchor.UTC(), EndsAt: personalAnchor.UTC().Add(domain.AIFiveHourDuration)}
			item.FiveHour.Window = &window
			lookup.StartsAt = &window.StartsAt
			lookup.EndsAt = &window.EndsAt
		}
		lookups = append(lookups, lookup)
		out.Items = append(out.Items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(out.Items) > 0 {
		data, e := json.Marshal(lookups)
		if e != nil {
			return nil, e
		}
		// Batch only the selected page. Weekly holds are company-scoped;
		// personal five-hour holds span all companies without exposing sources.
		const usageSQL = `WITH page AS (SELECT * FROM jsonb_to_recordset($2::jsonb) AS p(user_id bigint,starts_at timestamptz,ends_at timestamptz)), holds AS (
 SELECT p.user_id,
 COALESCE(SUM(r.reserved_microcredits) FILTER(WHERE o.organization_id=$1 AND ($4::timestamptz IS NULL OR w.starts_at<$4)),0)::bigint wr,
 COALESCE(SUM(r.reserved_microcredits) FILTER(WHERE o.organization_id=$1 AND ($3::timestamptz IS NULL OR w.starts_at<$3)),0)::bigint wc,
 COALESCE(SUM(r.reserved_microcredits) FILTER(WHERE h.starts_at=p.starts_at),0)::bigint hr,
 0::bigint hc
 FROM page p JOIN ai_usage_operations o ON o.user_id=p.user_id AND o.payer_kind='user'
 JOIN ai_usage_attempts a ON a.operation_id=o.id JOIN ai_usage_reservations r ON r.attempt_id=a.id AND r.status='active'
 JOIN organization_ai_weekly_periods w ON w.id=a.company_period_id JOIN user_ai_five_hour_periods h ON h.id=a.five_hour_period_id GROUP BY p.user_id)
 SELECT p.user_id,COALESCE(u.used_microcredits,0),COALESCE(s.wr,0),COALESCE(s.wc,0),h.id,COALESCE(h.used_microcredits,0),COALESCE(s.hr,0),COALESCE(s.hc,0)
 FROM page p LEFT JOIN organization_ai_weekly_periods w ON w.organization_id=$1 AND w.starts_at=$3
 LEFT JOIN organization_ai_user_weekly_usage u ON u.organization_id=$1 AND u.period_id=w.id AND u.user_id=p.user_id
 LEFT JOIN user_ai_five_hour_periods h ON h.user_id=p.user_id AND h.starts_at=p.starts_at LEFT JOIN holds s ON s.user_id=p.user_id ORDER BY p.user_id`
		usageRows, e := tx.Query(ctx, usageSQL, scope.ID, data, weekStart, weekEnd)
		if e != nil {
			return nil, e
		}
		index := 0
		for usageRows.Next() {
			item := &out.Items[index]
			var id int64
			if err = usageRows.Scan(&id, &item.Weekly.Used, &item.Weekly.Reserved, &item.Weekly.CarriedReserved, &item.FiveHour.PeriodID, &item.FiveHour.Used, &item.FiveHour.Reserved, &item.FiveHour.CarriedReserved); err != nil {
				usageRows.Close()
				return nil, err
			}
			if item.Weekly.Used > math.MaxInt64-item.Weekly.Reserved {
				usageRows.Close()
				return nil, domain.ErrAICreditOverflow
			}
			item.Committed = item.Weekly.Used + item.Weekly.Reserved
			if item.WeeklyQuota != nil && *item.WeeklyQuota > item.Committed {
				item.Committed = *item.WeeklyQuota
			}
			finishAIQuotaWindow(&item.Weekly)
			finishAIQuotaWindow(&item.FiveHour)
			index++
		}
		err = usageRows.Err()
		usageRows.Close()
		if err != nil {
			return nil, err
		}
	}
	if err = readAIPersonalEmployeeWeeks(ctx, tx, scope.ID, out.ServerTime, out.Items); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

func finishAIQuotaWindow(w *domain.AIQuotaWindow) {
	if w.Limit == nil {
		w.State = "unconfigured"
		return
	}
	available := aiBudgetRemaining(*w.Limit, w.Used, w.Reserved)
	w.Available = &available
	if *w.Limit == 0 {
		w.State = "disabled"
		return
	}
	percent := int(new(big.Int).Quo(new(big.Int).Mul(big.NewInt(available), big.NewInt(100)), big.NewInt(*w.Limit)).Int64())
	w.RemainingPercent = &percent
	w.State = "active"
	if available == 0 {
		w.State = "exhausted"
	}
}

func readAIPersonalEmployeeWeeks(ctx context.Context, tx pgx.Tx, org uuid.UUID, now time.Time, items []domain.AIEmployeeQuota) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]int64, len(items))
	index := map[int64]int{}
	for i := range items {
		ids[i] = items[i].UserID
		index[ids[i]] = i
	}
	rows, err := tx.Query(ctx, `SELECT x.user_id,p.id,p.starts_at,p.ends_at,COALESCE(p.used_microcredits,0),h.held,h.carried
 FROM unnest($2::bigint[])x(user_id)
 LEFT JOIN LATERAL(SELECT id,starts_at,ends_at,used_microcredits FROM ai_user_weekly_periods
 WHERE organization_id=$1 AND user_id=x.user_id AND starts_at<=$3 AND ends_at>$3 ORDER BY starts_at DESC LIMIT 1)p ON true
 CROSS JOIN LATERAL(SELECT COALESCE(sum(r.reserved_microcredits),0)::bigint held,
 COALESCE(sum(r.reserved_microcredits) FILTER(WHERE p.id IS NULL OR l.period_id IS DISTINCT FROM p.id),0)::bigint carried
 FROM ai_usage_reservations r JOIN ai_usage_attempts a ON a.id=r.attempt_id JOIN ai_usage_operations o ON o.id=a.operation_id
 LEFT JOIN ai_user_weekly_attempts l ON l.attempt_id=a.id WHERE o.organization_id=$1 AND o.user_id=x.user_id AND o.payer_kind='user' AND r.status='active')h`, org, ids, now)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var user int64
		var id *uuid.UUID
		var start, end *time.Time
		var used, held, carried int64
		if err = rows.Scan(&user, &id, &start, &end, &used, &held, &carried); err != nil {
			return err
		}
		w := &items[index[user]].Weekly
		w.PeriodID = id
		w.Window = nil
		w.Used, w.Reserved, w.CarriedReserved = used, held, carried
		if start != nil {
			w.Window = &domain.AIPeriodWindow{StartsAt: start.UTC(), EndsAt: end.UTC()}
		}
		finishAIQuotaWindow(w)
	}
	return rows.Err()
}
