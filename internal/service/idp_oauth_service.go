package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"strings"

	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/idp"
	"go.uber.org/zap"
)

type IDPClient interface {
	AuthorizeURL(clientID, redirectURI, state string, scopes []string, lang string) (string, error)
	ExchangeAuthorizationCode(ctx context.Context, code, redirectURI string) (*domain.IDPTokenResponse, error)
	RefreshAccessToken(ctx context.Context, refreshToken string) (*domain.IDPTokenResponse, error)
	GetUserBasic(ctx context.Context, accessToken string) (*domain.IDPUserBasicResponse, error)
	GetUserPhone(ctx context.Context, accessToken string) (*domain.IDPUserPhoneResponse, error)
	GetUserEmail(ctx context.Context, accessToken string) (*domain.IDPUserEmailResponse, error)
}

type IDPOAuthService struct {
	client IDPClient
	cfg    config.IDPConfig
	logger *zap.Logger
}

func NewIDPOAuthService(cfg config.IDPConfig, logger *zap.Logger) *IDPOAuthService {
	client := idp.NewClient(cfg.BaseURL, cfg.ClientID, cfg.ClientSecret, cfg.RequestTimeout, logger)
	return NewIDPOAuthServiceWithClient(client, cfg, logger)
}

func NewIDPOAuthServiceWithClient(client IDPClient, cfg config.IDPConfig, logger *zap.Logger) *IDPOAuthService {
	return &IDPOAuthService{
		client: client,
		cfg:    cfg,
		logger: logger.With(zap.String("service", "idp_oauth")),
	}
}

func (s *IDPOAuthService) GetAuthorizationURL(state string, scopes []string, lang string) (string, error) {
	if err := s.validateClientConfiguration(); err != nil {
		return "", err
	}

	state = strings.TrimSpace(state)
	if state == "" {
		generated, err := generateOAuthState()
		if err != nil {
			return "", apperrors.InternalWithErr("failed to generate oauth state", err)
		}
		state = generated
	}

	scopes = normalizeScopes(scopes, s.cfg.DefaultScopes)
	if len(scopes) == 0 {
		return "", apperrors.Validation("scope is required")
	}

	redirectURI := strings.TrimSpace(s.cfg.RedirectURL)
	if redirectURI == "" {
		return "", apperrors.Validation("redirect_uri is required")
	}

	lang = strings.TrimSpace(lang)
	if lang == "" {
		lang = strings.TrimSpace(s.cfg.DefaultLang)
	}

	authURL, err := s.client.AuthorizeURL(s.cfg.ClientID, redirectURI, state, scopes, lang)
	if err != nil {
		return "", apperrors.InternalWithErr("failed to build idp authorization url", err)
	}
	return authURL, nil
}

func (s *IDPOAuthService) ExchangeAuthorizationCode(ctx context.Context, code, redirectURI string) (*domain.IDPTokenResponse, error) {
	if err := s.validateClientConfiguration(); err != nil {
		return nil, err
	}

	code = strings.TrimSpace(code)
	if code == "" {
		return nil, apperrors.Validation("code is required")
	}

	redirectURI = s.resolveRedirectURI(redirectURI)
	if redirectURI == "" {
		return nil, apperrors.Validation("redirect_uri is required")
	}

	token, err := s.client.ExchangeAuthorizationCode(ctx, code, redirectURI)
	if err != nil {
		return nil, s.mapClientError("failed to exchange authorization code", err)
	}
	return token, nil
}

func (s *IDPOAuthService) RefreshAccessToken(ctx context.Context, refreshToken string) (*domain.IDPTokenResponse, error) {
	if err := s.validateClientConfiguration(); err != nil {
		return nil, err
	}

	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" {
		return nil, apperrors.Validation("refresh_token is required")
	}

	token, err := s.client.RefreshAccessToken(ctx, refreshToken)
	if err != nil {
		return nil, s.mapClientError("failed to refresh access token", err)
	}
	return token, nil
}

func (s *IDPOAuthService) GetUserBasic(ctx context.Context, accessToken string) (*domain.IDPUserBasicResponse, error) {
	response, err := s.client.GetUserBasic(ctx, strings.TrimSpace(accessToken))
	if err != nil {
		return nil, s.mapClientError("failed to get user basic info", err)
	}
	return response, nil
}

func (s *IDPOAuthService) GetUserPhone(ctx context.Context, accessToken string) (*domain.IDPUserPhoneResponse, error) {
	response, err := s.client.GetUserPhone(ctx, strings.TrimSpace(accessToken))
	if err != nil {
		return nil, s.mapClientError("failed to get user phone", err)
	}
	return response, nil
}

