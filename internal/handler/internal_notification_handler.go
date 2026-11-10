package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

type InternalNotificationHandler struct {
	service *service.InternalNotificationService
	logger  *zap.Logger
}

func NewInternalNotificationHandler(service *service.InternalNotificationService, logger *zap.Logger) *InternalNotificationHandler {
	return &InternalNotificationHandler{
		service: service,
		logger:  logger.With(zap.String("handler", "internal_notification")),
	}
}

func (h *InternalNotificationHandler) AcceptNotification(c *gin.Context) {
	var req domain.InternalNotificationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondAppError(c, apperrors.Validation("invalid request body"))
		return
	}

	response, appErr := h.service.Process(c.Request.Context(), req)
	if appErr != nil {
		respondAppError(c, appErr)
		return
	}

	respondJSON(c, http.StatusOK, response)
}
