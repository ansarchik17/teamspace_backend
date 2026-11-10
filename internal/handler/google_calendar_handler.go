package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

type GoogleCalendarHandler struct {
	service *service.GoogleCalendarService
	logger  *zap.Logger
}

func NewGoogleCalendarHandler(service *service.GoogleCalendarService, logger *zap.Logger) *GoogleCalendarHandler {
	return &GoogleCalendarHandler{
		service: service,
		logger:  logger,
	}
}

// GetAuthURL returns the Google OAuth URL for the user to authorize
// @Summary Get Google Calendar OAuth URL
// @Description Returns the URL where the user should go to authorize Google Calendar access
// @Tags google-calendar
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} domain.GoogleAuthURLResponse
// @Router /api/v1/calendar/google/auth-url [get]
func (h *GoogleCalendarHandler) GetAuthURL(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		respondJSON(c, http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	uid := userID.(int64)
	authURL := h.service.GetAuthURL(uid)

	respondJSON(c, http.StatusOK, domain.GoogleAuthURLResponse{
		AuthURL: authURL,
	})
}

// HandleOAuthCallback handles the OAuth callback from Google
// @Summary Handle Google OAuth callback
// @Description Handles the OAuth callback from Google after user authorization
// @Tags google-calendar
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body domain.EnableGoogleCalendarSyncRequest true "OAuth code"
// @Success 200 {object} map[string]string
// @Router /api/v1/calendar/google/callback [post]
func (h *GoogleCalendarHandler) HandleOAuthCallback(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		respondJSON(c, http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req domain.EnableGoogleCalendarSyncRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondJSON(c, http.StatusBadRequest, err)
		return
	}

	uid := userID.(int64)
	err := h.service.HandleOAuthCallback(c.Request.Context(), uid, req.Code)
	if err != nil {
		h.logger.Error("Failed to handle OAuth callback", zap.Error(err))

		if appErr, ok := err.(*apperrors.AppError); ok {
			respondJSON(c, appErr.StatusCode, appErr)
			return
		}

		respondJSON(c, http.StatusInternalServerError, gin.H{"error": "failed to connect Google Calendar"})
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "Google Calendar connected successfully")})
}

// GetSyncStatus returns the current Google Calendar sync status
// @Summary Get Google Calendar sync status
// @Description Returns the current sync status and connection info for Google Calendar
// @Tags google-calendar
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} domain.GoogleCalendarSyncStatusResponse
// @Router /api/v1/calendar/google/status [get]
func (h *GoogleCalendarHandler) GetSyncStatus(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		respondJSON(c, http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	uid := userID.(int64)
	status, err := h.service.GetSyncStatus(c.Request.Context(), uid)
	if err != nil {
		h.logger.Error("Failed to get sync status", zap.Error(err))

		if appErr, ok := err.(*apperrors.AppError); ok {
			respondJSON(c, appErr.StatusCode, appErr)
			return
		}

		respondJSON(c, http.StatusInternalServerError, gin.H{"error": "failed to get sync status"})
		return
	}

	respondJSON(c, http.StatusOK, status)
}

// DisconnectGoogleCalendar disconnects Google Calendar integration
// @Summary Disconnect Google Calendar
// @Description Disconnects Google Calendar integration and removes all sync data
// @Tags google-calendar
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]string
// @Router /api/v1/calendar/google/disconnect [post]
func (h *GoogleCalendarHandler) DisconnectGoogleCalendar(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		respondJSON(c, http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	uid := userID.(int64)
	err := h.service.DisconnectGoogleCalendar(c.Request.Context(), uid)
	if err != nil {
		h.logger.Error("Failed to disconnect Google Calendar", zap.Error(err))

		if appErr, ok := err.(*apperrors.AppError); ok {
			respondJSON(c, appErr.StatusCode, appErr)
			return
		}

		respondJSON(c, http.StatusInternalServerError, gin.H{"error": "failed to disconnect"})
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "Google Calendar disconnected successfully")})
}

// Public callback endpoint (for handling redirects from Google)
// This endpoint doesn't require authentication initially, as it's called by Google
// The state parameter contains the user ID for verification
func (h *GoogleCalendarHandler) PublicOAuthCallback(c *gin.Context) {
	code := c.Query("code")
	state := c.Query("state") // This is the user ID

	if code == "" || state == "" {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "missing code or state"})
		return
	}

	userID, err := strconv.ParseInt(state, 10, 64)
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "invalid state"})
		return
	}

	err = h.service.HandleOAuthCallback(c.Request.Context(), userID, code)
	if err != nil {
		h.logger.Error("Failed to handle OAuth callback", zap.Error(err))

		// Redirect to frontend with error
		c.Redirect(http.StatusTemporaryRedirect, "/calendar?google_sync=error")
		return
	}

	// Redirect to frontend with success
	c.Redirect(http.StatusTemporaryRedirect, "/calendar?google_sync=success")
}
