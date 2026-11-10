package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

type UserQRHandler struct {
	service *service.UserQRService
	logger  *zap.Logger
}

func NewUserQRHandler(service *service.UserQRService, logger *zap.Logger) *UserQRHandler {
	return &UserQRHandler{
		service: service,
		logger:  logger.With(zap.String("handler", "user_qr")),
	}
}

func (h *UserQRHandler) CreateOrRefreshMyQR(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	var req service.CreateUserQRCodeRequest
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			h.respondError(c, apperrors.Validation("invalid request body"))
			return
		}
	}

	resp, err := h.service.CreateOrRefresh(c.Request.Context(), userID, &req)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, resp)
}

func (h *UserQRHandler) ScanQR(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	var req service.ScanUserQRCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	resp, err := h.service.Scan(c.Request.Context(), userID, req.Token)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, resp)
}

func (h *UserQRHandler) respondError(c *gin.Context, err *apperrors.AppError) {
	respondAppError(c, err)
}
