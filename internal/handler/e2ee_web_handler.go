package handler

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/auth"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

// CreateWebPairingSession godoc
// @Summary Create web pairing session
// @Description Creates a QR pairing session for a new web linked device
// @Tags e2ee
// @Accept json
// @Produce json
// @Param request body service.CreateWebPairingSessionRequest true "Create web pairing session request"
// @Success 201 {object} service.CreateWebPairingSessionResponse
// @Router /web/pairing/sessions [post]
func (h *E2EEHandler) CreateWebPairingSession(c *gin.Context) {
	var req service.CreateWebPairingSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("invalid create web pairing session request", zap.Error(err))
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	resp, err := h.service.CreateWebPairingSession(c.Request.Context(), &req, extractClientMetadata(c))
	if err != nil {
		h.logger.Error("failed to create web pairing session",
			zap.String("web_device_id", strings.TrimSpace(req.WebDeviceID)),
			zap.Error(err),
		)
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	h.logger.Info("web pairing session created",
		zap.String("pairing_id", resp.PairingID.String()),
		zap.String("web_device_id", resp.WebDeviceID),
		zap.Time("expires_at", resp.ExpiresAt),
	)
	respondJSON(c, http.StatusCreated, resp)
}

// GetWebPairingSessionStatus godoc
// @Summary Get web pairing session status
// @Description Returns current status of a web pairing session for polling from web client
// @Tags e2ee
// @Produce json
// @Param pairing_id path string true "Pairing ID"
// @Param session_token query string true "Web session token"
// @Success 200 {object} service.WebPairingSessionStatusResponse
// @Router /web/pairing/sessions/{pairing_id} [get]
func (h *E2EEHandler) GetWebPairingSessionStatus(c *gin.Context) {
	pairingID, err := uuid.Parse(c.Param("pairing_id"))
	if err != nil {
		h.logger.Warn("invalid web pairing status request", zap.String("pairing_id", c.Param("pairing_id")))
		h.respondError(c, apperrors.BadRequest("invalid pairing id"))
		return
	}

	sessionToken := strings.TrimSpace(c.Query("session_token"))
	if sessionToken == "" {
		sessionToken = strings.TrimSpace(c.GetHeader("X-Session-Token"))
	}
	if sessionToken == "" {
		h.logger.Warn("missing session token for web pairing status", zap.String("pairing_id", pairingID.String()))
		h.respondError(c, apperrors.Validation("session_token is required"))
		return
	}

	resp, err := h.service.GetWebPairingSessionStatus(c.Request.Context(), pairingID, sessionToken)
	if err != nil {
		h.logger.Warn("failed to get web pairing status",
			zap.String("pairing_id", pairingID.String()),
			zap.Error(err),
		)
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	if resp.Status != domain.WebPairingStatusPending {
		h.logger.Info("web pairing status changed",
			zap.String("pairing_id", resp.PairingID.String()),
			zap.String("web_device_id", resp.WebDeviceID),
			zap.String("status", resp.Status),
			zap.Bool("approved", resp.Approved),
			zap.Bool("synced", resp.Synced),
			zap.Bool("redeemed", resp.Redeemed),
		)
	}
	respondJSON(c, http.StatusOK, resp)
}

// PreviewWebPairingSession godoc
// @Summary Preview web pairing request
// @Description Returns browser and client metadata for mobile approval UI before trusting the web device
// @Tags e2ee
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param pairing_id path string true "Pairing ID"
// @Param request body service.RejectWebPairingRequest true "Preview request with QR token"
// @Success 200 {object} service.WebPairingPreviewResponse
// @Router /e2ee/web/pairing/sessions/{pairing_id}/preview [post]
func (h *E2EEHandler) PreviewWebPairingSession(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	pairingID, err := uuid.Parse(c.Param("pairing_id"))
	if err != nil {
		h.logger.Warn("invalid preview web pairing request", zap.String("pairing_id", c.Param("pairing_id")))
		h.respondError(c, apperrors.BadRequest("invalid pairing id"))
		return
	}

	var req service.RejectWebPairingRequest
	if err = c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("invalid preview web pairing body",
			zap.Int64("user_id", userID),
			zap.String("pairing_id", pairingID.String()),
			zap.Error(err),
		)
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	resp, err := h.service.PreviewWebPairingSession(c.Request.Context(), pairingID, req.QRToken)
	if err != nil {
		h.logger.Warn("failed to preview web pairing session",
			zap.Int64("user_id", userID),
			zap.String("pairing_id", pairingID.String()),
			zap.Error(err),
		)
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	h.logger.Info("web pairing session preview loaded",
		zap.Int64("user_id", userID),
		zap.String("pairing_id", resp.PairingID.String()),
		zap.String("web_device_id", resp.WebDeviceID),
		zap.String("status", resp.Status),
	)
	respondJSON(c, http.StatusOK, resp)
}

// ApproveWebPairingSession godoc
// @Summary Approve web pairing session
// @Description Approves a web linked device from a trusted mobile session
// @Tags e2ee
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param pairing_id path string true "Pairing ID"
// @Param request body service.ApproveWebPairingRequest true "Approve web pairing request"
// @Success 200 {object} service.WebPairingSessionStatusResponse
// @Router /e2ee/web/pairing/sessions/{pairing_id}/approve [post]
func (h *E2EEHandler) ApproveWebPairingSession(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	pairingID, err := uuid.Parse(c.Param("pairing_id"))
	if err != nil {
		h.logger.Warn("invalid approve web pairing request",
			zap.Int64("user_id", userID),
			zap.String("pairing_id", c.Param("pairing_id")),
		)
		h.respondError(c, apperrors.BadRequest("invalid pairing id"))
		return
	}

	var req service.ApproveWebPairingRequest
	if err = c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("invalid approve web pairing body",
			zap.Int64("user_id", userID),
			zap.String("pairing_id", pairingID.String()),
			zap.Error(err),
		)
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	resp, err := h.service.ApproveWebPairingSession(c.Request.Context(), userID, pairingID, &req)
	if err != nil {
		h.logger.Warn("failed to approve web pairing session",
			zap.Int64("user_id", userID),
			zap.String("pairing_id", pairingID.String()),
			zap.String("web_device_id", strings.TrimSpace(req.WebDeviceID)),
			zap.Error(err),
		)
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	h.logger.Info("web pairing session approved",
		zap.Int64("user_id", userID),
		zap.String("pairing_id", resp.PairingID.String()),
		zap.String("web_device_id", resp.WebDeviceID),
		zap.String("status", resp.Status),
	)
	respondJSON(c, http.StatusOK, resp)
}

// RejectWebPairingSession godoc
// @Summary Reject web pairing session
// @Description Rejects a pending web linked device from mobile
// @Tags e2ee
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param pairing_id path string true "Pairing ID"
// @Param request body service.RejectWebPairingRequest true "Reject web pairing request"
// @Success 200 {object} service.WebPairingSessionStatusResponse
// @Router /e2ee/web/pairing/sessions/{pairing_id}/reject [post]
func (h *E2EEHandler) RejectWebPairingSession(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	pairingID, err := uuid.Parse(c.Param("pairing_id"))
	if err != nil {
		h.logger.Warn("invalid reject web pairing request",
			zap.Int64("user_id", userID),
			zap.String("pairing_id", c.Param("pairing_id")),
		)
		h.respondError(c, apperrors.BadRequest("invalid pairing id"))
		return
	}

	var req service.RejectWebPairingRequest
	if err = c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("invalid reject web pairing body",
			zap.Int64("user_id", userID),
			zap.String("pairing_id", pairingID.String()),
			zap.Error(err),
		)
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	resp, err := h.service.RejectWebPairingSession(c.Request.Context(), userID, pairingID, &req)
	if err != nil {
		h.logger.Warn("failed to reject web pairing session",
			zap.Int64("user_id", userID),
			zap.String("pairing_id", pairingID.String()),
			zap.Error(err),
		)
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	h.logger.Info("web pairing session rejected",
		zap.Int64("user_id", userID),
		zap.String("pairing_id", resp.PairingID.String()),
		zap.String("web_device_id", resp.WebDeviceID),
		zap.String("status", resp.Status),
	)
	respondJSON(c, http.StatusOK, resp)
}

// SyncWebPairingHistory godoc
// @Summary Sync wrapped history keys for web device
// @Description Uploads re-wrapped message keys so a linked web device can decrypt old history
// @Tags e2ee
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param pairing_id path string true "Pairing ID"
// @Param request body service.SyncWebPairingHistoryRequest true "History sync request"
// @Success 200 {object} service.SyncWebPairingHistoryResponse
// @Router /e2ee/web/pairing/sessions/{pairing_id}/history [post]
func (h *E2EEHandler) SyncWebPairingHistory(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	pairingID, err := uuid.Parse(c.Param("pairing_id"))
	if err != nil {
		h.logger.Warn("invalid web pairing history sync request",
			zap.Int64("user_id", userID),
			zap.String("pairing_id", c.Param("pairing_id")),
		)
		h.respondError(c, apperrors.BadRequest("invalid pairing id"))
		return
	}

	var req service.SyncWebPairingHistoryRequest
	if err = c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("invalid web pairing history sync body",
			zap.Int64("user_id", userID),
			zap.String("pairing_id", pairingID.String()),
			zap.Error(err),
		)
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	resp, err := h.service.SyncWebPairingHistory(c.Request.Context(), userID, pairingID, &req)
	if err != nil {
		h.logger.Warn("failed to sync web pairing history",
			zap.Int64("user_id", userID),
			zap.String("pairing_id", pairingID.String()),
			zap.String("web_device_id", strings.TrimSpace(req.WebDeviceID)),
			zap.Int("items_count", len(req.Items)),
			zap.Error(err),
		)
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	h.logger.Info("web pairing history synced",
		zap.Int64("user_id", userID),
		zap.String("pairing_id", resp.PairingID.String()),
		zap.String("web_device_id", resp.WebDeviceID),
		zap.Int("synced_count", resp.SyncedCount),
		zap.String("status", resp.Status),
	)
	respondJSON(c, http.StatusOK, resp)
}

// RedeemWebPairingSession godoc
// @Summary Redeem web pairing session
// @Description Exchanges a successful pairing session for backend web auth tokens
// @Tags e2ee
// @Accept json
// @Produce json
// @Param pairing_id path string true "Pairing ID"
// @Param request body service.RedeemWebPairingSessionRequest true "Redeem request"
// @Success 200 {object} map[string]interface{}
// @Router /web/pairing/sessions/{pairing_id}/redeem [post]
func (h *E2EEHandler) RedeemWebPairingSession(c *gin.Context) {
	pairingID, err := uuid.Parse(c.Param("pairing_id"))
	if err != nil {
		h.logger.Warn("invalid redeem web pairing request", zap.String("pairing_id", c.Param("pairing_id")))
		h.respondError(c, apperrors.BadRequest("invalid pairing id"))
		return
	}

	var req service.RedeemWebPairingSessionRequest
	if err = c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("invalid redeem web pairing body",
			zap.String("pairing_id", pairingID.String()),
			zap.Error(err),
		)
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	if h.tokenManager == nil {
		h.logger.Error("web session token manager is not configured",
			zap.String("pairing_id", pairingID.String()),
		)
		h.respondError(c, apperrors.Internal("web session token manager is not configured"))
		return
	}

	result, err := h.service.RedeemWebPairingSession(c.Request.Context(), pairingID, &req, extractClientMetadata(c))
	if err != nil {
		h.logger.Warn("failed to redeem web pairing session",
			zap.String("pairing_id", pairingID.String()),
			zap.Error(err),
		)
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	tokens, err := h.tokenManager.GenerateTokenPair(
		auth.WithAuthenticationTime(c.Request.Context(), result.AuthenticationTime),
		result.UserID,
		result.IIN,
		result.Email,
		result.Username,
		result.FirstName,
		result.LastName,
		false,
		map[string]bool{
			"web_linked_device": true,
		},
		&auth.Context{
			LinkedDeviceID: result.WebDeviceID,
		},
	)
	if err != nil {
		h.logger.Error("failed to generate linked web session tokens",
			zap.String("pairing_id", pairingID.String()),
			zap.String("web_device_id", result.WebDeviceID),
			zap.Int64("user_id", result.UserID),
			zap.Error(err),
		)
		h.respondError(c, apperrors.InternalWithErr("failed to generate linked web session tokens", err))
		return
	}

	tokens.MustChangePassword = result.IsFirstLogin
	tokens.CannotSkipPasswordChange = result.IsFirstLogin

	setAuthCookies(c, tokens)

	h.logger.Info("web pairing session redeemed",
		zap.String("pairing_id", result.PairingID.String()),
		zap.String("web_device_id", result.WebDeviceID),
		zap.Int64("user_id", result.UserID),
		zap.Time("expires_at", tokens.ExpiresAt),
	)
	respondJSON(c, http.StatusOK, gin.H{
		"pairing_id":                  result.PairingID,
		"web_device_id":               result.WebDeviceID,
		"access_token":                tokens.AccessToken,
		"refresh_token":               tokens.RefreshToken,
		"token_type":                  tokens.TokenType,
		"expires_in":                  tokens.ExpiresIn,
		"expires_at":                  tokens.ExpiresAt,
		"must_change_password":        result.IsFirstLogin,
		"cannot_skip_password_change": result.IsFirstLogin,
	})
}

// ListLinkedDevices godoc
// @Summary List linked devices
// @Description Returns linked web/mobile devices visible in mobile security UI
// @Tags e2ee
// @Security BearerAuth
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /e2ee/devices [get]
func (h *E2EEHandler) ListLinkedDevices(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	devices, err := h.service.ListLinkedDevices(c.Request.Context(), userID)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{
		"devices": devices,
		"count":   len(devices),
	})
}

// RevokeLinkedDevice godoc
// @Summary Revoke linked device
// @Description Revokes a linked device and terminates linked local web sessions
// @Tags e2ee
// @Security BearerAuth
// @Produce json
// @Param device_id path string true "Device ID"
// @Success 200 {object} map[string]string
// @Router /e2ee/devices/{device_id} [delete]
func (h *E2EEHandler) RevokeLinkedDevice(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	deviceID := strings.TrimSpace(c.Param("device_id"))
	if deviceID == "" {
		h.respondError(c, apperrors.BadRequest("device id is required"))
		return
	}

	if err := h.service.RevokeLinkedDevice(c.Request.Context(), userID, deviceID); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	if h.tokenManager != nil {
		if err := h.tokenManager.RevokeLinkedDeviceTokens(c.Request.Context(), userID, deviceID); err != nil {
			h.respondError(c, apperrors.InternalWithErr("failed to revoke linked web sessions", err))
			return
		}
	}
	h.terminateLinkedWebRealtimeSessions(userID, []string{deviceID})

	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "linked device revoked")})
}

func extractClientMetadata(c *gin.Context) *domain.ClientMetadata {
	ip := normalizeMetaString(c.ClientIP())
	userAgent := normalizeMetaString(c.Request.UserAgent())
	country := firstMetaHeader(c,
		"CF-IPCountry",
		"X-Appengine-Country",
		"X-Geo-Country",
		"X-Country-Code",
	)
	city := firstMetaHeader(c,
		"X-Appengine-City",
		"X-Geo-City",
		"X-City",
	)

	return &domain.ClientMetadata{
		IP:        ip,
		Country:   country,
		City:      city,
		UserAgent: userAgent,
	}
}

func firstMetaHeader(c *gin.Context, keys ...string) *string {
	for _, key := range keys {
		if value := normalizeMetaString(c.GetHeader(key)); value != nil {
			return value
		}
	}
	return nil
}

func normalizeMetaString(value string) *string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}
