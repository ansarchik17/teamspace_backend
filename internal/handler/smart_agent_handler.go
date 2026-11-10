package handler

import (
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/agent"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

type SmartAgentHandler struct {
	service smartAgentService
	logger  *zap.Logger
}

type smartAgentService interface {
	ProcessLegacy(ctx context.Context, userID int64, req *service.SmartAgentChatRequest) (*service.SmartAgentChatResponse, error)
	Chat(ctx context.Context, user domain.AgentUserContext, req *domain.AgentChatRequest) (*domain.AgentChatResponse, *apperrors.AppError)
	CreateSession(ctx context.Context, user domain.AgentUserContext, metadata map[string]interface{}) (*domain.AgentSession, error)
	ListSessions(ctx context.Context, user domain.AgentUserContext, limit int) ([]domain.AgentSession, *apperrors.AppError)
	GetSession(ctx context.Context, user domain.AgentUserContext, sessionID string) (*domain.AgentSession, *apperrors.AppError)
	GetMessages(ctx context.Context, user domain.AgentUserContext, sessionID string) ([]domain.AgentMessage, *apperrors.AppError)
	DeleteSession(ctx context.Context, user domain.AgentUserContext, sessionID string) *apperrors.AppError
	UpdateSessionTitle(ctx context.Context, user domain.AgentUserContext, sessionID, title string) (*domain.AgentSession, *apperrors.AppError)
	ResolveIntent(ctx context.Context, user domain.AgentUserContext, req *domain.AgentChatRequest) (*domain.AgentIntentResult, *apperrors.AppError)
	ExecuteAction(ctx context.Context, user domain.AgentUserContext, sessionID, confirmationToken string) (*domain.AgentChatResponse, *apperrors.AppError)
	SaveFeedback(ctx context.Context, user domain.AgentUserContext, feedback *domain.AgentFeedback) *apperrors.AppError
	Capabilities() []domain.AgentCapability
}

func NewSmartAgentHandler(service smartAgentService, logger *zap.Logger) *SmartAgentHandler {
	return &SmartAgentHandler{
		service: service,
		logger:  logger,
	}
}

// Chat handles the chat with the Smart Agent
// @Summary Chat with Smart Agent
// @Description Send a message to the AI agent to perform tasks (calendar, kanban, chat)
// @Tags agent
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body service.SmartAgentChatRequest true "Chat request"
// @Success 200 {object} service.SmartAgentChatResponse
// @Router /agent/chat [post]
func (h *SmartAgentHandler) Chat(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	var req service.SmartAgentChatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.BadRequest("Invalid request body"))
		return
	}

	if req.OperationID == "" {
		if opID := c.GetHeader("X-Operation-ID"); opID != "" {
			req.OperationID = opID
		} else if opID := c.GetHeader("Idempotency-Key"); opID != "" {
			req.OperationID = opID
		}
	}

	resp, err := h.service.ProcessLegacy(c.Request.Context(), userID, &req)
	if err != nil {
		if IsAICallError(err) {
			WriteAICallError(c, err)
			return
		}

		h.logger.Warn("Legacy SmartAgent chat failed, falling back to v2", zap.Error(err))

		v2Resp, appErr := h.service.Chat(c.Request.Context(), h.buildUserContext(c), &domain.AgentChatRequest{
			SessionID: req.SessionID,
			Message:   req.Message,
		})
		if appErr != nil {
			h.logger.Error("SmartAgent chat fallback failed", zap.Error(appErr))
			h.respondError(c, apperrors.Internal("Failed to process agent request"))
			return
		}

		respondJSON(c, http.StatusOK, legacyResponseFromV2(req.Message, v2Resp))
		return
	}

	respondJSON(c, http.StatusOK, resp)
}

func legacyResponseFromV2(message string, resp *domain.AgentChatResponse) *service.SmartAgentChatResponse {
	history := []service.LLMMessage{
		{Role: string(domain.AgentRoleUser), Content: message},
		{Role: string(domain.AgentRoleAssistant), Content: resp.Answer},
	}

	return &service.SmartAgentChatResponse{
		Reply:               resp.Answer,
		ConversationHistory: history,
		SessionID:           resp.SessionID,
	}
}

func (h *SmartAgentHandler) respondError(c *gin.Context, err *apperrors.AppError) {
	respondAppError(c, err)
}

func (h *SmartAgentHandler) ChatV2(c *gin.Context) {
	user := h.buildUserContext(c)

	var req agent.ChatV2Request
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	resp, appErr := h.service.Chat(c.Request.Context(), user, &domain.AgentChatRequest{
		SessionID: req.SessionID,
		Message:   req.Message,
		InputType: req.InputType,
		Locale:    req.Locale,
		Timezone:  req.Timezone,
		Context:   req.Context,
		Options:   req.Options,
	})
	if appErr != nil {
		h.respondError(c, appErr)
		return
	}

	respondJSON(c, http.StatusOK, resp)
}

