package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	graphflow "github.com/west6ide/workspace-backend/internal/agent/graph"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/domain"
	legacyservice "github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

type Service struct {
	cfg         config.AgentConfig
	logger      *zap.Logger
	repo        SessionRepository
	sessions    SessionService
	resolver    IntentResolver
	extractor   EntityExtractor
	permissions PermissionChecker
	registry    ToolRegistry
	responses   ResponseBuilder
	audit       AuditLogger
	flow        FlowExecutor
	legacy      LegacyChatProcessor
}

func NewService(
	cfg config.AgentConfig,
	logger *zap.Logger,
	repo SessionRepository,
	resolver IntentResolver,
	extractor EntityExtractor,
	permissions PermissionChecker,
	registry ToolRegistry,
	responses ResponseBuilder,
	audit AuditLogger,
	legacy LegacyChatProcessor,
) *Service {
	svc := &Service{
		cfg:         cfg,
		logger:      logger.With(zap.String("service", "agent_v2")),
		repo:        repo,
		sessions:    NewSessionService(cfg, repo),
		resolver:    resolver,
		extractor:   extractor,
		permissions: permissions,
		registry:    registry,
		responses:   responses,
		audit:       audit,
		legacy:      legacy,
	}
	svc.flow = NewGraphExecutor(svc.buildFlow)
	return svc
}

func (s *Service) ProcessLegacy(ctx context.Context, userID int64, req *legacyservice.SmartAgentChatRequest) (*legacyservice.SmartAgentChatResponse, error) {
	if s.legacy == nil {
		return nil, fmt.Errorf("legacy smart agent processor is not configured")
	}
	return s.legacy.ProcessChat(ctx, userID, req)
}

func (s *Service) Chat(ctx context.Context, user domain.AgentUserContext, req *domain.AgentChatRequest) (*domain.AgentChatResponse, *apperrors.AppError) {
	if !s.cfg.Enabled || !s.cfg.V2Enabled {
		return nil, apperrors.Forbidden("agent v2 is disabled")
	}

	ctx, cancel := context.WithTimeout(ctx, s.cfg.ExternalTimeout)
	defer cancel()

	state := &FlowState{
		Ctx:     ctx,
		User:    user,
		Request: req,
	}
	if err := s.flow.Execute(ctx, state); err != nil {
		return nil, apperrors.ToAppError(err)
	}
	return state.Response, nil
}

func (s *Service) CreateSession(ctx context.Context, user domain.AgentUserContext, metadata map[string]interface{}) (*domain.AgentSession, error) {
	return s.sessions.GetOrCreate(ctx, user, "", metadata)
}

func (s *Service) GetSession(ctx context.Context, user domain.AgentUserContext, sessionID string) (*domain.AgentSession, *apperrors.AppError) {
	session, err := s.sessions.Get(ctx, user, sessionID)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to get session", err)
	}
	if session == nil {
		return nil, ErrSessionNotFound("session not found")
	}
	return session, nil
}

func (s *Service) ListSessions(ctx context.Context, user domain.AgentUserContext, limit int) ([]domain.AgentSession, *apperrors.AppError) {
	sessions, err := s.sessions.List(ctx, user, limit)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to list sessions", err)
	}
	return sessions, nil
}

func (s *Service) GetMessages(ctx context.Context, user domain.AgentUserContext, sessionID string) ([]domain.AgentMessage, *apperrors.AppError) {
	session, appErr := s.GetSession(ctx, user, sessionID)
	if appErr != nil {
		return nil, appErr
	}
	messages, err := s.sessions.ListMessages(ctx, user, session.ID)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to load messages", err)
	}
	return messages, nil
}

func (s *Service) DeleteSession(ctx context.Context, user domain.AgentUserContext, sessionID string) *apperrors.AppError {
	if err := s.sessions.Delete(ctx, user, sessionID); err != nil {
		return apperrors.InternalWithErr("failed to delete session", err)
	}
	_ = s.audit.Log(ctx, user, sessionID, "session_deleted", nil)
	return nil
}

