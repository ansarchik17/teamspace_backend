package handler

import (
	"encoding/json"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"io"
	"net/http"
)

func (h *KanbanHandler) CompleteODOAssignment(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil || id == uuid.Nil {
		middleware.RespondAppError(c, apperrors.Validation("valid task id required"))
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 128<<10))
	if err != nil {
		middleware.RespondAppError(c, apperrors.Validation("invalid command payload"))
		return
	}
	fields, err := domain.DecodeODOObject(raw)
	if err != nil || len(fields) != 3 {
		middleware.RespondAppError(c, apperrors.Validation("command id, expected revision and result comment required"))
		return
	}
	for _, key := range []string{"command_id", "expected_revision", "result_comment"} {
		if _, ok := fields[key]; !ok {
			middleware.RespondAppError(c, apperrors.Validation("invalid command fields"))
			return
		}
	}
	var command domain.ODOCompleteRequest
	if err = json.Unmarshal(raw, &command); err != nil || !command.Valid() || c.GetHeader("Idempotency-Key") != "odo-command:"+command.CommandID.String() {
		middleware.RespondAppError(c, apperrors.Validation("invalid command or idempotency key"))
		return
	}
	receipt, err := h.svc.CompleteODOAssignment(c.Request.Context(), middleware.MustGetUserID(c), id, command, middleware.ExtractToken(c))
	if err != nil {
		middleware.RespondAppError(c, apperrors.ToAppError(err))
		return
	}
	c.Header("Cache-Control", "no-store")
	respondJSON(c, http.StatusOK, gin.H{"confirmed": true, "projection_pending": true, "task_id": id, "receipt": receipt})
}
