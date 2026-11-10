package handler

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/middleware"
)

func (h *KanbanHandler) BackfillODOAssignment(c *gin.Context) {
	raw, exists := c.Get(middleware.ODOAssignmentPrincipalKey)
	p, ok := raw.(middleware.ODOAssignmentPrincipal)
	if !exists || !ok || !p.BackfillEnabled {
		middleware.RespondAppError(c, apperrors.Forbidden("controlled ODO backfill is disabled"))
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 512<<10))
	if err != nil {
		middleware.RespondAppError(c, apperrors.Validation("invalid backfill payload"))
		return
	}
	fields, err := domain.DecodeODOObject(body)
	if err != nil || len(fields) != 5 {
		middleware.RespondAppError(c, apperrors.Validation("reviewed backfill envelope required"))
		return
	}
	for _, name := range []string{"operation_id", "task_id", "expected_task_hash", "reason", "event"} {
		if _, present := fields[name]; !present {
			middleware.RespondAppError(c, apperrors.Validation("incomplete backfill envelope"))
			return
		}
	}
	var request domain.ODOAssignmentBackfill
	if err = json.Unmarshal(body, &request); err != nil {
		middleware.RespondAppError(c, apperrors.Validation("invalid backfill envelope"))
		return
	}
	request.Event, err = domain.DecodeODOAssignmentEvent(fields["event"], p.SourceOrigin)
	if err != nil || c.GetHeader("Idempotency-Key") != "odo-assignment:"+request.Event.EventID.String() {
		middleware.RespondAppError(c, apperrors.New(apperrors.ErrCodeValidation, "invalid backfill snapshot", 422))
		return
	}
	actor, allowed := p.Actors[request.Event.OrganizationID]
	if !allowed || request.Event.IdentityIssuer != p.Issuer {
		middleware.RespondAppError(c, apperrors.Forbidden("backfill company or issuer is not authorized"))
		return
	}
	receipt, err := h.svc.ApplyODOAssignmentBackfill(c.Request.Context(), actor, request, p.SourceOrigin)
	if err != nil {
		middleware.RespondAppError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, receipt)
}