func (s *Service) UpdateSessionTitle(ctx context.Context, user domain.AgentUserContext, sessionID, title string) (*domain.AgentSession, *apperrors.AppError) {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, apperrors.BadRequest("title is required")
	}
	session, appErr := s.GetSession(ctx, user, sessionID)
	if appErr != nil {
		return nil, appErr
	}
	metadata := cloneMetadata(session.Metadata)
	metadata["title"] = title
	updated, err := s.sessions.UpdateMetadata(ctx, user, sessionID, metadata)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to update session title", err)
	}
	_ = s.audit.Log(ctx, user, sessionID, "session_title_updated", map[string]interface{}{"title": title})
	return updated, nil
}

func cloneMetadata(metadata map[string]interface{}) map[string]interface{} {
	cloned := make(map[string]interface{}, len(metadata))
	for key, value := range metadata {
		cloned[key] = value
	}
	return cloned
}

var multiSpaceRE = regexp.MustCompile(`\s+`)

func deriveSessionTitle(message string) string {
	clean := strings.TrimSpace(multiSpaceRE.ReplaceAllString(message, " "))
	if clean == "" {
		return ""
	}
	runes := []rune(clean)
	if len(runes) > 120 {
		clean = string(runes[:120])
	}
	return clean
}

func (s *Service) ensureSessionTitle(ctx context.Context, user domain.AgentUserContext, session *domain.AgentSession, message string) {
	if session == nil {
		return
	}
	if strings.TrimSpace(fmt.Sprint(session.Metadata["title"])) != "" {
		return
	}
	title := deriveSessionTitle(message)
	if title == "" {
		return
	}
	metadata := cloneMetadata(session.Metadata)
	metadata["title"] = title
	if _, err := s.sessions.UpdateMetadata(ctx, user, session.ID, metadata); err != nil {
		s.logger.Warn("failed to auto-set session title", zap.Error(err))
	}
}

func (s *Service) DeleteSessionLegacyClose(ctx context.Context, user domain.AgentUserContext, sessionID string) *apperrors.AppError {
	if err := s.sessions.Close(ctx, user, sessionID); err != nil {
		return apperrors.InternalWithErr("failed to close session", err)
	}
	_ = s.audit.Log(ctx, user, sessionID, "session_closed", nil)
	return nil
}

func (s *Service) ResolveIntent(ctx context.Context, user domain.AgentUserContext, req *domain.AgentChatRequest) (*domain.AgentIntentResult, *apperrors.AppError) {
	session, err := s.sessions.GetOrCreate(ctx, user, req.SessionID, req.Context)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to resolve session", err)
	}
	history, err := s.sessions.ListMessages(ctx, user, session.ID)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to load session history", err)
	}
	intent, err := s.resolver.Resolve(ctx, req, session, history)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to resolve intent", err)
	}
	entities, err := s.extractor.Extract(ctx, req, intent, history)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to extract entities", err)
	}
	intent.Entities = entities
	return intent, nil
}

