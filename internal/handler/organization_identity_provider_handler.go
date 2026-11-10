package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

type OrganizationIdentityProviderHandler struct {
	service *service.OrganizationIdentityProviderService
	logger  *zap.Logger
}

func NewOrganizationIdentityProviderHandler(svc *service.OrganizationIdentityProviderService, logger *zap.Logger) *OrganizationIdentityProviderHandler {
	return &OrganizationIdentityProviderHandler{service: svc, logger: logger.With(zap.String("handler", "organization_identity_provider"))}
}

func (h *OrganizationIdentityProviderHandler) List(c *gin.Context) {
	svc, organizationID, ok := h.scope(c)
	if !ok {
		return
	}
	items, err := svc.List(c.Request.Context(), middleware.MustGetUserID(c), organizationID)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	for i := range items {
		items[i] = items[i].Public()
	}
	c.Header("Cache-Control", "no-store")
	respondJSON(c, http.StatusOK, items)
}

func (h *OrganizationIdentityProviderHandler) Create(c *gin.Context) {
	svc, organizationID, ok := h.scope(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	var req domain.CreateOrganizationIdentityProviderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid identity provider request"))
		return
	}
	item, err := svc.Create(c.Request.Context(), middleware.MustGetUserID(c), organizationID, req)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	c.Header("Cache-Control", "no-store")
	respondJSON(c, http.StatusCreated, item.Public())
}

func (h *OrganizationIdentityProviderHandler) Update(c *gin.Context) {
	svc, organizationID, ok := h.scope(c)
	if !ok {
		return
	}
	providerID, err := uuid.Parse(c.Param("provider_id"))
	if err != nil {
		h.respondError(c, apperrors.Validation("invalid identity provider id"))
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	var req domain.UpdateOrganizationIdentityProviderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid identity provider update request"))
		return
	}
	item, err := svc.Update(c.Request.Context(), middleware.MustGetUserID(c), organizationID, providerID, req)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	c.Header("Cache-Control", "no-store")
	respondJSON(c, http.StatusOK, item.Public())
}

func (h *OrganizationIdentityProviderHandler) Delete(c *gin.Context) {
	svc, organizationID, ok := h.scope(c)
	if !ok {
		return
	}
	providerID, err := uuid.Parse(c.Param("provider_id"))
	if err != nil {
		h.respondError(c, apperrors.Validation("invalid identity provider id"))
		return
	}
	if err := svc.Delete(c.Request.Context(), middleware.MustGetUserID(c), organizationID, providerID); err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *OrganizationIdentityProviderHandler) scope(c *gin.Context) (*service.OrganizationIdentityProviderService, uuid.UUID, bool) {
	if c.Param("organization_id") == "" {
		svc, org, err := h.service.ForCurrentCompany(c.Request.Context(), middleware.MustGetUserID(c))
		if err != nil {
			h.respondError(c, organizationAppError(err))
			return nil, uuid.Nil, false
		}
		return svc, org, true
	}
	org, err := uuid.Parse(c.Param("organization_id"))
	if err != nil || org == uuid.Nil {
		h.respondError(c, apperrors.Validation("invalid organization id"))
		return nil, uuid.Nil, false
	}
	return h.service, org, true
}

func (h *OrganizationIdentityProviderHandler) respondError(c *gin.Context, err *apperrors.AppError) {
	respondAppError(c, err)
}
