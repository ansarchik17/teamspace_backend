package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"github.com/west6ide/workspace-backend/internal/repository"
	"net/http"
)

func accessOrganizationID(c *gin.Context) (*uuid.UUID, error) {
	if raw := c.Param("organization_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			return nil, apperrors.Validation("Некорректная компания")
		}
		return &id, nil
	}
	return nil, nil
}
func (h *OrganizationHandler) GetOrganizationAccess(c *gin.Context) {
	org, err := accessOrganizationID(c)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	out, err := h.service.OrganizationAccess(c.Request.Context(), middleware.MustGetUserID(c), org)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	c.Header("Cache-Control", "no-store")
	out.Catalog = localizeAccessCatalog(middleware.RequestLocale(c), out.Catalog)
	respondJSON(c, http.StatusOK, out)
}
func (h *OrganizationHandler) ChangeOrganizationAccess(c *gin.Context) {
	org, err := accessOrganizationID(c)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	var req repository.AccessChange
	if err = c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("Некорректные настройки доступа"))
		return
	}
	err = h.service.ChangeOrganizationAccess(c.Request.Context(), middleware.MustGetUserID(c), org, c.Param("action"), req)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	c.Status(http.StatusNoContent)
}
func (h *OrganizationHandler) GetOwnApplicationAccess(c *gin.Context) {
	out, err := h.service.ResolveOwnAccess(c.Request.Context(), middleware.MustGetUserID(c), c.Query("application"))
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	c.Header("Cache-Control", "no-store")
	respondJSON(c, http.StatusOK, out)
}
func (h *OrganizationHandler) ResolveSDApplicationAccess(c *gin.Context) {
	var req struct {
		CompanyID uuid.UUID `json:"company_id"`
		Subject   string    `json:"subject"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.CompanyID == uuid.Nil {
		h.respondError(c, apperrors.Validation("Укажите компанию и пользователя"))
		return
	}
	out, err := h.service.ResolveSDExternalAccess(c.Request.Context(), req.CompanyID, req.Subject)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	c.Header("Cache-Control", "no-store")
	respondJSON(c, http.StatusOK, out)
}

func (h *OrganizationHandler) GetAccessSDCompanies(c *gin.Context) {
	org, err := accessOrganizationID(c)
	if err != nil || org == nil {
		h.respondError(c, apperrors.Validation("Укажите компанию"))
		return
	}
	out, err := h.service.AccessSDCompanies(c.Request.Context(), middleware.MustGetUserID(c), *org)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	c.Header("Cache-Control", "no-store")
	respondJSON(c, http.StatusOK, out)
}

func (h *OrganizationHandler) ProvisionSDCompany(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	org, err := accessOrganizationID(c)
	if err != nil || org == nil {
		h.respondError(c, apperrors.Validation("Укажите компанию Workspace"))
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	var req struct {
		Address string `json:"address"`
	}
	if err = c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("Укажите адрес компании"))
		return
	}
	out, err := h.service.ProvisionSDCompany(c.Request.Context(), middleware.MustGetUserID(c), *org, req.Address)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, out)
}

func (h *OrganizationHandler) ResolveSDWorkspaceIdentity(c *gin.Context) {
	var req struct {
		Subject string `json:"subject"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	if err := c.ShouldBindJSON(&req); err != nil || req.Subject == "" {
		h.respondError(c, apperrors.Validation("Укажите идентификатор пользователя"))
		return
	}
	out, err := h.service.ResolveSDIdentity(c.Request.Context(), req.Subject)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	c.Header("Cache-Control", "no-store")
	respondJSON(c, http.StatusOK, out)
}
