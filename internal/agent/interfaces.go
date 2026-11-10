package agent

import (
	"context"
	"time"

	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	legacyservice "github.com/west6ide/workspace-backend/internal/service"
)

type LegacyChatProcessor interface {
	ProcessChat(ctx context.Context, userID int64, req *legacyservice.SmartAgentChatRequest) (*legacyservice.SmartAgentChatResponse, error)
}

type SessionRepository interface {
	CreateSession(ctx context.Context, session *domain.AgentSession) error
	GetSession(ctx context.Context, sessionID string, userID int64) (*domain.AgentSession, error)
	ListSessions(ctx context.Context, userID int64, limit int) ([]domain.AgentSession, error)
	TouchSession(ctx context.Context, sessionID string, userID int64, metadata map[string]interface{}, expiresAt time.Time) error
	UpdateSessionMetadata(ctx context.Context, sessionID string, userID int64, metadata map[string]interface{}, expiresAt time.Time) error
	CloseSession(ctx context.Context, sessionID string, userID int64) error
	DeleteSession(ctx context.Context, sessionID string, userID int64) error
	CreateMessage(ctx context.Context, message *domain.AgentMessage) error
	ListMessages(ctx context.Context, sessionID string, userID int64) ([]domain.AgentMessage, error)
	CreateConfirmation(ctx context.Context, confirmation *domain.AgentActionConfirmation) error
	GetConfirmationByHash(ctx context.Context, sessionID string, userID int64, tokenHash string) (*domain.AgentActionConfirmation, error)
	MarkConfirmationExecuted(ctx context.Context, id string) error
	SaveFeedback(ctx context.Context, feedback *domain.AgentFeedback) error
	SaveAuditLog(ctx context.Context, entry *domain.AgentAuditLog) error
}

type SessionService interface {
	GetOrCreate(ctx context.Context, user domain.AgentUserContext, requestedSessionID string, metadata map[string]interface{}) (*domain.AgentSession, error)
	Get(ctx context.Context, user domain.AgentUserContext, sessionID string) (*domain.AgentSession, error)
	List(ctx context.Context, user domain.AgentUserContext, limit int) ([]domain.AgentSession, error)
	UpdateMetadata(ctx context.Context, user domain.AgentUserContext, sessionID string, metadata map[string]interface{}) (*domain.AgentSession, error)
	Close(ctx context.Context, user domain.AgentUserContext, sessionID string) error
	Delete(ctx context.Context, user domain.AgentUserContext, sessionID string) error
	SaveMessage(ctx context.Context, message *domain.AgentMessage) error
	ListMessages(ctx context.Context, user domain.AgentUserContext, sessionID string) ([]domain.AgentMessage, error)
}

type IntentResolver interface {
	Resolve(ctx context.Context, req *domain.AgentChatRequest, session *domain.AgentSession, history []domain.AgentMessage) (*domain.AgentIntentResult, error)
}

type EntityExtractor interface {
	Extract(ctx context.Context, req *domain.AgentChatRequest, intent *domain.AgentIntentResult, history []domain.AgentMessage) (map[string]interface{}, error)
}

type PermissionChecker interface {
	Check(ctx context.Context, user domain.AgentUserContext, intent *domain.AgentIntentResult, entities map[string]interface{}) *apperrors.AppError
}

type Tool interface {
	Name() string
	Description() string
	Supports(intent domain.AgentIntentName) bool
	Execute(ctx context.Context, input ToolRequest) (*ToolResult, *apperrors.AppError)
}

type ToolRegistry interface {
	FindByIntent(intent domain.AgentIntentName) Tool
	FindByAction(action string) Tool
	Capabilities() []domain.AgentCapability
}

type ResponseBuilder interface {
	Build(ctx context.Context, input ResponseBuildInput) (*domain.AgentChatResponse, error)
}

type AuditLogger interface {
	Log(ctx context.Context, user domain.AgentUserContext, sessionID, eventType string, payload map[string]interface{}) error
}

type FlowExecutor interface {
	Execute(ctx context.Context, state *FlowState) error
}
