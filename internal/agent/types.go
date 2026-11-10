package agent

import (
	"context"

	"github.com/west6ide/workspace-backend/internal/domain"
)

type ToolRequest struct {
	User      domain.AgentUserContext
	Request   *domain.AgentChatRequest
	Session   *domain.AgentSession
	Intent    *domain.AgentIntentResult
	Entities  map[string]interface{}
	History   []domain.AgentMessage
	Action    *domain.AgentAction
	Confirmed bool
}

type ToolResult struct {
	Answer               string
	Actions              []domain.AgentAction
	Suggestions          []string
	Metadata             map[string]interface{}
	RequiresConfirmation bool
	ProposedAction       *domain.AgentAction
}

type ResponseBuildInput struct {
	Request             *domain.AgentChatRequest
	Session             *domain.AgentSession
	Intent              *domain.AgentIntentResult
	Entities            map[string]interface{}
	ToolResult          *ToolResult
	ConfirmationToken   string
	LowConfidencePrompt bool
}

type FlowState struct {
	Ctx               context.Context
	User              domain.AgentUserContext
	Request           *domain.AgentChatRequest
	Session           *domain.AgentSession
	History           []domain.AgentMessage
	Intent            *domain.AgentIntentResult
	Entities          map[string]interface{}
	ToolResult        *ToolResult
	Response          *domain.AgentChatResponse
	ConfirmationToken string
}
