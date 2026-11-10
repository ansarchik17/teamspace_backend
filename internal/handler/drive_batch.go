package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/i18n"
	"net/http"
)

func (h *DriveHandler) BatchItems(c *gin.Context) {
	var req domain.DriveBatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.BadRequest("invalid batch request"))
		return
	}
	result, err := h.drive.BatchItems(c.Request.Context(), h.getUserID(c), req)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	status := http.StatusOK
	if result.Failed > 0 {
		status = http.StatusMultiStatus
	}
	for index := range result.Results {
		item := &result.Results[index]
		if item.Status >= 400 {
			code := apperrors.SafeCode(item.ErrorCode, item.Status)
			item.ErrorCode = string(code)
			item.Error = apperrors.LocalizedMessage(code, item.Status, i18n.FromContext(c.Request.Context()))
		}
	}
	respondJSON(c, status, result)
}
