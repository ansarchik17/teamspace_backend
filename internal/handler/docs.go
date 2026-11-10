package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"net/http"
)

// CreateDocument godoc
// @Summary Create a blank document or a copy of a company template
// @Description Supply format (docx/xlsx/pptx) OR template_id. Creates an independent Drive file; open it via the DOC-001 editor-link endpoint.
// @Tags docs
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param body body domain.CreateDocumentRequest true "Document"
// @Success 201 {object} domain.DriveFile
// @Failure 400,401,403,404,413,507 {object} map[string]interface{}
// @Router /docs/files [post]
func (h *DriveHandler) CreateDocument(c *gin.Context) {
	var req domain.CreateDocumentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.BadRequest("invalid body"))
		return
	}
	file, err := h.drive.CreateDocument(c.Request.Context(), h.getUserID(c), req)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusCreated, file)
}

// ListDocumentTemplates godoc
// @Summary List available templates of the current company
// @Tags docs
// @Security BearerAuth
// @Produce json
// @Param limit query int false "Page size (default 50, max 100)"
// @Param offset query int false "Offset"
// @Success 200 {array} domain.DocumentTemplate
// @Failure 401,403 {object} map[string]interface{}
// @Router /docs/templates [get]
func (h *DriveHandler) ListDocumentTemplates(c *gin.Context) {
	limit, offset := parsePagination(c)
	items, err := h.drive.ListDocumentTemplates(c.Request.Context(), h.getUserID(c), limit, offset)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, items)
}

// PublishDocumentTemplate godoc
// @Summary Publish an owned Drive file as a company template
// @Description Requires organization owner/administrator. Explicitly makes the source's current contents available for copying by company members. Only DOCX/XLSX/PPTX; source edits update the template.
// @Tags docs
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param body body domain.PublishDocumentTemplateRequest true "Template"
// @Success 201 {object} domain.DocumentTemplate
// @Failure 400,401,403,404,409 {object} map[string]interface{}
// @Router /docs/templates [post]
func (h *DriveHandler) PublishDocumentTemplate(c *gin.Context) {
	var req domain.PublishDocumentTemplateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.BadRequest("invalid body"))
		return
	}
	item, err := h.drive.PublishDocumentTemplate(c.Request.Context(), h.getUserID(c), req)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusCreated, item)
}

// DeleteDocumentTemplate godoc
// @Summary Unpublish a company template without deleting source or copies
// @Tags docs
// @Security BearerAuth
// @Param id path string true "Template UUID"
// @Success 204
// @Failure 400,401,403,404 {object} map[string]interface{}
// @Router /docs/templates/{id} [delete]
func (h *DriveHandler) DeleteDocumentTemplate(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid template id"))
		return
	}
	if err = h.drive.DeleteDocumentTemplate(c.Request.Context(), h.getUserID(c), id); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	c.Status(http.StatusNoContent)
}
