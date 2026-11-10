package i18n

import (
	"context"
	"crypto/sha256"
	"fmt"
)

// APIMessageKey is deterministic, so adding another message never renumbers
// existing keys. Only audited server literals use this helper, never user data.
func APIMessageKey(original string) string {
	return fmt.Sprintf("api.%x", sha256.Sum256([]byte(original)))
}

func APIMessage(ctx context.Context, original string) string {
	return TextContext(ctx, APIMessageKey(original), nil)
}
