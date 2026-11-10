package handler

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/auth"
	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/i18n"
	"github.com/west6ide/workspace-backend/internal/keycloak"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"github.com/west6ide/workspace-backend/internal/repository"
	appservice "github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

const (
	keycloakOAuthStateCookie     = "kc_oauth_state"
	keycloakOAuthRedirectCookie  = "kc_oauth_redirect"
	keycloakSilentStateCookie    = "kc_silent_state"
	keycloakSilentRedirectCookie = "kc_silent_redirect"
	externalAdminStateCookie     = "kc_external_admin_state"
	externalAdminRedirectCookie  = "kc_external_admin_redirect"
	defaultWebRedirectTarget     = "/web/"
	defaultExternalAdminTarget   = "/external-admin/"
	mobileKeycloakStateTTL       = 5 * time.Minute
)

type mobileKeycloakState struct {
	Platform      string `json:"platform"`
	RedirectURI   string `json:"redirect_uri"`
	OriginalState string `json:"original_state"`
	IssuedAt      int64  `json:"issued_at"`
}

type mobileKeycloakLoginPayload struct {
	AuthURL string `json:"authUrl"`
	State   string `json:"state"`
}

type mobileKeycloakCallbackRequest struct {
	Code         string `json:"code" binding:"required"`
	RedirectURI  string `json:"redirectUri" binding:"required"`
	CodeVerifier string `json:"codeVerifier" binding:"required"`
	State        string `json:"state" binding:"required"`
}

type mobileKeycloakCallbackResponse struct {
	AccessToken  string               `json:"accessToken"`
	RefreshToken string               `json:"refreshToken"`
	IDToken      string               `json:"idToken,omitempty"`
	TokenType    string               `json:"tokenType"`
	ExpiresIn    int64                `json:"expiresIn"`
	ExpiresAt    time.Time            `json:"expiresAt"`
	State        string               `json:"state,omitempty"`
	User         *domain.UserResponse `json:"user,omitempty"`
}

type MailOAuthProfileResponse struct {
	UserID         int64       `json:"user_id"`
	KeycloakID     *string     `json:"keycloak_id,omitempty"`
	Email          string      `json:"email"`
	Username       string      `json:"username"`
	FirstName      string      `json:"first_name"`
	LastName       string      `json:"last_name"`
	Role           domain.Role `json:"role"`
	IsActive       bool        `json:"is_active"`
	MailboxLogin   string      `json:"mailbox_login"`
	MailboxAddr    string      `json:"mailbox_address"`
	OrganizationID *uuid.UUID  `json:"organization_id,omitempty"`
	TeamID         *uuid.UUID  `json:"team_id,omitempty"`
	ManagerID      *int64      `json:"manager_id,omitempty"`
}

type linkedDeviceRevoker interface {
	RevokeLinkedDevice(ctx context.Context, userID int64, deviceID string) error
}

type SessionTerminator interface {
	CloseAllUserSessions(userID int64) int
}

type pushDeviceManager interface {
	DeactivateDevice(ctx context.Context, userID int64, deviceID string) error
	DeactivateToken(ctx context.Context, userID int64, token string) error
	DeactivateAllDevices(ctx context.Context, userID int64) error
}

type egovFlowService interface {
	Start(ctx context.Context, lang, redirectTarget string) (*domain.EgovStartResponse, error)
	Complete(ctx context.Context, code, state, errorCode, errorDescription string) (*domain.EgovCallbackResponse, string, error)
}

type organizationIdentityProviderLoginResolver interface {
	ResolveEnabledAliasByOrganizationSlug(ctx context.Context, slug string) (string, error)
}

type AuthHandler struct {
	authService                          *auth.Service
	logger                               *zap.Logger
	tokenManager                         *auth.TokenManager
	linkedDeviceRevoker                  linkedDeviceRevoker
	quickPinService                      *auth.QuickPinService
	sessionTerminator                    SessionTerminator
	pushDeviceManager                    pushDeviceManager
	egovService                          egovFlowService
	organizationIdentityProviderResolver organizationIdentityProviderLoginResolver
	tenantHostMiddleware                 *middleware.TenantHostMiddleware
	tenantHandoffStore                   *auth.TenantHandoffStore
	externalAdminClientID                string
	odoHandoffStore                      auth.ODOHandoffStore
	odoHandoffAccessCheck                func(context.Context, string) error
	invitations                          *auth.InvitationService
	recovery                             *auth.RecoveryService
}

func NewAuthHandler(authService *auth.Service, quickPinService *auth.QuickPinService, linkedDeviceRevoker linkedDeviceRevoker, logger *zap.Logger, tokenManagers ...*auth.TokenManager) *AuthHandler {
	h := &AuthHandler{
		authService:         authService,
		quickPinService:     quickPinService,
		logger:              logger.With(zap.String("handler", "auth")),
		linkedDeviceRevoker: linkedDeviceRevoker,
	}
	if len(tokenManagers) > 0 {
		h.tokenManager = tokenManagers[0]
	}
	return h
}

func (h *AuthHandler) SetSessionTerminator(st SessionTerminator) {
	h.sessionTerminator = st
}

func (h *AuthHandler) SetPushDeviceManager(manager pushDeviceManager) {
	h.pushDeviceManager = manager
}

func (h *AuthHandler) SetEgovFlowService(service egovFlowService) {
	h.egovService = service
}

func (h *AuthHandler) SetOrganizationIdentityProviderResolver(resolver organizationIdentityProviderLoginResolver) {
	h.organizationIdentityProviderResolver = resolver
}

func (h *AuthHandler) SetTenantHostMiddleware(tenant *middleware.TenantHostMiddleware) {
	h.tenantHostMiddleware = tenant
}

func (h *AuthHandler) SetTenantHandoffStore(store *auth.TenantHandoffStore) {
	h.tenantHandoffStore = store
}

// Issued tokens may be local JWTs or Keycloak JWTs. Both have already been
// issued by a trusted login/refresh flow when this method is called.
func (h *AuthHandler) validateIssuedTokenHost(c *gin.Context, tokens *auth.TokenPair) bool {
	if h.tenantHostMiddleware == nil {
		return true
	}
	_, isTenant, valid := middleware.TenantSlugFromHost(c.Request.Host, h.tenantHostMiddleware.BaseDomain())
	if !valid {
		h.respondError(c, apperrors.Validation("invalid tenant host"))
		return false
	}
	if !isTenant {
		return true
	}
	userID, ok := h.issuedTokenUserID(c, tokens)
	if !ok {
		h.respondError(c, apperrors.Unauthorized("user cannot be resolved"))
		return false
	}
	return h.tenantHostMiddleware.ValidateUser(c, userID)
}

func (h *AuthHandler) issuedTokenUserID(c *gin.Context, tokens *auth.TokenPair) (int64, bool) {
	if tokens == nil || strings.TrimSpace(tokens.AccessToken) == "" {
		return 0, false
	}
	if h.tokenManager != nil {
		if claims, err := h.tokenManager.ValidateToken(c.Request.Context(), tokens.AccessToken); err == nil {
			return claims.UserID, claims.UserID > 0
		}
	}
	if h.authService == nil {
		return 0, false
	}
	user, err := h.authService.CurrentUserFromAccessToken(c.Request.Context(), tokens.AccessToken)
	if err != nil || user == nil {
		return 0, false
	}
	return user.ID, user.ID > 0
}

// CreateTenantHandoff is called on the platform host by an authenticated
// organization user. Only an opaque 30-second code leaves this origin.
func (h *AuthHandler) CreateTenantHandoff(c *gin.Context) {
	if h.tenantHostMiddleware == nil || h.tenantHandoffStore == nil || !strings.EqualFold(c.Request.Host, h.tenantHostMiddleware.BaseDomain()) {
		h.respondError(c, apperrors.Forbidden("tenant handoff is unavailable on this host"))
		return
	}
	userID, ok := middleware.GetUserID(c)
	if !ok {
		h.respondError(c, apperrors.Unauthorized("user is not authenticated"))
		return
	}
	slug, err := h.tenantHostMiddleware.OrganizationSlug(c.Request.Context(), userID)
	if err != nil || slug == "" {
		h.respondError(c, apperrors.Forbidden("user has no active organization"))
		return
	}
	refreshToken, err := c.Cookie("refresh_token")
	if err != nil || refreshToken == "" {
		h.respondError(c, apperrors.Unauthorized("refresh cookie missing"))
		return
	}
	code, err := h.tenantHandoffStore.Create(c.Request.Context(), auth.TenantHandoff{UserID: userID, Slug: slug, RefreshToken: refreshToken})
	if err != nil {
		h.respondError(c, apperrors.InternalWithErr("tenant handoff unavailable", err))
		return
	}
	c.Header("Cache-Control", "no-store")
	respondJSON(c, http.StatusOK, gin.H{"code": code, "slug": slug})
}

// RedeemTenantHandoff accepts only the short-lived code from the platform
// origin, then issues host-only cookies after binding it to this tenant.
func (h *AuthHandler) RedeemTenantHandoff(c *gin.Context) {
	if h.tenantHostMiddleware == nil || h.tenantHandoffStore == nil {
		h.respondError(c, apperrors.Forbidden("tenant handoff is unavailable"))
		return
	}
	baseDomain := h.tenantHostMiddleware.BaseDomain()
	slug, isTenant, valid := middleware.TenantSlugFromHost(c.Request.Host, baseDomain)
	if !valid || !isTenant || c.GetHeader("Origin") != "https://"+baseDomain {
		h.respondError(c, apperrors.Forbidden("invalid tenant handoff origin"))
		return
	}
	entry, err := h.tenantHandoffStore.Redeem(c.Request.Context(), c.PostForm("code"))
	if err != nil || !strings.EqualFold(entry.Slug, slug) {
		h.respondError(c, apperrors.Forbidden("tenant handoff is invalid or expired"))
		return
	}
	ctx := auth.WithLoginClient(c.Request.Context(), auth.LoginClientWeb)
	var tokens *auth.TokenPair
	if h.tokenManager != nil {
		if localTokens, localErr := h.tokenManager.RefreshAccessToken(ctx, entry.RefreshToken); localErr == nil {
			tokens = localTokens
		}
	}
	if tokens == nil && h.authService != nil {
		tokens, err = h.authService.RefreshToken(ctx, entry.RefreshToken)
		if err != nil {
			h.respondError(c, apperrors.ToAppError(err))
			return
		}
	}
	issuedUserID, ok := h.issuedTokenUserID(c, tokens)
	if !ok || issuedUserID != entry.UserID {
		h.respondError(c, apperrors.Forbidden("tenant handoff user mismatch"))
		return
	}
	if !h.validateIssuedTokenHost(c, tokens) {
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Header("Referrer-Policy", "no-referrer")
	setAuthCookies(c, tokens)
	h.markSecurityLogin(c, tokens)
	c.Redirect(http.StatusSeeOther, "/web/auth/callback")
}

func (h *AuthHandler) SetExternalAdminClientID(clientID string) {
	h.externalAdminClientID = strings.TrimSpace(clientID)
}

func (h *AuthHandler) ensureEgovFlowService() *apperrors.AppError {
	if h.egovService != nil {
		return nil
	}
	if h.authService == nil || h.tokenManager == nil {
		return apperrors.Internal("egov auth flow dependencies are not configured")
	}
	cfg, err := config.Load()
	if err != nil {
		return apperrors.InternalWithErr("failed to load egov config", err)
	}
	h.egovService = auth.NewEgovFlowService(cfg.IDP, h.authService, h.tokenManager, h.logger, nil)
	return nil
}

// Register godoc
// @Summary Legacy registration (invitation required)
// @Description Direct registration is closed. Use /auth/invitations/register.
// @Tags auth
// @Produce json
// @Failure 403 {object} map[string]interface{}
// @Router /auth/register [post]
// @Deprecated
func (h *AuthHandler) Register(c *gin.Context) {
	// Corporate self-registration is invitation-only. This also closes the old
	// path that accepted arbitrary organization_id/team_id from an anonymous caller.
	h.respondError(c, apperrors.New(apperrors.ErrCodeInvitationRegistrationRequired, "invitation required", 403))
}

// RegisterByHR godoc
// @Summary Register a user via HR integration
// @Description Creates or updates a user account using HR payload and stores a full HR snapshot separately
// @Tags auth
// @Accept json
// @Produce json
// @Param request body auth.RegisterByHRRequest true "HR registration request"
// @Success 201 {object} auth.RegisterByHRResponse
// @Failure 400 {object} map[string]interface{}
// @Failure 409 {object} map[string]interface{}
// @Router /auth/register-by-hr [post]
func (h *AuthHandler) RegisterByHR(c *gin.Context) {
	var req auth.RegisterByHRRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		var ve validator.ValidationErrors

		if errors.As(err, &ve) {
			details := make(map[string]any)
			for _, fieldErr := range ve {
				details[fieldErr.Field()] = fieldErr.Tag()
			}
			h.respondError(c, apperrors.Validation("Validation failed").WithDetails(details))
			return
		}

		h.respondError(c, apperrors.Validation("Invalid JSON format"))
		return
	}

	if req.Person == nil && strings.TrimSpace(req.IIN) == "" {
		h.respondError(c, apperrors.Validation("iin or person payload is required"))
		return
	}

	req.Email = strings.ToLower(strings.TrimSpace(req.Email))

	result, err := h.authService.RegisterByHR(c.Request.Context(), &req)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusCreated, result)
}

