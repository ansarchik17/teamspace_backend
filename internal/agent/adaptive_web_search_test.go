package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestWebSearchTool_BlocksUnaccountedAdaptiveSearch(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
		require.Equal(t, "access_token=test-cookie", r.Header.Get("Cookie"))

		var payload adaptiveWebSearchRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		require.Equal(t, "кто такой Жаслан Мадиев", payload.Question)
		require.True(t, payload.Learn)
		require.True(t, payload.IncludeWebCacheInRAG)
		require.True(t, payload.Fast)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"answer":"Жаслан Мадиев — государственный деятель Казахстана.",
			"used_web":true,
			"search_backend":"adaptive",
			"sources":[{"title":"Example","url":"https://example.com","content":"bio"}],
			"es_chunks_used":12,
			"searxng_hits":3,
			"gatekeeper":{"force_web_route":true},
			"indexed_chunks":0,
			"fast_mode":true,
			"route":"adaptive"
		}`))
	}))
	defer server.Close()

	registry := NewToolRegistry(BuildDefaultTools(ToolDependencies{
		AgentConfig: &config.AgentConfig{
			AdaptiveWebSearchURL: server.URL,
			ExternalTimeout:      time.Second,
		},
	})...)

	tool := registry.FindByIntent(domain.AgentIntentWebSearch)
	require.NotNil(t, tool)

	result, appErr := tool.Execute(context.Background(), ToolRequest{
		User: domain.AgentUserContext{
			UserID:              10,
			AuthorizationHeader: "Bearer test-token",
			CookieHeader:        "access_token=test-cookie",
		},
		Request: &domain.AgentChatRequest{
			Message: "Найди в интернете кто такой Жаслан Мадиев",
		},
		Entities: map[string]interface{}{
			"query": "кто такой Жаслан Мадиев",
		},
	})

	require.NotNil(t, appErr)
	require.Equal(t, apperrors.ErrCodeAIAccountingUnavailable, appErr.Code)
	require.Nil(t, result)
	require.Zero(t, calls, "no request may reach an unaccounted adaptive provider")
}
