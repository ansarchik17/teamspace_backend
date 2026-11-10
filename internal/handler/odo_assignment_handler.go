package handler

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

func (h *KanbanHandler) UpsertODOServiceAssignment(c *gin.Context) {
	raw, exists := c.Get(middleware.ODOAssignmentPrincipalKey)
	principal, ok := raw.(middleware.ODOAssignmentPrincipal)
	if !exists || !ok {
		middleware.RespondAppError(c, apperrors.Forbidden("service identity required"))
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 256<<10)
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		middleware.RespondAppError(c, apperrors.Validation("invalid assignment payload"))
		return
	}
	fields, err := domain.DecodeODOObject(body)
	if err != nil {
		middleware.RespondAppError(c, apperrors.Validation("invalid assignment payload"))
		return
	}
	if version, present := fields["schema_version"]; present && string(version) == "null" {
		middleware.RespondAppError(c, apperrors.New(apperrors.ErrCodeValidation, "invalid contract version", 422))
		return
	}
	var req service.DocumentologAssignmentUpsertRequest
	if err := json.Unmarshal(body, &req); err != nil {
		if version, versioned := fields["schema_version"]; versioned && string(version) != "0" {
			middleware.RespondAppError(c, apperrors.New(apperrors.ErrCodeValidation, "invalid versioned assignment payload", 422))
		} else {
			middleware.RespondAppError(c, apperrors.Validation("invalid assignment payload"))
		}
		return
	}
	org, err := uuid.Parse(req.OrganizationID)
	actor, allowed := principal.Actors[org]
	if err != nil || org == uuid.Nil || !allowed || req.IdentityIssuer != principal.Issuer || req.SourceSystem != "documentolog" {
		middleware.RespondAppError(c, apperrors.Forbidden("assignment source or company is not authorized"))
		return
	}
	if req.SchemaVersion != 0 {
		e, decodeErr := domain.DecodeODOAssignmentEvent(body, principal.SourceOrigin)
		if decodeErr != nil || c.GetHeader("Idempotency-Key") != "odo-assignment:"+e.EventID.String() {
			middleware.RespondAppError(c, apperrors.New(apperrors.ErrCodeValidation, "invalid v1 assignment snapshot or idempotency key", 422))
			return
		}
		receipt, applyErr := h.svc.ApplyODOAssignmentEvent(c.Request.Context(), actor, e, principal.SourceOrigin)
		if applyErr != nil {
			middleware.RespondAppError(c, apperrors.ToAppError(applyErr))
			return
		}
		if h.logger != nil {
			h.logger.Info("ODO assignment snapshot received", zap.String("organization_id", org.String()), zap.String("event_id", e.EventID.String()), zap.Int64("revision", e.Revision), zap.String("outcome", receipt.Outcome))
		}
		respondJSON(c, http.StatusOK, receipt)
		return
	}
	task, board, err := h.svc.UpsertODOIntegrationAssignment(c.Request.Context(), actor, org, req)
	if err != nil {
		middleware.RespondAppError(c, apperrors.ToAppError(err))
		return
	}
	if h.logger != nil {
		h.logger.Info("ODO assignment service delivery", zap.String("client_id", principal.ClientID), zap.String("service_sub", principal.Subject), zap.String("organization_id", org.String()), zap.String("assignment_id", req.AssignmentID), zap.Int64("automation_actor", actor))
	}
	respondJSON(c, http.StatusOK, gin.H{"task": task, "board_id": boardID(board), "source_system": "documentolog", "organization_id": org.String(), "assignment_id": req.AssignmentID})
}
