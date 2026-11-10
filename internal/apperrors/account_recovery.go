package apperrors

func RecoveryInvalid() *AppError {
	return New("RECOVERY_INVALID", "Recovery proof is invalid or expired", 400)
}
func RecoveryLimited() *AppError { return New("RECOVERY_LIMITED", "Too many recovery requests", 429) }
func RecoveryPending() *AppError {
	return New("RECOVERY_PENDING", "Recovery is in progress; retry the link or request a new one", 503)
}
func RecoveryEmailConflict() *AppError {
	return New("RECOVERY_EMAIL_CONFLICT", "This address cannot be used for recovery", 409)
}
func RecoveryUnavailable() *AppError {
	return New("RECOVERY_UNAVAILABLE", "Recovery is temporarily unavailable", 503)
}
