package middleware

import (
	"encoding/json"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/i18n"
)

// RespondJSON keeps successful data untouched and applies the public error
// contract to legacy JSON endpoints. It does not intercept streams or downloads.
func RespondJSON(c *gin.Context, status int, body any) {
	if status < 400 {
		c.JSON(status, body)
		return
	}
	locale := RequestLocale(c)
	if err, ok := body.(error); ok {
		appErr := apperrors.GetAppError(err)
		if appErr == nil {
			appErr = apperrors.New(apperrors.CodeForStatus(status), err.Error(), status).WithError(err)
		}
		RecordAppError(c, appErr)
		c.JSON(status, apperrors.ResponseBodyLocale(appErr, locale))
		return
	}
	raw := make(map[string]any)
	if data, err := json.Marshal(body); err == nil {
		_ = json.Unmarshal(data, &raw)
	}
	code, _ := raw["code"].(string)
	message, _ := raw["message"].(string)
	details, _ := raw["details"].(map[string]any)
	nested, canonical := raw["error"].(map[string]any)
	if canonical {
		if value, ok := nested["code"].(string); ok {
			code = value
		}
		if value, ok := nested["message"].(string); ok {
			message = value
		}
		if value, ok := nested["details"].(map[string]any); ok {
			details = value
		}
	} else if value, ok := raw["error"].(string); ok {
		message = value
	}
	appErr := apperrors.New(apperrors.SafeCode(code, status), message, status).WithDetails(details)
	RecordAppError(c, appErr)
	payload := apperrors.ResponseBodyLocale(appErr, locale).Error
	if canonical || len(raw) == 0 {
		c.JSON(status, apperrors.ErrorResponse{Error: payload})
		return
	}
	// Existing string-error and flat-message consumers remain compatible. Codes
	// and user_message are additive; arbitrary diagnostic fields are excluded.
	result := gin.H{"code": payload.Code, "message": payload.Message, "user_message": payload.UserMessage}
	if _, ok := raw["error"]; ok {
		result["error"] = payload.Message
	}
	if payload.Details != nil {
		result["details"] = payload.Details
	}
	c.JSON(status, result)
}

func AbortJSON(c *gin.Context, status int, body any) {
	c.Abort()
	RespondJSON(c, status, body)
}

func RequestLocale(c *gin.Context) i18n.Locale {
	if c.Request == nil {
		return i18n.Russian
	}
	return i18n.FromContext(c.Request.Context())
}
