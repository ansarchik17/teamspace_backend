package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

type AppleCalendarHandler struct {
	service *service.AppleCalendarService
	logger  *zap.Logger
}

func NewAppleCalendarHandler(service *service.AppleCalendarService, logger *zap.Logger) *AppleCalendarHandler {
	return &AppleCalendarHandler{
		service: service,
		logger:  logger,
	}
}

// ConnectAppleCalendar connects Apple Calendar
// @Summary Connect Apple Calendar
// @Description Connects Apple Calendar using email and app-specific password
// @Tags apple-calendar
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body domain.ConnectAppleCalendarRequest true "Credentials"
// @Success 200 {object} map[string]string
// @Router /api/v1/calendar/apple/connect [post]
func (h *AppleCalendarHandler) ConnectAppleCalendar(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		respondJSON(c, http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req domain.ConnectAppleCalendarRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondJSON(c, http.StatusBadRequest, err)
		return
	}

	uid := userID.(int64)
	err := h.service.ConnectAppleCalendar(c.Request.Context(), uid, req.Email, req.AppSpecificPassword)
	if err != nil {
		h.logger.Error("Failed to connect Apple Calendar", zap.Error(err))

		if appErr, ok := err.(*apperrors.AppError); ok {
			respondJSON(c, appErr.StatusCode, appErr)
			return
		}

		respondJSON(c, http.StatusInternalServerError, gin.H{"error": "failed to connect Apple Calendar"})
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "Apple Calendar connected successfully")})
}

// GetSyncStatus returns the current Apple Calendar sync status
// @Summary Get Apple Calendar sync status
// @Description Returns the current sync status and connection info for Apple Calendar
// @Tags apple-calendar
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} domain.AppleCalendarSyncStatusResponse
// @Router /api/v1/calendar/apple/status [get]
func (h *AppleCalendarHandler) GetSyncStatus(c *gin.Context) {
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

// DisconnectAppleCalendar disconnects Apple Calendar integration
// @Summary Disconnect Apple Calendar
// @Description Disconnects Apple Calendar integration and removes all sync data
// @Tags apple-calendar
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]string
// @Router /api/v1/calendar/apple/disconnect [post]
func (h *AppleCalendarHandler) DisconnectAppleCalendar(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		respondJSON(c, http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	uid := userID.(int64)
	err := h.service.DisconnectAppleCalendar(c.Request.Context(), uid)
	if err != nil {
		h.logger.Error("Failed to disconnect Apple Calendar", zap.Error(err))

		if appErr, ok := err.(*apperrors.AppError); ok {
			respondJSON(c, appErr.StatusCode, appErr)
			return
		}

		respondJSON(c, http.StatusInternalServerError, gin.H{"error": "failed to disconnect"})
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "Apple Calendar disconnected successfully")})
}
