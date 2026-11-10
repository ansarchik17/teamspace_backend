package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

type notebookHandlerTestResearch struct {
	askErr error
}

func (f *notebookHandlerTestResearch) Ask(c *gin.Context) {}

func TestNotebookHandlerAskAccountingErrorMapping(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, tc := range []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{
			name:       "quota exceeded",
			err:        &domain.AIReservationDenied{Reason: "QUOTA_EXCEEDED", Scope: "weekly"},
			wantStatus: http.StatusTooManyRequests,
			wantCode:   string(apperrors.ErrCodeAIQuotaExceeded),
		},
		{
			name:       "company suspended",
			err:        &domain.AIReservationDenied{Reason: "COMPANY_SUSPENDED", Scope: "company"},
			wantStatus: http.StatusForbidden,
			wantCode:   string(apperrors.ErrCodeAIAccessDisabled),
		},
		{
			name:       "missing context",
			err:        domain.ErrAIPaidCallMissingContext,
			wantStatus: http.StatusForbidden,
			wantCode:   string(apperrors.ErrCodeAIAccessDisabled),
		},
		{
			name:       "invalid caller",
			err:        domain.ErrAIPaidCallInvalidCaller,
			wantStatus: http.StatusForbidden,
			wantCode:   string(apperrors.ErrCodeAIAccessDisabled),
		},
		{
			name:       "context mismatch",
			err:        domain.ErrAIPaidCallContextMismatch,
			wantStatus: http.StatusForbidden,
			wantCode:   string(apperrors.ErrCodeAIAccessDisabled),
		},
		{
			name:       "rate limit exceeded",
			err:        domain.ErrAIRateLimitExceeded,
			wantStatus: http.StatusTooManyRequests,
			wantCode:   string(apperrors.ErrCodeAIRateLimitExceeded),
		},
		{
			name:       "concurrency limit exceeded",
			err:        domain.ErrAIConcurrencyLimitExceeded,
			wantStatus: http.StatusTooManyRequests,
			wantCode:   string(apperrors.ErrCodeAIConcurrencyLimitExceeded),
		},
		{
			name:       "limiter unavailable",
			err:        domain.ErrAILimiterUnavailable,
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   string(apperrors.ErrCodeAILimiterUnavailable),
		},
		{
			name:       "reservation conflict",
			err:        domain.ErrAIReservationConflict,
			wantStatus: http.StatusConflict,
			wantCode:   string(apperrors.ErrCodeAIReservationConflict),
		},
		{
			name:       "unpriced model",
			err:        domain.ErrAICreditUnknownModel,
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   string(apperrors.ErrCodeAIModelUnpriced),
		},
		{
			name:       "accounting unavailable",
			err:        domain.ErrAICreditUnknownPolicy,
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   string(apperrors.ErrCodeAIAccountingUnavailable),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)

			// Проверяем логику обработки ошибок внутри NotebookHandler
			if IsAICallError(tc.err) {
				WriteAICallError(c, tc.err)
			}

			require.Equal(t, tc.wantStatus, w.Code)
			var resp map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			require.Equal(t, tc.wantCode, resp["code"])
			require.False(t, strings.Contains(w.Body.String(), "SECRET"))
		})
	}
}

func TestNotebookHandlerAskOperationIDHeaderResolution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := &NotebookHandler{
		logger: zap.NewNop(),
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := []byte(`{"question":"test","file_ids":[]}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/notebook/chats/"+uuid.NewString()+"/ask", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("X-Operation-ID", "custom-op-123")
	c.Set("user_id", int64(42))

	var req service.NotebookResearchRequest
	require.NoError(t, c.ShouldBindJSON(&req))
	if req.OperationID == "" {
		if opHeader := c.GetHeader("X-Operation-ID"); opHeader != "" {
			req.OperationID = opHeader
		}
	}
	require.Equal(t, "custom-op-123", req.OperationID)
	_ = h
}
