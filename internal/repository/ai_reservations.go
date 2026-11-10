package repository

import (
	"bytes"
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/west6ide/workspace-backend/internal/domain"
)

type AIReservationRepository struct {
	policyMu             sync.RWMutex
	db                   *pgxpool.Pool
	periods              *AIPeriodRepository
	userConcurrencyLimit int
	accountingEnabled    bool
	enforcementMode      string
	quotaTestSubjects    domain.AIQuotaTestSubjects
}

type AIReservationOption func(*AIReservationRepository)

// WithAIQuotaTestSubjects is server-owned configuration, never request data.
func WithAIQuotaTestSubjects(subjects domain.AIQuotaTestSubjects) AIReservationOption {
	copyOfSubjects := append(domain.AIQuotaTestSubjects(nil), subjects...)
	return func(r *AIReservationRepository) { r.quotaTestSubjects = copyOfSubjects }
}

// WithAIUserConcurrencyLimit installs the server policy. The durable journal,
// not the expiring Redis lease, owns capacity for sent/unknown provider calls.
func WithAIUserConcurrencyLimit(limit int) AIReservationOption {
	return func(r *AIReservationRepository) { r.userConcurrencyLimit = limit }
}

func WithAIAccountingEnabled(enabled bool) AIReservationOption {
	return func(r *AIReservationRepository) { r.SetAccountingEnabled(enabled) }
}

func WithAIEnforcementMode(mode string) AIReservationOption {
	return func(r *AIReservationRepository) { r.SetEnforcementMode(mode) }
}

// Server-owned configuration only. Each admission uses one coherent snapshot.
func (r *AIReservationRepository) SetEnforcementMode(mode string) {
	r.policyMu.Lock()
	defer r.policyMu.Unlock()
	r.enforcementMode = domain.AIEnforcementModeEnforced
	if mode == domain.AIEnforcementModeObserve {
		r.enforcementMode = mode
	}
}

func (r *AIReservationRepository) SetAccountingEnabled(enabled bool) {
	r.policyMu.Lock()
	defer r.policyMu.Unlock()
	r.accountingEnabled = enabled
}

func NewAIReservationRepository(db *pgxpool.Pool, clock func() time.Time, options ...AIReservationOption) *AIReservationRepository {
	r := &AIReservationRepository{
		db:                db,
		periods:           NewAIPeriodRepository(clock),
		accountingEnabled: true,
		enforcementMode:   domain.AIEnforcementModeEnforced,
	}
	for _, option := range options {
		if option != nil {
			option(r)
		}
	}
	return r
}

