package service

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/west6ide/workspace-backend/internal/alem"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/i18n"
)

func (s *AlemService) completeFinalQuotaTurn(ctx context.Context, client *alemPaidChat, previous *alem.ChatCompletionResponse) (*alem.ChatCompletionResponse, error) {
	denied := client.quotaCutoff
	if denied == nil || denied.AvailableMicrocredits == nil || *denied.AvailableMicrocredits <= 0 {
		return nil, nil
	}
	caller, err := (domain.AIPaidCallParams{}).ResolveCaller(ctx)
	if err != nil {
		return nil, err
	}
	notice := i18n.TextContext(ctx, "ai.quota_cutoff."+denied.Scope, nil)
	text := notice
	usage := alem.Usage{}
	if previous != nil {
		usage = previous.Usage
		if len(previous.Choices) > 0 {
			if content, ok := previous.Choices[0].Message.Content.(string); ok && strings.TrimSpace(content) != "" {
				text = strings.TrimSpace(content) + "\n\n" + notice
			}
		}
	}
	result, err := s.repo.CompleteFinalQuotaTurn(ctx, caller, client.chatID.String(), client.turnID, denied.Scope, text, *denied.AvailableMicrocredits, client.dispatched, usage.TotalTokens)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, nil
	}
	// Server-generated completion notice; not a fabricated provider completion.
	return &alem.ChatCompletionResponse{ID: "quota-stop-" + result.MessageID, Object: "chat.quota_stop", Created: time.Now().Unix(), Model: "system", Usage: usage,
		Choices:   []alem.Choice{{Index: 0, FinishReason: "quota_exhausted", Message: alem.Message{Role: "assistant", Content: text}}},
		QuotaStop: &alem.QuotaStop{Scope: denied.Scope, ConsumedMicrocredits: strconv.FormatInt(result.Microcredits, 10), ResetsAt: result.ResetsAt.UTC().Format(time.RFC3339)},
	}, nil
}