// Login godoc
// @Summary Login user
// @Description Authenticate user and return tokens
// @Tags auth
// @Accept json
// @Produce json
// @Param request body auth.LoginRequest true "Login request"
// @Success 200 {object} auth.TokenPair
// @Failure 401 {object} map[string]interface{}
// @Router /auth/login [post]
func (h *AuthHandler) Login(c *gin.Context) {
	var req auth.LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	req.Username = strings.TrimSpace(req.Username)
	req.IIN = strings.TrimSpace(req.IIN)
	req.Identifier = strings.TrimSpace(req.Identifier)
	req.Login = strings.TrimSpace(req.Login)

	loginClient := detectLoginClient(c)
	ctx := auth.WithLoginClient(c.Request.Context(), loginClient)
	ctx = auth.WithLoginRequestMeta(ctx, buildLoginRequestMeta(c, loginClient, ""))
	result, err := h.authService.Login(ctx, &req)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	if !h.validateIssuedTokenHost(c, result) {
		return
	}

	h.logger.Info("User logged in", zap.String("login_field", loginFieldForLog(req)))
	report := setAuthCookies(c, result)
	h.markSecurityLogin(c, result)
	h.logAuthCookies("password_login", report, result)
	respondJSON(c, http.StatusOK, result)
}

func (h *AuthHandler) StartEgovLogin(c *gin.Context) {
	if err := h.ensureEgovFlowService(); err != nil {
		h.respondError(c, err)
		return
	}
	if h.egovService == nil {
		h.respondError(c, apperrors.Internal("egov auth service is not configured"))
		return
	}

	redirectTarget := sanitizeRedirectTarget(c.Query("redirect"))
	resp, err := h.egovService.Start(c.Request.Context(), c.Query("lang"), redirectTarget)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	h.logger.Info("Starting eGov login",
		zap.String("lang", strings.TrimSpace(c.Query("lang"))),
		zap.String("redirect_target", redirectTarget),
	)

	if prefersJSONResponse(c) {
		respondJSON(c, http.StatusOK, resp)
		return
	}

	c.Redirect(http.StatusFound, resp.RedirectURL)
}

func (h *AuthHandler) CompleteEgovLogin(c *gin.Context) {
	requestID := ginRequestID(c)
	hasCode := strings.TrimSpace(c.Query("code")) != ""
	hasState := strings.TrimSpace(c.Query("state")) != ""
	hasError := strings.TrimSpace(c.Query("error")) != ""
	errorCode := strings.TrimSpace(c.Query("error"))
	queryKeys := sortedQueryKeys(c.Request.URL.Query())
	h.logger.Info("egov_callback_received",
		zap.String("request_id", requestID),
		zap.Bool("has_code", hasCode),
		zap.Bool("has_state", hasState),
		zap.Bool("has_error", hasError),
		zap.Strings("query_keys", queryKeys),
		zap.String("user_agent", strings.TrimSpace(c.Request.UserAgent())),
		zap.String("provider", "egov"),
	)

	if !hasCode && !hasState && !hasError {
		h.logger.Warn("egov_callback_missing_params",
			zap.String("request_id", requestID),
			zap.Bool("has_code", hasCode),
			zap.Bool("has_state", hasState),
			zap.Bool("has_error", hasError),
			zap.String("provider", "egov"),
		)
		h.redirectFailedEgovLogin(c, apperrors.New("EGOV_INVALID_CALLBACK", "invalid egov callback", http.StatusBadRequest).
			WithUserMessage("Некорректный ответ от eGov. Попробуйте войти снова."))
		return
	}
	if !hasState && !hasError {
		h.logger.Warn("egov_callback_missing_params",
			zap.String("request_id", requestID),
			zap.Bool("has_code", hasCode),
			zap.Bool("has_state", hasState),
			zap.Bool("has_error", hasError),
			zap.String("provider", "egov"),
		)
		h.redirectFailedEgovLogin(c, apperrors.New("EGOV_INVALID_STATE", "egov state is required", http.StatusBadRequest).
			WithUserMessage("Сессия входа через eGov истекла. Попробуйте снова."))
		return
	}
	if !hasCode && !hasError {
		h.logger.Warn("egov_callback_missing_params",
			zap.String("request_id", requestID),
			zap.Bool("has_code", hasCode),
			zap.Bool("has_state", hasState),
			zap.Bool("has_error", hasError),
			zap.String("provider", "egov"),
		)
		h.redirectFailedEgovLogin(c, apperrors.New("EGOV_INVALID_CALLBACK", "invalid egov callback", http.StatusBadRequest).
			WithUserMessage("Некорректный ответ от eGov. Попробуйте войти снова."))
		return
	}
	if hasError && !strings.EqualFold(errorCode, "access_denied") && !hasCode && !hasState {
		h.logger.Warn("egov_callback_missing_params",
			zap.String("request_id", requestID),
			zap.Bool("has_code", hasCode),
			zap.Bool("has_state", hasState),
			zap.Bool("has_error", hasError),
			zap.String("provider", "egov"),
		)
		h.redirectFailedEgovLogin(c, apperrors.New("EGOV_INVALID_CALLBACK", "invalid egov callback", http.StatusBadRequest).
			WithUserMessage("Некорректный ответ от eGov. Попробуйте войти снова."))
		return
	}

	if err := h.ensureEgovFlowService(); err != nil {
		h.redirectFailedEgovLogin(c, err)
		return
	}
	if h.egovService == nil {
		h.redirectFailedEgovLogin(c, apperrors.Internal("egov auth service is not configured"))
		return
	}

	loginClient := auth.LoginClientWeb
	ctx := auth.WithLoginClient(c.Request.Context(), loginClient)
	ctx = auth.WithLoginRequestMeta(ctx, buildLoginRequestMeta(c, loginClient, ""))
	resp, redirectTarget, err := h.egovService.Complete(
		ctx,
		c.Query("code"),
		c.Query("state"),
		c.Query("error"),
		c.Query("error_description"),
	)
	if err != nil {
		h.redirectFailedEgovLogin(c, apperrors.ToAppError(err))
		return
	}

	localTokens := &auth.TokenPair{
		AccessToken:  resp.AccessToken,
		RefreshToken: resp.RefreshToken,
		TokenType:    resp.TokenType,
		ExpiresIn:    resp.ExpiresIn,
		ExpiresAt:    time.Now().Add(time.Duration(resp.ExpiresIn) * time.Second),
	}
	report := setAuthCookies(c, localTokens)
	h.logger.Info("egov_auth_cookies_set",
		zap.String("request_id", requestID),
		zap.Bool("access_cookie_set", report.AccessSet),
		zap.Bool("refresh_cookie_set", report.RefreshSet),
		zap.String("cookie_same_site", report.SameSiteLabel),
		zap.Bool("cookie_secure", report.Secure),
		zap.Bool("cookie_http_only", report.HttpOnly),
		zap.String("cookie_path", report.Path),
	)
	h.markSecurityLogin(c, localTokens)
	h.logAuthCookies("egov_callback", report, localTokens)

	if prefersJSONResponse(c) {
		respondJSON(c, http.StatusOK, resp)
		return
	}

	c.Redirect(http.StatusFound, frontendEgovSuccessTarget(redirectTarget))
}

func (h *AuthHandler) StartKeycloakLogin(c *gin.Context) {
	if h.authService == nil {
		h.respondError(c, apperrors.Internal("auth service is not configured"))
		return
	}

	state := randomURLToken()
	redirectTarget := sanitizeRedirectTarget(c.Query("redirect"))
	redirectURI := publicRequestBaseURL(c) + "/api/v1/auth/keycloak/callback"
	secure := requestIsSecure(c)
	organizationSlug := strings.TrimSpace(c.Query("organization"))
	authURL := h.authService.KeycloakAuthCodeURL(redirectURI, state, c.Query("login_hint"))
	if organizationSlug != "" {
		if h.organizationIdentityProviderResolver == nil {
			h.respondError(c, apperrors.NotFound("organization login provider"))
			return
		}
		providerAlias, resolveErr := h.organizationIdentityProviderResolver.ResolveEnabledAliasByOrganizationSlug(c.Request.Context(), organizationSlug)
		if resolveErr != nil || strings.TrimSpace(providerAlias) == "" {
			h.logger.Warn("Organization identity provider was not found for login", zap.String("organization", organizationSlug), zap.Error(resolveErr))
			h.respondError(c, apperrors.NotFound("organization login provider"))
			return
		}
		authURL = h.authService.KeycloakAuthCodeURLForIdentityProvider(redirectURI, state, providerAlias, c.Query("login_hint"))
	}
	h.logger.Info("Starting Keycloak login",
		zap.String("redirect_target", redirectTarget),
		zap.String("redirect_uri", redirectURI),
		zap.String("auth_url", authURL),
		zap.Bool("secure_request", secure),
		zap.Bool("has_login_hint", strings.TrimSpace(c.Query("login_hint")) != ""),
		zap.Bool("has_organization_hint", organizationSlug != ""),
	)

	if secure {
		c.SetSameSite(http.SameSiteNoneMode)
	} else {
		c.SetSameSite(http.SameSiteLaxMode)
	}
	c.SetCookie(keycloakOAuthStateCookie, state, 5*60, "/", "", secure, true)
	c.SetCookie(keycloakOAuthRedirectCookie, redirectTarget, 5*60, "/", "", secure, true)

	c.Redirect(http.StatusFound, authURL)
}

func (h *AuthHandler) StartExternalAdminKeycloakLogin(c *gin.Context) {
	if h.authService == nil {
		h.respondError(c, apperrors.Internal("auth service is not configured"))
		return
	}
	if strings.TrimSpace(h.externalAdminClientID) == "" {
		h.respondError(c, apperrors.NotFound("external admin keycloak client is not configured"))
		return
	}

	state := randomURLToken()
	redirectTarget := sanitizeExternalAdminRedirectTarget(c.Query("redirect"))
	redirectURI := publicRequestBaseURL(c) + "/api/v1/external-admin/auth/keycloak/callback"
	secure := requestIsSecure(c)
	authURL, err := h.authService.KeycloakAuthCodeURLForClient(h.externalAdminClientID, redirectURI, state, c.Query("login_hint"))
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	if secure {
		c.SetSameSite(http.SameSiteNoneMode)
	} else {
		c.SetSameSite(http.SameSiteLaxMode)
	}
	c.SetCookie(externalAdminStateCookie, state, 5*60, "/", "", secure, true)
	c.SetCookie(externalAdminRedirectCookie, redirectTarget, 5*60, "/", "", secure, true)

	c.Redirect(http.StatusFound, authURL)
}

func (h *AuthHandler) CompleteKeycloakLogin(c *gin.Context) {
	if h.authService == nil {
		h.logger.Warn("Keycloak callback failed: auth service is not configured")
		h.redirectFailedKeycloakLogin(c)
		return
	}

	expectedState, err := c.Cookie(keycloakOAuthStateCookie)
	if err != nil || strings.TrimSpace(expectedState) == "" || expectedState != c.Query("state") {
		h.logger.Warn("Keycloak callback failed: invalid state",
			zap.Bool("state_cookie_present", err == nil && strings.TrimSpace(expectedState) != ""),
			zap.Bool("state_matches", err == nil && expectedState == c.Query("state")),
		)
		h.redirectFailedKeycloakLogin(c)
		return
	}

	code := strings.TrimSpace(c.Query("code"))
	if code == "" {
		// Keycloak returned an error (e.g. access_denied, interaction_required)
		h.logger.Warn("Keycloak callback failed: code missing",
			zap.String("keycloak_error", strings.TrimSpace(c.Query("error"))),
			zap.String("error_description", strings.TrimSpace(c.Query("error_description"))),
		)
		h.redirectFailedKeycloakLogin(c)
		return
	}

	redirectTarget := defaultWebRedirectTarget
	if cookieRedirect, err := c.Cookie(keycloakOAuthRedirectCookie); err == nil {
		redirectTarget = sanitizeRedirectTarget(cookieRedirect)
	}

	redirectURI := publicRequestBaseURL(c) + c.Request.URL.Path
	loginClient := auth.LoginClientWeb
	ctx := auth.WithLoginClient(c.Request.Context(), loginClient)
	ctx = auth.WithLoginRequestMeta(ctx, buildLoginRequestMeta(c, loginClient, ""))
	tokens, loginErr := h.authService.LoginWithKeycloakAuthCode(ctx, code, redirectURI)
	if loginErr != nil {
		h.logger.Warn("Keycloak callback failed during code exchange",
			zap.String("redirect_uri", redirectURI),
			zap.Error(loginErr),
		)
		h.redirectFailedKeycloakLogin(c)
		return
	}
	if !h.validateIssuedTokenHost(c, tokens) {
		return
	}

	secure := requestIsSecure(c)
	if secure {
		c.SetSameSite(http.SameSiteNoneMode)
	} else {
		c.SetSameSite(http.SameSiteLaxMode)
	}
	c.SetCookie(keycloakOAuthStateCookie, "", -1, "/", "", secure, true)
	c.SetCookie(keycloakOAuthRedirectCookie, "", -1, "/", "", secure, true)
	report := setAuthCookies(c, tokens)
	h.logger.Info("Keycloak callback completed",
		zap.String("redirect_target", redirectTarget),
		zap.String("frontend_callback", frontendAuthCallbackTarget(redirectTarget)),
		zap.String("access_token_meta", tokenLogMeta(tokens.AccessToken)),
		zap.String("refresh_token_meta", tokenLogMeta(tokens.RefreshToken)),
	)
	h.markSecurityLogin(c, tokens)
	h.logAuthCookies("keycloak_callback", report, tokens)

	c.Redirect(http.StatusFound, frontendAuthCallbackTarget(redirectTarget))
}

