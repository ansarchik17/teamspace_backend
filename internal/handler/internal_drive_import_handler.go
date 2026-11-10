package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

type InternalDriveImportHandler struct {
	service *service.InternalDriveImportService
	logger  *zap.Logger
}

func NewInternalDriveImportHandler(service *service.InternalDriveImportService, logger *zap.Logger) *InternalDriveImportHandler {
	return &InternalDriveImportHandler{
		service: service,
		logger:  logger.With(zap.String("handler", "internal_drive_import")),
	}
}

func (h *InternalDriveImportHandler) Import(c *gin.Context) {
	var req domain.InternalDriveImportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondAppError(c, apperrors.Validation("invalid request body"))
		return
	}

	response, appErr := h.service.Import(c.Request.Context(), req)
	if appErr != nil {
		respondAppError(c, appErr)
		return
	}

	respondJSON(c, http.StatusOK, response)
}
