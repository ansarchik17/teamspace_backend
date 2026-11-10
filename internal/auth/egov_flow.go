package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/idp"
	"go.uber.org/zap"
)

const (
	egovScopeBasic = "user:basic:read"
	egovScopePhone = "user:phone:read"
	egovScopeEmail = "user:email:read"
)

type EgovIDPClient interface {
	AuthorizeURL(clientID, redirectURI, state string, scopes []string, lang string) (string, error)
	ExchangeAuthorizationCode(ctx context.Context, code, redirectURI string) (*domain.IDPTokenResponse, error)
	GetUserBasic(ctx context.Context, accessToken string) (*domain.IDPUserBasicResponse, error)
	GetUserPhone(ctx context.Context, accessToken string) (*domain.IDPUserPhoneResponse, error)
	GetUserEmail(ctx context.Context, accessToken string) (*domain.IDPUserEmailResponse, error)
}

type EgovFlowService struct {
	cfg          config.IDPConfig
	client       EgovIDPClient
	stateStore   EgovStateStore
	authService  *Service
	tokenManager *TokenManager
	logger       *zap.Logger
}

func NewEgovFlowService(cfg config.IDPConfig, authService *Service, tokenManager *TokenManager, logger *zap.Logger, store EgovStateStore) *EgovFlowService {
	client := idp.NewClient(cfg.BaseURL, cfg.ClientID, cfg.ClientSecret, cfg.RequestTimeout, logger)
	return NewEgovFlowServiceWithClient(cfg, authService, tokenManager, logger, store, client)
}

func NewEgovFlowServiceWithClient(cfg config.IDPConfig, authService *Service, tokenManager *TokenManager, logger *zap.Logger, store EgovStateStore, client EgovIDPClient) *EgovFlowService {
	if store == nil {
		store = NewInMemoryEgovStateStore()
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &EgovFlowService{
		cfg:          cfg,
		client:       client,
		stateStore:   store,
		authService:  authService,
		tokenManager: tokenManager,
		logger:       logger.With(zap.String("component", "egov_flow")),
	}
}

func (s *EgovFlowService) Start(ctx context.Context, lang, redirectTarget string) (*domain.EgovStartResponse, error) {
	if err := s.validateConfiguration(); err != nil {
		return nil, err
	}

	lang, err := validateEgovLang(lang, s.cfg.DefaultLang)
	if err != nil {
		return nil, err
	}
	scopes := normalizeEgovScopes(s.cfg.DefaultScopes)
	if len(scopes) == 0 {
		return nil, apperrors.Validation("egov scopes are not configured")
	}

	state, err := generateEgovState()
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to generate egov state", err)
	}
	if err := s.stateStore.Save(ctx, &EgovAuthState{
		State:          state,
		RedirectTarget: strings.TrimSpace(redirectTarget),
		Scopes:         append([]string(nil), scopes...),
		Lang:           lang,
	}, s.cfg.StateTTL); err != nil {
		return nil, apperrors.InternalWithErr("failed to persist egov state", err)
	}

	authURL, err := s.client.AuthorizeURL(strings.TrimSpace(s.cfg.ClientID), strings.TrimSpace(s.cfg.RedirectURL), state, scopes, lang)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to build egov authorization url", err)
	}
	s.logger.Info("egov_auth_start", zap.String("lang", lang), zap.Int("scope_count", len(scopes)))
	return &domain.EgovStartResponse{RedirectURL: authURL}, nil
}