func (s *IDPOAuthService) GetUserEmail(ctx context.Context, accessToken string) (*domain.IDPUserEmailResponse, error) {
	response, err := s.client.GetUserEmail(ctx, strings.TrimSpace(accessToken))
	if err != nil {
		return nil, s.mapClientError("failed to get user email", err)
	}
	return response, nil
}

func (s *IDPOAuthService) validateClientConfiguration() error {
	if strings.TrimSpace(s.cfg.ClientID) == "" {
		return apperrors.Internal("idp client is not configured: missing client_id")
	}
	if strings.TrimSpace(s.cfg.ClientSecret) == "" {
		return apperrors.Internal("idp client is not configured: missing client_secret")
	}
	return nil
}

func (s *IDPOAuthService) resolveRedirectURI(redirectURI string) string {
	return strings.TrimSpace(s.cfg.RedirectURL)
}

func (s *IDPOAuthService) mapClientError(message string, err error) error {
	apiErr, ok := err.(*idp.APIError)
	if !ok {
		if strings.Contains(strings.ToLower(err.Error()), "timeout") {
			return apperrors.New("EGOV_SERVICE_UNAVAILABLE", message, http.StatusBadGateway).
				WithUserMessage("Сервис eGov временно недоступен. Попробуйте позже.")
		}
		return apperrors.InternalWithErr(message, err)
	}

	switch apiErr.StatusCode {
	case http.StatusBadRequest:
		switch {
		case strings.EqualFold(apiErr.ErrorCode, "invalid_client"):
			return apperrors.New("EGOV_INVALID_CLIENT", message, http.StatusUnauthorized).
				WithUserMessage("Клиент eGov настроен некорректно. Обратитесь к администратору.")
		case strings.EqualFold(apiErr.ErrorCode, "invalid_grant"):
			if isRedirectMismatch(apiErr.ErrorDescription) || isRedirectMismatch(apiErr.RawBody) {
				return apperrors.New("EGOV_REDIRECT_MISMATCH", message, http.StatusBadRequest).
					WithUserMessage("Callback URL не зарегистрирован в eGov. Обратитесь к администратору.")
			}
			return apperrors.New("EGOV_INVALID_GRANT", message, http.StatusUnauthorized).
				WithUserMessage("Код авторизации eGov недействителен или истек.")
		}
		appErr := apperrors.BadRequest(apiErr.ErrorDescription)
		if appErr.Message == "" {
			appErr.Message = message
		}
		if apiErr.ErrorCode != "" {
			appErr = appErr.WithDetail("error", apiErr.ErrorCode)
		}
		if apiErr.RequiredScope != "" {
			appErr = appErr.WithDetail("scope", apiErr.RequiredScope)
		}
		return appErr
	case http.StatusUnauthorized:
		switch {
		case strings.EqualFold(apiErr.ErrorCode, "invalid_client"):
			return apperrors.New("EGOV_INVALID_CLIENT", message, http.StatusUnauthorized).
				WithUserMessage("Клиент eGov настроен некорректно. Обратитесь к администратору.")
		case strings.EqualFold(apiErr.ErrorCode, "invalid_grant"):
			return apperrors.New("EGOV_INVALID_GRANT", message, http.StatusUnauthorized).
				WithUserMessage("Код авторизации eGov недействителен или истек.")
		}
		return apperrors.New("EGOV_INVALID_CLIENT", message, http.StatusUnauthorized).
			WithUserMessage("Клиент eGov настроен некорректно. Обратитесь к администратору.")
	case http.StatusForbidden:
		appErr := apperrors.Forbidden(apiErr.ErrorDescription)
		if apiErr.ErrorCode != "" {
			appErr = appErr.WithDetail("error", apiErr.ErrorCode)
		}
		if apiErr.RequiredScope != "" {
			appErr = appErr.WithDetail("scope", apiErr.RequiredScope)
		}
		return appErr
	default:
		if strings.Contains(strings.ToLower(apiErr.RawBody), "timeout") {
			return apperrors.New("EGOV_SERVICE_UNAVAILABLE", message, http.StatusBadGateway).
				WithUserMessage("Сервис eGov временно недоступен. Попробуйте позже.")
		}
		return apperrors.InternalWithErr(message, err)
	}
}

func isRedirectMismatch(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return false
	}
	return strings.Contains(value, "redirect") || strings.Contains(value, "callback") || strings.Contains(value, "mismatch")
}

func normalizeScopes(scopes, fallback []string) []string {
	source := scopes
	if len(source) == 0 {
		source = fallback
	}

	seen := make(map[string]struct{}, len(source))
	result := make([]string, 0, len(source))
	for _, scope := range source {
		for _, part := range strings.FieldsFunc(scope, func(r rune) bool {
			return r == ',' || r == ' '
		}) {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if _, exists := seen[part]; exists {
				continue
			}
			seen[part] = struct{}{}
			result = append(result, part)
		}
	}
	return result
}

func generateOAuthState() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
