package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	legacyservice "github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

func TestSmartAgentHandler_LegacyChatContractStillWorks(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeSmartAgentService{
		legacyResp: &legacyservice.SmartAgentChatResponse{
			Reply:     "legacy",
			SessionID: "s1",
		},
	}
	h := NewSmartAgentHandler(svc, zap.NewNop())

	r := gin.New()
	r.POST("/agent/chat", withUserContext(h.Chat, 10))

	body := bytes.NewBufferString(`{"message":"hello"}`)
	req := httptest.NewRequest(http.MethodPost, "/agent/chat", body)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	r.ServeHTTP(resp, req)

	require.Equal(t, http.StatusOK, resp.Code)
	var payload legacyservice.SmartAgentChatResponse
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &payload))
	require.Equal(t, "legacy", payload.Reply)
}

func TestSmartAgentHandler_ChatV2ExtendedResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeSmartAgentService{
		chatResp: &domain.AgentChatResponse{
			Answer:               "ok",
			SessionID:            "s1",
			RequiresConfirmation: true,
			ConfirmationToken:    "token",
		},
	}
	h := NewSmartAgentHandler(svc, zap.NewNop())
	r := gin.New()
	r.POST("/agent/chat/v2", withUserContext(h.ChatV2, 10))

	body := bytes.NewBufferString(`{"message":"create reminder"}`)
	req := httptest.NewRequest(http.MethodPost, "/agent/chat/v2", body)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	r.ServeHTTP(resp, req)

	require.Equal(t, http.StatusOK, resp.Code)
	var payload map[string]interface{}
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &payload))
	require.Equal(t, "ok", payload["answer"])
	require.Equal(t, "token", payload["confirmation_token"])
}

func TestSmartAgentHandler_LegacyChatFallsBackToV2(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeSmartAgentService{
		legacyErr: errors.New("OPENAI_API_KEY missing"),
		chatResp: &domain.AgentChatResponse{
			Answer:    "fallback-ok",
			SessionID: "s2",
		},
	}
	h := NewSmartAgentHandler(svc, zap.NewNop())

	r := gin.New()
	r.POST("/agent/chat", withUserContext(h.Chat, 10))

	body := bytes.NewBufferString(`{"message":"hello"}`)
	req := httptest.NewRequest(http.MethodPost, "/agent/chat", body)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	r.ServeHTTP(resp, req)

	require.Equal(t, http.StatusOK, resp.Code)
	var payload legacyservice.SmartAgentChatResponse
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &payload))
	require.Equal(t, "fallback-ok", payload.Reply)
	require.Equal(t, "s2", payload.SessionID)
	require.Len(t, payload.ConversationHistory, 2)
}

func TestSmartAgentHandler_GetSessionUnauthorizedUserCannotAccessAnotherSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeSmartAgentService{
		getSessionErr: apperrors.Forbidden("forbidden"),
	}
	h := NewSmartAgentHandler(svc, zap.NewNop())
	r := gin.New()
	r.GET("/agent/sessions/:sessionId", withUserContext(h.GetSession, 99))

	req := httptest.NewRequest(http.MethodGet, "/agent/sessions/s1", nil)
	resp := httptest.NewRecorder()

	r.ServeHTTP(resp, req)

	require.Equal(t, http.StatusForbidden, resp.Code)
}

func withUserContext(next gin.HandlerFunc, userID int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set("user_id", userID)
		next(c)
	}
}

type fakeSmartAgentService struct {
	legacyResp     *legacyservice.SmartAgentChatResponse
	legacyErr      error
	chatResp       *domain.AgentChatResponse
	getSessionResp *domain.AgentSession
	getSessionErr  *apperrors.AppError
}

func (f *fakeSmartAgentService) ProcessLegacy(_ context.Context, _ int64, _ *legacyservice.SmartAgentChatRequest) (*legacyservice.SmartAgentChatResponse, error) {
	return f.legacyResp, f.legacyErr
}

func (f *fakeSmartAgentService) Chat(_ context.Context, _ domain.AgentUserContext, _ *domain.AgentChatRequest) (*domain.AgentChatResponse, *apperrors.AppError) {
	return f.chatResp, nil
}

func (f *fakeSmartAgentService) CreateSession(_ context.Context, _ domain.AgentUserContext, _ map[string]interface{}) (*domain.AgentSession, error) {
	return &domain.AgentSession{ID: "s1", UserID: 10}, nil
}

func (f *fakeSmartAgentService) ListSessions(_ context.Context, _ domain.AgentUserContext, _ int) ([]domain.AgentSession, *apperrors.AppError) {
	return nil, nil
}

func (f *fakeSmartAgentService) GetSession(_ context.Context, _ domain.AgentUserContext, _ string) (*domain.AgentSession, *apperrors.AppError) {
	return f.getSessionResp, f.getSessionErr
}

func (f *fakeSmartAgentService) GetMessages(_ context.Context, _ domain.AgentUserContext, _ string) ([]domain.AgentMessage, *apperrors.AppError) {
	return nil, nil
}

func (f *fakeSmartAgentService) DeleteSession(_ context.Context, _ domain.AgentUserContext, _ string) *apperrors.AppError {
	return nil
}

func (f *fakeSmartAgentService) UpdateSessionTitle(_ context.Context, _ domain.AgentUserContext, _ string, title string) (*domain.AgentSession, *apperrors.AppError) {
	return &domain.AgentSession{ID: "s1", Metadata: map[string]interface{}{"title": title}}, nil
}

func (f *fakeSmartAgentService) ResolveIntent(_ context.Context, _ domain.AgentUserContext, _ *domain.AgentChatRequest) (*domain.AgentIntentResult, *apperrors.AppError) {
	return &domain.AgentIntentResult{Name: domain.AgentIntentHelp}, nil
}

func (f *fakeSmartAgentService) ExecuteAction(_ context.Context, _ domain.AgentUserContext, _, _ string) (*domain.AgentChatResponse, *apperrors.AppError) {
	return &domain.AgentChatResponse{Answer: "done"}, nil
}

func (f *fakeSmartAgentService) SaveFeedback(_ context.Context, _ domain.AgentUserContext, _ *domain.AgentFeedback) *apperrors.AppError {
	return nil
}

func (f *fakeSmartAgentService) Capabilities() []domain.AgentCapability {
	return []domain.AgentCapability{{Name: "help"}}
}