func (s *EgovFlowService) Complete(ctx context.Context, code, state, errorCode, errorDescription string) (*domain.EgovCallbackResponse, string, error) {
	if err := s.validateConfiguration(); err != nil {
		return nil, "", err
	}
	meta := LoginRequestMetaFromContext(ctx)
	requestID := strings.TrimSpace(meta.RequestID)
	if strings.TrimSpace(errorCode) != "" {
		s.logger.Warn("egov_auth_denied", zap.String("request_id", requestID), zap.String("error_code", strings.TrimSpace(errorCode)))
		return nil, "", apperrors.New("EGOV_ACCESS_DENIED", "egov authorization was denied", http.StatusUnauthorized).
			WithDetail("error", strings.TrimSpace(errorCode)).
			WithUserMessage(firstNonEmptyValue(strings.TrimSpace(errorDescription), "Авторизация через eGov была отменена."))
	}
	code = strings.TrimSpace(code)
	state = strings.TrimSpace(state)
	if state == "" {
		return nil, "", apperrors.New("EGOV_INVALID_CALLBACK", "egov callback state is required", http.StatusBadRequest).
			WithUserMessage("Некорректный callback от eGov. Попробуйте снова.")
	}
	if code == "" {
		return nil, "", apperrors.New("EGOV_INVALID_CALLBACK", "egov callback code is required", http.StatusBadRequest).
			WithUserMessage("Некорректный callback от eGov. Попробуйте снова.")
	}

	s.logger.Info("egov_state_validation_started",
		zap.String("request_id", requestID),
		zap.String("state_suffix", stateSuffix(state)),
		zap.String("provider", "egov"),
	)
	storedState, err := s.stateStore.Consume(ctx, state)
	if err != nil {
		stateResult, appErr := mapEgovStateError(err)
		s.logger.Warn("egov_state_validation_result",
			zap.String("request_id", requestID),
			zap.String("state_suffix", stateSuffix(state)),
			zap.String("result", stateResult),
		)
		return nil, "", appErr
	}
	s.logger.Info("egov_state_validation_result",
		zap.String("request_id", requestID),
		zap.String("state_suffix", stateSuffix(state)),
		zap.String("result", "ok"),
	)

	tokenURL := buildEgovEndpointURL(s.cfg.BaseURL, "/idp/oauth/token")
	rawClientSecret := s.cfg.ClientSecret
	trimmedClientSecret := strings.TrimSpace(rawClientSecret)
	s.logger.Info("egov_token_exchange_started",
		zap.String("request_id", requestID),
		zap.String("idp_base_url", strings.TrimSpace(s.cfg.BaseURL)),
		zap.String("token_url", tokenURL),
		zap.String("client_id", strings.TrimSpace(s.cfg.ClientID)),
		zap.String("redirect_uri", strings.TrimSpace(s.cfg.RedirectURL)),
		zap.Bool("has_client_secret", trimmedClientSecret != ""),
		zap.Int("client_secret_length", len(trimmedClientSecret)),
		zap.Bool("client_secret_has_surrounding_whitespace", rawClientSecret != trimmedClientSecret),
		zap.Bool("client_secret_has_newlines", strings.ContainsAny(rawClientSecret, "\r\n")),
		zap.String("auth_method", "basic"),
		zap.String("content_type", "application/x-www-form-urlencoded; charset=utf-8"),
	)
	tokenStart := time.Now()
	token, err := s.client.ExchangeAuthorizationCode(ctx, code, strings.TrimSpace(s.cfg.RedirectURL))
	if err != nil {
		appErr := s.mapIDPError("EGOV_CODE_EXCHANGE_FAILED", "failed to exchange egov authorization code", err)
		s.logIDPErrorResult(requestID, "egov_token_exchange_result", tokenStart, err)
		return nil, storedState.RedirectTarget, appErr
	}
	s.logger.Info("egov_token_exchange_result",
		zap.String("request_id", requestID),
		zap.Int("status_code", http.StatusOK),
		zap.String("idp_error", ""),
		zap.String("idp_error_description", ""),
		zap.Int64("duration_ms", time.Since(tokenStart).Milliseconds()),
	)

	basicURL := buildEgovEndpointURL(s.cfg.BaseURL, "/idp/resource/user/basic")
	s.logger.Info("egov_basic_fetch_started",
		zap.String("request_id", requestID),
		zap.String("resource_url", basicURL),
	)
	basicStart := time.Now()
	basic, err := s.client.GetUserBasic(ctx, strings.TrimSpace(token.AccessToken))
	if err != nil {
		s.logIDPErrorResult(requestID, "egov_basic_fetch_result", basicStart, err)
		return nil, storedState.RedirectTarget, s.mapIDPError("EGOV_PROFILE_FETCH_FAILED", "failed to fetch egov basic profile", err)
	}
	s.logger.Info("egov_basic_fetch_result",
		zap.String("request_id", requestID),
		zap.Int("status_code", http.StatusOK),
		zap.String("idp_error", ""),
		zap.String("idp_error_description", ""),
		zap.Int64("duration_ms", time.Since(basicStart).Milliseconds()),
	)

	var phone string
	if hasScope(token.Scope, egovScopePhone) || containsString(storedState.Scopes, egovScopePhone) {
		phoneURL := buildEgovEndpointURL(s.cfg.BaseURL, "/idp/resource/user/phone")
		s.logger.Info("egov_phone_fetch_started",
			zap.String("request_id", requestID),
			zap.String("resource_url", phoneURL),
			zap.Bool("optional", true),
		)
		phoneStart := time.Now()
		phoneResp, phoneErr := s.client.GetUserPhone(ctx, strings.TrimSpace(token.AccessToken))
		if phoneErr != nil {
			s.logIDPErrorResult(requestID, "egov_phone_fetch_result", phoneStart, phoneErr, zap.Bool("optional", true))
			if !isInsufficientScope(phoneErr) && !isOptionalResourceMissing(phoneErr) {
				if isInvalidToken(phoneErr) {
					return nil, storedState.RedirectTarget, s.mapIDPError("EGOV_INVALID_TOKEN", "failed to fetch egov phone", phoneErr)
				}
				return nil, storedState.RedirectTarget, s.mapIDPError("EGOV_PROFILE_FETCH_FAILED", "failed to fetch egov phone", phoneErr)
			}
		} else {
			if phoneResp != nil {
				phone = phoneResp.Phone
			}
			s.logger.Info("egov_phone_fetch_result",
				zap.String("request_id", requestID),
				zap.Int("status_code", http.StatusOK),
				zap.String("idp_error", ""),
				zap.String("idp_error_description", ""),
				zap.Int64("duration_ms", time.Since(phoneStart).Milliseconds()),
				zap.Bool("optional", true),
			)
		}
	}

	var email string
	if hasScope(token.Scope, egovScopeEmail) || containsString(storedState.Scopes, egovScopeEmail) {
		emailURL := buildEgovEndpointURL(s.cfg.BaseURL, "/idp/resource/user/email")
		s.logger.Info("egov_email_fetch_started",
			zap.String("request_id", requestID),
			zap.String("resource_url", emailURL),
			zap.Bool("optional", true),
		)
		emailStart := time.Now()
		emailResp, emailErr := s.client.GetUserEmail(ctx, strings.TrimSpace(token.AccessToken))
		if emailErr != nil {
			s.logIDPErrorResult(requestID, "egov_email_fetch_result", emailStart, emailErr, zap.Bool("optional", true))
			if !isInsufficientScope(emailErr) && !isOptionalResourceMissing(emailErr) {
				if isInvalidToken(emailErr) {
					return nil, storedState.RedirectTarget, s.mapIDPError("EGOV_INVALID_TOKEN", "failed to fetch egov email", emailErr)
				}
				return nil, storedState.RedirectTarget, s.mapIDPError("EGOV_PROFILE_FETCH_FAILED", "failed to fetch egov email", emailErr)
			}
		} else {
			if emailResp != nil {
				email = emailResp.Email
			}
			s.logger.Info("egov_email_fetch_result",
				zap.String("request_id", requestID),
				zap.Int("status_code", http.StatusOK),
				zap.String("idp_error", ""),
				zap.String("idp_error_description", ""),
				zap.Int64("duration_ms", time.Since(emailStart).Milliseconds()),
				zap.Bool("optional", true),
			)
		}
	}

	s.logger.Info("egov_user_link_or_create_started",
		zap.String("request_id", requestID),
		zap.Bool("has_phone", strings.TrimSpace(phone) != ""),
		zap.Bool("has_email", strings.TrimSpace(email) != ""),
		zap.String("user_type", string(basic.Type)),
		zap.String("auth_level", string(basic.Level)),
	)
	loginResult, err := s.authService.LoginOrRegisterFromEgov(ctx, basic, phone, email)
	if err != nil {
		s.logger.Warn("egov_user_link_or_create_result",
			zap.String("request_id", requestID),
			zap.String("result", "error"),
			zap.Error(err),
		)
		return nil, storedState.RedirectTarget, err
	}
	s.logger.Info("egov_user_link_or_create_result",
		zap.String("request_id", requestID),
		zap.String("result", "ok"),
		zap.Int64("user_id", loginResult.User.ID),
		zap.Bool("is_new_user", loginResult.IsNewUser),
	)
	localTokens, err := s.tokenManager.GenerateTokenPair(ctx, loginResult.User.ID, derefStringValue(loginResult.User.IIN), loginResult.User.Email, loginResult.User.Username, loginResult.User.FirstName, loginResult.User.LastName, false, nil)
	if err != nil {
		return nil, storedState.RedirectTarget, apperrors.InternalWithErr("failed to issue local application token", err)
	}

	response := &domain.EgovCallbackResponse{
		AccessToken:  localTokens.AccessToken,
		RefreshToken: localTokens.RefreshToken,
		TokenType:    localTokens.TokenType,
		ExpiresIn:    localTokens.ExpiresIn,
		IsNewUser:    loginResult.IsNewUser,
		User: &domain.EgovAuthenticatedUser{
			ID:         loginResult.User.ID,
			IIN:        loginResult.Profile.IIN,
			Phone:      loginResult.Profile.Phone,
			Email:      firstNonEmptyValue(loginResult.Profile.Email, loginResult.User.Email),
			FirstName:  loginResult.User.FirstName,
			LastName:   loginResult.User.LastName,
			Patronymic: loginResult.Profile.Patronymic,
			Type:       loginResult.Profile.UserType,
			AuthLevel:  loginResult.Profile.AuthLevel,
		},
	}
	s.logger.Info("egov_auth_success", zap.Int64("user_id", loginResult.User.ID), zap.Bool("is_new_user", loginResult.IsNewUser))
	return response, storedState.RedirectTarget, nil
}

