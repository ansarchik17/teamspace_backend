package handler

import (
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/service"
)

// GetFilePreview godoc
// @Summary Describe the file viewer and download fallback
// @Tags drive
// @Security BearerAuth
// @Produce json
// @Param id path string true "File UUID"
// @Success 200 {object} domain.DrivePreview
// @Failure 403 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /drive/files/{id}/preview [get]
func (h *DriveHandler) GetFilePreview(c *gin.Context) {
	setPublicLinkHeaders(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid file id"))
		return
	}
	p, err := h.drive.GetFilePreview(c.Request.Context(), h.getUserID(c), id)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, p)
}

// GetPublicFilePreview godoc
// @Summary Describe a file viewer through an active public link
// @Tags public
// @Produce json
// @Param token path string true "Public link UUID"
// @Param resource_type query string false "Target type: file"
// @Param resource_id query string false "Target UUID within shared folder"
// @Success 200 {object} domain.DrivePreview
// @Router /public/drive/files/{token}/preview/info [get]
func (h *DriveHandler) GetPublicFilePreview(c *gin.Context) {
	setPublicLinkHeaders(c)
	token, err := uuid.Parse(c.Param("token"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid token"))
		return
	}
	target, err := parsePublicLinkTarget(c)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	p, err := h.drive.GetPublicFilePreview(c.Request.Context(), token, target)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, p)
}

// PreviewPublicOfficeFile godoc
// @Summary Open an office file through an active public link, read-only
// @Tags public
// @Produce text/html
// @Param token path string true "Public link UUID"
// @Param resource_type query string false "Target type: file"
// @Param resource_id query string false "Target UUID within shared folder"
// @Success 200 {string} string
// @Router /public/drive/files/{token}/preview/office [get]
func (h *DriveHandler) PreviewPublicOfficeFile(c *gin.Context) {
	setPublicLinkHeaders(c)
	token, err := uuid.Parse(c.Param("token"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid token"))
		return
	}
	target, err := parsePublicLinkTarget(c)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	p, err := h.drive.GetPublicFilePreview(c.Request.Context(), token, target)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	if p.Kind != "office" {
		h.respondError(c, apperrors.New(apperrors.ErrCodeInvalidFileType, "office preview unavailable for this format", http.StatusUnsupportedMediaType))
		return
	}
	session, err := h.drive.BuildPublicWOPISession(c.Request.Context(), token, p.FileID, h.jwtSecret)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	h.serveCollaboraLaunch(c, session)
}

// PreviewFileContent godoc
// @Summary Stream native preview with byte ranges (also supports HEAD)
// @Tags drive
// @Security BearerAuth
// @Param id path string true "File UUID"
// @Param Range header string false "Byte range"
// @Success 200 {file} binary
// @Success 206 {file} binary
// @Failure 403 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Failure 415 {object} map[string]interface{}
// @Failure 416 {string} string
// @Router /drive/files/{id}/preview/content [get]
// @Router /drive/files/{id}/preview/content [head]
func (h *DriveHandler) PreviewFileContent(c *gin.Context) {
	setPublicLinkHeaders(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid file id"))
		return
	}
	reader, p, err := h.drive.OpenFilePreview(c.Request.Context(), h.getUserID(c), id)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	defer reader.Close()
	serveDrivePreview(c, reader, p)
}

// PreviewPublicFile godoc
// @Summary Stream native preview through an active public link (also supports HEAD)
// @Tags public
// @Param token path string true "Public link UUID"
// @Param resource_type query string false "Target type within shared folder: file"
// @Param resource_id query string false "Target file UUID within shared folder"
// @Param Range header string false "Byte range"
// @Success 200 {file} binary
// @Success 206 {file} binary
// @Failure 403 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Failure 415 {object} map[string]interface{}
// @Failure 416 {string} string
// @Router /public/drive/files/{token}/preview [get]
// @Router /public/drive/files/{token}/preview [head]
func (h *DriveHandler) PreviewPublicFile(c *gin.Context) {
	setPublicLinkHeaders(c)
	token, err := uuid.Parse(c.Param("token"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid token"))
		return
	}
	target, err := parsePublicLinkTarget(c)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	reader, p, err := h.drive.OpenPublicFilePreview(c.Request.Context(), token, target)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	defer reader.Close()
	serveDrivePreview(c, reader, p)
}

// PreviewOfficeFile godoc
// @Summary Open an office file in Collabora Online in read-only mode
// @Tags drive
// @Security BearerAuth
// @Produce text/html
// @Param id path string true "File UUID"
// @Success 200 {string} string
// @Failure 403 {object} map[string]interface{}
// @Failure 415 {object} map[string]interface{}
// @Router /drive/files/{id}/preview/office [get]
func (h *DriveHandler) PreviewOfficeFile(c *gin.Context) {
	setPublicLinkHeaders(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid file id"))
		return
	}
	p, err := h.drive.GetFilePreview(c.Request.Context(), h.getUserID(c), id)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	if p.Kind != "office" {
		h.respondError(c, apperrors.New(apperrors.ErrCodeInvalidFileType, "office preview unavailable for this format", http.StatusUnsupportedMediaType))
		return
	}
	session, err := h.drive.BuildWOPISession(c.Request.Context(), h.getUserID(c), id, "view", h.jwtSecret)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	// Reuse the existing server integration; query parameters cannot enable editing.
	h.serveCollaboraLaunch(c, session)
}

// ServeContent implements full/suffix/multipart ranges and HEAD using MinIO's
// seekable object, without buffering the whole file or issuing presigned URLs.
func serveDrivePreview(c *gin.Context, reader io.ReadSeeker, p *domain.DrivePreview) {
	c.Header("Content-Type", p.MIMEType)
	c.Header("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": p.Name}))
	setDrivePreviewEmbedding(c, false)
	c.Header("Access-Control-Expose-Headers", "Accept-Ranges, Content-Range, Content-Length, Content-Disposition")
	http.ServeContent(previewResponseWriter{c.Writer}, c.Request, p.Name, time.Time{}, reader)
}

// Go's ServeContent removes cache headers on range errors; keep token-bearing
// responses non-cacheable on every status as well as successful streams.
type previewResponseWriter struct{ http.ResponseWriter }

func (w previewResponseWriter) WriteHeader(code int) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.ResponseWriter.WriteHeader(code)
}

// Override the global DENY/default-src policy only on preview responses. The
// frontend and the WOPI launch page are served from the same origin.
func setDrivePreviewEmbedding(c *gin.Context, office bool) {
	c.Header("X-Frame-Options", "SAMEORIGIN")
	policy := "default-src 'none'; frame-ancestors 'self'"
	if office {
		policy = "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; frame-src 'self' blob:; connect-src 'self' ws: wss:; font-src 'self' data:; frame-ancestors 'self'"
	}
	if office {
		if apiURL, err := service.OnlyOfficeAPIURL(); err == nil {
			if u, err := url.Parse(apiURL); err == nil && u.Host != "" {
				origin := u.Scheme + "://" + u.Host
				policy = strings.Replace(policy, "script-src 'self'", "script-src 'self' "+origin, 1)
				policy = strings.Replace(policy, "frame-src 'self'", "frame-src 'self' "+origin, 1)
			}
		}
	}
	c.Header("Content-Security-Policy", policy)
}
