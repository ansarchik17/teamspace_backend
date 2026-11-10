package websocket

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// BuildSessionKey derives a stable, non-reversible identifier from a session source
// so we can avoid echoing the same event back into the originating session.
func BuildSessionKey(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}

	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