func (s *EgovFlowService) validateConfiguration() error {
	if s.client == nil || s.authService == nil || s.tokenManager == nil {
		return apperrors.Internal("egov auth flow is not fully configured")
	}
	if strings.TrimSpace(s.cfg.ClientID) == "" || strings.TrimSpace(s.cfg.ClientSecret) == "" {
		return apperrors.Internal("egov client credentials are not configured")
	}
	if s.cfg.ClientSecret != strings.TrimSpace(s.cfg.ClientSecret) || strings.ContainsAny(s.cfg.ClientSecret, "\r\n") {
		return apperrors.Internal("egov client secret contains surrounding whitespace or newlines")
	}
	if strings.TrimSpace(s.cfg.RedirectURL) == "" {
		return apperrors.Internal("egov redirect uri is not configured")
	}
	if err := validateEgovBaseURL(strings.TrimSpace(s.cfg.BaseURL), strings.TrimSpace(s.cfg.Environment)); err != nil {
		return err
	}
	parsedRedirectURL, err := url.Parse(strings.TrimSpace(s.cfg.RedirectURL))
	if err != nil || !strings.EqualFold(parsedRedirectURL.Scheme, "https") {
		return apperrors.Internal("egov redirect uri must use https")
	}
	if s.cfg.StateTTL <= 0 {
		return apperrors.Internal("egov state ttl must be configured")
	}
	return nil
}

