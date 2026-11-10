package service

import (
	"context"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/domain"
)

type notebookHistoryReader interface {
	GetChatHistory(context.Context, int64, uuid.UUID) ([]*domain.NotebookMessage, error)
}

var notebookFollowup = regexp.MustCompile(`(?i)(предыдущ|сократ|перепиш|сделай короче|по ответу|по предыдущему|previous|shorten|rewrite)`)
var notebookStoredCitation = regexp.MustCompile(`\]\((/web/drive/notebook\?[^\s()]+)\)`)

// Reuse a draft only for explicit follow-ups, after every source citation and
// revision is validated against the current selection. A changed selection,
// changed document or unverifiable answer must not reintroduce stale facts.
func notebookFollowupDraft(content string, chatID uuid.UUID, sources []NotebookResearchSource) string {
	if len([]rune(content)) > 16000 || len(sources) == 0 {
		return ""
	}
	byID := make(map[string]NotebookResearchSource, len(sources))
	for _, source := range sources {
		byID[source.FileID.String()] = source
	}
	seen := make(map[string]bool)
	for _, match := range notebookStoredCitation.FindAllStringSubmatch(content, -1) {
		u, err := url.Parse(match[1])
		if err != nil {
			return ""
		}
		q := u.Query()
		source, exists := byID[q.Get("source")]
		if !exists || q.Get("chat") != chatID.String() || source.Revision == "" || q.Get("revision") != source.Revision {
			return ""
		}
		start, startErr := strconv.Atoi(q.Get("start_line"))
		end, endErr := strconv.Atoi(q.Get("end_line"))
		if startErr != nil || endErr != nil || start < 1 || end < start || end-start > 200 {
			return ""
		}
		available := false
		for _, excerpt := range source.Excerpts {
			if start >= excerpt.StartLine && end <= excerpt.EndLine {
				available = true
			}
		}
		if !available {
			return ""
		}
		seen[source.FileID.String()] = true
	}
	if len(seen) != len(byID) {
		return ""
	}
	return content
}

func (s *NotebookResearchService) followupDraft(ctx context.Context, userID int64, chatID uuid.UUID, question string, sources []NotebookResearchSource) string {
	reader, ok := s.notebooks.(notebookHistoryReader)
	if !ok || !notebookFollowup.MatchString(question) {
		return ""
	}
	messages, err := reader.GetChatHistory(ctx, userID, chatID)
	if err != nil {
		return ""
	}
	for i := len(messages) - 1; i >= 0; i-- {
		message := messages[i]
		if message != nil && message.Role == "assistant" && message.ChatID == chatID {
			return notebookFollowupDraft(strings.TrimSpace(message.Content), chatID, sources)
		}
	}
	return ""
}
