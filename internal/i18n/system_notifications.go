package i18n

import (
	"crypto/sha256"
	"fmt"
)

// SystemNotificationKey identifies a server template independently of locale.
func SystemNotificationKey(source string) string {
	return fmt.Sprintf("system_notification.%x", sha256.Sum256([]byte(source)))
}

// SystemNotification renders only explicitly marked server text. User data is
// passed as parameters and is never interpreted as a template.
func SystemNotification(locale Locale, source string, args ...any) string {
	params := make(map[string]any, len(args))
	for i, arg := range args {
		params[fmt.Sprintf("P%d", i)] = arg
	}
	text, err := Render(locale, SystemNotificationKey(source), params)
	if err != nil {
		return source
	}
	return text
}
