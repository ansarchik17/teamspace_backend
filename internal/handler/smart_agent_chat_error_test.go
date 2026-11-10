package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

type mockSmartAgentService struct {
	processLegacyFunc func(ctx context.Context, userID int64, req *service.SmartAgentChatRequest) (*service.SmartAgentChatResponse, error)
	chatV2Calls       int
}

func (m *mockSmartAgentService) ProcessLegacy(ctx context.Context, userID int64, req *service.SmartAgentChatRequest) (*service.SmartAgentChatResponse, error) {
	if m.processLegacyFunc != nil {
		return m.processLegacyFunc(ctx, userID, req)
	}
	return nil, nil
}

func (m *mockSmartAgentService) Chat(ctx context.Context, user domain.AgentUserContext, req *domain.AgentChatRequest) (*domain.AgentChatResponse, *apperrors.AppError) {
	m.chatV2Calls++
	return &domain.AgentChatResponse{Answer: "v2 fallback answer", SessionID: "v2-session"}, nil
}

func (m *mockSmartAgentService) CreateSession(context.Context, domain.AgentUserContext, map[string]interface{}) (*domain.AgentSession, error) {
	return nil, nil
}
func (m *mockSmartAgentService) ListSessions(context.Context, domain.AgentUserContext, int) ([]domain.AgentSession, *apperrors.AppError) {
	return nil, nil
}
func (m *mockSmartAgentService) GetSession(context.Context, domain.AgentUserContext, string) (*domain.AgentSession, *apperrors.AppError) {
	return nil, nil
}
func (m *mockSmartAgentService) GetMessages(context.Context, domain.AgentUserContext, string) ([]domain.AgentMessage, *apperrors.AppError) {
	return nil, nil
}
func (m *mockSmartAgentService) DeleteSession(context.Context, domain.AgentUserContext, string) *apperrors.AppError {
	return nil
}
func (m *mockSmartAgentService) UpdateSessionTitle(context.Context, domain.AgentUserContext, string, string) (*domain.AgentSession, *apperrors.AppError) {
	return nil, nil
}
func (m *mockSmartAgentService) ResolveIntent(context.Context, domain.AgentUserContext, *domain.AgentChatRequest) (*domain.AgentIntentResult, *apperrors.AppError) {
	return nil, nil
}
func (m *mockSmartAgentService) ExecuteAction(context.Context, domain.AgentUserContext, string, string) (*domain.AgentChatResponse, *apperrors.AppError) {
	return nil, nil
}
func (m *mockSmartAgentService) SaveFeedback(context.Context, domain.AgentUserContext, *domain.AgentFeedback) *apperrors.AppError {
	return nil
}
func (m *mockSmartAgentService) Capabilities() []domain.AgentCapability {
	return nil
}

func TestSmartAgentHandlerBudgetErrorsDoNotFallbackToV2(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{
			name:       "quota exceeded returns 429 without fallback",
			err:        &domain.AIReservationDenied{Reason: "DAILY_EXHAUSTED", Scope: "daily"},
			wantStatus: http.StatusTooManyRequests,
		},
		{
			name:       "access denied returns 403 without fallback",
			err:        &domain.AIReservationDenied{Reason: "AI_ACCESS_DENIED", Scope: "account"},
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "missing accounting context returns 403 without fallback",
			err:        domain.ErrAIPaidCallMissingContext,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "invalid caller returns 403 without fallback",
			err:        domain.ErrAIPaidCallInvalidCaller,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "context mismatch returns 403 without fallback",
			err:        domain.ErrAIPaidCallContextMismatch,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "reservation conflict returns 409 without fallback",
			err:        domain.ErrAIReservationConflict,
			wantStatus: http.StatusConflict,
		},
		{
			name:       "rate limit returns 429 without fallback",
			err:        domain.ErrAIRateLimitExceeded,
			wantStatus: http.StatusTooManyRequests,
		},
		{
			name:       "concurrency limit returns 429 without fallback",
			err:        domain.ErrAIConcurrencyLimitExceeded,
			wantStatus: http.StatusTooManyRequests,
		},
		{
			name:       "limiter unavailable returns 503 without fallback",
			err:        domain.ErrAILimiterUnavailable,
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name:       "unpriced model returns 503 without fallback",
			err:        domain.ErrAICreditUnknownModel,
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name:       "unknown policy returns 503 without fallback",
			err:        domain.ErrAICreditUnknownPolicy,
			wantStatus: http.StatusServiceUnavailable,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := &mockSmartAgentService{
				processLegacyFunc: func(ctx context.Context, userID int64, req *service.SmartAgentChatRequest) (*service.SmartAgentChatResponse, error) {
					return nil, tc.err
				},
			}
			h := NewSmartAgentHandler(svc, zap.NewNop())

			router := gin.New()
			router.POST("/agent/chat", func(c *gin.Context) {
				c.Set("user_id", int64(123))
				h.Chat(c)
			})

			body, _ := json.Marshal(service.SmartAgentChatRequest{Message: "Тест"})
			req := httptest.NewRequest("POST", "/agent/chat", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			require.Equal(t, tc.wantStatus, w.Code)
			// Проверяем, что fallback на v2 НЕ был вызван
			require.Zero(t, svc.chatV2Calls, "fallback to v2 must not be triggered for budget errors")
		})
	}
}

func TestSmartAgentHandlerPassesOperationIDHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var capturedOpID string
	svc := &mockSmartAgentService{
		processLegacyFunc: func(ctx context.Context, userID int64, req *service.SmartAgentChatRequest) (*service.SmartAgentChatResponse, error) {
			capturedOpID = req.OperationID
			return &service.SmartAgentChatResponse{Reply: "ok", SessionID: "s1"}, nil
		},
	}
	h := NewSmartAgentHandler(svc, zap.NewNop())

	router := gin.New()
	router.POST("/agent/chat", func(c *gin.Context) {
		c.Set("user_id", int64(123))
		h.Chat(c)
	})

	body, _ := json.Marshal(service.SmartAgentChatRequest{Message: "Привет"})
	req := httptest.NewRequest("POST", "/agent/chat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Operation-ID", "custom-op-id-999")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "custom-op-id-999", capturedOpID)
}
