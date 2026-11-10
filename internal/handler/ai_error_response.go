package handler

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/alem"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/i18n"
	"github.com/west6ide/workspace-backend/internal/middleware"
)

// IsAICallError reports whether err is an AI accounting, budget, rate-limit, or provider error
// that should be translated into the task 04 unified AI error response format.
func IsAICallError(err error) bool {
	if err == nil {
		return false
	}
	var denied *domain.AIReservationDenied
	if errors.As(err, &denied) {
		return true
	}
	if errors.Is(err, domain.ErrAIPaidCallMissingContext) ||
		errors.Is(err, domain.ErrAIPaidCallInvalidCaller) ||
		errors.Is(err, domain.ErrAIPaidCallContextMismatch) ||
		errors.Is(err, domain.ErrAIRateLimitExceeded) ||
		errors.Is(err, domain.ErrAIConcurrencyLimitExceeded) ||
		errors.Is(err, domain.ErrAILimiterUnavailable) ||
		errors.Is(err, domain.ErrAIReservationConflict) ||
		errors.Is(err, domain.ErrAIAccountingDisabled) ||
		errors.Is(err, domain.ErrAICreditUnknownModel) ||
		errors.Is(err, domain.ErrAICreditUnknownPolicy) ||
		errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var apiErr *alem.APIError
	if errors.As(err, &apiErr) {
		return true
	}
	return false
}