func (h *SmartAgentHandler) CreateSession(c *gin.Context) {
	user := h.buildUserContext(c)
	var req agent.CreateSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}
	session, err := h.service.CreateSession(c.Request.Context(), user, req.Metadata)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusCreated, session)
}

func (h *SmartAgentHandler) ListSessions(c *gin.Context) {
	user := h.buildUserContext(c)
	var query agent.ListSessionsQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		h.respondError(c, apperrors.Validation("invalid query params"))
		return
	}
	limit := query.Limit
	if limit <= 0 {
		limit = 100
	}
	sessions, appErr := h.service.ListSessions(c.Request.Context(), user, limit)
	if appErr != nil {
		h.respondError(c, appErr)
		return
	}
	respondJSON(c, http.StatusOK, gin.H{"sessions": sessions})
}

func (h *SmartAgentHandler) GetSession(c *gin.Context) {
	user := h.buildUserContext(c)
	session, appErr := h.service.GetSession(c.Request.Context(), user, c.Param("sessionId"))
	if appErr != nil {
		h.respondError(c, appErr)
		return
	}
	respondJSON(c, http.StatusOK, session)
}

func (h *SmartAgentHandler) GetMessages(c *gin.Context) {
	user := h.buildUserContext(c)
	messages, appErr := h.service.GetMessages(c.Request.Context(), user, c.Param("sessionId"))
	if appErr != nil {
		h.respondError(c, appErr)
		return
	}
	respondJSON(c, http.StatusOK, gin.H{"messages": messages})
}

func (h *SmartAgentHandler) DeleteSession(c *gin.Context) {
	user := h.buildUserContext(c)
	if appErr := h.service.DeleteSession(c.Request.Context(), user, c.Param("sessionId")); appErr != nil {
		h.respondError(c, appErr)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *SmartAgentHandler) UpdateSessionTitle(c *gin.Context) {
	user := h.buildUserContext(c)
	var req agent.UpdateSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}
	session, appErr := h.service.UpdateSessionTitle(c.Request.Context(), user, c.Param("sessionId"), req.Title)
	if appErr != nil {
		h.respondError(c, appErr)
		return
	}
	respondJSON(c, http.StatusOK, session)
}

func (h *SmartAgentHandler) ResolveIntent(c *gin.Context) {
	user := h.buildUserContext(c)
	var req agent.ChatV2Request
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}
	intent, appErr := h.service.ResolveIntent(c.Request.Context(), user, &domain.AgentChatRequest{
		SessionID: req.SessionID,
		Message:   req.Message,
		InputType: req.InputType,
		Locale:    req.Locale,
		Timezone:  req.Timezone,
		Context:   req.Context,
		Options:   req.Options,
	})
	if appErr != nil {
		h.respondError(c, appErr)
		return
	}
	respondJSON(c, http.StatusOK, intent)
}

func (h *SmartAgentHandler) ExecuteAction(c *gin.Context) {
	user := h.buildUserContext(c)
	var req agent.ExecuteActionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}
	resp, appErr := h.service.ExecuteAction(c.Request.Context(), user, req.SessionID, req.ConfirmationToken)
	if appErr != nil {
		h.respondError(c, appErr)
		return
	}
	respondJSON(c, http.StatusOK, resp)
}

func (h *SmartAgentHandler) SaveFeedback(c *gin.Context) {
	user := h.buildUserContext(c)
	var req agent.FeedbackRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}
	appErr := h.service.SaveFeedback(c.Request.Context(), user, &domain.AgentFeedback{
		SessionID: req.SessionID,
		MessageID: req.MessageID,
		Rating:    req.Rating,
		Comment:   req.Comment,
		Metadata:  req.Metadata,
	})
	if appErr != nil {
		h.respondError(c, appErr)
		return
	}
	c.Status(http.StatusCreated)
}

func (h *SmartAgentHandler) Capabilities(c *gin.Context) {
	respondJSON(c, http.StatusOK, gin.H{"capabilities": h.service.Capabilities()})
}

func (h *SmartAgentHandler) buildUserContext(c *gin.Context) domain.AgentUserContext {
	requestID, _ := c.Get("request_id")
	role, _ := c.Get("role")
	return domain.AgentUserContext{
		UserID:              middleware.MustGetUserID(c),
		RequestID:           toString(requestID),
		Role:                toString(role),
		IP:                  c.ClientIP(),
		UserAgent:           c.Request.UserAgent(),
		AuthorizationHeader: c.GetHeader("Authorization"),
		CookieHeader:        c.GetHeader("Cookie"),
	}
}

func toString(value interface{}) string {
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}
