package handler

import (
	"encoding/json"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"net/http"
	"strconv"
	"strings"
)

// Internal read-only lookup: same current-company HR check as provisioning.
func (h *OrganizationHandler) IFormEmailSuggestion(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 512<<10)
	var input struct {
		OrganizationID uuid.UUID `json:"organization_id"`
		Subject        string    `json:"subject"`
		FirstName      string    `json:"first_name"`
		LastName       string    `json:"last_name"`
		Unavailable    []string  `json:"unavailable"`
	}
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || input.OrganizationID == uuid.Nil || len(input.FirstName) > 255 || len(input.LastName) > 255 || len(input.Unavailable) > 2000 {
		h.respondError(c, apperrors.Validation("Некорректные параметры корпоративной почты"))
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
		h.respondError(c, apperrors.Forbidden("Корпоративную почту подбирает HR компании"))
		return
	}
	email, mailDomain, err := h.service.IFormEmailSuggestion(c.Request.Context(), actor, input.OrganizationID, input.FirstName, input.LastName, input.Unavailable)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	c.Header("Cache-Control", "no-store, private")
	respondJSON(c, 200, gin.H{"email": email, "domain": mailDomain})
}
