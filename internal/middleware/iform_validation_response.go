package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/i18n"
)

// RespondIFormValidation exposes only locally translated domain reasons.
func RespondIFormValidation(c *gin.Context, code string) bool {
	switch code {
	case "IIN_INVALID", "ONBOARDING_CREDENTIALS_REQUIRED", "DATE_INVALID", "DATE_ORDER_INVALID", "DATE_RANGE_REQUIRED", "DELEGATION_PERIOD_INVALID", "DELEGATION_APPROVER_REQUIRED", "DELEGATION_OVERLAP":
	default:
		return false
	}
	RecordAppError(c, apperrors.New(apperrors.ErrorCode(code), code, http.StatusUnprocessableEntity))
	message := i18n.Text(RequestLocale(c), "iform.validation."+code, nil)
	c.JSON(http.StatusUnprocessableEntity, gin.H{"code": code, "message": message, "user_message": message})
	return true
}
