package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"github.com/west6ide/workspace-backend/internal/service"
)

// ImportContacts imports employee profiles, without creating SSO credentials.
// @Summary Import employees from CSV or XLSX
// @Tags contacts
// @Security BearerAuth
// @Accept multipart/form-data
// @Produce json
// @Param file formData file true "UTF-8 CSV or single-sheet XLSX, up to 10 MiB/5000 employees"
// @Param dry_run query bool false "Validate without applying; default true"
// @Success 200 {object} domain.ContactImportResult
// @Failure 400 {object} apperrors.ErrorResponse
// @Failure 403 {object} apperrors.ErrorResponse
// @Router /contacts/import [post]
func (h *OrganizationHandler) ImportContacts(c *gin.Context) {
	actor := middleware.MustGetUserID(c)
	if _, err := h.service.ContactImportScope(c.Request.Context(), actor); err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	dry, err := strconv.ParseBool(c.DefaultQuery("dry_run", "true"))
	if err != nil {
		h.respondError(c, apperrors.Validation("invalid dry_run"))
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, service.MaxContactImportBytes+(1<<20))
	if err = c.Request.ParseMultipartForm(service.MaxContactImportBytes); err != nil {
		h.respondError(c, apperrors.Validation("invalid or oversized import"))
		return
	}
	if c.Request.MultipartForm != nil {
		defer c.Request.MultipartForm.RemoveAll()
	}
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		h.respondError(c, apperrors.Validation("file is required"))
		return
	}
	defer file.Close()
	result, err := h.service.ImportContacts(c.Request.Context(), actor, header.Filename, file, dry)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, result)
}

// ContactDirectoryConfig returns non-secret connector settings.
// @Summary Get LDAP/AD directory connector
// @Tags contacts
// @Security BearerAuth
// @Produce json
// @Success 200 {object} domain.DirectoryConfig
// @Router /contacts/directory [get]
func (h *OrganizationHandler) ContactDirectoryConfig(c *gin.Context) {
	result, err := h.service.ContactDirectoryConfig(c.Request.Context(), middleware.MustGetUserID(c))
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, result)
}

func decodeDirectoryBody(c *gin.Context, target any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 512<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return apperrors.Validation("invalid directory request")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return apperrors.Validation("invalid directory request")
	}
	return nil
}

// SaveContactDirectoryConfig encrypts the bind password and replaces connector settings.
// @Summary Configure LDAP/AD employee directory
// @Tags contacts
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body domain.DirectoryConfigRequest true "Connector; omitted bind_password preserves the existing secret"
// @Success 200 {object} domain.DirectoryConfig
// @Router /contacts/directory [put]
func (h *OrganizationHandler) SaveContactDirectoryConfig(c *gin.Context) {
	var req domain.DirectoryConfigRequest
	if err := decodeDirectoryBody(c, &req); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	result, err := h.service.SaveContactDirectoryConfig(c.Request.Context(), middleware.MustGetUserID(c), req)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, result)
}

// SyncContactDirectory reads a complete bounded snapshot and applies it atomically.
// @Summary Synchronize employee profiles from LDAP/AD
// @Tags contacts
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body domain.DirectorySyncRequest true "dry_run defaults to true; false applies"
// @Success 200 {object} domain.ContactImportResult
// @Router /contacts/directory/sync [post]
func (h *OrganizationHandler) SyncContactDirectory(c *gin.Context) {
	req := domain.DirectorySyncRequest{DryRun: true}
	if err := decodeDirectoryBody(c, &req); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	result, err := h.service.SyncContactDirectory(c.Request.Context(), middleware.MustGetUserID(c), req.DryRun)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, result)
}