// WriteAICallError is the unified error handler for all AI features (Alem Chat, Notebook, Smart Agent, Unified AI).
// It maps domain reservation, rate limit, quota, provider, and system errors to the task 04 public error contract.
func WriteAICallError(c *gin.Context, err error) {
	if err == nil {
		return
	}
	c.Header("Cache-Control", "no-store")
	locale := middleware.RequestLocale(c)

	// 1. Domain reservation denied (Quota / Access restrictions)
	var denied *domain.AIReservationDenied
	if errors.As(err, &denied) {
		writeReservationDeniedError(c, denied, locale)
		return
	}

	// 2. Caller context / identity errors -> 403 AI_ACCESS_DISABLED
	if errors.Is(err, domain.ErrAIPaidCallMissingContext) ||
		errors.Is(err, domain.ErrAIPaidCallInvalidCaller) ||
		errors.Is(err, domain.ErrAIPaidCallContextMismatch) {
		details := gin.H{"reason": "AI_ACCESS_DENIED", "scope": "account"}
		msg := i18nMessage(locale, "error.AI_ACCESS_DISABLED.AI_ACCESS_DENIED", "error.AI_ACCESS_DISABLED")
		respondJSON(c, http.StatusForbidden, gin.H{
			"code":    string(apperrors.ErrCodeAIAccessDisabled),
			"error":   msg,
			"details": details,
		})
		return
	}

	// 3. Rate and Concurrency limits -> 429
	if errors.Is(err, domain.ErrAIRateLimitExceeded) {
		msg := i18nMessage(locale, "error.AI_RATE_LIMIT", "error.AI_RATE_LIMIT")
		respondJSON(c, http.StatusTooManyRequests, gin.H{
			"code":  string(apperrors.ErrCodeAIRateLimitExceeded),
			"error": msg,
		})
		return
	}
	if errors.Is(err, domain.ErrAIConcurrencyLimitExceeded) {
		msg := i18nMessage(locale, "error.AI_CONCURRENCY_LIMIT", "error.AI_CONCURRENCY_LIMIT")
		respondJSON(c, http.StatusTooManyRequests, gin.H{
			"code":  string(apperrors.ErrCodeAIConcurrencyLimitExceeded),
			"error": msg,
		})
		return
	}

	// 4. Limiter unavailable (Redis) -> 503
	if errors.Is(err, domain.ErrAILimiterUnavailable) {
		msg := i18nMessage(locale, "error.AI_LIMITER_UNAVAILABLE", "error.AI_LIMITER_UNAVAILABLE")
		respondJSON(c, http.StatusServiceUnavailable, gin.H{
			"code":  string(apperrors.ErrCodeAILimiterUnavailable),
			"error": msg,
		})
		return
	}

	// 5. Operation conflict / Idempotency -> 409 AI_RESERVATION_CONFLICT
	if errors.Is(err, domain.ErrAIReservationConflict) {
		details := gin.H{"reason": "OPERATION_CONFLICT"}
		msg := i18nMessage(locale, "error.AI_RESERVATION_CONFLICT", "error.AI_RESERVATION_CONFLICT")
		respondJSON(c, http.StatusConflict, gin.H{
			"code":    string(apperrors.ErrCodeAIReservationConflict),
			"error":   msg,
			"details": details,
		})
		return
	}

	// 6. Unpriced model -> 503 AI_MODEL_UNPRICED
	if errors.Is(err, domain.ErrAICreditUnknownModel) {
		msg := i18nMessage(locale, "error.AI_MODEL_UNPRICED", "error.AI_MODEL_UNPRICED")
		respondJSON(c, http.StatusServiceUnavailable, gin.H{
			"code":  string(apperrors.ErrCodeAIModelUnpriced),
			"error": msg,
		})
		return
	}

	// 7. Accounting unavailable / disabled / unknown policy -> 503 AI_ACCOUNTING_UNAVAILABLE
	if errors.Is(err, domain.ErrAIAccountingDisabled) || errors.Is(err, domain.ErrAICreditUnknownPolicy) {
		msg := i18nMessage(locale, "error.AI_ACCOUNTING_UNAVAILABLE", "error.AI_ACCOUNTING_UNAVAILABLE")
		respondJSON(c, http.StatusServiceUnavailable, gin.H{
			"code":  string(apperrors.ErrCodeAIAccountingUnavailable),
			"error": msg,
		})
		return
	}

	// 8. Upstream provider errors (Alem API / network / timeouts)
	var apiErr *alem.APIError
	if errors.As(err, &apiErr) {
		// HTTP 429 from provider is NOT user quota exhaustion (Task 04 section 8)
		if apiErr.StatusCode == http.StatusTooManyRequests {
			msg := "Сейчас поступает много запросов. Попробуйте ещё раз через минуту."
			if locale == i18n.Kazakh {
				msg = "Қазір көп сұраныс түсуде. Бір минуттан кейін қайталап көріңіз."
			}
			respondJSON(c, http.StatusServiceUnavailable, gin.H{
				"code":  string(apperrors.ErrCodeInternal),
				"error": msg,
			})
			return
		}
		msg := "Не получилось получить ответ. Попробуйте ещё раз."
		if locale == i18n.Kazakh {
			msg = "ЖИ-ден жауап алу мүмкін болмады. Қайталап көріңіз."
		}
		respondJSON(c, http.StatusBadGateway, gin.H{
			"code":  string(apperrors.ErrCodeInternal),
			"error": msg,
		})
		return
	}

	if errors.Is(err, context.DeadlineExceeded) {
		msg := "Проверка занимает больше времени, чем обычно. Попробуйте ещё раз."
		if locale == i18n.Kazakh {
			msg = "ЖИ жауабын күту уақыты аяқталды. Қайталап көріңіз."
		}
		respondJSON(c, http.StatusGatewayTimeout, gin.H{
			"code":  string(apperrors.ErrCodeInternal),
			"error": msg,
		})
		return
	}

	if strings.Contains(strings.ToLower(err.Error()), "unauthorized") {
		msg := "У вас нет доступа к этому чату."
		if locale == i18n.Kazakh {
			msg = "Бұл чатқа қолжетімділік жоқ."
		}
		respondJSON(c, http.StatusForbidden, gin.H{
			"code":  string(apperrors.ErrCodeForbidden),
			"error": msg,
		})
		return
	}

	// 9. Generic safe fallback error
	msg := "Не получилось получить ответ. Попробуйте ещё раз."
	if locale == i18n.Kazakh {
		msg = "ЖИ-ден жауап алу мүмкін болмады. Қайталап көріңіз."
	}
	respondJSON(c, http.StatusBadGateway, gin.H{
		"code":  string(apperrors.ErrCodeInternal),
		"error": msg,
	})
}

