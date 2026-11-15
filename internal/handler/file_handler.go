package handler

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

type FileHandler struct {
	fileService *service.FileService
	logger      *zap.Logger
}

func NewFileHandler(fileService *service.FileService, logger *zap.Logger) *FileHandler {
	return &FileHandler{
		fileService: fileService,
		logger:      logger.With(zap.String("handler", "file")),
	}
}

// UploadFile godoc
// @Summary Upload file
// @Description Upload a file to storage
// @Tags files
// @Security BearerAuth
// @Accept multipart/form-data
// @Produce json
// @Param file formData file true "File to upload"
// @Param type formData string true "File type (image/video/document)"
// @Success 200 {object} map[string]interface{}
// @Router /files/upload [post]
func (h *FileHandler) UploadFile(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	// Get file from form
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		h.respondError(c, apperrors.BadRequest("file  is required"))
		return
	}
	defer file.Close()

	// Get file type
	fileType := c.PostForm("type")
	if fileType == "" {
		fileType = "file"
	}

	// Upload file
	objectName, err := h.fileService.UploadFile(
		c.Request.Context(),
		userID,
		header.Filename,
		fileType,
		header.Size,
		file,
	)

	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	// Generate download URL
	downloadURL, err := h.fileService.GetFileURL(c.Request.Context(), objectName, 24*time.Hour)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	h.logger.Info("File uploaded",
		zap.Int64("user_id", userID),
		zap.String("filename", header.Filename),
		zap.Int64("size", header.Size),
	)

	respondJSON(c, http.StatusOK, gin.H{
		"object_name": objectName,
		"url":         downloadURL,
		"filename":    header.Filename,
		"size":        header.Size,
		"type":        fileType,
	})
}

// GetFileURL godoc
// @Summary Get file URL
// @Description Get presigned URL for file download
// @Tags files
// @Security BearerAuth
// @Param object_name query string true "Object name in storage"
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /files/url [get]
func (h *FileHandler) GetFileURL(c *gin.Context) {
	objectName := c.Query("object_name")
	if objectName == "" {
		h.respondError(c, apperrors.BadRequest("object_name is required"))
		return
	}

	url, err := h.fileService.GetFileURL(c.Request.Context(), objectName, 1*time.Hour)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{
		"url":        url,
		"expires_in": "1h",
	})
}

// DownloadFile godoc
// @Summary Download file
// @Description Download a file from storage via proxy
// @Tags files
// @Param object_name query string true "Object name in storage"
// @Produce application/octet-stream
// @Success 200 {file} file
// @Router /files/download [get]
func (h *FileHandler) DownloadFile(c *gin.Context) {
	objectName := c.Query("object_name")
	if objectName == "" {
		h.respondError(c, apperrors.BadRequest("object_name is required"))
		return
	}

	expiresStr := c.Query("expires")
	signature := c.Query("signature")

	// 1. If signed URL parameters are provided, validate signature & expiry
	if signature != "" && expiresStr != "" {
		var expires int64
		_, _ = fmt.Sscanf(expiresStr, "%d", &expires)
		if h.fileService.ValidateSignedURL(objectName, expires, signature) {
			h.serveFileStream(c, objectName)
			return
		}
		h.respondError(c, apperrors.Unauthorized("download link signature is invalid or expired"))
		return
	}

	// 2. Fallback check for authenticated user
	userID, exists := middleware.GetUserID(c)
	if !exists || userID <= 0 {
		h.respondError(c, apperrors.Unauthorized("unauthorized file access"))
		return
	}

	if err := h.fileService.ValidateUserAccessToObject(c.Request.Context(), userID, objectName); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	h.serveFileStream(c, objectName)
}

func (h *FileHandler) serveFileStream(c *gin.Context, objectName string) {
	object, contentType, _, lastModified, err := h.fileService.OpenObject(c.Request.Context(), objectName)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	defer object.Close()
	if strings.HasPrefix(objectName, "note-audios/") || strings.HasPrefix(objectName, "audio/") {
		contentType = h.fileService.GetContentTypeByFilename(objectName)
		if strings.HasSuffix(strings.ToLower(objectName), ".webm") {
			contentType = "audio/webm"
		}
		if !strings.HasPrefix(contentType, "audio/") {
			contentType = "application/octet-stream"
			c.Header("Content-Disposition", "attachment")
		}
		c.Header("X-Content-Type-Options", "nosniff")
	}

	c.Header("Content-Type", contentType)
	http.ServeContent(c.Writer, c.Request, objectName, lastModified, object)
}

// DeleteFile godoc
// @Summary Delete file
// @Description Delete an owned standalone upload. Managed files must be deleted through their resource API.
// @Tags files
// @Security BearerAuth
// @Param object_name query string true "Object name in storage"
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{}
// @Failure 403 {object} map[string]interface{}
// @Router /files [delete]
func (h *FileHandler) DeleteFile(c *gin.Context) {
	userID, authenticated := middleware.GetUserID(c)
	if !authenticated || userID <= 0 {
		h.respondError(c, apperrors.Unauthorized("unauthorized file access"))
		return
	}

	objectName := c.Query("object_name")
	if objectName == "" {
		h.respondError(c, apperrors.BadRequest("object_name is required"))
		return
	}

	if err := h.fileService.DeleteFileForUser(c.Request.Context(), userID, objectName); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "file deleted successfully")})
}

// UploadPublicFile godoc
// @Summary Upload public file
// @Description Upload a file to storage without authentication
// @Tags files
// @Accept multipart/form-data
// @Produce json
// @Param file formData file true "File to upload"
// @Param type formData string true "File type (image/video/document)"
// @Success 200 {object} map[string]interface{}
// @Router /files/public/upload [post]
func (h *FileHandler) UploadPublicFile(c *gin.Context) {
	// Public upload, use 0 as userID
	userID := int64(0)

	// Get file from form
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		h.respondError(c, apperrors.BadRequest("file is required"))
		return
	}
	defer file.Close()

	// Get file type
	fileType := c.PostForm("type")
	if fileType == "" {
		fileType = "file"
	}

	// Upload file
	objectName, err := h.fileService.UploadFile(
		c.Request.Context(),
		userID,
		header.Filename,
		fileType,
		header.Size,
		file,
	)

	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	// A signed download URL permits reading, never deletion of the object.
	downloadURL, err := h.fileService.GetFileURL(c.Request.Context(), objectName, 24*time.Hour)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	h.logger.Info("Public file uploaded",
		zap.String("filename", header.Filename),
		zap.Int64("size", header.Size),
	)

	respondJSON(c, http.StatusOK, gin.H{
		"object_name": objectName,
		"url":         downloadURL,
		"filename":    header.Filename,
		"size":        header.Size,
		"type":        fileType,
	})
}

// DeletePublicFile godoc
// @Summary Delete an owned standalone upload (compatibility alias)
// @Description Requires authentication and the same ownership checks as DELETE /files. Public visibility does not grant deletion rights.
// @Tags files
// @Security BearerAuth
// @Param object_name query string true "Object name in storage"
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{}
// @Failure 403 {object} map[string]interface{}
// @Router /files/public [delete]
func (h *FileHandler) DeletePublicFile(c *gin.Context) {
	h.DeleteFile(c)
}

func (h *FileHandler) respondError(c *gin.Context, err *apperrors.AppError) {
	respondAppError(c, err)
}