func (h *AuthHandler) CompleteExternalAdminKeycloakLogin(c *gin.Context) {
	if h.authService == nil {
		h.redirectFailedExternalAdminLogin(c)
		return
	}
	if strings.TrimSpace(h.externalAdminClientID) == "" {
		h.redirectFailedExternalAdminLogin(c)
		return
	}

	expectedState, err := c.Cookie(externalAdminStateCookie)
	if err != nil || strings.TrimSpace(expectedState) == "" || expectedState != c.Query("state") {
		h.redirectFailedExternalAdminLogin(c)
		return
	}

	code := strings.TrimSpace(c.Query("code"))
	if code == "" {
		h.redirectFailedExternalAdminLogin(c)
		return
	}

	redirectTarget := defaultExternalAdminTarget
	if cookieRedirect, err := c.Cookie(externalAdminRedirectCookie); err == nil {
		redirectTarget = sanitizeExternalAdminRedirectTarget(cookieRedirect)
	}

	redirectURI := publicRequestBaseURL(c) + c.Request.URL.Path
	loginClient := auth.LoginClientWeb
	ctx := auth.WithLoginClient(c.Request.Context(), loginClient)
	ctx = auth.WithLoginRequestMeta(ctx, buildLoginRequestMeta(c, loginClient, ""))
	tokens, loginErr := h.authService.LoginWithKeycloakAuthCodeForClient(ctx, h.externalAdminClientID, code, redirectURI)
	if loginErr != nil {
		h.redirectFailedExternalAdminLogin(c)
		return
	}
	if !h.validateIssuedTokenHost(c, tokens) {
		return
	}

	secure := requestIsSecure(c)
	if secure {
		c.SetSameSite(http.SameSiteNoneMode)
	} else {
		c.SetSameSite(http.SameSiteLaxMode)
	}
	c.SetCookie(externalAdminStateCookie, "", -1, "/", "", secure, true)
	c.SetCookie(externalAdminRedirectCookie, "", -1, "/", "", secure, true)
	_ = setAuthCookies(c, tokens)
	h.markSecurityLogin(c, tokens)

	c.Redirect(http.StatusFound, frontendExternalAdminCallbackTarget(redirectTarget))
}

func (h *AuthHandler) redirectFailedKeycloakLogin(c *gin.Context) {
	secure := requestIsSecure(c)
	if secure {
		c.SetSameSite(http.SameSiteNoneMode)
	} else {
		c.SetSameSite(http.SameSiteLaxMode)
	}
	c.SetCookie(keycloakOAuthStateCookie, "", -1, "/", "", secure, true)
	c.SetCookie(keycloakOAuthRedirectCookie, "", -1, "/", "", secure, true)
	c.Redirect(http.StatusFound, "/web/login")
}

func (h *AuthHandler) redirectFailedExternalAdminLogin(c *gin.Context) {
	secure := requestIsSecure(c)
	if secure {
		c.SetSameSite(http.SameSiteNoneMode)
	} else {
		c.SetSameSite(http.SameSiteLaxMode)
	}
	c.SetCookie(externalAdminStateCookie, "", -1, "/", "", secure, true)
	c.SetCookie(externalAdminRedirectCookie, "", -1, "/", "", secure, true)
	c.Redirect(http.StatusFound, externalAdminLoginTarget(defaultExternalAdminTarget))
}

func (h *AuthHandler) redirectFailedEgovLogin(c *gin.Context, appErr *apperrors.AppError) {
	if prefersJSONResponse(c) {
		h.respondError(c, appErr)
		return
	}
	redirectTarget := sanitizeRedirectTarget(c.Query("redirect"))
	c.Redirect(http.StatusFound, frontendEgovErrorTarget(redirectTarget, appErr, middleware.RequestLocale(c)))
}

type KeycloakExchangeRequest struct {
	Code        string `json:"code" binding:"required"`
	RedirectURI string `json:"redirect_uri" binding:"required"`
}

// ExchangeKeycloakCode exchanges a Keycloak authorization code for tokens
// @Summary Exchange Keycloak code for tokens
// @Tags auth
// @Produce json
// @Param req body KeycloakExchangeRequest true "Exchange request"
// @Success 200 {object} map[string]interface{}
// @Router /auth/keycloak/exchange [post]
func (h *AuthHandler) ExchangeKeycloakCode(c *gin.Context) {
	if h.authService == nil {
		h.respondError(c, apperrors.Internal("auth service is not configured"))
		return
	}

	var req KeycloakExchangeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.BadRequest("invalid request body"))
		return
	}

	loginClient := detectLoginClient(c)
	ctx := auth.WithLoginClient(c.Request.Context(), loginClient)
	ctx = auth.WithLoginRequestMeta(ctx, buildLoginRequestMeta(c, loginClient, ""))
	tokens, loginErr := h.authService.LoginWithKeycloakAuthCode(ctx, req.Code, req.RedirectURI)
	if loginErr != nil {
		h.respondError(c, apperrors.ToAppError(loginErr))
		return
	}
	if !h.validateIssuedTokenHost(c, tokens) {
		return
	}

	h.logger.Info("User logged in via Keycloak frontend exchange")
	report := setAuthCookies(c, tokens)
	h.markSecurityLogin(c, tokens)
	h.logAuthCookies("keycloak_exchange", report, tokens)
	respondJSON(c, http.StatusOK, tokens)
}

// BootstrapKeycloakSession creates local messenger cookies from a valid Keycloak
// access token that was already issued for another trusted GovApps web client.
func (h *AuthHandler) BootstrapKeycloakSession(c *gin.Context) {
	if h.authService == nil || h.tokenManager == nil {
		h.respondError(c, apperrors.Internal("keycloak bootstrap dependencies are not configured"))
		return
	}

	keycloakAccessToken := extractAuthorizationBearer(c)
	if keycloakAccessToken == "" {
		h.respondError(c, apperrors.Unauthorized("missing keycloak access token"))
		return
	}

	claims, err := verifyBootstrapKeycloakToken(c.Request.Context(), keycloakAccessToken)
	if err != nil {
		iss, sub, azp, audiences := extractBootstrapJWTDebugSummary(keycloakAccessToken)
		h.logger.Warn("keycloak bootstrap token validation failed",
			zap.Error(err),
			zap.String("issuer", iss),
			zap.String("subject", sub),
			zap.String("authorized_party", azp),
			zap.Strings("audiences", audiences),
		)
		h.respondError(c, classifyBootstrapTokenValidationError(err))
		return
	}

	if err := h.authService.SyncUserFromKeycloakAccessToken(c.Request.Context(), keycloakAccessToken); err != nil {
		h.respondError(c, apperrors.InternalWithErr("failed to sync user from keycloak access token", err))
		return
	}

	user, err := h.authService.CurrentUserFromAccessToken(c.Request.Context(), keycloakAccessToken)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	if err := h.tokenManager.CheckCredentialClaims(c.Request.Context(), user.ID, claims); err != nil {
		h.respondError(c, apperrors.ReauthRequired("Please sign in again"))
		return
	}
	loginClient := detectLoginClient(c)
	ctx := auth.WithLoginClient(c.Request.Context(), loginClient)
	if at, err := auth.AuthenticationTime(claims); err == nil {
		ctx = auth.WithAuthenticationTime(ctx, at)
	}
	ctx = auth.WithLoginRequestMeta(ctx, buildLoginRequestMeta(c, loginClient, ""))

	tokens, err := h.tokenManager.GenerateTokenPair(
		ctx,
		user.ID,
		derefStringPtr(user.IIN),
		user.Email,
		user.Username,
		user.FirstName,
		user.LastName,
		false,
		nil,
	)
	if err != nil {
		h.respondError(c, apperrors.InternalWithErr("failed to issue local messenger session", err))
		return
	}
	tokens.MustChangePassword = user.IsFirstLogin
	if !h.validateIssuedTokenHost(c, tokens) {
		return
	}

	report := setAuthCookies(c, tokens)
	h.logger.Info("keycloak bootstrap completed",
		zap.String("subject", mapClaimString(claims, "sub")),
		zap.String("authorized_party", mapClaimString(claims, "azp")),
		zap.Strings("audiences", keycloak.ExtractAudiencesForLog(claims["aud"])),
	)
	h.markSecurityLogin(c, tokens)
	h.logAuthCookies("keycloak_bootstrap", report, tokens)

	respondJSON(c, http.StatusOK, gin.H{"ok": true})
}

func (h *AuthHandler) StartMobileKeycloakLogin(c *gin.Context) {
	if h.authService == nil {
		h.respondError(c, apperrors.Internal("auth service is not configured"))
		return
	}

	redirectURI := strings.TrimSpace(c.Query("redirect_uri"))
	if !isAllowedMobileRedirectURI(redirectURI) {
		h.respondError(c, apperrors.Validation("unsupported mobile redirect_uri"))
		return
	}

	codeChallenge := strings.TrimSpace(c.Query("code_challenge"))
	if codeChallenge == "" {
		h.respondError(c, apperrors.Validation("code_challenge is required"))
		return
	}

	codeChallengeMethod := strings.TrimSpace(c.DefaultQuery("code_challenge_method", "S256"))
	if !strings.EqualFold(codeChallengeMethod, "S256") {
		h.respondError(c, apperrors.Validation("code_challenge_method must be S256"))
		return
	}

	stateToken, err := signMobileKeycloakState(mobileKeycloakState{
		Platform:      string(auth.LoginClientMobile),
		RedirectURI:   redirectURI,
		OriginalState: strings.TrimSpace(c.Query("state")),
		IssuedAt:      time.Now().Unix(),
	})
	if err != nil {
		h.respondError(c, apperrors.InternalWithErr("failed to sign mobile keycloak state", err))
		return
	}

	authURL, err := h.authService.KeycloakMobileAuthCodeURL(redirectURI, stateToken, codeChallenge, codeChallengeMethod)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, mobileKeycloakLoginPayload{
		AuthURL: authURL,
		State:   stateToken,
	})
}

func (h *AuthHandler) CompleteMobileKeycloakLogin(c *gin.Context) {
	if h.authService == nil {
		h.respondError(c, apperrors.Internal("auth service is not configured"))
		return
	}

	var req mobileKeycloakCallbackRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.BadRequest("invalid request body"))
		return
	}

	if !isAllowedMobileRedirectURI(req.RedirectURI) {
		h.respondError(c, apperrors.Validation("unsupported mobile redirect_uri"))
		return
	}

	statePayload, err := verifyMobileKeycloakState(req.State)
	if err != nil {
		h.respondError(c, apperrors.Unauthorized("invalid mobile login state"))
		return
	}
	if statePayload.RedirectURI != strings.TrimSpace(req.RedirectURI) {
		h.respondError(c, apperrors.Unauthorized("mobile redirect_uri does not match login state"))
		return
	}

	loginClient := auth.LoginClientMobile
	ctx := auth.WithLoginClient(c.Request.Context(), loginClient)
	ctx = auth.WithLoginRequestMeta(ctx, buildLoginRequestMeta(c, loginClient, ""))

	tokens, exchangeErr := h.authService.LoginWithMobileKeycloakAuthCode(ctx, strings.TrimSpace(req.Code), strings.TrimSpace(req.RedirectURI), strings.TrimSpace(req.CodeVerifier))
	if exchangeErr != nil {
		h.respondError(c, apperrors.ToAppError(exchangeErr))
		return
	}

	user, userErr := h.authService.CurrentUserFromAccessToken(ctx, tokens.AccessToken)
	if userErr != nil {
		h.respondError(c, apperrors.ToAppError(userErr))
		return
	}

	middleware.SetSecurityActor(c, user.ID)
	respondJSON(c, http.StatusOK, mobileKeycloakCallbackResponse{
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		IDToken:      tokens.IDToken,
		TokenType:    tokens.TokenType,
		ExpiresIn:    tokens.ExpiresIn,
		ExpiresAt:    tokens.ExpiresAt,
		State:        statePayload.OriginalState,
		User:         user,
	})
}

