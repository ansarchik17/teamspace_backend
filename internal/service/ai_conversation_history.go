package service

import (
	"encoding/json"

	"github.com/west6ide/workspace-backend/internal/alem"
)

// Keep recent complete turns within a byte budget. Never truncate the current
// request: silently removing code or conditions would change the user's task.
func limitAIConversationHistory(messages []alem.Message) []alem.Message {
	const maxHistoryBytes = 64000
	start, size := len(messages), 0
	for i := len(messages) - 1; i >= 0; i-- {
		encoded, err := json.Marshal(messages[i])
		if err != nil {
			break
		}
		if size+len(encoded) > maxHistoryBytes && start < len(messages) {
			break
		}
		size += len(encoded)
		start = i
	}
	// A model accepting alternating roles must not receive an orphaned reply.
	for start < len(messages) && messages[start].Role != "user" {
		start++
	}
	return messages[start:]
}
