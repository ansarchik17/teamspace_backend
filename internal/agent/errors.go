package agent

import (
	"net/http"

	"github.com/west6ide/workspace-backend/internal/apperrors"
)

const (
	ErrCodeIntentNotFound        apperrors.ErrorCode = "INTENT_NOT_FOUND"
	ErrCodeActionRequiresConfirm apperrors.ErrorCode = "ACTION_REQUIRES_CONFIRMATION"
	ErrCodeActionFailed          apperrors.ErrorCode = "ACTION_FAILED"
	ErrCodeSessionNotFound       apperrors.ErrorCode = "SESSION_NOT_FOUND"
	ErrCodeRateLimited           apperrors.ErrorCode = "RATE_LIMITED"
)

func ErrIntentNotFound(message string) *apperrors.AppError {
	return apperrors.New(ErrCodeIntentNotFound, message, http.StatusBadRequest)
}

func ErrActionRequiresConfirmation(message string) *apperrors.AppError {
	return apperrors.New(ErrCodeActionRequiresConfirm, message, http.StatusConflict)
}

func ErrActionFailed(message string) *apperrors.AppError {
	return apperrors.New(ErrCodeActionFailed, message, http.StatusBadGateway)
}

func ErrSessionNotFound(message string) *apperrors.AppError {
	return apperrors.New(ErrCodeSessionNotFound, message, http.StatusNotFound)
}
