package handler

import (
	"io"
	"mime"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
)

// ImportDocument godoc
// @Summary Import an office document or PDF into Drive without changing its bytes
// @Tags docs
// @Security BearerAuth
// @Accept multipart/form-data
// @Produce json
// @Param file formData file true "DOC/DOCX/XLS/XLSX/PPT/PPTX/PDF; maximum 100 MiB"
// @Param folder_id formData string false "Target folder UUID"
// @Success 201 {object} domain.DriveFile
// @Failure 400,401,403,413,415,507 {object} map[string]interface{}
// @Router /docs/import [post]
func (h *DriveHandler) ImportDocument(c *gin.Context) {
	if !h.parseDriveUpload(c) {
		return
	}
	defer c.Request.MultipartForm.RemoveAll()
	if len(c.Request.MultipartForm.File["file"]) != 1 {
		h.respondError(c, apperrors.BadRequest("exactly one file is required"))
		return
	}
	var folder *uuid.UUID
	if raw := c.PostForm("folder_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			h.respondError(c, apperrors.BadRequest("invalid folder_id"))
			return
		}
		folder = &id
	}
	header := c.Request.MultipartForm.File["file"][0]
	reader, err := header.Open()
	if err != nil {
		h.respondError(c, apperrors.BadRequest("cannot read uploaded document"))
		return
	}
	defer reader.Close()
	file, err := h.drive.ImportDocument(c.Request.Context(), h.getUserID(c), folder, header.Filename, header.Size, reader)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusCreated, file)
}

// ExportDocument godoc
// @Summary Export a saved document revision in its original format, OOXML or PDF
// @Description Conversion supports DOC to DOCX, XLS to XLSX, PPT to PPTX and office formats to PDF. PDF to editable formats and cross-family conversions are unsupported. Does not include unsaved editor changes or modify the source. Requires viewer access. Returns 202 while converting; repeat with the returned version.
// @Tags docs
// @Security BearerAuth
// @Produce application/octet-stream
// @Param id path string true "Drive file UUID"
// @Param format query string true "doc/docx/xls/xlsx/ppt/pptx/pdf; see conversion matrix"
// @Param version query int false "Saved revision (default current)"
// @Success 200 {file} binary
// @Success 202 {object} map[string]interface{}
// @Header 200 {integer} X-Document-Version "Exported saved revision"
// @Failure 400,401,403,404,413,415,422,502,503,504 {object} map[string]interface{}
// @Router /docs/files/{id}/export [get]
func (h *DriveHandler) ExportDocument(c *gin.Context) {
	setPublicLinkHeaders(c)
	id, _, ok := h.versionParams(c)
	if !ok {
		return
	}
	version := 0
	if raw, exists := c.GetQuery("version"); exists {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			h.respondError(c, apperrors.BadRequest("invalid version"))
			return
		}
		version = parsed
	}
	result, err := h.drive.ExportDocument(c.Request.Context(), h.getUserID(c), id, version, c.Query("format"), h.jwtSecret, h.publicURL)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	if result.Pending {
		c.Header("Retry-After", "2")
		respondJSON(c, http.StatusAccepted, gin.H{"status": "processing", "version": result.Version})
		return
	}
	defer result.Reader.Close()
	c.Header("Content-Type", result.MIME)
	c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": result.Name}))
	c.Header("Content-Length", strconv.FormatInt(result.Size, 10))
	c.Header("X-Document-Version", strconv.Itoa(result.Version))
	c.Status(http.StatusOK)
	_, _ = io.Copy(c.Writer, result.Reader)
}