func (s *Service) ExecuteAction(ctx context.Context, user domain.AgentUserContext, sessionID, confirmationToken string) (*domain.AgentChatResponse, *apperrors.AppError) {
	if strings.TrimSpace(confirmationToken) == "" {
		return nil, apperrors.BadRequest("confirmation_token is required")
	}
	confirmation, err := s.repo.GetConfirmationByHash(ctx, sessionID, user.UserID, hashToken(confirmationToken))
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to load confirmation", err)
	}
	if confirmation == nil {
		return nil, apperrors.Forbidden("invalid confirmation token")
	}
	if confirmation.Status != domain.AgentConfirmationPending {
		return nil, apperrors.Conflict("action is already processed")
	}
	if confirmation.ExpiresAt.Before(time.Now()) {
		return nil, apperrors.Forbidden("confirmation token expired")
	}
	session, appErr := s.GetSession(ctx, user, sessionID)
	if appErr != nil {
		return nil, appErr
	}
	tool := s.registry.FindByAction(confirmation.ActionType)
	if tool == nil {
		return nil, ErrIntentNotFound("tool for action not found")
	}
	result, toolErr := tool.Execute(ctx, ToolRequest{
		User:      user,
		Session:   session,
		Entities:  confirmation.Payload,
		Confirmed: true,
		Action: &domain.AgentAction{
			Type:    confirmation.ActionType,
			Payload: confirmation.Payload,
		},
	})
	if toolErr != nil {
		return nil, toolErr
	}
	if err := s.repo.MarkConfirmationExecuted(ctx, confirmation.ID); err != nil {
		return nil, apperrors.InternalWithErr("failed to mark confirmation", err)
	}
	response, err := s.responses.Build(ctx, ResponseBuildInput{
		Request: &domain.AgentChatRequest{
			SessionID: session.ID,
			Message:   confirmation.ActionType,
		},
		Session:    session,
		ToolResult: result,
	})
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to build action response", err)
	}
	if saveErr := s.persistAssistantMessage(ctx, user, session.ID, response.Answer, confirmation.ActionType, confirmation.Payload, response.Actions); saveErr != nil {
		s.logger.Warn("failed to save action execution message", zap.Error(saveErr))
	}
	_ = s.audit.Log(ctx, user, session.ID, "action_executed", map[string]interface{}{"action_type": confirmation.ActionType})
	return response, nil
}

func (s *Service) SaveFeedback(ctx context.Context, user domain.AgentUserContext, feedback *domain.AgentFeedback) *apperrors.AppError {
	feedback.UserID = user.UserID
	feedback.CreatedAt = time.Now()
	if err := s.repo.SaveFeedback(ctx, feedback); err != nil {
		return apperrors.InternalWithErr("failed to save feedback", err)
	}
	return nil
}

func (s *Service) Capabilities() []domain.AgentCapability {
	return s.registry.Capabilities()
}

func (s *Service) buildFlow(state *FlowState) *graphflow.Executor {
	return graphflow.NewExecutor(
		func(ctx context.Context) error {
			state.Request.Message = strings.TrimSpace(state.Request.Message)
			session, err := s.sessions.GetOrCreate(ctx, state.User, state.Request.SessionID, state.Request.Context)
			if err != nil {
				return err
			}
			state.Session = session
			return nil
		},
		func(ctx context.Context) error {
			history, err := s.sessions.ListMessages(ctx, state.User, state.Session.ID)
			if err != nil {
				return err
			}
			state.History = history
			return nil
		},
		func(ctx context.Context) error {
			intent, err := s.resolver.Resolve(ctx, state.Request, state.Session, state.History)
			if err != nil {
				return err
			}
			state.Intent = intent
			return nil
		},
		func(ctx context.Context) error {
			entities, err := s.extractor.Extract(ctx, state.Request, state.Intent, state.History)
			if err != nil {
				return err
			}
			state.Entities = entities
			state.Intent.Entities = entities
			return nil
		},
		func(ctx context.Context) error {
			if appErr := s.permissions.Check(ctx, state.User, state.Intent, state.Entities); appErr != nil {
				return appErr
			}
			return nil
		},
		func(ctx context.Context) error {
			state.ToolResult = &ToolResult{Answer: "Не удалось подобрать действие."}
			if state.Intent.Confidence < s.cfg.IntentConfidenceThreshold {
				state.Response, _ = s.responses.Build(ctx, ResponseBuildInput{
					Request:             state.Request,
					Session:             state.Session,
					Intent:              state.Intent,
					Entities:            state.Entities,
					ToolResult:          &ToolResult{Suggestions: []string{"Уточнить запрос", "Показать возможности"}},
					LowConfidencePrompt: true,
				})
				return nil
			}
			tool := s.registry.FindByIntent(state.Intent.Name)
			if tool == nil {
				tool = s.registry.FindByIntent(domain.AgentIntentHelp)
			}
			result, appErr := tool.Execute(ctx, ToolRequest{
				User:     state.User,
				Request:  state.Request,
				Session:  state.Session,
				Intent:   state.Intent,
				Entities: state.Entities,
				History:  state.History,
			})
			if appErr != nil {
				return appErr
			}
			state.ToolResult = result
			if result.RequiresConfirmation && result.ProposedAction != nil {
				token, err := s.createConfirmation(ctx, state.User, state.Session.ID, result.ProposedAction.Type, result.ProposedAction.Payload)
				if err != nil {
					return err
				}
				state.ConfirmationToken = token
				state.Response = nil
			}
			return nil
		},
		func(ctx context.Context) error {
			if state.Response != nil {
				return nil
			}
			response, err := s.responses.Build(ctx, ResponseBuildInput{
				Request:           state.Request,
				Session:           state.Session,
				Intent:            state.Intent,
				Entities:          state.Entities,
				ToolResult:        state.ToolResult,
				ConfirmationToken: state.ConfirmationToken,
			})
			if err != nil {
				return err
			}
			state.Response = response
			return nil
		},
		func(ctx context.Context) error {
			if err := s.persistConversation(ctx, state); err != nil {
				s.logger.Warn("failed to persist conversation", zap.Error(err))
			}
			s.ensureSessionTitle(ctx, state.User, state.Session, state.Request.Message)
			return nil
		},
		func(ctx context.Context) error {
			payload := map[string]interface{}{
				"intent":                state.Intent.Name,
				"confidence":            state.Intent.Confidence,
				"requires_confirmation": state.Response.RequiresConfirmation,
			}
			return s.audit.Log(ctx, state.User, state.Session.ID, "chat_processed", payload)
		},
	)
}

