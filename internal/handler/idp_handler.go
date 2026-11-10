package handler

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

type IDPOAuthService interface {
	GetAuthorizationURL(state string, scopes []string, lang string) (string, error)
	ExchangeAuthorizationCode(ctx context.Context, code, redirectURI string) (*domain.IDPTokenResponse, error)
	RefreshAccessToken(ctx context.Context, refreshToken string) (*domain.IDPTokenResponse, error)
	GetUserBasic(ctx context.Context, accessToken string) (*domain.IDPUserBasicResponse, error)
	GetUserPhone(ctx context.Context, accessToken string) (*domain.IDPUserPhoneResponse, error)
	GetUserEmail(ctx context.Context, accessToken string) (*domain.IDPUserEmailResponse, error)
}

type IDPHandler struct {
	service *service.IDPOAuthService
	logger  *zap.Logger
}

func NewIDPHandler(service *service.IDPOAuthService, logger *zap.Logger) *IDPHandler {
	return &IDPHandler{
		service: service,
		logger:  logger.With(zap.String("handler", "idp_oauth")),
	}
}

// GetAuthURL godoc
// @Summary Get IDP OAuth authorization URL
// @Description Builds the redirect URL for authorization at idp.egov.kz using configured client credentials, redirect URI and scopes
// @Tags idp-oauth
// @Accept json
// @Produce json
// @Param state query string false "CSRF state value; generated automatically when omitted"
// @Param scope query []string false "Requested scopes; may be repeated or passed space-separated"
// @Param lang query string false "Interface language, for example kk or ru"
// @Success 200 {object} domain.IDPAuthURLResponse
// @Failure 400 {object} apperrors.AppError
// @Failure 500 {object} apperrors.AppError
// @Router /api/v1/idp/auth-url [get]
func (h *IDPHandler) GetAuthURL(c *gin.Context) {
	scopes := c.QueryArray("scope")
	if len(scopes) == 0 {
		if raw := strings.TrimSpace(c.Query("scopes")); raw != "" {
			scopes = append(scopes, raw)
		}
	}
	if len(scopes) == 0 {
		if raw := strings.TrimSpace(c.Query("scope")); raw != "" {
			scopes = append(scopes, raw)
		}
	}

	authURL, err := h.service.GetAuthorizationURL(
		c.Query("state"),
		scopes,
		c.Query("lang"),
	)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, domain.IDPAuthURLResponse{AuthURL: authURL})
}

// ExchangeToken godoc
// @Summary Exchange IDP authorization code for tokens
// @Description Exchanges a one-time authorization code issued by idp.egov.kz for access and refresh tokens
// @Tags idp-oauth
// @Accept json
// @Produce json
// @Param request body domain.IDPExchangeTokenRequest true "Authorization code exchange payload"
// @Success 200 {object} domain.IDPTokenResponse
// @Failure 400 {object} apperrors.AppError
// @Failure 401 {object} apperrors.AppError
// @Failure 500 {object} apperrors.AppError
// @Router /api/v1/idp/token [post]
func (h *IDPHandler) ExchangeToken(c *gin.Context) {
	var req domain.IDPExchangeTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	token, err := h.service.ExchangeAuthorizationCode(c.Request.Context(), req.Code, req.RedirectURI)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, token)
}

// RefreshToken godoc
// @Summary Refresh IDP access token
// @Description Requests a new access token from idp.egov.kz using a refresh token
// @Tags idp-oauth
// @Accept json
// @Produce json
// @Param request body domain.IDPRefreshTokenRequest true "Refresh token payload"
// @Success 200 {object} domain.IDPTokenResponse
// @Failure 400 {object} apperrors.AppError
// @Failure 401 {object} apperrors.AppError
// @Failure 500 {object} apperrors.AppError
// @Router /api/v1/idp/token/refresh [post]
func (h *IDPHandler) RefreshToken(c *gin.Context) {
	var req domain.IDPRefreshTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	token, err := h.service.RefreshAccessToken(c.Request.Context(), req.RefreshToken)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, token)
}

// GetUserBasic godoc
// @Summary Get IDP basic user profile
// @Description Calls the idp.egov.kz protected resource for the user's main profile fields using Bearer access token
// @Tags idp-oauth
// @Accept json
// @Produce json
// @Param Authorization header string true "Bearer IDP access token"
// @Success 200 {object} domain.IDPUserBasicResponse
// @Failure 400 {object} apperrors.AppError
// @Failure 401 {object} apperrors.AppError
// @Failure 403 {object} apperrors.AppError
// @Failure 500 {object} apperrors.AppError
// @Router /api/v1/idp/user/basic [get]
func (h *IDPHandler) GetUserBasic(c *gin.Context) {
	accessToken := bearerTokenFromRequest(c)
	if accessToken == "" {
		h.respondError(c, apperrors.Unauthorized("missing bearer token"))
		return
	}

	result, err := h.service.GetUserBasic(c.Request.Context(), accessToken)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, result)
}

// GetUserPhone godoc
// @Summary Get IDP user phone
// @Description Calls the idp.egov.kz protected resource for the user's mobile phone using Bearer access token
// @Tags idp-oauth
// @Accept json
// @Produce json
// @Param Authorization header string true "Bearer IDP access token"
// @Success 200 {object} domain.IDPUserPhoneResponse
// @Failure 400 {object} apperrors.AppError
// @Failure 401 {object} apperrors.AppError
// @Failure 403 {object} apperrors.AppError
// @Failure 500 {object} apperrors.AppError
// @Router /api/v1/idp/user/phone [get]
func (h *IDPHandler) GetUserPhone(c *gin.Context) {
	accessToken := bearerTokenFromRequest(c)
	if accessToken == "" {
		h.respondError(c, apperrors.Unauthorized("missing bearer token"))
		return
	}

	result, err := h.service.GetUserPhone(c.Request.Context(), accessToken)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, result)
}

// GetUserEmail godoc
// @Summary Get IDP user email
// @Description Calls the idp.egov.kz protected resource for the user's email using Bearer access token
// @Tags idp-oauth
// @Accept json
// @Produce json
// @Param Authorization header string true "Bearer IDP access token"
// @Success 200 {object} domain.IDPUserEmailResponse
// @Failure 400 {object} apperrors.AppError
// @Failure 401 {object} apperrors.AppError
// @Failure 403 {object} apperrors.AppError
// @Failure 500 {object} apperrors.AppError
// @Router /api/v1/idp/user/email [get]
func (h *IDPHandler) GetUserEmail(c *gin.Context) {
	accessToken := bearerTokenFromRequest(c)
	if accessToken == "" {
		h.respondError(c, apperrors.Unauthorized("missing bearer token"))
		return
	}

	result, err := h.service.GetUserEmail(c.Request.Context(), accessToken)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, result)
}

func (h *IDPHandler) respondError(c *gin.Context, err *apperrors.AppError) {
	respondJSON(c, err.StatusCode, err)
}

func bearerTokenFromRequest(c *gin.Context) string {
	header := strings.TrimSpace(c.GetHeader("Authorization"))
	if strings.HasPrefix(strings.ToLower(header), "bearer ") {
		return strings.TrimSpace(header[7:])
	}
	return ""
}