func validateEgovBaseURL(baseURL, environment string) error {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return apperrors.Internal("egov base url is invalid")
	}
	if strings.TrimSpace(parsed.Path) != "" && strings.TrimSpace(parsed.Path) != "/" {
		return apperrors.Internal("egov base url must be origin only")
	}
	if strings.TrimSpace(parsed.RawQuery) != "" || strings.TrimSpace(parsed.Fragment) != "" {
		return apperrors.Internal("egov base url must be origin only")
	}

	env := strings.ToLower(strings.TrimSpace(environment))
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	switch env {
	case "test", "dev":
		if host != "test.idp.egov.kz" {
			return apperrors.Internal("egov base url is not allowed for test/dev environment")
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return apperrors.Internal("egov base url must use http or https")
		}
	case "prod", "production", "":
		if parsed.Scheme != "https" || host != "idp.egov.kz" {
			return apperrors.Internal("egov base url must be https://idp.egov.kz in prod")
		}
	default:
		if parsed.Scheme != "https" {
			return apperrors.Internal("egov base url must use https")
		}
	}

	return nil
}

func (s *EgovFlowService) mapIDPError(code, message string, err error) error {
	var apiErr *idp.APIError
	if !errors.As(err, &apiErr) {
		if strings.Contains(strings.ToLower(err.Error()), "timeout") || errors.Is(err, context.DeadlineExceeded) {
			return apperrors.New("EGOV_SERVICE_UNAVAILABLE", message, http.StatusBadGateway).
				WithUserMessage("Сервис eGov временно недоступен. Попробуйте позже.")
		}
		return apperrors.New(apperrors.ErrorCode(code), message, http.StatusBadGateway).
			WithUserMessage("Не удалось завершить вход через eGov. Попробуйте позже.")
	}

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
	case strings.EqualFold(apiErr.ErrorCode, "invalid_token"):
		return apperrors.New("EGOV_INVALID_TOKEN", message, http.StatusUnauthorized).
			WithUserMessage("Токен eGov недействителен или истек.")
	case strings.EqualFold(apiErr.ErrorCode, "insufficient_scope"):
		return apperrors.New("EGOV_INSUFFICIENT_SCOPE", message, http.StatusForbidden).
			WithDetail("scope", apiErr.RequiredScope).
			WithUserMessage("Недостаточно прав доступа к данным eGov.")
	default:
		if apiErr.StatusCode == http.StatusUnauthorized {
			return apperrors.New("EGOV_INVALID_CLIENT", message, http.StatusUnauthorized).
				WithUserMessage("Клиент eGov настроен некорректно. Обратитесь к администратору.")
		}
		if apiErr.StatusCode >= http.StatusInternalServerError {
			return apperrors.New("EGOV_SERVICE_UNAVAILABLE", message, http.StatusBadGateway).
				WithUserMessage("Сервис eGov временно недоступен. Попробуйте позже.")
		}
		appErr := apperrors.New(apperrors.ErrorCode(code), message, http.StatusBadGateway).
			WithUserMessage("Не удалось завершить вход через eGov.")
		if apiErr.ErrorCode != "" {
			appErr = appErr.WithDetail("error", apiErr.ErrorCode)
		}
		if apiErr.RequiredScope != "" {
			appErr = appErr.WithDetail("scope", apiErr.RequiredScope)
		}
		return appErr
	}
}