func (s *Service) createConfirmation(ctx context.Context, user domain.AgentUserContext, sessionID, actionType string, payload map[string]interface{}) (string, error) {
	token := uuid.NewString()
	now := time.Now()
	err := s.repo.CreateConfirmation(ctx, &domain.AgentActionConfirmation{
		SessionID:  sessionID,
		UserID:     user.UserID,
		TokenHash:  hashToken(token),
		ActionType: actionType,
		Payload:    payload,
		Status:     domain.AgentConfirmationPending,
		ExpiresAt:  now.Add(s.cfg.ConfirmationTTL),
		CreatedAt:  now,
	})
	return token, err
}

func (s *Service) persistConversation(ctx context.Context, state *FlowState) error {
	if err := s.persistUserMessage(ctx, state.User, state.Session.ID, state.Request.Message, string(state.Intent.Name), state.Entities); err != nil {
		return err
	}
	return s.persistAssistantMessage(ctx, state.User, state.Session.ID, state.Response.Answer, string(state.Intent.Name), state.Entities, state.Response.Actions)
}

func (s *Service) persistUserMessage(ctx context.Context, user domain.AgentUserContext, sessionID, content, intent string, entities map[string]interface{}) error {
	return s.sessions.SaveMessage(ctx, &domain.AgentMessage{
		SessionID: sessionID,
		UserID:    user.UserID,
		Role:      domain.AgentRoleUser,
		Content:   content,
		Intent:    intent,
		Entities:  entities,
		CreatedAt: time.Now(),
	})
}

func (s *Service) persistAssistantMessage(ctx context.Context, user domain.AgentUserContext, sessionID, content, intent string, entities map[string]interface{}, actions []domain.AgentAction) error {
	return s.sessions.SaveMessage(ctx, &domain.AgentMessage{
		SessionID: sessionID,
		UserID:    user.UserID,
		Role:      domain.AgentRoleAssistant,
		Content:   content,
		Intent:    intent,
		Entities:  entities,
		Actions:   actions,
		CreatedAt: time.Now(),
	})
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
