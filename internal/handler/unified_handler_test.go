package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

type unifiedTimingStub struct {
	t   *testing.T
	err error
}

func (s unifiedTimingStub) GetUnifiedChatsTimed(_ context.Context, userID int64, limit, offset int) ([]*domain.ConversationView, service.UnifiedChatsTiming, error) {
	require.Equal(s.t, int64(42), userID)
	require.Equal(s.t, 10, limit)
	require.Equal(s.t, 2, offset)
	return []*domain.ConversationView{}, service.UnifiedChatsTiming{
		Personal: 12*time.Millisecond + 500*time.Microsecond,
		Groups:   8 * time.Millisecond, Channels: 3 * time.Millisecond,
		Pinned: time.Millisecond, Fetch: 13 * time.Millisecond, Total: 15 * time.Millisecond,
	}, s.err
}

func TestUnifiedHandlerServerTiming(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
	}{
		{"success", nil, http.StatusOK},
		{"failure", errors.New("database unavailable"), http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := gin.New()
			r.Use(func(c *gin.Context) { c.Set("user_id", int64(42)); c.Next() })
			h := NewUnifiedHandler(unifiedTimingStub{t: t, err: tc.err}, zap.NewNop())
			r.GET("/chats/unified", h.GetUnifiedChats)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/chats/unified?limit=10&offset=2", nil))
			require.Equal(t, tc.status, w.Code)
			require.Equal(t, "personal;dur=12.50, groups;dur=8.00, channels;dur=3.00, pinned;dur=1.00, fetch;dur=13.00, app;dur=15.00", w.Header().Get("Server-Timing"))
			if tc.err == nil {
				require.JSONEq(t, "[]", w.Body.String())
			}
		})
	}
}
