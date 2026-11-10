package agent

import (
	"context"

	"github.com/west6ide/workspace-backend/internal/domain"
)

type defaultResponseBuilder struct{}

func NewResponseBuilder() ResponseBuilder {
	return &defaultResponseBuilder{}
}

func (b *defaultResponseBuilder) Build(_ context.Context, input ResponseBuildInput) (*domain.AgentChatResponse, error) {
	response := &domain.AgentChatResponse{
		Answer:    "Запрос обработан.",
		Message:   "Запрос обработан.",
		SessionID: input.Session.ID,
		Intent:    input.Intent,
		Entities:  input.Entities,
		Metadata:  map[string]interface{}{},
	}
	if input.ToolResult != nil {
		if input.ToolResult.Answer != "" {
			response.Answer = input.ToolResult.Answer
			response.Message = input.ToolResult.Answer
		}
		response.Actions = input.ToolResult.Actions
		response.Suggestions = input.ToolResult.Suggestions
		response.RequiresConfirmation = input.ToolResult.RequiresConfirmation
		response.ProposedAction = input.ToolResult.ProposedAction
		response.Metadata = input.ToolResult.Metadata
	}
	if input.LowConfidencePrompt {
		response.Answer = "Нужно уточнение по запросу. Скажите, какое действие выполнить."
		response.Message = response.Answer
	}
	if input.ConfirmationToken != "" {
		response.ConfirmationToken = input.ConfirmationToken
	}
	return response, nil
}
