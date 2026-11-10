package handler

import (
	"encoding/json"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"net/http"
	"strconv"
	"strings"
)

func (h *OrganizationHandler) IFormEmploymentAccess(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	var input struct {
		OrganizationID uuid.UUID `json:"organization_id"`
		RequestID      uuid.UUID `json:"request_id"`
		Subject        string    `json:"subject"`
		Action         string    `json:"action"`
		StartDate      string    `json:"start_date"`
		Revision       int64     `json:"revision"`
	}
	d := json.NewDecoder(c.Request.Body)
	d.DisallowUnknownFields()
	if d.Decode(&input) != nil || input.OrganizationID == uuid.Nil || input.RequestID == uuid.Nil || (input.Action != "get" && input.Action != "update" && input.Action != "cancel") {
		h.respondError(c, apperrors.Validation("Invalid admission request"))
		return
	}
	actor, err := strconv.ParseInt(strings.TrimPrefix(input.Subject, "workspace:"), 10, 64)
	if err != nil || actor <= 0 || serviceSubject(actor) != input.Subject {
		c.Status(400)
		return
	}
	snapshot, err := h.service.IFormContext(c.Request.Context(), actor, input.OrganizationID)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	if !iformHRAllowed(snapshot, domain.IFormOperation{OrganizationID: input.OrganizationID, Subject: input.Subject}) {
		h.respondError(c, apperrors.Forbidden("HR required"))
		return
	}
	out, err := h.service.IFormEmploymentAccess(c.Request.Context(), actor, input.OrganizationID, input.RequestID, input.Action, input.StartDate, input.Revision)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	c.Header("Cache-Control", "no-store, private")
	respondJSON(c, 200, out)
}

func (h *OrganizationHandler) EmploymentTimezone(c *gin.Context) {
	company, err := accessOrganizationID(c)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	var input struct {
		Timezone *string `json:"timezone"`
		Revision int64   `json:"revision"`
	}
	if c.Request.Method == http.MethodPut {
		if c.ShouldBindJSON(&input) != nil || input.Timezone == nil {
			h.respondError(c, apperrors.Validation("Timezone required"))
			return
		}
	}
	out, err := h.service.EmploymentTimezone(c.Request.Context(), middleware.MustGetUserID(c), company, input.Timezone, input.Revision)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	c.Header("Cache-Control", "no-store")
	respondJSON(c, 200, out)
}
