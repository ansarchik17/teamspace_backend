package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

type idpHandlerClientStub struct{}

func (s *idpHandlerClientStub) AuthorizeURL(clientID, redirectURI, state string, scopes []string, lang string) (string, error) {
	return "https://idp.egov.kz/idp/oauth/authorize?state=" + state, nil
}

func (s *idpHandlerClientStub) ExchangeAuthorizationCode(context.Context, string, string) (*domain.IDPTokenResponse, error) {
	return &domain.IDPTokenResponse{AccessToken: "access"}, nil
}

func (s *idpHandlerClientStub) RefreshAccessToken(context.Context, string) (*domain.IDPTokenResponse, error) {
	return &domain.IDPTokenResponse{AccessToken: "refreshed"}, nil
}

func (s *idpHandlerClientStub) GetUserBasic(context.Context, string) (*domain.IDPUserBasicResponse, error) {
	return &domain.IDPUserBasicResponse{Type: "INDIVIDUAL"}, nil
}

func (s *idpHandlerClientStub) GetUserPhone(context.Context, string) (*domain.IDPUserPhoneResponse, error) {
	return &domain.IDPUserPhoneResponse{Phone: "77001234567"}, nil
}

func (s *idpHandlerClientStub) GetUserEmail(context.Context, string) (*domain.IDPUserEmailResponse, error) {
	return &domain.IDPUserEmailResponse{Email: "user@example.com"}, nil
}

func TestIDPHandlerGetAuthURL(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	svc := service.NewIDPOAuthServiceWithClient(&idpHandlerClientStub{}, config.IDPConfig{
		ClientID:      "client-id",
		ClientSecret:  "client-secret",
		RedirectURL:   "https://example.com/callback",
		DefaultScopes: []string{"user:basic:read"},
		DefaultLang:   "kk",
	}, zap.NewNop())
	h := NewIDPHandler(svc, zap.NewNop())

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/idp/auth-url?state=state-1", nil)

	h.GetAuthURL(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", recorder.Code)
	}
}

func TestIDPHandlerGetUserBasicRequiresBearerToken(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	svc := service.NewIDPOAuthServiceWithClient(&idpHandlerClientStub{}, config.IDPConfig{
		ClientID:     "client-id",
		ClientSecret: "client-secret",
	}, zap.NewNop())
	h := NewIDPHandler(svc, zap.NewNop())

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/idp/user/basic", nil)

	h.GetUserBasic(ctx)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("unexpected status: %d", recorder.Code)
	}
}
