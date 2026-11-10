package i18n

import (
	"context"
	"crypto/sha256"
	"fmt"
)

func UIMessageKey(source string) string { return fmt.Sprintf("ui.%x", sha256.Sum256([]byte(source))) }
func UIMessage(ctx context.Context, source string, args ...any) string {
	params := make(map[string]any, len(args))
	for i, arg := range args {
		params[fmt.Sprintf("P%d", i)] = arg
	}
	value, err := Render(FromContext(ctx), UIMessageKey(source), params)
	if err != nil {
		return source
	}
	return value
}