func mapEgovStateError(err error) (string, error) {
	switch {
	case errors.Is(err, ErrEgovStateExpired):
		return "expired", apperrors.New("EGOV_STATE_EXPIRED", "egov state has expired", http.StatusUnauthorized).
			WithUserMessage("Сессия входа через eGov истекла. Попробуйте снова.")
	case errors.Is(err, ErrEgovStateReused):
		return "reused", apperrors.New("EGOV_INVALID_STATE", "egov state is invalid or already used", http.StatusUnauthorized).
			WithUserMessage("Состояние авторизации eGov уже было использовано. Попробуйте снова.")
	case errors.Is(err, ErrEgovStateNotFound):
		return "not_found", apperrors.New("EGOV_INVALID_STATE", "egov state is invalid or already used", http.StatusUnauthorized).
			WithUserMessage("Состояние авторизации eGov недействительно. Попробуйте снова.")
	default:
		if err == nil {
			return "ok", nil
		}
		return "not_found", apperrors.New("EGOV_INVALID_STATE", "egov state is invalid or already used", http.StatusUnauthorized).
			WithUserMessage("Состояние авторизации eGov недействительно. Попробуйте снова.")
	}
}

func stateSuffix(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	if len(value) <= 6 {
		return value + ":" + hex.EncodeToString(sum[:4])
	}
	return value[len(value)-6:] + ":" + hex.EncodeToString(sum[:4])
}

func buildEgovEndpointURL(baseURL, path string) string {
	return strings.TrimRight(strings.TrimSpace(baseURL), "/") + path
}

