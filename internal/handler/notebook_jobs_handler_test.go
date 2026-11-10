package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/service"
)

// A failed job reports the same status and body the synchronous /ask writes.
func TestNotebookJobErrorMatchesSynchronousResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		err      error
		status   int
		code     string
		sameBody bool
	}{
		{&domain.AIReservationDenied{Reason: "QUOTA_EXCEEDED", Scope: "weekly"}, http.StatusTooManyRequests, string(apperrors.ErrCodeAIQuotaExceeded), true},
		{domain.ErrAIRateLimitExceeded, http.StatusTooManyRequests, string(apperrors.ErrCodeAIRateLimitExceeded), true},
		{apperrors.Validation("question is empty"), http.StatusBadRequest, string(apperrors.ErrCodeValidation), true},
		{context.DeadlineExceeded, 0, "", false},
	} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
		(&NotebookHandler{}).respondResearchError(c, tc.err)

		got := renderNotebookJobError(context.Background(), tc.err)
		require.Equal(t, w.Code, got.HTTPStatus, tc.err.Error())
		require.Equal(t, withoutServerTime(t, w.Body.Bytes()), withoutServerTime(t, got.Body))
		require.NotEmpty(t, got.Message)
		if tc.sameBody {
			require.Equal(t, tc.status, got.HTTPStatus)
			require.Equal(t, tc.code, got.Code)
		}
	}
}

func withoutServerTime(t *testing.T, body []byte) map[string]any {
	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))
	if details, ok := payload["details"].(map[string]any); ok {
		delete(details, "server_time")
	}
	return payload
}

func TestNotebookJobRoutesValidateIDsAndOperationHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &NotebookHandler{jobs: &service.NotebookResearchJobService{}}
	for _, path := range []string{"/chats/bad/ask/jobs/" + uuid.NewString(), "/chats/" + uuid.NewString() + "/ask/jobs/bad"} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, path, nil)
		c.Params = gin.Params{{Key: "id", Value: "bad"}, {Key: "job_id", Value: "bad"}}
		if path[7:10] != "bad" {
			c.Params[0].Value = uuid.NewString()
		}
		h.GetAskJob(c)
		require.Equal(t, http.StatusBadRequest, w.Code, path)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte(`{"question":"q"}`)))
	c.Request.Header.Set("Idempotency-Key", "idem-1")
	var req service.NotebookResearchRequest
	require.NoError(t, json.NewDecoder(c.Request.Body).Decode(&req))
	notebookOperationID(c, &req)
	require.Equal(t, "idem-1", req.OperationID)
	req.OperationID = "body-op"
	notebookOperationID(c, &req)
	require.Equal(t, "body-op", req.OperationID)
}
