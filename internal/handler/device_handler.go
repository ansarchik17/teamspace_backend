package handler

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

type DeviceHandler struct {
	notificationService *service.NotificationService
	logger              *zap.Logger
}

func NewDeviceHandler(notificationService *service.NotificationService, logger *zap.Logger) *DeviceHandler {
	return &DeviceHandler{
		notificationService: notificationService,
		logger:              logger.With(zap.String("handler", "device")),
	}
}

// RegisterDeviceRequest represents the request to register a device
type RegisterDeviceRequest struct {
	FCMToken   string `json:"fcm_token"`
	DeviceType string `json:"device_type"`
	DeviceID   string `json:"device_id"`
	VoIPToken  string `json:"voip_token"`
}

// RegisterDevice godoc
// @Summary Register device for push notifications
// @Description Register FCM token
// @Tags devices
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body RegisterDeviceRequest true "Device registration request"
// @Success 200 {object} map[string]string
// @Router /devices [post]
func (h *DeviceHandler) RegisterDevice(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	var req RegisterDeviceRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	deviceID := strings.TrimSpace(req.DeviceID)
	if deviceID == "" {
		deviceID = strings.TrimSpace(c.GetHeader("X-Device-ID"))
	}

	if err := h.notificationService.RegisterDevice(c.Request.Context(), userID, req.FCMToken, req.DeviceType, deviceID, req.VoIPToken); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "device registered successfully")})
}

func (h *DeviceHandler) respondError(c *gin.Context, err *apperrors.AppError) {
	respondAppError(c, err)
}