// StartKeycloakSilentCheck initiates a prompt=none authorization flow to let the
// browser present Keycloak's own SSO cookies on /keycloak/realms/... paths.
// App cookies and Keycloak cookies remain separate by design.
func (h *AuthHandler) StartKeycloakSilentCheck(c *gin.Context) {
	redirectTarget := sanitizeRedirectTarget(c.Query("redirect"))

	if h.authService == nil {
		c.Redirect(http.StatusFound, "/web/login")
		return
	}

	state := randomURLToken()
	redirectURI := publicRequestBaseURL(c) + "/api/v1/auth/keycloak/sso-check/callback"
	secure := requestIsSecure(c)

	if secure {
		c.SetSameSite(http.SameSiteNoneMode)
	} else {
		c.SetSameSite(http.SameSiteLaxMode)
	}
	c.SetCookie(keycloakSilentStateCookie, state, 5*60, "/", "", secure, true)
	c.SetCookie(keycloakSilentRedirectCookie, redirectTarget, 5*60, "/", "", secure, true)

	c.Redirect(http.StatusFound, h.authService.KeycloakSilentAuthCodeURL(redirectURI, state))
}

// KeycloakLogoutRedirect performs a full browser-redirect logout through Keycloak.
// This is the ONLY correct way to clear KEYCLOAK_IDENTITY and KEYCLOAK_SESSION cookies,
// because those cookies are set by the Keycloak domain and can only be cleared by
// navigating the browser to Keycloak's end-session endpoint.
//
// Flow:
//  1. Revoke app-level tokens on backend (best-effort)
//  2. Clear app cookies (access_token, refresh_token)
//  3. Redirect browser to Keycloak /logout?post_logout_redirect_uri=.../web/login
func (h *AuthHandler) KeycloakLogoutRedirect(c *gin.Context) {
	if id, ok := logoutUserID(h.extractLogoutClaims(c.Request.Context(), extractAccessToken(c), extractRefreshToken(c))); ok {
		middleware.SetSecurityActor(c, id)
	}
	if h.authService == nil {
		clearAuthCookies(c)
		c.Redirect(http.StatusFound, "/web/login")
		return
	}

	refreshToken := extractRefreshToken(c)
	accessToken := extractAccessToken(c)

	// Best-effort backend revocation
	if refreshToken != "" {
		if h.tokenManager != nil {
			_ = h.tokenManager.RevokeToken(c.Request.Context(), refreshToken)
		}
		_ = h.authService.Logout(c.Request.Context(), refreshToken)
	}
	if accessToken != "" && h.tokenManager != nil {
		_ = h.tokenManager.RevokeToken(c.Request.Context(), accessToken)
	}

	// Clear app-level cookies
	clearAuthCookies(c)

	// Since we are logging out from Keycloak browser flow, we should terminate sessions
	// if we can identify the user. Try to get it from the gin context middleware.
	if userID, ok := middleware.GetUserID(c); ok && h.sessionTerminator != nil {
		h.sessionTerminator.CloseAllUserSessions(userID)
	} else if accessToken != "" {
		// Try to parse the token to get the user ID
		if claims, err := h.tokenManager.ExtractClaims(accessToken); err == nil && claims.Subject != "" {
			if uid, err := strconv.ParseInt(claims.Subject, 10, 64); err == nil {
				h.sessionTerminator.CloseAllUserSessions(uid)
			}
		}
	}

	// Return to a platform-origin completion page after Keycloak logout. It
	// clears the platform host's browser session before showing the shared login
	// page; tenant hosts cannot clear another origin's localStorage themselves.
	postLogoutRedirect := platformPublicBaseURL(c) + "/web/logout/complete"
	logoutURL := h.authService.KeycloakLogoutURL(postLogoutRedirect)

	// Redirect browser to Keycloak end-session endpoint
	// Keycloak will clear KEYCLOAK_IDENTITY and KEYCLOAK_SESSION
	c.Redirect(http.StatusFound, logoutURL)
}

func (h *AuthHandler) ExternalAdminKeycloakLogoutRedirect(c *gin.Context) {
	if id, ok := logoutUserID(h.extractLogoutClaims(c.Request.Context(), extractAccessToken(c), extractRefreshToken(c))); ok {
		middleware.SetSecurityActor(c, id)
	}
	if h.authService == nil {
		clearAuthCookies(c)
		c.Redirect(http.StatusFound, externalAdminLoginTarget(defaultExternalAdminTarget))
		return
	}

	refreshToken := extractRefreshToken(c)
	accessToken := extractAccessToken(c)

	if refreshToken != "" {
		if h.tokenManager != nil {
			_ = h.tokenManager.RevokeToken(c.Request.Context(), refreshToken)
		}
		_ = h.authService.Logout(c.Request.Context(), refreshToken)
	}
	if accessToken != "" && h.tokenManager != nil {
		_ = h.tokenManager.RevokeToken(c.Request.Context(), accessToken)
	}

	clearAuthCookies(c)

	if userID, ok := middleware.GetUserID(c); ok && h.sessionTerminator != nil {
		h.sessionTerminator.CloseAllUserSessions(userID)
	}

	postLogoutRedirect := publicRequestBaseURL(c) + "/external-admin/login"
	logoutURL, err := h.authService.KeycloakLogoutURLForClient(h.externalAdminClientID, postLogoutRedirect)
	if err != nil {
		c.Redirect(http.StatusFound, externalAdminLoginTarget(defaultExternalAdminTarget))
		return
	}

	c.Redirect(http.StatusFound, logoutURL)
}

// CompleteKeycloakSilentCheck handles the callback from prompt=none SSO check.
// It must not clear Keycloak cookies, and it should avoid aggressive app-cookie
// cleanup when Keycloak simply reports login_required.
func (h *AuthHandler) CompleteKeycloakSilentCheck(c *gin.Context) {
	if h.authService == nil {
		c.Redirect(http.StatusFound, "/web/login")
		return
	}

	secure := requestIsSecure(c)
	if secure {
		c.SetSameSite(http.SameSiteNoneMode)
	} else {
		c.SetSameSite(http.SameSiteLaxMode)
	}

	redirectTarget := defaultWebRedirectTarget
	if cookieRedirect, err := c.Cookie(keycloakSilentRedirectCookie); err == nil {
		redirectTarget = sanitizeRedirectTarget(cookieRedirect)
	}

	expectedState, err := c.Cookie(keycloakSilentStateCookie)
	c.SetCookie(keycloakSilentStateCookie, "", -1, "/", "", secure, true)
	c.SetCookie(keycloakSilentRedirectCookie, "", -1, "/", "", secure, true)

	if err != nil || strings.TrimSpace(expectedState) == "" || expectedState != c.Query("state") {
		h.logger.Warn("Silent Keycloak callback failed: invalid state",
			zap.Bool("state_cookie_present", err == nil && strings.TrimSpace(expectedState) != ""),
			zap.Bool("state_matches", err == nil && expectedState == c.Query("state")),
		)
		c.Redirect(http.StatusFound, frontendLoginTarget(redirectTarget))
		return
	}

	kcError := strings.TrimSpace(c.Query("error"))
	if kcError != "" {
		h.logger.Info("Silent Keycloak callback returned without active SSO session",
			zap.String("keycloak_error", kcError),
			zap.String("error_description", strings.TrimSpace(c.Query("error_description"))),
		)
		c.Redirect(http.StatusFound, frontendLoginTarget(redirectTarget))
		return
	}

	code := strings.TrimSpace(c.Query("code"))
	if code == "" {
		c.Redirect(http.StatusFound, frontendLoginTarget(redirectTarget))
		return
	}

	redirectURI := publicRequestBaseURL(c) + "/api/v1/auth/keycloak/sso-check/callback"
	loginClient := detectLoginClient(c)
	ctx := auth.WithLoginClient(c.Request.Context(), loginClient)
	ctx = auth.WithLoginRequestMeta(ctx, buildLoginRequestMeta(c, loginClient, ""))
	tokens, loginErr := h.authService.LoginWithKeycloakAuthCode(ctx, code, redirectURI)
	if loginErr != nil {
		h.logger.Warn("Silent Keycloak callback failed during code exchange", zap.Error(loginErr))
		c.Redirect(http.StatusFound, frontendLoginTarget(redirectTarget))
		return
	}
	if !h.validateIssuedTokenHost(c, tokens) {
		return
	}

	report := setAuthCookies(c, tokens)
	h.markSecurityLogin(c, tokens)
	h.logAuthCookies("keycloak_silent_callback", report, tokens)
	c.Redirect(http.StatusFound, frontendAuthCallbackTarget(redirectTarget))
}

func loginFieldForLog(req auth.LoginRequest) string {
	switch {
	case req.Identifier != "":
		return "identifier"
	case req.Login != "":
		return "login"
	case req.Email != "":
		return "email"
	case req.Username != "":
		return "username"
	case req.IIN != "":
		return "iin"
	default:
		return "unknown"
	}
}

func detectLoginClient(c *gin.Context) auth.LoginClient {
	if c == nil || c.Request == nil {
		return auth.LoginClientUnknown
	}

	if strings.EqualFold(strings.TrimSpace(c.GetHeader("X-Client-Platform")), string(auth.LoginClientMobile)) {
		return auth.LoginClientMobile
	}
	if strings.EqualFold(strings.TrimSpace(c.Query("platform")), string(auth.LoginClientMobile)) {
		return auth.LoginClientMobile
	}

	if value := strings.TrimSpace(c.GetHeader("X-Web-Device-ID")); value != "" {
		return auth.LoginClientWeb
	}
	if value := strings.TrimSpace(c.Query("web_device_id")); value != "" {
		return auth.LoginClientWeb
	}
	if strings.EqualFold(strings.TrimSpace(c.GetHeader("X-Client-Platform")), string(auth.LoginClientWeb)) {
		return auth.LoginClientWeb
	}
	if strings.EqualFold(strings.TrimSpace(c.Query("platform")), string(auth.LoginClientWeb)) {
		return auth.LoginClientWeb
	}

	for _, header := range []string{"Origin", "Sec-Fetch-Site", "Sec-Fetch-Mode", "Sec-Fetch-Dest"} {
		if strings.TrimSpace(c.GetHeader(header)) != "" {
			return auth.LoginClientWeb
		}
	}

	return auth.LoginClientUnknown
}

func buildLoginRequestMeta(c *gin.Context, client auth.LoginClient, fallbackDeviceID string) auth.LoginRequestMeta {
	if c == nil || c.Request == nil {
		return auth.LoginRequestMeta{}
	}

	source := strings.TrimSpace(c.GetHeader("X-Client-Platform"))
	if source == "" {
		source = strings.TrimSpace(c.Query("platform"))
	}
	if source == "" {
		switch client {
		case auth.LoginClientWeb:
			source = "web"
		case auth.LoginClientMobile:
			source = "mobile"
		default:
			source = "unknown"
		}
	}

	deviceID := strings.TrimSpace(c.GetHeader("X-Web-Device-ID"))
	if deviceID == "" {
		deviceID = strings.TrimSpace(c.GetHeader("X-Device-ID"))
	}
	if deviceID == "" {
		deviceID = strings.TrimSpace(c.Query("web_device_id"))
	}
	if deviceID == "" {
		deviceID = strings.TrimSpace(fallbackDeviceID)
	}
	if deviceID == "" && client == auth.LoginClientWeb {
		deviceID = "Web Browser"
	}

	sessionID := strings.TrimSpace(c.GetHeader("X-Session-ID"))
	if sessionID == "" {
		sessionID = strings.TrimSpace(c.GetHeader("X-Request-ID"))
	}

	return auth.LoginRequestMeta{
		Source:    source,
		IP:        strings.TrimSpace(c.ClientIP()),
		UserAgent: strings.TrimSpace(c.Request.UserAgent()),
		DeviceID:  deviceID,
		SessionID: sessionID,
		RequestID: ginRequestID(c),
	}
}

func ginRequestID(c *gin.Context) string {
	if c == nil {
		return ""
	}
	if value, ok := c.Get("request_id"); ok {
		if id, ok := value.(string); ok {
			return strings.TrimSpace(id)
		}
	}
	return strings.TrimSpace(c.GetHeader("X-Request-ID"))
}

