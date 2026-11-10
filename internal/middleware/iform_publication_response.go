package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/i18n"
)

// RespondIFormPublicationIssues is a public response boundary for bounded issue
// codes. All messages are generated locally; no upstream diagnostic is rendered.
// Invalid codes leave the response unwritten so callers can use generic errors.
func RespondIFormPublicationIssues(c *gin.Context, codes []string) bool {
	if len(codes) == 0 || len(codes) > 20 {
		return false
	}
	allowed := map[string]bool{
		"CONNECTION_REQUIRED": true, "TEMPLATE_REQUIRED": true,
		"ASSIGNEE_REQUIRED": true, "SD_CONFIGURATION_REQUIRED": true,
		"PROCESS_CONFIGURATION_REQUIRED": true, "SETUP_CONFIRMATION_REQUIRED": true,
	}
	for _, code := range codes {
		if !allowed[code] {
			return false
		}
	}
	errors, unresolved := []string{}, []string{}
	seen := make(map[string]bool)
	locale := RequestLocale(c)
	for _, code := range codes {
		if seen[code] {
			continue
		}
		seen[code] = true
		text := i18n.Text(locale, "iform.setup."+code, nil)
		if code == "SETUP_CONFIRMATION_REQUIRED" {
			unresolved = append(unresolved, text)
		} else {
			errors = append(errors, text)
		}
	}
	appErr := apperrors.New("IFORM_PUBLICATION_BLOCKED", "iForm publication blocked", http.StatusUnprocessableEntity)
	RecordAppError(c, appErr)
	message := apperrors.ResponseBodyLocale(appErr, locale).Error.Message
	c.JSON(http.StatusUnprocessableEntity, gin.H{
		"code": "IFORM_PUBLICATION_BLOCKED", "message": message, "user_message": message,
		"validation": gin.H{"valid": false, "errors": errors, "unresolvedItems": unresolved, "warnings": []string{}},
	})
	return true
}
