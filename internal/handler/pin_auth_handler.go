package handler

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/auth"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"go.uber.org/zap"
)

func azpFromJWT(tokenStr string) *string {
	parts := strings.SplitN(tokenStr, ".", 3)
	if len(parts) != 3 {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var claims struct {
		AZP string `json:"azp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil
	}
	azp := strings.TrimSpace(claims.AZP)
	if azp == "" {
		return nil
	}
	return &azp
}

func extractClientIDFromAuthHeader(c *gin.Context) *string {
	authHeader := c.GetHeader("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return nil
	}
	return azpFromJWT(strings.TrimPrefix(authHeader, "Bearer "))
}

type PinAuthHandler struct {
	svc    *auth.QuickPinService
	logger *zap.Logger
}

func NewPinAuthHandler(svc *auth.QuickPinService, logger *zap.Logger) *PinAuthHandler {
	return &PinAuthHandler{svc: svc, logger: logger.With(zap.String("handler", "pin_auth"))}
}

func (h *PinAuthHandler) Setup(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	var req auth.PinSetupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondAppError(c, apperrors.Validation("invalid request body"))
		return
	}

	defer func() {
		if r := recover(); r != nil {
			if err, ok := r.(error); ok {
				respondAppError(c, apperrors.ToAppError(err))
				return
			}
			respondAppError(c, apperrors.Internal("internal error"))
		}
	}()

	clientID := extractClientIDFromAuthHeader(c)
	if err := h.svc.SetupPin(c.Request.Context(), userID, &req, clientID); err != nil {
		respondAppError(c, apperrors.ToAppError(err))
		return
	}

	c.Status(http.StatusOK)
}

func (h *PinAuthHandler) SyncRefreshToken(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	var req struct {
		DeviceID     string `json:"device_id" binding:"required"`
		RefreshToken string `json:"refresh_token" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respondAppError(c, apperrors.Validation("invalid request body"))
		return
	}

	defer func() {
		if r := recover(); r != nil {
			if err, ok := r.(error); ok {
				respondAppError(c, apperrors.ToAppError(err))
				return
			}
			respondAppError(c, apperrors.Internal("internal error"))
		}
	}()

	clientID := extractClientIDFromAuthHeader(c)
	if err := h.svc.SyncRefreshToken(c.Request.Context(), userID, req.DeviceID, req.RefreshToken, clientID); err != nil {
		respondAppError(c, apperrors.ToAppError(err))
		return
	}

	c.Status(http.StatusOK)
}

func (h *PinAuthHandler) LoginByPin(c *gin.Context) {
	var req auth.PinLoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondAppError(c, apperrors.Validation("invalid request body"))
		return
	}

	defer func() {
		if r := recover(); r != nil {
			if err, ok := r.(error); ok {
				respondAppError(c, apperrors.ToAppError(err))
				return
			}
			respondAppError(c, apperrors.Internal("internal error"))
		}
	}()

	ctx := auth.WithLoginRequestMeta(c.Request.Context(), buildLoginRequestMeta(c, detectLoginClient(c), req.DeviceID))
	resp, err := h.svc.LoginByPin(ctx, &req)
	if err != nil {
		respondAppError(c, apperrors.ToAppError(err))
		return
	}

	middleware.SetSecurityActor(c, resp.UserID)
	respondJSON(c, http.StatusOK, resp)
}
