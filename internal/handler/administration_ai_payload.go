package handler

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func aiAmount(value int64) string { return strconv.FormatInt(value, 10) }
func aiOptionalAmount(value *int64) any {
	if value == nil {
		return nil
	}
	return aiAmount(*value)
}
func aiTime(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }

func aiPeriodResponse(window *domain.AIPeriodWindow, id *uuid.UUID) any {
	if window == nil {
		return nil
	}
	return gin.H{"id": id, "starts_at": aiTime(window.StartsAt), "ends_at": aiTime(window.EndsAt)}
}

func aiQuotaWindowResponse(w domain.AIQuotaWindow) gin.H {
	return gin.H{"state": w.State, "limit_microcredits": aiOptionalAmount(w.Limit), "used_microcredits": aiAmount(w.Used), "reserved_microcredits": aiAmount(w.Reserved), "carried_reserved_microcredits": aiAmount(w.CarriedReserved), "available_microcredits": aiOptionalAmount(w.Available), "remaining_percent": w.RemainingPercent, "period": aiPeriodResponse(w.Window, w.PeriodID)}
}

func aiUserQuotaResponse(item domain.AIEmployeeQuota, companyRevision int64, now time.Time) gin.H {
	return gin.H{"user_id": item.UserID, "full_name": item.FullName, "username": item.Username, "email": item.Email, "is_active": item.IsActive, "is_blocked": item.IsBlocked, "is_director": item.IsDirector, "configured": item.Configured, "revision": aiAmount(item.Revision), "company_revision": aiAmount(companyRevision), "weekly_quota_microcredits": aiOptionalAmount(item.WeeklyQuota), "five_hour_limit_microcredits": aiOptionalAmount(item.FiveHourLimit), "effective_five_hour_microcredits": aiOptionalAmount(item.EffectiveFiveHour), "committed_microcredits": aiAmount(item.Committed), "server_time": aiTime(now), "weekly": aiQuotaWindowResponse(item.Weekly), "five_hour": aiQuotaWindowResponse(item.FiveHour)}
}

func aiBudgetResponse(out *domain.AIBudgetSummary) gin.H {
	return gin.H{
		"organization_id": out.OrganizationID, "server_time": aiTime(out.ServerTime),
		// Paid provider paths enforce budgets. Task 40 will add shared mode selection.
		"enforcement_mode": aiSummaryEnforcementMode(out), "configured": out.Configured, "revision": aiAmount(out.Revision),
		"weekly_budget_microcredits": aiOptionalAmount(out.WeeklyBudget), "max_five_hour_microcredits": aiOptionalAmount(out.MaxFiveHour), "service_weekly_quota_microcredits": aiAmount(out.ServiceQuota),
		"period": aiPeriodResponse(out.Window, out.PeriodID), "employee_allocated_microcredits": aiAmount(out.EmployeeAllocated), "allocated_microcredits": aiAmount(out.Allocated), "committed_microcredits": aiAmount(out.Committed),
		"used_microcredits": aiAmount(out.Used), "reserved_microcredits": aiAmount(out.Reserved), "carried_reserved_microcredits": aiAmount(out.CarriedReserved), "unallocated_microcredits": aiOptionalAmount(out.Unallocated), "available_microcredits": aiOptionalAmount(out.Available), "overcommitted_microcredits": aiOptionalAmount(out.Overcommitted), "remaining_percent": out.RemainingPercent,
		"service":      gin.H{"used_microcredits": aiAmount(out.Service.Used), "reserved_microcredits": aiAmount(out.Service.Reserved), "carried_reserved_microcredits": aiAmount(out.Service.CarriedReserved), "available_microcredits": aiAmount(out.Service.Available)},
		"capabilities": gin.H{"set_company_budget": out.CanSetCompanyBudget, "set_service_quota": out.CanSetServiceQuota, "set_user_quotas": out.CanSetUserQuotas},
	}
}

func aiBlockersResponse(blockers []domain.AIBlocker) []gin.H {
	if len(blockers) == 0 {
		return []gin.H{}
	}
	out := make([]gin.H, len(blockers))
	for i, b := range blockers {
		var resetsAt any
		if b.ResetsAt != nil {
			resetsAt = aiTime(*b.ResetsAt)
		}
		out[i] = gin.H{
			"reason":    b.Reason,
			"scope":     b.Scope,
			"enforced":  b.Enforced,
			"resets_at": resetsAt,
		}
	}
	return out
}

func aiAccessResponse(access domain.AIAccessState) gin.H {
	var retryAt any
	if access.RetryAt != nil {
		retryAt = aiTime(*access.RetryAt)
	}
	var recheckAt any
	if access.RecheckAt != nil {
		recheckAt = aiTime(*access.RecheckAt)
	}
	return gin.H{
		"can_start_request": access.CanStartRequest,
		"blockers":          aiBlockersResponse(access.Blockers),
		"retry_at":          retryAt,
		"retry_kind":        access.RetryKind,
		"recheck_at":        recheckAt,
	}
}

func aiUserLimitsResponse(limits *domain.UserAILimits) gin.H {
	return gin.H{
		"server_time":           aiTime(limits.ServerTime),
		"organization_id":       limits.OrganizationID,
		"enforcement_mode":      limits.EnforcementMode,
		"credit_policy_version": limits.CreditPolicyVersion,
		"five_hour":             aiQuotaWindowResponse(limits.FiveHour),
		"weekly":                aiQuotaWindowResponse(limits.Weekly),
		"access":                aiAccessResponse(limits.Access),
	}
}

func aiSummaryEnforcementMode(out *domain.AIBudgetSummary) string {
	if out.EnforcementMode == domain.AIEnforcementModeObserve {
		return domain.AIEnforcementModeObserve
	}
	return domain.AIEnforcementModeEnforced
}
