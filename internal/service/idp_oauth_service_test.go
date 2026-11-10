package service

import (
	"context"
	"errors"
	"testing"

	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/idp"
	"go.uber.org/zap"
)

type idpClientStub struct {
	authorizeURLFn func(clientID, redirectURI, state string, scopes []string, lang string) (string, error)
	exchangeFn     func(ctx context.Context, code, redirectURI string) (*domain.IDPTokenResponse, error)
}

func (s *idpClientStub) AuthorizeURL(clientID, redirectURI, state string, scopes []string, lang string) (string, error) {
	return s.authorizeURLFn(clientID, redirectURI, state, scopes, lang)
}

func (s *idpClientStub) ExchangeAuthorizationCode(ctx context.Context, code, redirectURI string) (*domain.IDPTokenResponse, error) {
	return s.exchangeFn(ctx, code, redirectURI)
}

func (s *idpClientStub) RefreshAccessToken(context.Context, string) (*domain.IDPTokenResponse, error) {
	return nil, nil
}

func (s *idpClientStub) GetUserBasic(context.Context, string) (*domain.IDPUserBasicResponse, error) {
	return nil, nil
}

func (s *idpClientStub) GetUserPhone(context.Context, string) (*domain.IDPUserPhoneResponse, error) {
	return nil, nil
}

func (s *idpClientStub) GetUserEmail(context.Context, string) (*domain.IDPUserEmailResponse, error) {
	return nil, nil
}

func TestIDPOAuthServiceGetAuthorizationURLUsesDefaults(t *testing.T) {
	t.Parallel()

	stub := &idpClientStub{
		authorizeURLFn: func(clientID, redirectURI, state string, scopes []string, lang string) (string, error) {
			if clientID != "client-id" {
				t.Fatalf("unexpected clientID: %s", clientID)
			}
			if redirectURI != "https://example.com/callback" {
				t.Fatalf("unexpected redirectURI: %s", redirectURI)
			}
			if len(scopes) != 1 || scopes[0] != "user:basic:read" {
				t.Fatalf("unexpected scopes: %#v", scopes)
			}
			if lang != "kk" {
				t.Fatalf("unexpected lang: %s", lang)
			}
			if state == "" {
				t.Fatal("expected generated state")
			}
			return "https://idp.egov.kz/idp/oauth/authorize?state=" + state, nil
		},
	}

	svc := NewIDPOAuthServiceWithClient(stub, config.IDPConfig{
		ClientID:      "client-id",
		ClientSecret:  "client-secret",
		RedirectURL:   "https://example.com/callback",
		DefaultScopes: []string{"user:basic:read"},
		DefaultLang:   "kk",
	}, zap.NewNop())

	authURL, err := svc.GetAuthorizationURL("", nil, "")
	if err != nil {
		t.Fatalf("GetAuthorizationURL: %v", err)
	}
	if authURL == "" {
		t.Fatal("expected auth URL")
	}
}

func TestIDPOAuthServiceExchangeAuthorizationCodeMapsClientError(t *testing.T) {
	t.Parallel()

	stub := &idpClientStub{
		exchangeFn: func(context.Context, string, string) (*domain.IDPTokenResponse, error) {
			return nil, &idp.APIError{
				StatusCode:       400,
				ErrorCode:        "invalid_grant",
				ErrorDescription: "Redirect URI mismatch.",
			}
		},
	}

	svc := NewIDPOAuthServiceWithClient(stub, config.IDPConfig{
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		RedirectURL:  "https://example.com/callback",
	}, zap.NewNop())

	_, err := svc.ExchangeAuthorizationCode(context.Background(), "code", "")
	if err == nil {
		t.Fatal("expected error")
	}

	var appErr *apperrors.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("expected AppError, got %T", err)
	}
	if string(appErr.Code) != "EGOV_REDIRECT_MISMATCH" {
		t.Fatalf("unexpected code: %s", appErr.Code)
	}
}

func TestIDPOAuthServiceExchangeAuthorizationCodeMapsInvalidClient(t *testing.T) {
	t.Parallel()

	stub := &idpClientStub{
		exchangeFn: func(context.Context, string, string) (*domain.IDPTokenResponse, error) {
			return nil, &idp.APIError{
				StatusCode:       401,
				ErrorCode:        "invalid_client",
				ErrorDescription: "client authentication failed",
			}
		},
	}

	svc := NewIDPOAuthServiceWithClient(stub, config.IDPConfig{
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		RedirectURL:  "https://example.com/callback",
	}, zap.NewNop())

	_, err := svc.ExchangeAuthorizationCode(context.Background(), "code", "")
	if err == nil {
		t.Fatal("expected error")
	}
	var appErr *apperrors.AppError
	if !errors.As(err, &appErr) || string(appErr.Code) != "EGOV_INVALID_CLIENT" {
		t.Fatalf("unexpected error: %#v", err)
	}
}

func TestIDPOAuthServiceExchangeAuthorizationCodeMapsUnauthorizedToInvalidClient(t *testing.T) {
	t.Parallel()

	stub := &idpClientStub{
		exchangeFn: func(context.Context, string, string) (*domain.IDPTokenResponse, error) {
			return nil, &idp.APIError{
				StatusCode:       401,
				ErrorCode:        "Unauthorized",
				ErrorDescription: "Unauthorized",
			}
		},
	}

	svc := NewIDPOAuthServiceWithClient(stub, config.IDPConfig{
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		RedirectURL:  "https://example.com/callback",
	}, zap.NewNop())

	_, err := svc.ExchangeAuthorizationCode(context.Background(), "code", "")
	if err == nil {
		t.Fatal("expected error")
	}
	var appErr *apperrors.AppError
	if !errors.As(err, &appErr) || string(appErr.Code) != "EGOV_INVALID_CLIENT" {
		t.Fatalf("unexpected error: %#v", err)
	}
}

func TestIDPOAuthServiceExchangeAuthorizationCodeIgnoresRedirectOverride(t *testing.T) {
	t.Parallel()

	stub := &idpClientStub{
		exchangeFn: func(_ context.Context, _ string, redirectURI string) (*domain.IDPTokenResponse, error) {
			if redirectURI != "https://example.com/callback" {
				t.Fatalf("unexpected redirect uri: %s", redirectURI)
			}
			return &domain.IDPTokenResponse{AccessToken: "access"}, nil
		},
	}

	svc := NewIDPOAuthServiceWithClient(stub, config.IDPConfig{
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		RedirectURL:  "https://example.com/callback",
	}, zap.NewNop())

	if _, err := svc.ExchangeAuthorizationCode(context.Background(), "code", "https://evil.example.com/callback"); err != nil {
		t.Fatalf("exchange: %v", err)
	}
}