// Reserve commits before returning permission to send. Lock order shared with
// administration: organization -> user -> budget. Under these locks the journal
// replay, quota and periods are accessed; none acquire another owner lock.
// Lifecycle updates take user -> sorted budgets, never organization afterwards.
// No provider/network call may execute inside this transaction.
func (r *AIReservationRepository) Reserve(ctx context.Context, req domain.AIReserveRequest) (domain.AIReservation, error) {
	r.policyMu.RLock()
	accountingEnabled, mode := r.accountingEnabled, r.enforcementMode
	r.policyMu.RUnlock()
	if !accountingEnabled {
		return domain.AIReservation{}, domain.ErrAIAccountingDisabled
	}
	if !validAIReserveRequest(req) {
		return domain.AIReservation{}, domain.ErrAIReservationInvalid
	}
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return domain.AIReservation{}, err
	}
	defer tx.Rollback(ctx)
	if req.OrganizationID != uuid.Nil {
		var status string
		err = tx.QueryRow(ctx, `SELECT status FROM organizations WHERE id=$1 FOR UPDATE`, req.OrganizationID).Scan(&status)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.AIReservation{}, reservationDenied("AI_ACCESS_DENIED", "account", nil)
		}
		if err != nil {
			return domain.AIReservation{}, err
		}
		if status != "active" {
			return domain.AIReservation{}, reservationDenied("COMPANY_SUSPENDED", "company", nil)
		}
	}
	if req.UserID != nil {
		var member bool
		err = tx.QueryRow(ctx, `SELECT COALESCE(organization_id IS NOT DISTINCT FROM NULLIF($2::uuid,'00000000-0000-0000-0000-000000000000'::uuid) AND deleted_at IS NULL AND is_active,false) FROM users WHERE id=$1 FOR UPDATE`, *req.UserID, req.OrganizationID).Scan(&member)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && !member) {
			return domain.AIReservation{}, reservationDenied("AI_ACCESS_DENIED", "account", nil)
		}
		if err != nil {
			return domain.AIReservation{}, err
		}
	}
	isObserve := mode == domain.AIEnforcementModeObserve || r.quotaTestSubjects.Allows(req.OrganizationID, req.UserID)
	var budget, maxFive int64
	var hasBudget bool
	err = tx.QueryRow(ctx, `SELECT weekly_budget_microcredits,max_five_hour_microcredits FROM organization_ai_budgets WHERE organization_id=$1 FOR UPDATE`, req.OrganizationID).Scan(&budget, &maxFive)
	if err == nil {
		hasBudget = true
	} else if errors.Is(err, pgx.ErrNoRows) {
		if !isObserve && req.UserID == nil {
			return domain.AIReservation{}, reservationDenied("COMPANY_BUDGET_UNCONFIGURED", "company", nil)
		}
	} else {
		return domain.AIReservation{}, err
	}
	// Check replays before remaining capacity: a successful reservation can have
	// consumed the last credit. Existing operations never grant permission twice.
	op, replay, found, err := replayAIReservation(ctx, tx, req)
	if err != nil {
		return domain.AIReservation{}, err
	}
	if found {
		if err = tx.Commit(ctx); err != nil {
			return domain.AIReservation{}, err
		}
		return replay, nil
	}
	if req.UserID != nil && r.userConcurrencyLimit > 0 {
		// The user row is already locked: concurrent API instances cannot both
		// pass this check. Pending outcomes protect recent uncertain execution.
		// Manual reconciliation is a billing task, not a running generation:
		// keep its financial hold, but do not permanently occupy execution slots.
		var active, unknown int
		err = tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE a.status='unknown') FROM ai_usage_reservations r
			JOIN ai_usage_attempts a ON a.id=r.attempt_id
			JOIN ai_usage_operations o ON o.id=a.operation_id
			WHERE o.payer_kind='user' AND o.user_id=$1 AND r.status='active'
			AND NOT (a.status='unknown' AND EXISTS (
				SELECT 1 FROM ai_usage_reconciliation_jobs j WHERE j.attempt_id=a.id AND j.status='manual'
			))`, *req.UserID).Scan(&active, &unknown)
		if err != nil {
			return domain.AIReservation{}, err
		}
		if active >= r.userConcurrencyLimit {
			if active-unknown < r.userConcurrencyLimit {
				return domain.AIReservation{}, reservationDenied("RESERVATION_PENDING", "account", nil)
			}
			return domain.AIReservation{}, domain.ErrAIConcurrencyLimitExceeded
		}
	}
	rates, err := domain.AICreditRatesForModel(req.Model)
	if err != nil {
		return domain.AIReservation{}, err
	}
	if hasBudget && budget == 0 && !isObserve && req.UserID == nil {
		return domain.AIReservation{}, reservationDenied("COMPANY_BUDGET_DISABLED", "company", nil)
	}
	quota, fiveLimit := int64(0), int64(0)
	hasQuota := false // Background operations use only the common company budget.
	if req.UserID != nil {
		q, h, quotaErr := readAIPersonalQuota(ctx, tx, req.OrganizationID, *req.UserID)
		if quotaErr != nil {
			return domain.AIReservation{}, quotaErr
		}
		hasQuota = true
		quota, fiveLimit = q, min(h, q)
		if hasQuota {
			if quota == 0 && !isObserve {
				return domain.AIReservation{}, reservationDenied("USER_QUOTA_DISABLED", "weekly", nil)
			}
			if fiveLimit == 0 && !isObserve {
				return domain.AIReservation{}, reservationDenied("FIVE_HOUR_DISABLED", "five_hour", nil)
			}
		}
	}
	windows, err := r.periods.OpenForReservation(ctx, tx, req.OrganizationID, req.UserID)
	if err != nil {
		return domain.AIReservation{}, err
	}
	var companyUsed, used int64
	err = tx.QueryRow(ctx, `SELECT COALESCE((SELECT used_microcredits FROM organization_ai_weekly_periods WHERE id=$1),0)`, windows.Weekly.ID).Scan(&companyUsed)
	if err != nil {
		return domain.AIReservation{}, err
	}
	payer := "service"
	if req.UserID != nil {
		payer = "user"
		if req.OrganizationID != uuid.Nil {
			_, err = tx.Exec(ctx, `INSERT INTO organization_ai_user_weekly_usage(organization_id,period_id,user_id) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, req.OrganizationID, windows.Weekly.ID, *req.UserID)
		}
		if err == nil {
			err = tx.QueryRow(ctx, `SELECT used_microcredits FROM ai_user_weekly_periods WHERE organization_id IS NOT DISTINCT FROM NULLIF($1::uuid,'00000000-0000-0000-0000-000000000000'::uuid) AND id=$2 AND user_id=$3`, req.OrganizationID, windows.UserWeekly.ID, *req.UserID).Scan(&used)
		}
	} else {
		_, err = tx.Exec(ctx, `INSERT INTO organization_ai_service_weekly_usage(organization_id,period_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, req.OrganizationID, windows.Weekly.ID)
		if err == nil {
			err = tx.QueryRow(ctx, `SELECT used_microcredits FROM organization_ai_service_weekly_usage WHERE organization_id=$1 AND period_id=$2`, req.OrganizationID, windows.Weekly.ID).Scan(&used)
		}
	}
	if err != nil {
		return domain.AIReservation{}, err
	}
	// Journal rows are authoritative; do not sum mirrored period counters again.
	var companyHeld, companyCarried, held, carried int64
	err = tx.QueryRow(ctx, `SELECT COALESCE(SUM(r.reserved_microcredits),0)::bigint,
 COALESCE(SUM(r.reserved_microcredits) FILTER(WHERE a.company_period_id<>$2),0)::bigint,
 COALESCE(SUM(r.reserved_microcredits) FILTER(WHERE o.payer_kind=$3 AND ($3='service' OR o.user_id=$4)),0)::bigint,
 COALESCE(SUM(r.reserved_microcredits) FILTER(WHERE o.payer_kind=$3 AND ($3='service' OR o.user_id=$4) AND a.company_period_id<>$2),0)::bigint
 FROM ai_usage_reservations r JOIN ai_usage_attempts a ON a.id=r.attempt_id JOIN ai_usage_operations o ON o.id=a.operation_id
 WHERE o.organization_id IS NOT DISTINCT FROM NULLIF($1::uuid,'00000000-0000-0000-0000-000000000000'::uuid) AND r.status='active'`, req.OrganizationID, windows.Weekly.ID, payer, req.UserID).Scan(&companyHeld, &companyCarried, &held, &carried)
	if err != nil {
		return domain.AIReservation{}, err
	}
	var fiveHeld, fiveCarried, fiveUsed int64
	var fiveID *uuid.UUID
	if windows.FiveHour != nil {
		fiveID = &windows.FiveHour.ID
		err = tx.QueryRow(ctx, `SELECT used_microcredits FROM user_ai_five_hour_periods WHERE id=$1`, fiveID).Scan(&fiveUsed)
		if err != nil {
			return domain.AIReservation{}, err
		}
		err = tx.QueryRow(ctx, `SELECT COALESCE(SUM(r.reserved_microcredits),0)::bigint,0::bigint FROM ai_usage_reservations r JOIN ai_usage_attempts a ON a.id=r.attempt_id JOIN ai_usage_operations o ON o.id=a.operation_id WHERE o.payer_kind='user' AND o.user_id=$1 AND r.status='active' AND a.five_hour_period_id=$2`, *req.UserID, fiveID).Scan(&fiveHeld, &fiveCarried)
		if err != nil {
			return domain.AIReservation{}, err
		}
	}
	if !isObserve {
		gates := []aiReservationCapacity{}
		if hasBudget && budget > 0 && req.UserID == nil {
			gates = append(gates, aiReservationCapacity{"COMPANY_WEEKLY_EXHAUSTED", "company", &windows.Weekly.EndsAt, budget, companyUsed, companyHeld})
		}
		if hasQuota && quota > 0 {
			gates = append(gates, aiReservationCapacity{"USER_WEEKLY_EXHAUSTED", "weekly", &windows.UserWeekly.EndsAt, quota, used, held})
		}
		if hasQuota && fiveLimit > 0 && windows.FiveHour != nil {
			gates = append(gates, aiReservationCapacity{"FIVE_HOUR_EXHAUSTED", "five_hour", &windows.FiveHour.EndsAt, fiveLimit, fiveUsed, fiveHeld})
		}
		if denied := limitingAIReservationCapacity(req.Microcredits, gates); denied != nil {
			return domain.AIReservation{}, denied
		}
	}
	// Observe lifts financial limits, never the integer/storage bounds.
	for _, totals := range [][2]int64{{companyUsed, companyHeld}, {used, held}, {fiveUsed, fiveHeld}} {
		if req.Microcredits > math.MaxInt64-totals[1] || totals[0] > math.MaxInt64-totals[1]-req.Microcredits {
			return domain.AIReservation{}, domain.ErrAICreditOverflow
		}
	}
	out := domain.AIReservation{OperationID: op, AttemptID: uuid.New(), ReservationID: uuid.New(), CompanyPeriodID: windows.Weekly.ID, FiveHourPeriodID: fiveID, Microcredits: req.Microcredits, AttemptStatus: "reserved", ReservationStatus: "active", AttemptNumber: req.AttemptNumber, Disposition: domain.AIOperationCreated, Created: true}
	if op == uuid.Nil {
		out.OperationID = uuid.New()
		_, err = tx.Exec(ctx, `INSERT INTO ai_usage_operations(id,organization_id,user_id,payer_kind,operation_kind,idempotency_key,request_fingerprint,source_kind,source_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, out.OperationID, nullableAIUUID(req.OrganizationID), req.UserID, payer, req.OperationKind, req.IdempotencyKey, req.Fingerprint[:], req.SourceKind, req.SourceID)
		if err != nil {
			return domain.AIReservation{}, err
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO ai_usage_attempts(id,operation_id,organization_id,attempt_number,provider_id,endpoint_kind,requested_model,credit_policy_version,usage_adapter_version,input_weight_microcredits,output_weight_microcredits,cached_input_weight_microcredits,reasoning_output_weight_microcredits,company_period_id,five_hour_period_id,request_fingerprint) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`, out.AttemptID, out.OperationID, nullableAIUUID(req.OrganizationID), req.AttemptNumber, req.Model.ProviderID, req.Model.EndpointKind, req.Model.ModelID, req.Model.PolicyVersion, req.UsageAdapterVersion, rates.Input, nullableAIWeight(rates.Output), nullableAIWeight(rates.Cached), nullableAIWeight(rates.Reasoning), nullableAIUUID(out.CompanyPeriodID), fiveID, req.AttemptFingerprint[:])
	if err != nil {
		return domain.AIReservation{}, err
	}
	if windows.UserWeekly != nil {
		_, err = tx.Exec(ctx, `INSERT INTO ai_user_weekly_attempts(attempt_id,period_id) VALUES($1,$2)`, out.AttemptID, windows.UserWeekly.ID)
		if err != nil {
			return domain.AIReservation{}, err
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO ai_usage_reservations(id,attempt_id,reserved_microcredits) VALUES($1,$2,$3)`, out.ReservationID, out.AttemptID, req.Microcredits)
	if err != nil {
		return domain.AIReservation{}, err
	}
	if isObserve {
		scopes := []aiBudgetObservation{{scope: "company", configured: hasBudget, limit: budget, used: companyUsed, held: companyHeld}}
		if req.UserID != nil {
			scopes = append(scopes, aiBudgetObservation{scope: "weekly", configured: hasQuota, limit: quota, used: used, held: held}, aiBudgetObservation{scope: "five_hour", configured: hasQuota, limit: fiveLimit, used: fiveUsed, held: fiveHeld})
		}
		for _, scope := range scopes {
			if err = recordAIBudgetObservation(ctx, tx, out.AttemptID, req.Microcredits, scope); err != nil {
				return domain.AIReservation{}, err
			}
		}
	}
	// Storage overflow is a data-integrity error even when financial enforcement is off.
	_, err = tx.Exec(ctx, `UPDATE organization_ai_weekly_periods SET reserved_microcredits=$2,carried_reserved_microcredits=$3 WHERE id=$1`, windows.Weekly.ID, companyHeld+req.Microcredits, companyCarried)
	if err != nil {
		return domain.AIReservation{}, err
	}
	if req.UserID != nil {
		_, err = tx.Exec(ctx, `UPDATE organization_ai_user_weekly_usage SET reserved_microcredits=$4,carried_reserved_microcredits=$5 WHERE organization_id=$1 AND period_id=$2 AND user_id=$3`, req.OrganizationID, windows.Weekly.ID, *req.UserID, held+req.Microcredits, carried)
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE user_ai_five_hour_periods SET reserved_microcredits=$2,carried_reserved_microcredits=$3 WHERE id=$1`, fiveID, fiveHeld+req.Microcredits, fiveCarried)
		}
	} else {
		_, err = tx.Exec(ctx, `UPDATE organization_ai_service_weekly_usage SET reserved_microcredits=$3,carried_reserved_microcredits=$4 WHERE organization_id=$1 AND period_id=$2`, req.OrganizationID, windows.Weekly.ID, held+req.Microcredits, carried)
	}
	if err != nil {
		return domain.AIReservation{}, err
	}
	if err = tx.QueryRow(ctx, `SELECT status FROM ai_usage_operations WHERE id=$1`, out.OperationID).Scan(&out.OperationStatus); err != nil {
		return domain.AIReservation{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.AIReservation{}, err
	}
	return out, nil
}

func reservationDenied(reason, scope string, at *time.Time) error {
	return &domain.AIReservationDenied{Reason: reason, Scope: scope, RecheckAt: at}
}

func reservationCapacityDenied(reason, scope string, at *time.Time, amount, limit, used, held int64) error {
	available := aiBudgetRemaining(limit, used, held)
	// Exhaustion means actual spending reached the limit. Live holds and a
	// request larger than a positive remainder are separate, recoverable cases.
	if used >= limit {
		// Keep the window's exhaustion reason and accounting boundary.
	} else if held > 0 && (available == 0 || amount <= aiBudgetRemaining(limit, used, 0)) {
		reason, at = "RESERVATION_PENDING", nil
	} else {
		reason, at = "RESERVATION_TOO_LARGE", nil
	}
	return &domain.AIReservationDenied{Reason: reason, Scope: scope, RecheckAt: at, AvailableMicrocredits: &available}
}

type aiReservationCapacity struct {
	reason, scope     string
	reset             *time.Time
	limit, used, held int64
}

func limitingAIReservationCapacity(amount int64, gates []aiReservationCapacity) error {
	var selected *aiReservationCapacity
	var smallest int64
	for i := range gates {
		gate := &gates[i]
		available := aiBudgetRemaining(gate.limit, gate.used, gate.held)
		if amount <= available {
			continue
		}
		// Read every applicable window before choosing a reason. Otherwise a
		// large estimate can blame the week even when five hours is the bottleneck.
		if selected == nil || available < smallest || (available == smallest && gate.reset != nil && (selected.reset == nil || gate.reset.After(*selected.reset))) {
			selected, smallest = gate, available
		}
	}
	if selected == nil {
		return nil
	}
	return reservationCapacityDenied(selected.reason, selected.scope, selected.reset, amount, selected.limit, selected.used, selected.held)
}

func nullableAIWeight(value int64) *int64 {
	if value == 0 {
		return nil
	}
	return &value
}

func validAIReserveRequest(req domain.AIReserveRequest) bool {
	validText := func(s string, max int) bool {
		return utf8.ValidString(s) && !strings.ContainsRune(s, 0) && strings.TrimSpace(s) != "" && utf8.RuneCountInString(s) <= max
	}
	return (req.OrganizationID != uuid.Nil || req.UserID != nil) && (req.UserID == nil || *req.UserID > 0) && req.Fingerprint != ([32]byte{}) && req.AttemptFingerprint != ([32]byte{}) && req.Microcredits > 0 && req.Microcredits <= domain.MaxAIBudgetMicrocredits && req.AttemptNumber > 0 && validText(req.OperationKind, 100) && validText(req.IdempotencyKey, 200) && validText(req.UsageAdapterVersion, 100) && (req.SourceKind == nil) == (req.SourceID == nil) && (req.SourceKind == nil || (validText(*req.SourceKind, 100) && *req.SourceID != uuid.Nil))
}

func replayAIReservation(ctx context.Context, tx pgx.Tx, req domain.AIReserveRequest) (uuid.UUID, domain.AIReservation, bool, error) {
	var op, org uuid.UUID
	var fingerprint []byte
	var kind, status string
	var sourceKind *string
	var sourceID *uuid.UUID
	err := tx.QueryRow(ctx, `SELECT id,COALESCE(organization_id,'00000000-0000-0000-0000-000000000000'::uuid),request_fingerprint,operation_kind,status,source_kind,source_id FROM ai_usage_operations WHERE (payer_kind='user' AND user_id=$1 AND idempotency_key=$3) OR (payer_kind='service' AND $1::bigint IS NULL AND organization_id=$2 AND idempotency_key=$3) FOR UPDATE`, req.UserID, req.OrganizationID, req.IdempotencyKey).Scan(&op, &org, &fingerprint, &kind, &status, &sourceKind, &sourceID)
	if errors.Is(err, pgx.ErrNoRows) {
		if req.AttemptNumber != 1 {
			return uuid.Nil, domain.AIReservation{}, false, domain.ErrAIReservationConflict
		}
		return uuid.Nil, domain.AIReservation{}, false, nil
	}
	if err != nil {
		return uuid.Nil, domain.AIReservation{}, false, err
	}
	if org != req.OrganizationID || !bytes.Equal(fingerprint, req.Fingerprint[:]) || kind != req.OperationKind || !sameAIOptional(sourceKind, req.SourceKind) || !sameAIOptional(sourceID, req.SourceID) {
		return uuid.Nil, domain.AIReservation{}, false, domain.ErrAIReservationConflict
	}
	out := domain.AIReservation{OperationID: op}
	var key domain.AICreditModelKey
	var adapter string
	var attemptFingerprint []byte
	var charged *int64
	err = tx.QueryRow(ctx, `SELECT a.id,r.id,COALESCE(a.company_period_id,'00000000-0000-0000-0000-000000000000'::uuid),a.five_hour_period_id,r.reserved_microcredits,a.status,r.status,a.provider_id,a.endpoint_kind,a.requested_model,a.credit_policy_version,a.usage_adapter_version,a.request_fingerprint,a.charged_microcredits FROM ai_usage_attempts a JOIN ai_usage_reservations r ON r.attempt_id=a.id WHERE a.operation_id=$1 AND a.attempt_number=$2`, op, req.AttemptNumber).Scan(&out.AttemptID, &out.ReservationID, &out.CompanyPeriodID, &out.FiveHourPeriodID, &out.Microcredits, &out.AttemptStatus, &out.ReservationStatus, &key.ProviderID, &key.EndpointKind, &key.ModelID, &key.PolicyVersion, &adapter, &attemptFingerprint, &charged)
	if errors.Is(err, pgx.ErrNoRows) {
		var last int32
		if err = tx.QueryRow(ctx, `SELECT COALESCE(MAX(attempt_number),0) FROM ai_usage_attempts WHERE operation_id=$1`, op).Scan(&last); err != nil {
			return uuid.Nil, domain.AIReservation{}, false, err
		}
		if (status != "pending" && status != "running") || req.AttemptNumber != last+1 {
			return uuid.Nil, domain.AIReservation{}, false, domain.ErrAIReservationConflict
		}
		return op, domain.AIReservation{}, false, nil
	}
	if err != nil {
		return uuid.Nil, domain.AIReservation{}, false, err
	}
	if key != req.Model || adapter != req.UsageAdapterVersion || out.Microcredits != req.Microcredits || !bytes.Equal(attemptFingerprint, req.AttemptFingerprint[:]) {
		return uuid.Nil, domain.AIReservation{}, false, domain.ErrAIReservationConflict
	}
	out.AttemptNumber = req.AttemptNumber
	fillAIReplay(&out, status, charged)
	return op, out, true, nil
}

func sameAIOptional[T comparable](a, b *T) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}
