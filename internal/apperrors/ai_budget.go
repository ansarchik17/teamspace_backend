package apperrors

const (
	ErrCodeAICreditLimit              ErrorCode = "AI_CREDIT_LIMIT"
	ErrCodeAIChatAccess               ErrorCode = "AI_CHAT_ACCESS"
	ErrCodeAIRevisionConflict         ErrorCode = "AI_REVISION_CONFLICT"
	ErrCodeAIBudgetConflict           ErrorCode = "AI_BUDGET_CONFLICT"
	ErrCodeAICompanySuspended         ErrorCode = "AI_COMPANY_SUSPENDED"
	ErrCodeAIConfigurationRequired    ErrorCode = "AI_CONFIGURATION_REQUIRED"
	ErrCodeAIRateLimitExceeded        ErrorCode = "AI_RATE_LIMIT"
	ErrCodeAIConcurrencyLimitExceeded ErrorCode = "AI_CONCURRENCY_LIMIT"
	ErrCodeAILimiterUnavailable       ErrorCode = "AI_LIMITER_UNAVAILABLE"
	ErrCodeAIAccountingUnavailable    ErrorCode = "AI_ACCOUNTING_UNAVAILABLE"
	ErrCodeAIAccessDisabled           ErrorCode = "AI_ACCESS_DISABLED"
	ErrCodeAIQuotaExceeded            ErrorCode = "AI_QUOTA_EXCEEDED"
	ErrCodeAIModelUnpriced            ErrorCode = "AI_MODEL_UNPRICED"
	ErrCodeAIReservationConflict      ErrorCode = "AI_RESERVATION_CONFLICT"
)