func isRedirectMismatch(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return false
	}
	return strings.Contains(value, "redirect") || strings.Contains(value, "callback") || strings.Contains(value, "mismatch")
}

func (s *EgovFlowService) logIDPErrorResult(requestID, event string, startedAt time.Time, err error, extraFields ...zap.Field) {
	fields := []zap.Field{
		zap.String("request_id", requestID),
		zap.Int64("duration_ms", time.Since(startedAt).Milliseconds()),
		zap.Int("status_code", 0),
		zap.String("idp_error", ""),
		zap.String("idp_error_description", ""),
		zap.String("error_type", fmt.Sprintf("%T", err)),
		zap.String("error_message", err.Error()),
		zap.Bool("timeout", strings.Contains(strings.ToLower(err.Error()), "timeout") || errors.Is(err, context.DeadlineExceeded)),
		zap.String("network_class", classifyEgovNetworkError(err)),
	}
	var apiErr *idp.APIError
	if errors.As(err, &apiErr) {
		fields[2] = zap.Int("status_code", apiErr.StatusCode)
		fields[3] = zap.String("idp_error", apiErr.ErrorCode)
		fields[4] = zap.String("idp_error_description", apiErr.ErrorDescription)
		fields = append(fields, zap.String("raw_body_preview", truncateEgovLogValue(apiErr.RawBody, 256)))
	}
	fields = append(fields, extraFields...)
	s.logger.Warn(event, fields...)
}

func classifyEgovNetworkError(err error) string {
	if err == nil {
		return "unknown"
	}
	lowered := strings.ToLower(err.Error())
	switch {
	case errors.Is(err, context.DeadlineExceeded) || strings.Contains(lowered, "timeout") || strings.Contains(lowered, "deadline exceeded"):
		return "timeout"
	case strings.Contains(lowered, "no such host") || strings.Contains(lowered, "temporary failure in name resolution"):
		return "dns"
	case strings.Contains(lowered, "connection refused"):
		return "connection_refused"
	case strings.Contains(lowered, "connection reset"):
		return "connection_reset"
	case strings.Contains(lowered, "eof"):
		return "eof"
	case strings.Contains(lowered, "tls:") || strings.Contains(lowered, "handshake"):
		return "tls"
	case strings.Contains(lowered, "protocol"):
		return "protocol"
	default:
		return "network"
	}
}

func generateEgovState() (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func validateEgovLang(value, fallback string) (string, error) {
	lang := strings.ToLower(strings.TrimSpace(value))
	if lang == "" {
		lang = strings.ToLower(strings.TrimSpace(fallback))
	}
	switch lang {
	case "ru", "kk", "en":
		return lang, nil
	default:
		return "", apperrors.New("EGOV_INVALID_LANG", "lang must be one of: ru, kk, en", http.StatusBadRequest).
			WithUserMessage("Допустимые языки eGov: ru, kk, en.")
	}
}

func normalizeEgovScopes(scopes []string) []string {
	seen := make(map[string]struct{}, len(scopes))
	result := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		for _, part := range strings.FieldsFunc(scope, func(r rune) bool { return r == ',' || r == ' ' }) {
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

func hasScope(scopeString, scope string) bool {
	for _, part := range strings.Fields(strings.TrimSpace(scopeString)) {
		if part == scope {
			return true
		}
	}
	return false
}

func isInsufficientScope(err error) bool {
	var apiErr *idp.APIError
	return errors.As(err, &apiErr) && strings.EqualFold(apiErr.ErrorCode, "insufficient_scope")
}

func isOptionalResourceMissing(err error) bool {
	var apiErr *idp.APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound
}

func isInvalidToken(err error) bool {
	var apiErr *idp.APIError
	return errors.As(err, &apiErr) && (strings.EqualFold(apiErr.ErrorCode, "invalid_token") || apiErr.StatusCode == http.StatusUnauthorized)
}

func derefStringValue(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func firstNonEmptyValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func containsString(values []string, target string) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return false
	}
	for _, value := range values {
		if strings.TrimSpace(value) == target {
			return true
		}
	}
	return false
}

func truncateEgovLogValue(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}