func sortedQueryKeys(values url.Values) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// CompleteOnboarding godoc
// @Summary Complete user onboarding
// @Description Completes welcome screens. Native iForm hires must change their temporary password first.
// @Tags auth
// @Security BearerAuth
// @Produce json
// @Success 200 {object} map[string]string
// @Failure 403 {object} map[string]interface{}
// @Router /auth/onboarding/complete [post]
func (h *AuthHandler) CompleteOnboarding(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	if err := h.authService.CompleteOnboarding(c.Request.Context(), userID); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "onboarding completed")})
}

// RefreshToken godoc
// @Summary Refresh access token
// @Description Get new access token using refresh token
// @Tags auth
// @Security BearerAuth
// @Produce json
// @Param request body object{refresh_token=string} false "Refresh token in body"
// @Success 200 {object} auth.TokenPair
// @Failure 401 {object} map[string]interface{}
// @Router /auth/refresh [post]
func (h *AuthHandler) RefreshToken(c *gin.Context) {
	refreshToken := extractRefreshToken(c)

	if refreshToken == "" {
		h.logger.Warn("Refresh token not found in header, cookie or body")
		h.respondError(c, apperrors.Unauthorized("refresh token not found"))
		return
	}

	loginClient := detectLoginClient(c)
	ctx := auth.WithLoginClient(c.Request.Context(), loginClient)
	ctx = auth.WithLoginRequestMeta(ctx, buildLoginRequestMeta(c, loginClient, ""))

	if h.tokenManager != nil {
		if tokens, err := h.tokenManager.RefreshAccessToken(ctx, refreshToken); err == nil {
			if !h.validateIssuedTokenHost(c, tokens) {
				return
			}
			report := setAuthCookies(c, tokens)
			h.markSecurityLogin(c, tokens)
			h.logAuthCookies("refresh_local_token_manager", report, tokens)
			respondJSON(c, http.StatusOK, tokens)
			return
		}
	}

	tokens, err := h.authService.RefreshToken(ctx, refreshToken)
	if err != nil {
		h.logger.Warn("Failed to refresh token", zap.Error(err))
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	if !h.validateIssuedTokenHost(c, tokens) {
		return
	}

	// Automatic PIN refresh token sync if device_id is present
	deviceID, _ := c.Get("device_id")
	if deviceID == "" {
		deviceID = c.GetHeader("X-Device-ID")
	}
	if deviceID == "" {
		deviceID = c.GetHeader("X-Web-Device-ID")
	}

	if devID, ok := deviceID.(string); ok && devID != "" && h.quickPinService != nil {
		if sub := extractSubFromJWT(tokens.AccessToken); sub != "" {
			user, err := h.authService.GetCurrentUserByKeycloakID(ctx, sub)
			if err == nil && user != nil {
				_ = h.quickPinService.SyncRefreshToken(ctx, user.ID, devID, tokens.RefreshToken, azpFromJWT(tokens.AccessToken))
			}
		}
	}

	report := setAuthCookies(c, tokens)
	h.markSecurityLogin(c, tokens)
	h.logAuthCookies("refresh_keycloak", report, tokens)
	respondJSON(c, http.StatusOK, tokens)
}

// Helper to extract sub from JWT without full validation (already done by Keycloak or not needed for this)
func extractSubFromJWT(tokenStr string) string {
	parts := strings.Split(tokenStr, ".")
	if len(parts) < 2 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		Sub string `json:"sub"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	return claims.Sub
}

// Logout godoc
// @Summary Logout user
// @Description Logout user and revoke tokens
// @Tags auth
// @Security BearerAuth
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /auth/logout [post]
func (h *AuthHandler) Logout(c *gin.Context) {
	refreshToken := extractRefreshToken(c)
	accessToken := extractAccessToken(c)
	hadAuthCookies := hasAuthCookies(c)
	logoutClaims := h.extractLogoutClaims(c.Request.Context(), accessToken, refreshToken)
	if id, ok := logoutUserID(logoutClaims); ok {
		middleware.SetSecurityActor(c, id)
	}

	if err := h.revokeLinkedWebSessionOnLogout(c.Request.Context(), logoutClaims); err != nil {
		h.respondError(c, err)
		return
	}

	if h.pushDeviceManager != nil {
		if userID, ok := logoutUserID(logoutClaims); ok {
			if deviceID := currentLogoutDeviceID(c, logoutClaims); deviceID != "" {
				if err := h.pushDeviceManager.DeactivateDevice(c.Request.Context(), userID, deviceID); err != nil {
					h.respondError(c, apperrors.InternalWithErr("failed to deactivate device token", err))
					return
				}
			} else if fcmToken := currentLogoutFCMToken(c); fcmToken != "" {
				if err := h.pushDeviceManager.DeactivateToken(c.Request.Context(), userID, fcmToken); err != nil {
					h.respondError(c, apperrors.InternalWithErr("failed to deactivate device token", err))
					return
				}
			}
		}
	}

	if h.tokenManager != nil {
		localRevoked := false

		if refreshToken != "" {
			if err := h.tokenManager.RevokeToken(c.Request.Context(), refreshToken); err == nil {
				localRevoked = true
			} else {
				h.logger.Debug("failed to revoke local refresh token during logout", zap.Error(err))
			}
		}

		if accessToken != "" {
			if err := h.tokenManager.RevokeToken(c.Request.Context(), accessToken); err == nil {
				localRevoked = true
			} else {
				h.logger.Debug("failed to revoke local access token during logout", zap.Error(err))
			}
		}

		if localRevoked || hadAuthCookies {
			report := clearAuthCookies(c)
			h.logClearedAuthCookies("logout_local", report)
			respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "logged out successfully")})
			return
		}
	}

	if refreshToken == "" {
		report := clearAuthCookies(c)
		h.logClearedAuthCookies("logout_missing_refresh", report)
		h.respondError(c, apperrors.Unauthorized("refresh token not found"))
		return
	}

	if err := h.authService.Logout(c.Request.Context(), refreshToken); err != nil {
		h.logger.Warn("failed to logout via auth provider", zap.Error(err))
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	report := clearAuthCookies(c)
	h.logClearedAuthCookies("logout_provider", report)

	if logoutClaims != nil && logoutClaims.Subject != "" {
		if uid, err := strconv.ParseInt(logoutClaims.Subject, 10, 64); err == nil && h.sessionTerminator != nil {
			h.sessionTerminator.CloseAllUserSessions(uid)
		}
	}

	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "logged out successfully")})
}

// LogoutAll godoc
// @Summary Logout from all devices
// @Description Logout user from all devices
// @Tags auth
// @Security BearerAuth
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /auth/logout-all [post]
func (h *AuthHandler) LogoutAll(c *gin.Context) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		claims := h.extractLogoutClaims(c.Request.Context(), extractAccessToken(c), extractRefreshToken(c))
		var parsed bool
		userID, parsed = logoutUserID(claims)
		if !parsed {
			h.respondError(c, apperrors.Unauthorized("authentication required"))
			return
		}
	}

	middleware.SetSecurityActor(c, userID)
	if h.tokenManager != nil {
		if err := h.tokenManager.RevokeAllUserTokens(c.Request.Context(), userID); err != nil {
			h.respondError(c, apperrors.InternalWithErr("failed to revoke local sessions", err))
			return
		}
	}

	if err := h.authService.LogoutAll(c.Request.Context(), userID); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	if h.pushDeviceManager != nil {
		if err := h.pushDeviceManager.DeactivateAllDevices(c.Request.Context(), userID); err != nil {
			h.respondError(c, apperrors.InternalWithErr("failed to deactivate device tokens", err))
			return
		}
	}

	if h.sessionTerminator != nil {
		h.sessionTerminator.CloseAllUserSessions(userID)
	}

	h.logger.Info("User logged out from all devices", zap.Int64("user_id", userID))
	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "logged out from all devices")})
}

// Me godoc
// @Summary Get current user
// @Description Get current authenticated user info
// @Tags auth
// @Security BearerAuth
// @Produce json
// @Success 200 {object} domain.UserResponse
// @Failure 401 {object} map[string]interface{}
// @Router /auth/me [get]
func (h *AuthHandler) Me(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	user, err := h.authService.GetCurrentUser(c.Request.Context(), userID)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, user)
}

// MailOAuthProfile godoc
// @Summary Get mail OAuth profile
// @Description Returns a narrow authenticated identity payload for the internal mail stack after internal token and bearer auth validation
// @Tags auth
// @Security BearerAuth
// @Produce json
// @Param X-Internal-Token header string true "Internal mail token"
// @Success 200 {object} MailOAuthProfileResponse
// @Failure 401 {object} map[string]interface{}
// @Router /api/internal/mail/oauth/profile [get]
func (h *AuthHandler) MailOAuthProfile(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	user, err := h.authService.GetCurrentUser(c.Request.Context(), userID)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	mailboxLogin := strings.ToLower(strings.TrimSpace(user.Email))
	if h.authService != nil {
		resolvedMailboxLogin, resolveErr := h.authService.ResolveMailOAuthMailboxLogin(c.Request.Context(), user)
		if resolveErr != nil {
			h.respondError(c, apperrors.ToAppError(resolveErr))
			return
		}
		if strings.TrimSpace(resolvedMailboxLogin) != "" {
			mailboxLogin = strings.ToLower(strings.TrimSpace(resolvedMailboxLogin))
			if mailboxLogin != "" {
				user.Email = mailboxLogin
			}
		}
	}

	resp := MailOAuthProfileResponse{
		UserID:         user.ID,
		KeycloakID:     user.KeycloakID,
		Email:          strings.ToLower(strings.TrimSpace(user.Email)),
		Username:       strings.TrimSpace(user.Username),
		FirstName:      user.FirstName,
		LastName:       user.LastName,
		Role:           user.Role,
		IsActive:       user.IsActive,
		MailboxLogin:   mailboxLogin,
		MailboxAddr:    mailboxLogin,
		OrganizationID: user.OrganizationID,
		TeamID:         user.TeamID,
		ManagerID:      user.ManagerID,
	}

	respondJSON(c, http.StatusOK, resp)
}

// ChangePasswordRequest represents the request to change password
type ChangePasswordRequest struct {
	OldPassword string                       `json:"old_password" binding:"required"`
	NewPassword string                       `json:"new_password" binding:"required,min=8"`
	E2EEBackup  *ChangePasswordBackupRequest `json:"e2ee_backup,omitempty"`
}

type ChangePasswordBackupRequest struct {
	EncryptedPrivateKey string                      `json:"encrypted_private_key" binding:"required"`
	Salt                string                      `json:"salt" binding:"required"`
	IV                  string                      `json:"iv" binding:"required"`
	KDFAlgorithm        string                      `json:"kdf_algorithm"`
	KDFParams           *domain.KeyDerivationParams `json:"kdf_params,omitempty"`
	Version             int                         `json:"version"`
	BackupKeyType       string                      `json:"backup_key_type"`
	BackupKeyFormat     string                      `json:"backup_key_format"`
	BackupKeyScope      string                      `json:"backup_key_scope"`
	BackupFormatVersion int                         `json:"backup_format_version"`
	CipherAlgorithm     string                      `json:"cipher_algorithm"`
	CipherTagEmbedded   *bool                       `json:"cipher_tag_embedded,omitempty"`
	AADMode             string                      `json:"aad_mode"`
	PasswordProcessing  string                      `json:"password_processing"`
}

// UpdateAvatarRequest represents the request to update avatar
type UpdateAvatarRequest struct {
	AvatarURL string `json:"avatar_url" binding:"required,url"`
}

// UpdateUserAvatarRequest represents the request to update another user's avatar
type UpdateUserAvatarRequest struct {
	UserID    int64  `json:"user_id" binding:"required"`
	AvatarURL string `json:"avatar_url" binding:"required,url"`
}

// ChangePassword godoc
// @Summary Change password
// @Description Change user password. No chat encryption backup is required; optional legacy backups retain compensation support.
// @Tags auth
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body ChangePasswordRequest true "Password change request"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Router /auth/password [put]
func (h *AuthHandler) ChangePassword(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	var req ChangePasswordRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	var backupReq *appservice.SaveKeyBackupRequest
	if req.E2EEBackup != nil {
		backupReq = &appservice.SaveKeyBackupRequest{
			EncryptedPrivateKey: req.E2EEBackup.EncryptedPrivateKey,
			Salt:                req.E2EEBackup.Salt,
			IV:                  req.E2EEBackup.IV,
			KDFAlgorithm:        req.E2EEBackup.KDFAlgorithm,
			KDFParams:           req.E2EEBackup.KDFParams,
			Version:             req.E2EEBackup.Version,
			BackupKeyType:       req.E2EEBackup.BackupKeyType,
			BackupKeyFormat:     req.E2EEBackup.BackupKeyFormat,
			BackupKeyScope:      req.E2EEBackup.BackupKeyScope,
			BackupFormatVersion: req.E2EEBackup.BackupFormatVersion,
			CipherAlgorithm:     req.E2EEBackup.CipherAlgorithm,
			CipherTagEmbedded:   req.E2EEBackup.CipherTagEmbedded,
			AADMode:             req.E2EEBackup.AADMode,
			PasswordProcessing:  req.E2EEBackup.PasswordProcessing,
		}
	}

	if err := h.authService.ChangePassword(c.Request.Context(), userID, req.OldPassword, req.NewPassword, backupReq); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	h.logger.Info("Password changed", zap.Int64("user_id", userID))
	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "password changed successfully")})
}

// UpdateAvatar godoc
// @Summary Update user avatar
// @Description Update current user's avatar URL
// @Tags auth
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body UpdateAvatarRequest true "Avatar update request"
// @Success 200 {object} domain.UserResponse
// @Failure 400 {object} map[string]interface{}
// @Router /auth/me/avatar [put]
func (h *AuthHandler) UpdateAvatar(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	var req UpdateAvatarRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	user, err := h.authService.UpdateAvatar(c.Request.Context(), userID, req.AvatarURL)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	h.logger.Info("Avatar updated", zap.Int64("user_id", userID))
	respondJSON(c, http.StatusOK, user)
}

// UpdateEmailRequest represents the request to update email
type UpdateEmailRequest struct {
	Email string `json:"email" binding:"required,email"`
}

// UpdateEmail godoc
// @Summary Update user email
// @Description Update current user's email address
// @Tags auth
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body UpdateEmailRequest true "Email update request"
// @Success 200 {object} domain.UserResponse
// @Failure 400 {object} map[string]interface{}
// @Router /auth/me/email [put]
func (h *AuthHandler) UpdateEmail(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	var req UpdateEmailRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	req.Email = strings.ToLower(strings.TrimSpace(req.Email))

	if strings.HasSuffix(req.Email, "@hr.local") {
		h.respondError(c, apperrors.Validation("Новый email не может быть в домене @hr.local"))
		return
	}

	user, err := h.authService.UpdateEmail(c.Request.Context(), userID, req.Email)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	h.logger.Info("Email updated", zap.Int64("user_id", userID), zap.String("new_email", req.Email))
	respondJSON(c, http.StatusOK, user)
}

// UpdateUserAvatar godoc
// @Summary Update any user's avatar
// @Description Open endpoint to update avatar URL for any user by user_id
// @Tags auth
// @Accept json
// @Produce json
// @Param request body UpdateUserAvatarRequest true "User avatar update request"
// @Success 200 {object} domain.UserResponse
// @Failure 400 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /auth/users/avatar [put]
func (h *AuthHandler) UpdateUserAvatar(c *gin.Context) {
	var req UpdateUserAvatarRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	if req.UserID <= 0 {
		h.respondError(c, apperrors.Validation("user_id must be greater than 0"))
		return
	}

	user, err := h.authService.UpdateAvatar(c.Request.Context(), req.UserID, req.AvatarURL)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	h.logger.Info("User avatar updated via open endpoint", zap.Int64("user_id", req.UserID))
	respondJSON(c, http.StatusOK, user)
}

// GetAllUsers godoc
// @Summary Get paginated users
// @Description Get a paginated list of all registered users with search capabilities
// @Tags users
// @Security BearerAuth
// @Param q query string false "Search query"
// @Param search query string false "Search query (alias)"
// @Param limit query int false "Limit" default(100)
// @Param offset query int false "Offset" default(0)
// @Param position_name query string false "Filter by position name"
// @Param department_name query string false "Filter by department name"
// @Param organization_name query string false "Filter by organization name"
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /users [get]
func (h *AuthHandler) GetAllUsers(c *gin.Context) {
	var params struct {
		Limit            int    `form:"limit" binding:"omitempty,min=1,max=1000"`
		Offset           int    `form:"offset" binding:"omitempty,min=0"`
		Query            string `form:"q"`
		Search           string `form:"search"`
		PositionName     string `form:"position_name"`
		DepartmentName   string `form:"department_name"`
		OrganizationName string `form:"organization_name"`
	}

	if err := c.ShouldBindQuery(&params); err != nil {
		h.respondError(c, apperrors.Validation("invalid query parameters"))
		return
	}

	if params.Limit <= 0 {
		params.Limit = 100
	}

	query := params.Query
	if query == "" {
		query = params.Search
	}

	filters := domain.UserFilters{
		PositionName:     params.PositionName,
		DepartmentName:   params.DepartmentName,
		OrganizationName: params.OrganizationName,
	}

	var users []*domain.UserResponse
	var total int
	var err error
	if requesterID, ok := middleware.GetUserID(c); ok {
		users, total, err = h.authService.GetDirectoryUsers(c.Request.Context(), requesterID, query, filters, params.Limit, params.Offset)
	} else {
		// Keep direct handler tests and non-router callers backward-compatible.
		users, total, err = h.authService.GetAllUsers(c.Request.Context(), query, filters, params.Limit, params.Offset)
	}
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	// Check if the client requested a paginated wrapper structure
	rawLimit := c.Query("limit")
	rawOffset := c.Query("offset")
	rawPage := c.Query("page")
	isPaginatedRequest := rawLimit != "" || rawOffset != "" || rawPage != "" || c.Query("paginated") == "true"

	if !isPaginatedRequest {
		// Return backward-compatible flat array
		respondJSON(c, http.StatusOK, users)
		return
	}

	respondJSON(c, http.StatusOK, gin.H{
		"items":  users,
		"total":  total,
		"limit":  params.Limit,
		"offset": params.Offset,
	})
}

// ListHRProfiles godoc
// @Summary List HR profiles
// @Description List user profiles linked to HR data with pagination, global search and specific filters.
// @Tags HR
// @Accept json
// @Produce json
// @Param q query string false "Global search (name, iin, position, etc.)"
// @Param job_name query string false "Filter by exact job title"
// @Param sub_name query string false "Filter by exact department name"
// @Param name_go query string false "Filter by exact organization name"
// @Param person_status query string false "Filter by person status"
// @Param iin query string false "Filter by exact IIN"
// @Param limit query int false "Limit" default(20)
// @Param offset query int false "Offset" default(0)
// @Success 200 {object} map[string]interface{}
// @Failure 401 {object} domain.ErrorResponse
// @Failure 500 {object} domain.ErrorResponse
// @Router /users/hr [get]
func (h *AuthHandler) ListHRProfiles(c *gin.Context) {
	requesterID := middleware.MustGetUserID(c)

	var params struct {
		Query        string `form:"q"`
		JobName      string `form:"job_name"`
		SubName      string `form:"sub_name"`
		NameGO       string `form:"name_go"`
		PersonStatus string `form:"person_status"`
		IIN          string `form:"iin"`
		Limit        int    `form:"limit" binding:"omitempty,min=1,max=100"`
		Offset       int    `form:"offset" binding:"omitempty,min=0"`
		Cursor       string `form:"cursor"`
	}

	if err := c.ShouldBindQuery(&params); err != nil {
		h.respondError(c, apperrors.Validation("invalid query parameters"))
		return
	}

	if params.Limit == 0 {
		params.Limit = 20
	}

	var cursorUpdatedAt *time.Time
	var cursorID *int64

	if params.Cursor != "" {
		decoded, err := base64.StdEncoding.DecodeString(params.Cursor)
		if err == nil {
			parts := strings.Split(string(decoded), "|")
			if len(parts) == 2 {
				parsedTime, errTime := time.Parse(time.RFC3339Nano, parts[0])
				parsedID, errID := strconv.ParseInt(parts[1], 10, 64)
				if errTime == nil && errID == nil {
					cursorUpdatedAt = &parsedTime
					cursorID = &parsedID
				}
			}
		}
	}

	filter := repository.HRProfileFilter{
		Query:           params.Query,
		JobName:         params.JobName,
		SubName:         params.SubName,
		NameGO:          params.NameGO,
		PersonStatus:    params.PersonStatus,
		IIN:             params.IIN,
		CursorUpdatedAt: cursorUpdatedAt,
		CursorID:        cursorID,
	}

	profiles, total, err := h.authService.ListHRProfiles(c.Request.Context(), requesterID, filter, params.Limit, params.Offset)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	pages := 0
	if params.Limit > 0 {
		pages = (total + params.Limit - 1) / params.Limit
	}

	nextCursor := ""
	if len(profiles) > 0 {
		lastProfile := profiles[len(profiles)-1]
		if lastProfile.HRProfile != nil && !lastProfile.HRProfile.UpdatedAt.IsZero() {
			cursorStr := lastProfile.HRProfile.UpdatedAt.Format(time.RFC3339Nano) + "|" + strconv.FormatInt(lastProfile.HRProfile.ID, 10)
			nextCursor = base64.StdEncoding.EncodeToString([]byte(cursorStr))
		}
	}

	respondJSON(c, http.StatusOK, gin.H{
		"items":       profiles,
		"total":       total,
		"pages":       pages,
		"limit":       params.Limit,
		"offset":      params.Offset,
		"query":       strings.TrimSpace(params.Query),
		"filter":      filter,
		"next_cursor": nextCursor,
	})
}

// GetManagementVacations godoc
// @Summary List management vacations
// @Description Get a list of management users currently on vacation.
// @Tags HR
// @Accept json
// @Produce json
// @Success 200 {array} domain.ManagementVacationResponse
// @Failure 500 {object} domain.ErrorResponse
// @Router /users/hr/vacations [get]
func (h *AuthHandler) GetManagementVacations(c *gin.Context) {
	vacations, err := h.authService.GetManagementVacations(c.Request.Context())
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, vacations)
}

// GetHRFilterValues godoc
// @Summary Get unique values for HR filters
// @Description Get a list of unique values for a specific field (job_name, sub_name, name_go, person_status, iin) to populate filter dropdowns.
// @Tags HR
// @Accept json
// @Produce json
// @Param field path string true "Field name (job_name, sub_name, name_go, person_status, iin)"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} domain.ErrorResponse
// @Failure 500 {object} domain.ErrorResponse
// @Router /users/hr/filters/{field} [get]
func (h *AuthHandler) GetHRFilterValues(c *gin.Context) {
	requesterID := middleware.MustGetUserID(c)
	field := c.Param("field")

	values, err := h.authService.GetHRFilterValues(c.Request.Context(), requesterID, field)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{
		"field":  field,
		"values": values,
	})
}

// GetHRProfileByUserID godoc
// @Summary Get HR profile by user id
// @Description Get one HR-linked user profile by local user id
// @Tags users
// @Security BearerAuth
// @Param user_id path int true "User ID"
// @Produce json
// @Success 200 {object} domain.UserHRProfileResponse
// @Router /users/hr/{user_id} [get]
func (h *AuthHandler) GetHRProfileByUserID(c *gin.Context) {
	requesterID := middleware.MustGetUserID(c)
	userID, err := strconv.ParseInt(c.Param("user_id"), 10, 64)
	if err != nil || userID <= 0 {
		h.respondError(c, apperrors.BadRequest("invalid user id"))
		return
	}

	profile, err := h.authService.GetHRProfileByUserID(c.Request.Context(), requesterID, userID)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, profile)
}

func (h *AuthHandler) respondError(c *gin.Context, err *apperrors.AppError) {
	respondAppError(c, err)
}

func (h *AuthHandler) extractLogoutClaims(ctx context.Context, accessToken, refreshToken string) *auth.Claims {
	if h.tokenManager == nil {
		return nil
	}

	candidates := []string{strings.TrimSpace(accessToken), strings.TrimSpace(refreshToken)}
	for _, token := range candidates {
		if token == "" {
			continue
		}

		if claims, err := h.tokenManager.ValidateToken(ctx, token); err == nil {
			return claims
		}
		if claims, err := h.tokenManager.ExtractClaims(token); err == nil {
			return claims
		}
	}

	return nil
}

func (h *AuthHandler) revokeLinkedWebSessionOnLogout(ctx context.Context, claims *auth.Claims) *apperrors.AppError {
	if h.linkedDeviceRevoker == nil || claims == nil || claims.Context == nil {
		return nil
	}
	if !claims.Context.Features["web_linked_device"] {
		return nil
	}

	deviceID := strings.TrimSpace(claims.Context.LinkedDeviceID)
	if deviceID == "" || claims.UserID <= 0 {
		return nil
	}

	if err := h.linkedDeviceRevoker.RevokeLinkedDevice(ctx, claims.UserID, deviceID); err != nil {
		if appErr := apperrors.GetAppError(err); appErr != nil {
			if appErr.Code == apperrors.ErrCodeNotFound {
				h.logger.Info("Linked web device already revoked during logout",
					zap.Int64("user_id", claims.UserID),
					zap.String("device_id", deviceID),
				)
				return nil
			}
			return appErr
		}
		return apperrors.InternalWithErr("failed to revoke linked web device during logout", err)
	}

	if h.tokenManager != nil {
		if err := h.tokenManager.RevokeLinkedDeviceTokens(ctx, claims.UserID, deviceID); err != nil {
			return apperrors.InternalWithErr("failed to revoke linked web tokens during logout", err)
		}
	}

	h.logger.Info("Linked web device revoked during logout",
		zap.Int64("user_id", claims.UserID),
		zap.String("device_id", deviceID),
	)
	return nil
}

func extractRefreshToken(c *gin.Context) string {
	if v, exists := c.Get("refresh_token"); exists {
		if token, ok := v.(string); ok && strings.TrimSpace(token) != "" {
			return strings.TrimSpace(token)
		}
	}

	if token, err := c.Cookie("refresh_token"); err == nil && strings.TrimSpace(token) != "" {
		return strings.TrimSpace(token)
	}

	var req struct {
		RefreshToken string `json:"refresh_token"`
		DeviceID     string `json:"device_id"`
		FCMToken     string `json:"fcm_token"`
	}
	if err := c.ShouldBindJSON(&req); err == nil {
		if strings.TrimSpace(req.DeviceID) != "" {
			c.Set("device_id", strings.TrimSpace(req.DeviceID))
		}
		if strings.TrimSpace(req.FCMToken) != "" {
			c.Set("fcm_token", strings.TrimSpace(req.FCMToken))
		}
		if strings.TrimSpace(req.RefreshToken) != "" {
			return strings.TrimSpace(req.RefreshToken)
		}
	}

	authHeader := c.GetHeader("Authorization")
	if authHeader != "" {
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
			return strings.TrimSpace(parts[1])
		}
	}

	return ""
}

func extractAccessToken(c *gin.Context) string {
	if bearer := extractAuthorizationBearer(c); bearer != "" {
		return bearer
	}

	if token, err := c.Cookie("access_token"); err == nil && strings.TrimSpace(token) != "" {
		return strings.TrimSpace(token)
	}

	return ""
}

func extractAuthorizationBearer(c *gin.Context) string {
	authHeader := c.GetHeader("Authorization")
	if authHeader == "" {
		return ""
	}

	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		return strings.TrimSpace(parts[1])
	}

	return ""
}

func verifyBootstrapKeycloakToken(ctx context.Context, token string) (jwt.MapClaims, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("load keycloak config: %w", err)
	}

	allowedAZP := appendUniqueStrings(cfg.Keycloak.AllowedAZP, cfg.Keycloak.ClientID, "alem-documentolog-web")
	verifier, err := keycloak.NewVerifier(ctx, cfg.Keycloak.JWKSURL, cfg.Keycloak.Issuer, nil, allowedAZP)
	if err != nil {
		return nil, err
	}

	_, claims, err := verifier.Parse(ctx, token)
	if err != nil {
		return nil, err
	}

	return claims, nil
}

func classifyBootstrapTokenValidationError(err error) *apperrors.AppError {
	if err == nil {
		return apperrors.TokenInvalid()
	}

	msg := strings.ToLower(err.Error())
	switch {
	case errors.Is(err, keycloak.ErrInvalidIssuer):
		return apperrors.TokenInvalid().WithDetail("reason", "invalid issuer")
	case errors.Is(err, keycloak.ErrMissingAudience):
		return apperrors.TokenInvalid().WithDetail("reason", "missing audience")
	case errors.Is(err, keycloak.ErrInvalidAudience):
		return apperrors.TokenInvalid().WithDetail("reason", "invalid audience")
	case errors.Is(err, keycloak.ErrInvalidAuthorizedParty):
		return apperrors.TokenInvalid().WithDetail("reason", "invalid authorized party")
	case errors.Is(err, keycloak.ErrRefreshTokenNotAccepted):
		return apperrors.TokenInvalid().WithDetail("reason", "refresh token is not accepted")
	}
	if strings.Contains(msg, "expired") {
		return apperrors.TokenExpired()
	}
	if strings.Contains(msg, "signature") || strings.Contains(msg, "signing method") || strings.Contains(msg, "unknown kid") {
		return apperrors.TokenInvalid().WithDetail("reason", "invalid token signature")
	}

	return apperrors.TokenInvalid()
}

func extractBootstrapJWTDebugSummary(tokenStr string) (iss, sub, azp string, audiences []string) {
	parser := jwt.Parser{}
	claims := jwt.MapClaims{}
	if _, _, err := parser.ParseUnverified(tokenStr, claims); err != nil {
		return "", "", "", nil
	}

	iss, _ = claims["iss"].(string)
	sub, _ = claims["sub"].(string)
	azp, _ = claims["azp"].(string)
	return iss, sub, azp, keycloak.ExtractAudiencesForLog(claims["aud"])
}

func appendUniqueStrings(base []string, extras ...string) []string {
	seen := make(map[string]struct{}, len(base)+len(extras))
	out := make([]string, 0, len(base)+len(extras))

	for _, value := range append(append([]string{}, base...), extras...) {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, exists := seen[trimmed]; exists {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}

	return out
}

func derefStringPtr(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func mapClaimString(claims jwt.MapClaims, key string) string {
	value, _ := claims[key].(string)
	return strings.TrimSpace(value)
}

func currentLogoutDeviceID(c *gin.Context, claims *auth.Claims) string {
	if c == nil {
		return ""
	}

	if value, exists := c.Get("device_id"); exists {
		if deviceID, ok := value.(string); ok && strings.TrimSpace(deviceID) != "" {
			return strings.TrimSpace(deviceID)
		}
	}

	if deviceID := strings.TrimSpace(c.GetHeader("X-Device-ID")); deviceID != "" {
		return deviceID
	}
	if deviceID := strings.TrimSpace(c.Query("device_id")); deviceID != "" {
		return deviceID
	}
	if claims != nil && claims.Context != nil {
		if deviceID := strings.TrimSpace(claims.Context.LinkedDeviceID); deviceID != "" {
			return deviceID
		}
	}

	return ""
}

func currentLogoutFCMToken(c *gin.Context) string {
	if c == nil {
		return ""
	}

	if value, exists := c.Get("fcm_token"); exists {
		if token, ok := value.(string); ok && strings.TrimSpace(token) != "" {
			return strings.TrimSpace(token)
		}
	}

	return ""
}

func logoutUserID(claims *auth.Claims) (int64, bool) {
	if claims == nil || strings.TrimSpace(claims.Subject) == "" {
		return 0, false
	}

	userID, err := strconv.ParseInt(strings.TrimSpace(claims.Subject), 10, 64)
	if err != nil || userID == 0 {
		return 0, false
	}

	return userID, true
}

func hasAuthCookies(c *gin.Context) bool {
	if token, err := c.Cookie("access_token"); err == nil && strings.TrimSpace(token) != "" {
		return true
	}
	if token, err := c.Cookie("refresh_token"); err == nil && strings.TrimSpace(token) != "" {
		return true
	}
	return false
}

func (h *AuthHandler) logAuthCookies(flow string, report authCookieReport, tokens *auth.TokenPair) {
	if h == nil || h.logger == nil {
		return
	}

	fields := []zap.Field{
		zap.String("flow", flow),
		zap.Bool("access_cookie_set", report.AccessSet),
		zap.Bool("refresh_cookie_set", report.RefreshSet),
		zap.Strings("cookie_names", report.cookieNames()),
		zap.Strings("additional_app_cookie_names", []string{}),
		zap.Bool("refresh_token_empty", report.RefreshTokenEmpty),
		zap.Int("access_cookie_max_age", report.AccessMaxAge),
		zap.Int("refresh_cookie_max_age", report.RefreshMaxAge),
		zap.Bool("cookie_secure", report.Secure),
		zap.Bool("cookie_http_only", report.HttpOnly),
		zap.String("cookie_same_site", report.SameSiteLabel),
		zap.String("cookie_path", report.Path),
	}

	if tokens != nil {
		fields = append(fields,
			zap.String("access_token_meta", tokenLogMeta(tokens.AccessToken)),
			zap.String("refresh_token_meta", tokenLogMeta(tokens.RefreshToken)),
		)
	}

	if !report.AccessSet {
		h.logger.Warn("Auth cookies were not set", fields...)
		return
	}

	h.logger.Info("Auth cookies set", fields...)
}

func (h *AuthHandler) logClearedAuthCookies(flow string, report authCookieReport) {
	if h == nil || h.logger == nil {
		return
	}

	h.logger.Info("Auth cookies cleared",
		zap.String("flow", flow),
		zap.Strings("cookie_names", report.cookieNames()),
		zap.Bool("cookie_secure", report.Secure),
		zap.Bool("cookie_http_only", report.HttpOnly),
		zap.String("cookie_same_site", report.SameSiteLabel),
		zap.String("cookie_path", report.Path),
	)
}

func tokenLogMeta(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return "empty"
	}
	if len(token) <= 12 {
		return "len=" + strconv.Itoa(len(token))
	}
	return "len=" + strconv.Itoa(len(token)) + ",prefix=" + token[:6] + ",suffix=" + token[len(token)-6:]
}

func randomURLToken() string {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return strconv.FormatInt(timeNowUnixNano(), 36)
	}
	return base64.RawURLEncoding.EncodeToString(buf[:])
}

func timeNowUnixNano() int64 {
	return time.Now().UnixNano()
}

func publicRequestBaseURL(c *gin.Context) string {
	if configuredPublicBase := configuredAppPublicBaseURL(); configuredPublicBase != "" {
		// Keep OAuth callbacks and post-login redirects on the tenant host. The
		// configured APP_PUBLIC_URL is the platform host and is only the fallback
		// for requests received on that host.
		baseDomain := strings.TrimSpace(os.Getenv("TENANT_BASE_DOMAIN"))
		if baseDomain == "" {
			baseDomain = "teamspace.kz"
		}
		if _, isTenant, valid := middleware.TenantSlugFromHost(c.Request.Host, baseDomain); valid && isTenant {
			parsed, err := url.Parse(configuredPublicBase)
			if err == nil && parsed.Scheme != "" && c.Request.Host != "" {
				return parsed.Scheme + "://" + c.Request.Host
			}
		}
		return configuredPublicBase
	}

	scheme := strings.TrimSpace(c.GetHeader("X-Forwarded-Proto"))
	if scheme == "" {
		if requestIsSecure(c) {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}

	host := strings.TrimSpace(c.GetHeader("X-Forwarded-Host"))
	if host == "" {
		host = c.Request.Host
	}
	return scheme + "://" + host
}

// platformPublicBaseURL returns the canonical platform host for shared pages
// such as the Workspace login page. Unlike publicRequestBaseURL, this must not
// preserve a tenant subdomain from the incoming request.
func platformPublicBaseURL(c *gin.Context) string {
	if configuredPublicBase := configuredAppPublicBaseURL(); configuredPublicBase != "" {
		return configuredPublicBase
	}

	baseDomain := strings.TrimSpace(os.Getenv("TENANT_BASE_DOMAIN"))
	if baseDomain == "" {
		baseDomain = "teamspace.kz"
	}
	if _, isTenant, valid := middleware.TenantSlugFromHost(c.Request.Host, baseDomain); valid && isTenant {
		scheme := strings.TrimSpace(c.GetHeader("X-Forwarded-Proto"))
		if scheme == "" {
			if requestIsSecure(c) {
				scheme = "https"
			} else {
				scheme = "http"
			}
		}
		return scheme + "://" + baseDomain
	}

	return publicRequestBaseURL(c)
}

func configuredAppPublicBaseURL() string {
	raw := strings.TrimSpace(os.Getenv("APP_PUBLIC_URL"))
	if raw == "" {
		return ""
	}

	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}

	return strings.TrimRight(parsed.Scheme+"://"+parsed.Host, "/")
}

func sanitizeRedirectTarget(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return defaultWebRedirectTarget
	}
	if parsed, err := url.Parse(value); err == nil && parsed.IsAbs() {
		return defaultWebRedirectTarget
	}
	if !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") {
		return defaultWebRedirectTarget
	}

	parsed, err := url.Parse(value)
	if err != nil {
		return defaultWebRedirectTarget
	}
	if parsed.Path == "/" {
		parsed.Path = defaultWebRedirectTarget
		return parsed.String()
	}
	if parsed.Path == "/web" {
		parsed.Path = defaultWebRedirectTarget
		return parsed.String()
	}
	if strings.HasPrefix(parsed.Path, "/web/") {
		return parsed.String()
	}

	for _, legacyPrefix := range []string{
		"/dashboard",
		"/main",
		"/messenger",
		"/settings",
		"/login",
		"/register",
		"/alemstore",
		"/alemai",
		"/teamstore",
		"/teamai",
		"/team-rag",
		"/team-contact",
		"/notes",
		"/drive",
	} {
		if parsed.Path == legacyPrefix || strings.HasPrefix(parsed.Path, legacyPrefix+"/") {
			parsed.Path = "/web" + parsed.Path
			return parsed.String()
		}
	}
	return value
}

func sanitizeExternalAdminRedirectTarget(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return defaultExternalAdminTarget
	}
	if parsed, err := url.Parse(value); err == nil && parsed.IsAbs() {
		return defaultExternalAdminTarget
	}
	if !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") {
		return defaultExternalAdminTarget
	}

	parsed, err := url.Parse(value)
	if err != nil {
		return defaultExternalAdminTarget
	}
	if parsed.Path == "/external-admin" {
		parsed.Path = defaultExternalAdminTarget
		return parsed.String()
	}
	if parsed.Path == "/" || !strings.HasPrefix(parsed.Path, "/external-admin/") {
		return defaultExternalAdminTarget
	}
	return parsed.String()
}

func frontendAuthCallbackTarget(redirectTarget string) string {
	safeRedirect := sanitizeRedirectTarget(redirectTarget)
	values := url.Values{}
	values.Set("login", "success")
	values.Set("redirect", safeRedirect)
	return "/web/auth/callback?" + values.Encode()
}

func frontendExternalAdminCallbackTarget(redirectTarget string) string {
	safeRedirect := sanitizeExternalAdminRedirectTarget(redirectTarget)
	values := url.Values{}
	values.Set("login", "success")
	values.Set("redirect", safeRedirect)
	return "/external-admin/auth/callback?" + values.Encode()
}

// firstNonEmptyValue returns the first non-empty string from the provided values,
// or the last value if all are empty (acts as a default fallback).
func firstNonEmptyValue(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	if len(values) > 0 {
		return values[len(values)-1]
	}
	return ""
}

func frontendEgovSuccessTarget(redirectTarget string) string {
	return buildFrontendEgovTarget(
		firstNonEmptyValue(strings.TrimSpace(os.Getenv("FRONTEND_EGOV_SUCCESS_URL")), "/web/auth/callback"),
		redirectTarget,
		nil,
	)
}

func frontendEgovErrorTarget(redirectTarget string, appErr *apperrors.AppError, locales ...i18n.Locale) string {
	return buildFrontendEgovTarget(
		firstNonEmptyValue(strings.TrimSpace(os.Getenv("FRONTEND_EGOV_ERROR_URL")), "/web/auth/callback"),
		redirectTarget,
		appErr,
		locales...,
	)
}

func buildFrontendEgovTarget(baseTarget, redirectTarget string, appErr *apperrors.AppError, locales ...i18n.Locale) string {
	safeRedirect := sanitizeRedirectTarget(redirectTarget)
	targetURL, ok := parseFrontendRedirectBaseURL(baseTarget)
	if !ok {
		targetURL, _ = url.Parse("/web/auth/callback")
	}
	values := targetURL.Query()
	values.Del("error")
	values.Del("message")
	values.Set("provider", "egov")
	values.Set("redirect", safeRedirect)
	if appErr == nil {
		values.Set("login", "success")
	} else {
		values.Set("login", "error")
		values.Set("error", string(appErr.Code))
		locale := i18n.Russian
		if len(locales) > 0 {
			locale = locales[0]
		}
		values.Set("message", apperrors.ResponseBodyLocale(appErr, locale).Error.UserMessage)
	}
	targetURL.RawQuery = values.Encode()
	return targetURL.String()
}

func parseFrontendRedirectBaseURL(raw string) (*url.URL, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, false
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, false
	}
	if parsed.IsAbs() {
		if parsed.Scheme == "" || parsed.Host == "" {
			return nil, false
		}
		return parsed, true
	}
	if !strings.HasPrefix(parsed.Path, "/") || strings.HasPrefix(parsed.Path, "//") {
		return nil, false
	}
	return parsed, true
}

func frontendLoginTarget(redirectTarget string) string {
	safeRedirect := sanitizeRedirectTarget(redirectTarget)
	values := url.Values{}
	if safeRedirect != defaultWebRedirectTarget {
		values.Set("redirect", safeRedirect)
	}
	if encoded := values.Encode(); encoded != "" {
		return "/web/login?" + encoded
	}
	return "/web/login"
}

func externalAdminLoginTarget(redirectTarget string) string {
	safeRedirect := sanitizeExternalAdminRedirectTarget(redirectTarget)
	values := url.Values{}
	if safeRedirect != defaultExternalAdminTarget {
		values.Set("redirect", safeRedirect)
	}
	if encoded := values.Encode(); encoded != "" {
		return "/external-admin/login?" + encoded
	}
	return "/external-admin/login"
}

func prefersJSONResponse(c *gin.Context) bool {
	accept := strings.ToLower(strings.TrimSpace(c.GetHeader("Accept")))
	return strings.Contains(accept, "application/json")
}

func signMobileKeycloakState(state mobileKeycloakState) (string, error) {
	payloadBytes, err := json.Marshal(state)
	if err != nil {
		return "", err
	}

	payload := base64.RawURLEncoding.EncodeToString(payloadBytes)
	mac := hmac.New(sha256.New, mobileKeycloakStateSecret())
	_, _ = mac.Write([]byte(payload))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return payload + "." + signature, nil
}

func verifyMobileKeycloakState(stateToken string) (*mobileKeycloakState, error) {
	parts := strings.Split(strings.TrimSpace(stateToken), ".")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid mobile state format")
	}

	payload, signature := parts[0], parts[1]
	mac := hmac.New(sha256.New, mobileKeycloakStateSecret())
	_, _ = mac.Write([]byte(payload))
	expectedSignature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(signature), []byte(expectedSignature)) {
		return nil, fmt.Errorf("invalid mobile state signature")
	}

	payloadBytes, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return nil, fmt.Errorf("decode mobile state payload: %w", err)
	}

	var result mobileKeycloakState
	if err := json.Unmarshal(payloadBytes, &result); err != nil {
		return nil, fmt.Errorf("unmarshal mobile state payload: %w", err)
	}
	if result.Platform != string(auth.LoginClientMobile) {
		return nil, fmt.Errorf("unexpected mobile state platform")
	}
	if time.Since(time.Unix(result.IssuedAt, 0)) > mobileKeycloakStateTTL {
		return nil, fmt.Errorf("mobile state expired")
	}
	return &result, nil
}

func mobileKeycloakStateSecret() []byte {
	secret := strings.TrimSpace(os.Getenv("JWT_SECRET"))
	if secret == "" {
		secret = "mobile-keycloak-state-secret"
	}
	return []byte(secret)
}

func isAllowedMobileRedirectURI(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}

	allowed := strings.Split(strings.TrimSpace(os.Getenv("KEYCLOAK_MOBILE_REDIRECT_URIS")), ",")
	if len(allowed) == 1 && strings.TrimSpace(allowed[0]) == "" {
		allowed = []string{
			"kz.alemSuperapp.app://auth/callback",
			"kz.superapp.app://auth/callback",
		}
	}

	for _, candidate := range allowed {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if value == candidate || strings.HasPrefix(value, candidate) {
			return true
		}
	}

	return false
}

// CreateUser godoc
// @Summary Create a new user (administrator)
// @Description Create a new user account with role and organization scope. Employees in an organization must have a team_id belonging to that organization.
// @Tags users
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body auth.CreateUserRequest true "Creation request"
// @Success 201 {object} domain.UserResponse
// @Router /users [post]
func (h *AuthHandler) CreateUser(c *gin.Context) {
	requesterID := middleware.MustGetUserID(c)
	if err := h.authService.RequireUserCreationAdministration(c.Request.Context(), requesterID); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	var req auth.CreateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation(err.Error()))
		return
	}

	req.Email = strings.ToLower(req.Email)
	if err := h.authService.ValidateScopedUserCreate(c.Request.Context(), requesterID, &req); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	user, err := h.authService.CreateUser(c.Request.Context(), requesterID, &req)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusCreated, user)
}

func (h *AuthHandler) UpdateUserAdmin(c *gin.Context) {
	requesterID := middleware.MustGetUserID(c)
	if err := h.authService.RequirePlatformUserAdministration(c.Request.Context(), requesterID); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	id, err := strconv.ParseInt(c.Param("user_id"), 10, 64)
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid user id"))
		return
	}

	var req auth.UpdateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation(err.Error()))
		return
	}

	if req.Email != nil {
		lowerEmail := strings.ToLower(*req.Email)
		req.Email = &lowerEmail
	}

	user, err := h.authService.UpdateUserAdmin(c.Request.Context(), requesterID, id, &req)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, user)
}

func (h *AuthHandler) DeleteUser(c *gin.Context) {
	requesterID := middleware.MustGetUserID(c)
	id, err := strconv.ParseInt(c.Param("user_id"), 10, 64)
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid user id"))
		return
	}

	if err := h.authService.DeleteUser(c.Request.Context(), requesterID, id); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "user deleted successfully")})
}

type AssignMinistryAdminRequest struct {
	UserID int64 `json:"user_id" binding:"required"`
}

func (h *AuthHandler) AssignMinistryAdmin(c *gin.Context) {
	requesterID := middleware.MustGetUserID(c)
	ministryID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid ministry id"))
		return
	}

	var req AssignMinistryAdminRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("user_id is required"))
		return
	}

	if err := h.authService.AssignMinistryAdmin(c.Request.Context(), requesterID, ministryID, req.UserID); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "ministry admin assigned successfully")})
}

func (h *AuthHandler) RevokeMinistryAdmin(c *gin.Context) {
	requesterID := middleware.MustGetUserID(c)
	ministryID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid ministry id"))
		return
	}

	if err := h.authService.RevokeMinistryAdmin(c.Request.Context(), requesterID, ministryID); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "ministry admin revoked successfully")})
}

func (h *AuthHandler) ListMinistryAdmins(c *gin.Context) {
	ministries, err := h.authService.ListMinistryAdmins(c.Request.Context())
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, ministries)
}

// GetUserFullDetails godoc
// @Summary Get full user details
// @Description Get comprehensive user info including hierarchy names
// @Tags users
// @Security BearerAuth
// @Param user_id path int true "User ID"
// @Produce json
// @Success 200 {object} domain.UserDetailResponse
// @Router /users/{user_id}/details [get]
func (h *AuthHandler) GetUserFullDetails(c *gin.Context) {
	requesterID := middleware.MustGetUserID(c)

	targetIDStr := c.Param("user_id")
	targetID, err := strconv.ParseInt(targetIDStr, 10, 64)
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid user id"))
		return
	}

	u, err := h.authService.GetUserFullDetails(c.Request.Context(), targetID, requesterID)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, u)
}

type ChangeStatusRequest struct {
	Status string `json:"status" binding:"required"`
}

// ChangeStatus godoc
// @Summary Change user availability status
// @Description Set status to online, offline, sick, or vacation
// @Tags users
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body ChangeStatusRequest true "Status change request"
// @Success 200 {object} map[string]string
// @Router /users/status [put]
// @Router /users/me/status [put]
func (h *AuthHandler) ChangeStatus(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	var req ChangeStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	if err := h.authService.ChangeAvailabilityStatus(c.Request.Context(), userID, req.Status); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "status updated"), "status": req.Status})
}

// GetOnlineUsers godoc
// @Summary Get online users
// @Description Returns users that have active Keycloak sessions for the specified client (deduplicated)
// @Tags users
// @Security BearerAuth
// @Produce json
// @Success 200 {array} domain.UserResponse
// @Failure 401 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /users/online [get]
func (h *AuthHandler) GetOnlineUsers(c *gin.Context) {
	const keycloakClientID = "alem-workspace"

	users, err := h.authService.GetOnlineUsers(c.Request.Context(), keycloakClientID)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, users)
}

// GetNeverLoggedInUsers godoc
// @Summary Get users who never logged in
// @Description Returns a list of users with last_login_at is null
// @Tags users
// @Security BearerAuth
// @Produce json
// @Success 200 {array} domain.UserResponse
// @Failure 500 {object} map[string]interface{}
// @Router /auth/users/never-logged-in [get]
func (h *AuthHandler) GetNeverLoggedInUsers(c *gin.Context) {
	users, err := h.authService.GetNeverLoggedInUsers(c.Request.Context())
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, users)
}

// SyncUsersWithKeycloak godoc
// @Summary Sync users with Keycloak
// @Description Creates missing users in Keycloak with default password and roles
// @Tags auth
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /auth/sync-keycloak [post]
func (h *AuthHandler) SyncUsersWithKeycloak(c *gin.Context) {
	if err := h.authService.SyncUsersWithKeycloak(c.Request.Context()); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "users synchronized successfully")})
}

func (h *AuthHandler) markSecurityLogin(c *gin.Context, tokens *auth.TokenPair) {
	if domain.SecurityMetadata(c.Request.Context()) == nil {
		return
	}
	if middleware.SecurityAction(c.Request.Method, c.FullPath()) == "auth.login" {
		lookupCtx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
		defer cancel()
		lookup := c.Copy()
		lookup.Request = c.Request.WithContext(lookupCtx)
		if id, ok := h.issuedTokenUserID(lookup, tokens); ok {
			middleware.SetSecurityActor(c, id)
		}
	}
}
