package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

type fileSearchHTTPBackend struct {
	calls int
	user  int64
	err   error
}

func (f *fileSearchHTTPBackend) Search(_ context.Context, user int64, req domain.DriveSearchQueryRequest) (*domain.DriveSearchQueryResponse, error) {
	f.calls++
	f.user = user
	if f.err != nil {
		return nil, f.err
	}
	return &domain.DriveSearchQueryResponse{Question: req.Question, Indexing: true, Incomplete: true, Sources: []domain.DriveSearchSource{}}, nil
}
func (f *fileSearchHTTPBackend) Enqueue(context.Context, uuid.UUID) error  { return nil }
func (f *fileSearchHTTPBackend) Delete(context.Context, []uuid.UUID) error { return nil }
func TestFileSearchHTTPContractPreservesIdentityAndErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	backend := &fileSearchHTTPBackend{}
	facade := service.NewDriveSearchService(config.DriveSearchConfig{}, zap.NewNop())
	facade.SetLocalRetrieval(backend)
	handler := NewDriveHandler(nil, facade, "", "", zap.NewNop())
	for _, tc := range []struct {
		name, body string
		user       int64
		err        error
		status     int
		dispatch   bool
	}{
		{name: "authenticated", body: `{"question":"договор","user_id":7}`, user: 42, status: 200, dispatch: true},
		{name: "unauthenticated", body: `{"question":"договор"}`, status: 401},
		{name: "malformed scope", body: `{"question":"договор","file_ids":["invalid"]}`, user: 42, status: 400},
		{name: "forbidden", body: `{"question":"договор"}`, user: 42, err: apperrors.Forbidden("selected file unavailable"), status: 403, dispatch: true},
		{name: "internal", body: `{"question":"договор"}`, user: 42, err: fmt.Errorf("private SQL details"), status: 500, dispatch: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend.err = tc.err
			before := backend.calls
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			if tc.user > 0 {
				c.Set("user_id", tc.user)
			}
			c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/drive/search/query", strings.NewReader(tc.body))
			c.Request.Header.Set("Content-Type", "application/json")
			handler.SearchContent(c)
			require.Equal(t, tc.status, recorder.Code, recorder.Body.String())
			if tc.dispatch {
				require.Equal(t, before+1, backend.calls)
				require.Equal(t, tc.user, backend.user)
			} else {
				require.Equal(t, before, backend.calls)
			}
			require.NotContains(t, recorder.Body.String(), "private SQL details")
			if tc.status == 200 {
				require.Contains(t, recorder.Body.String(), `"indexing":true`)
			}
		})
	}
}
