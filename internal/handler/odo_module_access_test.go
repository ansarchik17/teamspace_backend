package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/auth"
	"go.uber.org/zap"
)

func TestODOHandoffRechecksAccessBeforeReturningToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := auth.NewODOHandoffStore(nil)
	h := NewAuthHandler(nil, nil, nil, zap.NewNop())
	h.SetODOHandoffStore(store)
	allowed := true
	h.SetODOHandoffAccessCheck(func(_ context.Context, token string) error {
		require.Equal(t, "secret-workspace-token", token)
		if !allowed {
			return apperrors.Forbidden("module disabled")
		}
		return nil
	})
	router := gin.New()
	router.POST("/redeem", h.RedeemODOHandoff)
	code, err := store.Create(context.Background(), "secret-workspace-token")
	require.NoError(t, err)
	allowed = false
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/redeem", strings.NewReader(`{"code":"`+code+`"}`)))
	require.Equal(t, http.StatusForbidden, w.Code)
	require.NotContains(t, w.Body.String(), "secret-workspace-token")
	_, err = store.Redeem(context.Background(), code)
	require.ErrorIs(t, err, auth.ErrODOHandoffInvalid)
	allowed = true
	code, err = store.Create(context.Background(), "secret-workspace-token")
	require.NoError(t, err)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/redeem", strings.NewReader(`{"code":"`+code+`"}`)))
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "secret-workspace-token")
}
