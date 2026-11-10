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

func (h *DriveHandler) versionParams(c *gin.Context) (uuid.UUID, int, bool) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid file id"))
		return id, 0, false
	}
	version := 0
	if c.Param("version") != "" {
		version, err = strconv.Atoi(c.Param("version"))
		if err != nil || version <= 0 {
			h.respondError(c, apperrors.BadRequest("invalid file version"))
			return id, 0, false
		}
	}
	return id, version, true
}

// ListFileVersions godoc
// @Summary List immutable Drive file revisions
// @Tags drive
// @Security BearerAuth
// @Produce json
// @Param id path string true "File UUID"
// @Param limit query int false "Page size (default 20, maximum 100)"
// @Param offset query int false "Offset"
// @Success 200 {array} domain.DriveFileVersion
// @Failure 400,401,403,404 {object} map[string]interface{}
// @Router /drive/files/{id}/versions [get]
func (h *DriveHandler) ListFileVersions(c *gin.Context) {
	setPublicLinkHeaders(c)
	id, _, ok := h.versionParams(c)
	if !ok {
		return
	}
	limit, offset := parsePagination(c)
	versions, err := h.drive.ListFileVersions(c.Request.Context(), h.getUserID(c), id, limit, offset)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, versions)
}

// DownloadFileVersion godoc
// @Summary Download a saved revision
// @Tags drive
// @Security BearerAuth
// @Produce application/octet-stream
// @Param id path string true "File UUID"
// @Param version path int true "Revision number"
// @Success 200 {file} binary
// @Failure 400,401,403,404 {object} map[string]interface{}
// @Router /drive/files/{id}/versions/{version}/download [get]
func (h *DriveHandler) DownloadFileVersion(c *gin.Context) {
	setPublicLinkHeaders(c)
	id, version, ok := h.versionParams(c)
	if !ok {
		return
	}
	reader, size, name, err := h.drive.DownloadFileVersion(c.Request.Context(), h.getUserID(c), id, version, c.Request.Method == http.MethodHead)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	defer reader.Close()
	c.Header("Content-Type", "application/octet-stream")
	c.Header("Content-Length", strconv.FormatInt(size, 10))
	c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	if c.Request.Method != http.MethodHead {
		_, _ = io.Copy(c.Writer, reader)
	}
}

// RestoreFileVersion godoc
// @Summary Restore a saved revision as a new current revision
// @Description Requires editor access. Retains existing history and rotates the editor session key; stale callbacks cannot overwrite the restored version.
// @Tags drive
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "File UUID"
// @Param version path int true "Revision to restore"
// @Param body body domain.OfficeRecoveryRequest true "Expected current revision"
// @Success 200 {object} map[string]int
// @Failure 400,401,403,404,409 {object} map[string]interface{}
// @Router /drive/files/{id}/versions/{version}/restore [post]
func (h *DriveHandler) RestoreFileVersion(c *gin.Context) {
	id, version, ok := h.versionParams(c)
	if !ok {
		return
	}
	var req struct {
		ExpectedVersion int `json:"expected_version" binding:"required,min=1"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.BadRequest("expected_version is required"))
		return
	}
	next, err := h.drive.RestoreFileVersion(c.Request.Context(), h.getUserID(c), id, version, req.ExpectedVersion)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, gin.H{"version": next})
}

func (h *DriveHandler) UploadFileVersion(c *gin.Context) {
	id, _, ok := h.versionParams(c)
	if !ok {
		return
	}
	if !h.parseDriveUpload(c) {
		return
	}
	defer c.Request.MultipartForm.RemoveAll()
	if len(c.Request.MultipartForm.File["file"]) != 1 {
		h.respondError(c, apperrors.BadRequest("exactly one file is required"))
		return
	}
	expected, err := strconv.Atoi(c.PostForm("expected_version"))
	if err != nil || expected <= 0 {
		h.respondError(c, apperrors.BadRequest("expected_version is required"))
		return
	}
	header, err := c.FormFile("file")
	if err != nil {
		h.respondError(c, apperrors.BadRequest("file is required"))
		return
	}
	reader, err := header.Open()
	if err != nil {
		h.respondError(c, apperrors.BadRequest("cannot open uploaded file"))
		return
	}
	defer reader.Close()
	next, err := h.drive.UploadFileVersion(c.Request.Context(), h.getUserID(c), id, header.Filename, header.Size, reader, expected)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusCreated, gin.H{"version": next})
}