func writeReservationDeniedError(c *gin.Context, denied *domain.AIReservationDenied, locale i18n.Locale) {
	reason := denied.Reason
	scope := denied.Scope

	switch reason {
	case "NO_ORGANIZATION", "COMPANY_SUSPENDED", "AI_ACCESS_DENIED",
		"COMPANY_BUDGET_UNCONFIGURED", "COMPANY_BUDGET_DISABLED",
		"USER_QUOTA_UNCONFIGURED", "USER_QUOTA_DISABLED", "FIVE_HOUR_DISABLED",
		"SERVICE_QUOTA_DISABLED", "CREDIT_POLICY_UNAVAILABLE":

		if scope == "" {
			switch reason {
			case "NO_ORGANIZATION", "AI_ACCESS_DENIED", "USER_QUOTA_UNCONFIGURED":
				scope = "account"
			case "COMPANY_SUSPENDED", "COMPANY_BUDGET_UNCONFIGURED", "COMPANY_BUDGET_DISABLED":
				scope = "company"
			case "USER_QUOTA_DISABLED":
				scope = "weekly"
			case "FIVE_HOUR_DISABLED":
				scope = "five_hour"
			case "SERVICE_QUOTA_DISABLED":
				scope = "service"
			case "CREDIT_POLICY_UNAVAILABLE":
				scope = "policy"
			}
		}

		msg := i18nMessage(locale, "error.AI_ACCESS_DISABLED."+reason, "error.AI_ACCESS_DISABLED")
		details := gin.H{"reason": reason, "scope": scope}
		respondJSON(c, http.StatusForbidden, gin.H{
			"code":    string(apperrors.ErrCodeAIAccessDisabled),
			"error":   msg,
			"details": details,
		})

	default: // Exhaustion cases: FIVE_HOUR_EXHAUSTED, USER_WEEKLY_EXHAUSTED, COMPANY_WEEKLY_EXHAUSTED, RESERVATION_PENDING, etc.
		if scope == "" {
			switch reason {
			case "FIVE_HOUR_EXHAUSTED":
				scope = "five_hour"
			case "USER_WEEKLY_EXHAUSTED":
				scope = "weekly"
			case "COMPANY_WEEKLY_EXHAUSTED":
				scope = "company"
			case "SERVICE_WEEKLY_EXHAUSTED":
				scope = "service"
			default:
				scope = "weekly"
			}
		}

		serverTime := time.Now().UTC().Truncate(time.Microsecond)
		var resetsAt *time.Time = denied.RecheckAt
		if reason == "RESERVATION_TOO_LARGE" {
			resetsAt = nil
		}
		blocker := gin.H{
			"reason":   reason,
			"scope":    scope,
			"enforced": true,
		}
		if resetsAt != nil {
			blocker["resets_at"] = resetsAt.UTC().Format(time.RFC3339Nano)
		} else {
			blocker["resets_at"] = nil
		}

		retryKind := "estimated"
		var retryAt any = nil
		var recheckAt any = nil

		if reason == "RESERVATION_PENDING" {
			retryKind = "unknown"
		} else if resetsAt != nil {
			retryKind = "estimated"
			formatted := resetsAt.UTC().Format(time.RFC3339Nano)
			retryAt = formatted
			recheckAt = formatted
			if resetsAt.After(serverTime) {
				seconds := int(math.Ceil(resetsAt.Sub(serverTime).Seconds()))
				if seconds < 1 {
					seconds = 1
				}
				c.Header("Retry-After", strconv.Itoa(seconds))
			}
		} else {
			retryKind = "requires_action"
		}

		details := gin.H{
			"blockers":    []gin.H{blocker},
			"retry_at":    retryAt,
			"retry_kind":  retryKind,
			"recheck_at":  recheckAt,
			"server_time": serverTime.Format(time.RFC3339Nano),
		}

		msg := i18nMessage(locale, "error.AI_QUOTA_EXCEEDED."+reason, "error.AI_QUOTA_EXCEEDED")
		respondJSON(c, http.StatusTooManyRequests, gin.H{
			"code":    string(apperrors.ErrCodeAIQuotaExceeded),
			"error":   msg,
			"details": details,
		})
	}
}

func i18nMessage(locale i18n.Locale, specificKey, fallbackKey string) string {
	if text, err := i18n.Render(locale, specificKey, nil); err == nil && text != "" {
		return text
	}
	if text, err := i18n.Render(locale, fallbackKey, nil); err == nil && text != "" {
		return text
	}
	return i18n.Text(locale, fallbackKey, nil)
}
