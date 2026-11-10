package service

import (
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/domain"
	"strings"
)

// A bounded vocabulary also bounds the number of reactions per user and target.
func canonicalReaction(value string) string {
	value = strings.TrimSpace(value)
	if value == "like" {
		return "👍"
	}
	switch value {
	case "👍", "👎", "❤️", "❤", "🔥", "🎉", "😂", "😮", "😢", "👏", "🙏", "😍", "🤔", "💯":
		return value
	default:
		return ""
	}
}

func buildCommentTree(node *domain.CommentResponse, children map[uuid.UUID][]*domain.CommentResponse, seen map[uuid.UUID]bool) domain.CommentResponse {
	result := *node
	result.Replies = nil
	if seen[node.ID] {
		return result
	}
	seen[node.ID] = true
	for _, child := range children[node.ID] {
		if !seen[child.ID] {
			result.Replies = append(result.Replies, buildCommentTree(child, children, seen))
		}
	}
	result.RepliesCount = len(result.Replies)
	return result
}
