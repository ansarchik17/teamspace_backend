package handler

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

type DeviceObservabilityHandler struct {
	observabilityService *service.DeviceObservabilityService
	logger               *zap.Logger
}

func NewDeviceObservabilityHandler(
	observabilityService *service.DeviceObservabilityService,
	logger *zap.Logger,
) *DeviceObservabilityHandler {
	return &DeviceObservabilityHandler{
		observabilityService: observabilityService,
		logger:               logger.With(zap.String("handler", "device_observability")),
	}
}

// SaveMetadataRequest represents client metadata payload
type SaveMetadataRequest struct {
	DeviceID    string  `json:"device_id" binding:"required"`
	Platform    string  `json:"platform" binding:"required"`
	DeviceType  string  `json:"device_type" binding:"required"`
	OSVersion   *string `json:"os_version"`
	AppVersion  *string `json:"app_version"`
	BuildNumber *string `json:"build_number"`
}

// SaveMetadata godoc
// @Summary Save extended device metadata
// @Description Logs detailed device properties (OS version, app version, build number)
// @Tags client-devices
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body SaveMetadataRequest true "Device metadata"
// @Success 200 {object} map[string]string
// @Router /client/devices/metadata [post]
func (h *DeviceObservabilityHandler) SaveMetadata(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	var req SaveMetadataRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	device := &domain.UserDevice{
		DeviceID:    strings.TrimSpace(req.DeviceID),
		Platform:    strings.TrimSpace(req.Platform),
		DeviceType:  strings.TrimSpace(req.DeviceType),
		OSVersion:   req.OSVersion,
		AppVersion:  req.AppVersion,
		BuildNumber: req.BuildNumber,
	}

	if err := h.observabilityService.SaveMetadata(c.Request.Context(), userID, device); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "device metadata saved successfully")})
}

// ListUsers godoc
// @Summary List users with device observability stats (admin only)
// @Description Retrieve a detailed list of users with associated device telemetry
// @Tags admin-observability
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param q query string false "Search query"
// @Param platform query string false "Filter by device platform"
// @Param device_type query string false "Filter by device type"
// @Param is_online query bool false "Filter by online status"
// @Param app_version query string false "Filter by app version"
// @Param limit query int false "Page limit"
// @Param offset query int false "Page offset"
// @Success 200 {object} map[string]interface{}
// @Router /admin/users [get]
func (h *DeviceObservabilityHandler) ListUsers(c *gin.Context) {
	var params struct {
		Limit      int    `form:"limit" binding:"omitempty,min=1,max=1000"`
		Offset     int    `form:"offset" binding:"omitempty,min=0"`
		Query      string `form:"q"`
		Platform   string `form:"platform"`
		DeviceType string `form:"device_type"`
		AppVersion string `form:"app_version"`
	}

	if err := c.ShouldBindQuery(&params); err != nil {
		h.respondError(c, apperrors.Validation("invalid query parameters"))
		return
	}

	if params.Limit <= 0 {
		params.Limit = 50
	}

	var isOnline *bool
	if val := c.Query("is_online"); val != "" {
		b, err := strconv.ParseBool(val)
		if err == nil {
			isOnline = &b
		}
	}

	items, total, err := h.observabilityService.ListUsers(
		c.Request.Context(),
		params.Query,
		params.Platform,
		params.DeviceType,
		isOnline,
		params.AppVersion,
		params.Limit,
		params.Offset,
	)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{
		"items":  items,
		"total":  total,
		"limit":  params.Limit,
		"offset": params.Offset,
	})
}

func (h *DeviceObservabilityHandler) respondError(c *gin.Context, err *apperrors.AppError) {
	respondAppError(c, err)
}
