package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"github.com/west6ide/workspace-backend/internal/repository"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

func TestCurrentIdentityProviderRoutesRejectManagement(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// Nil database and Keycloak deliberately prove rejection happens before
	// reading provider settings or contacting the identity service.
	logger := zap.NewNop()
	repo := repository.NewOrganizationIdentityProviderRepository(nil, logger)
	svc := service.NewOrganizationIdentityProviderService(repo, nil, "test", logger)
	handler := NewOrganizationIdentityProviderHandler(svc, logger)
	router := gin.New()
	router.Use(middleware.Localization(nil), func(c *gin.Context) { c.Set("user_id", int64(2)); c.Next() })
	path := "/api/v1/organizations/current/identity-providers"
	router.GET(path, handler.List)
	router.POST(path, handler.Create)
	router.PATCH(path+"/:provider_id", handler.Update)
	router.DELETE(path+"/:provider_id", handler.Delete)
	for _, language := range []string{"ru", "kk"} {
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodDelete} {
			t.Run(language+"/"+method, func(t *testing.T) {
				url := path
				if method == http.MethodPatch || method == http.MethodDelete {
					url += "/00000000-0000-4000-8000-000000000001"
				}
				request := httptest.NewRequest(method, url, strings.NewReader(`{"client_secret":"must-not-leak"}`))
				request.Header.Set("Accept-Language", language)
				request.Header.Set("Content-Type", "application/json")
				recorder := httptest.NewRecorder()
				router.ServeHTTP(recorder, request)
				require.Equal(t, http.StatusForbidden, recorder.Code)
				require.Equal(t, language, recorder.Header().Get("Content-Language"))
				require.Contains(t, recorder.Body.String(), `"code":"FORBIDDEN"`)
				require.NotContains(t, recorder.Body.String(), "must-not-leak")
				require.NotContains(t, recorder.Body.String(), "require platform administration")
			})
		}
	}
}
