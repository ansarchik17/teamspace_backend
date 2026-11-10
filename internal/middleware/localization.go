package middleware

import (
	"context"
	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/i18n"
	"strings"
)

type UserLanguageLookup func(context.Context, int64) (string, error)

// Localization must run after authentication when a profile lookup is supplied.
// Database lookup failures use the default locale and do not block the request.
func Localization(lookup UserLanguageLookup) gin.HandlerFunc {
	return func(c *gin.Context) {
		locale, supported := i18n.RequestLocale(c.GetHeader("Accept-Language"))
		if !supported {
			profileLanguage := ""
			if userID, ok := GetUserID(c); ok && lookup != nil {
				if value, err := lookup(c.Request.Context(), userID); err == nil {
					profileLanguage = value
				}
			}
			locale = i18n.ResolveRecipient(profileLanguage)
		}
		c.Request = c.Request.WithContext(i18n.WithLocale(c.Request.Context(), locale))
		c.Set("locale", string(locale))
		c.Header("Content-Language", string(locale))
		vary := c.Writer.Header().Values("Vary")
		found := false
		for _, entry := range vary {
			for _, field := range strings.Split(entry, ",") {
				if strings.EqualFold(strings.TrimSpace(field), "Accept-Language") || strings.TrimSpace(field) == "*" {
					found = true
				}
			}
		}
		if !found {
			c.Writer.Header().Add("Vary", "Accept-Language")
		}
		c.Next()
	}
}
