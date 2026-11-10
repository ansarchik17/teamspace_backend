package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

type DriveHandler struct {
	drive       *service.DriveService
	driveSearch *service.DriveSearchService
	logger      *zap.Logger
	publicURL   string
	jwtSecret   string
}

func NewDriveHandler(drive *service.DriveService, driveSearch *service.DriveSearchService, publicURL, jwtSecret string, logger *zap.Logger) *DriveHandler {
	return &DriveHandler{
		drive:       drive,
		driveSearch: driveSearch,
		logger:      logger.With(zap.String("handler", "drive")),
		publicURL:   strings.TrimRight(publicURL, "/"),
		jwtSecret:   jwtSecret,
	}
}

type createFolderReq struct {
	ParentID *uuid.UUID `json:"parent_id"`
	Name     string     `json:"name" binding:"required,min=1,max=200"`
}

// SearchContent godoc
// @Summary Search file contents in Alem Drive
// @Description Searches Elasticsearch chunks only across Drive files available to the authenticated user and groups the best matches by file.
// @Tags drive
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param body body domain.DriveSearchQueryRequest true "Drive content search payload"
// @Success 200 {object} domain.DriveSearchQueryResponse
// @Failure 400 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /drive/search/query [post]
func (h *DriveHandler) SearchContent(c *gin.Context) {
	userID := h.getUserID(c)
	if userID <= 0 {
		h.respondError(c, apperrors.Unauthorized("unauthorized"))
		return
	}
	if h.driveSearch == nil || !h.driveSearch.Enabled() {
		h.respondError(c, apperrors.Internal("drive search is not configured"))
		return
	}

	var req domain.DriveSearchQueryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.BadRequest("invalid body"))
		return
	}

	resp, err := h.driveSearch.Search(c.Request.Context(), userID, req)
	if err != nil {
		var appErr *apperrors.AppError
		if errors.As(err, &appErr) {
			h.respondError(c, appErr)
		} else {
			h.logger.Error("file content search failed", zap.Error(err))
			h.respondError(c, apperrors.Internal("file search is temporarily unavailable"))
		}
		return
	}

	respondJSON(c, http.StatusOK, resp)
}

// CreateFolder godoc
// @Summary Create folder
// @Description Create a new folder in drive. ParentID is optional (root if null).
// @Tags drive
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param body body createFolderReq true "Create folder payload"
// @Success 201 {object} domain.Folder
// @Failure 400 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /drive/folders [post]
func (h *DriveHandler) CreateFolder(c *gin.Context) {
	userID := h.getUserID(c)

	var req createFolderReq
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.BadRequest("invalid body"))
		return
	}

	f, err := h.drive.CreateFolder(c.Request.Context(), userID, req.ParentID, req.Name)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusCreated, f)
}

type copyItemReq struct {
	ResourceType   domain.ResourceType `json:"resource_type" binding:"required,oneof=file folder"`
	ResourceID     uuid.UUID           `json:"resource_id" binding:"required"`
	TargetParentID *uuid.UUID          `json:"target_parent_id"`
}

type moveItemReq struct {
	ResourceType   domain.ResourceType `json:"resource_type" binding:"required,oneof=file folder"`
	ResourceID     uuid.UUID           `json:"resource_id" binding:"required"`
	TargetParentID *uuid.UUID          `json:"target_parent_id"`
}

// CopyItem godoc
// @Summary Copy file or folder
// @Description Copy file or folder to a target parent folder (or root if null)
// @Tags drive
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param body body copyItemReq true "Copy item payload"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /drive/items/copy [post]
func (h *DriveHandler) CopyItem(c *gin.Context) {
	userID := h.getUserID(c)
	if userID == 0 {
		h.respondError(c, apperrors.Unauthorized("unauthorized"))
		return
	}

	var req copyItemReq
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.BadRequest("invalid body"))
		return
	}

	result, err := h.drive.CopyItem(c.Request.Context(), userID, req.ResourceType, req.ResourceID, req.TargetParentID)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, result)
}

// MoveItem godoc
// @Summary Move file or folder
// @Description Move file or folder to a target parent folder (or root if null)
// @Tags drive
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param body body moveItemReq true "Move item payload"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /drive/items/move [post]
func (h *DriveHandler) MoveItem(c *gin.Context) {
	userID := h.getUserID(c)
	if userID == 0 {
		h.respondError(c, apperrors.Unauthorized("unauthorized"))
		return
	}

	var req moveItemReq
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.BadRequest("invalid body"))
		return
	}

	result, err := h.drive.MoveItem(c.Request.Context(), userID, req.ResourceType, req.ResourceID, req.TargetParentID)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, result)
}

// GetFolder godoc
// @Summary Get folder details
// @Description Get folder details and immediate children (files and subfolders)
// @Tags drive
// @Security BearerAuth
// @Produce json
// @Param id path string true "Folder UUID"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /drive/folders/{id} [get]
func (h *DriveHandler) GetFolder(c *gin.Context) {
	userID := h.getUserID(c)

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid folder id"))
		return
	}

	children, err := h.drive.ListFolderChildren(c.Request.Context(), userID, &id)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	path, err := h.drive.GetFolderPath(c.Request.Context(), userID, id)
	if err != nil {
		h.logger.Warn("failed to get folder path", zap.String("folder_id", id.String()), zap.Error(err))
		path = []service.FolderBreadcrumb{}
	}

	respondJSON(c, http.StatusOK, gin.H{"id": id, "children": children, "path": path})
}

// ListFolderChildren godoc
// @Summary List folder children (Legacy/Alt)
// @Description List folders and files inside a folder (use id=root for root)
// @Tags drive
// @Security BearerAuth
// @Produce json
// @Param id path string true "Folder id or 'root'"
// @Success 200 {array} domain.DriveItem
// @Failure 400 {object} map[string]interface{}
// @Param limit query int false "Limit"
// @Param offset query int false "Offset"
// @Param type query string false "File type filter"
// @Param owner_id query int false "Owner ID"
// @Param sort_by query string false "Sort by: name, date, size"
// @Param sort_order query string false "Sort order: asc, desc"
// @Success 200 {array} domain.DriveItem
// @Failure 400 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /drive/folders/{id}/children [get]
func (h *DriveHandler) ListFolderChildren(c *gin.Context) {
	userID := h.getUserID(c)

	raw := c.Param("id")
	var folderID *uuid.UUID
	if raw != "root" {
		id, err := uuid.Parse(raw)
		if err != nil {
			h.respondError(c, apperrors.BadRequest("invalid folder id"))
			return
		}
		folderID = &id
	}

	limit, offset := parsePagination(c)
	fileType := c.Query("type")
	ownerIDStr := c.Query("owner_id")
	var ownerID *int64
	if ownerIDStr != "" {
		if oid, err := strconv.ParseInt(ownerIDStr, 10, 64); err == nil && oid > 0 {
			ownerID = &oid
		}
	}
	sortBy := c.DefaultQuery("sort_by", "date")
	sortOrder := c.DefaultQuery("sort_order", "desc")

	var children []domain.DriveItem
	var err error

	if folderID == nil {
		// Root -> ListOwnItemsFiltered
		children, err = h.drive.ListOwnItemsFiltered(c.Request.Context(), userID, limit, offset, fileType, ownerID, sortBy, sortOrder)
	} else {
		// Folder -> ListFolderChildrenFiltered
		children, err = h.drive.ListFolderChildrenFiltered(c.Request.Context(), userID, *folderID, limit, offset, fileType, ownerID, sortBy, sortOrder)
	}

	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, children)
}

type updateFolderReq struct {
	Name     *string    `json:"name,omitempty"`
	ParentID *uuid.UUID `json:"parent_id,omitempty"`
}

// UpdateFolder godoc
// @Summary Update folder
// @Description Rename or move a folder
// @Tags drive
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "Folder UUID"
// @Param body body updateFolderReq true "Update folder payload"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /drive/folders/{id} [put]
func (h *DriveHandler) UpdateFolder(c *gin.Context) {
	userID := h.getUserID(c)

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid folder id"))
		return
	}

	var req updateFolderReq
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.BadRequest("invalid body"))
		return
	}

	if err := h.drive.UpdateFolder(c.Request.Context(), userID, id, req.Name, req.ParentID); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "folder updated")})
}

// UploadFile godoc
// @Summary Upload file
// @Description Upload up to 20 files within Drive quota. Request body is limited to 1 GiB plus 1 MiB overhead. On partial failure, error.details.uploaded_files lists files already saved.
// @Tags drive
// @Security BearerAuth
// @Accept multipart/form-data
// @Produce json
// @Param folder_id formData string false "Folder UUID"
// @Param file formData file true "File content. Repeat this field to upload multiple files."
// @Success 201 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 413 {object} map[string]interface{}
// @Failure 507 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /drive/files [post]
func (h *DriveHandler) UploadFile(c *gin.Context) {
	if !h.parseDriveUpload(c) {
		return
	}
	defer c.Request.MultipartForm.RemoveAll()
	userID := h.getUserID(c)

	// folder_id optional
	var folderID *uuid.UUID
	if v := c.PostForm("folder_id"); v != "" {
		parsed, err := uuid.Parse(v)
		if err != nil {
			h.respondError(c, apperrors.BadRequest("invalid folder_id"))
			return
		}
		folderID = &parsed
	}

	form, err := c.MultipartForm()
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid multipart form"))
		return
	}

	headers := form.File["file"]
	if len(headers) == 0 {
		h.respondError(c, apperrors.BadRequest("file is required"))
		return
	}

	if len(headers) > service.DriveMaxFilesPerRequest {
		h.respondError(c, apperrors.BadRequest("too many files in one request").WithDetails(map[string]any{"max_files_per_request": service.DriveMaxFilesPerRequest}))
		return
	}
	var total int64
	for _, header := range headers {
		total += header.Size
	}
	if err := h.drive.CheckDriveCapacity(c.Request.Context(), userID, total); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	uploaded := make([]*domain.DriveFile, 0, len(headers))
	for _, header := range headers {
		file, err := header.Open()
		if err != nil {
			h.respondError(c, apperrors.BadRequest("failed to read uploaded file"))
			return
		}

		mime, reader, err := service.SniffMimeAndRestoreReader(file)
		if err != nil {
			_ = file.Close()
			h.respondError(c, apperrors.BadRequest("failed to read file"))
			return
		}

		df, err := h.drive.UploadFileToFolder(
			c.Request.Context(),
			userID,
			folderID,
			header.Filename,
			header.Size,
			mime,
			reader,
		)
		_ = file.Close()
		if err != nil {
			appErr := apperrors.ToAppError(err)
			if len(uploaded) > 0 {
				appErr = appErr.WithDetails(map[string]any{"uploaded_files": uploaded, "uploaded_count": len(uploaded)})
			}
			h.respondError(c, appErr)
			return
		}
		uploaded = append(uploaded, df)
	}

	if len(uploaded) == 1 {
		respondJSON(c, http.StatusCreated, uploaded[0])
		return
	}

	respondJSON(c, http.StatusCreated, gin.H{
		"count": len(uploaded),
		"files": uploaded,
	})
}

// GetFile godoc
// @Summary Get file metadata
// @Description Get details of a specific file
// @Tags drive
// @Security BearerAuth
// @Produce json
// @Param id path string true "File UUID"
// @Success 200 {object} domain.DriveFile
// @Failure 400 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /drive/files/{id} [get]
func (h *DriveHandler) GetFile(c *gin.Context) {
	userID := h.getUserID(c)

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid file id"))
		return
	}

	f, err := h.drive.GetFileMeta(c.Request.Context(), userID, id)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, f)
}

// DownloadFile godoc
// @Summary Download file
// @Description Stream file content by file id. Requires authentication for Recent history tracking. Pass token via Header OR query param ?access_token=...
// @Tags drive
// @Security BearerAuth
// @Produce application/octet-stream
// @Param id path string true "File UUID"
// @Param access_token query string false "JWT Token (if header not possible)"
// @Success 200 {file} file
// @Failure 400 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /drive/files/{id}/download [get]
func (h *DriveHandler) DownloadFile(c *gin.Context) {
	start := time.Now()
	userID := h.getUserID(c)

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid file id"))
		return
	}

	remoteIP := c.ClientIP()
	userAgent := c.GetHeader("User-Agent")
	// DocumentServer (OnlyOffice) скачивает файл server-to-server.
	// Его User-Agent содержит "nurl" или строку с IP документ-сервера.
	// Это позволяет отличить s2s-скачивание от запроса пользователя.
	h.logger.Info("drive download start",
		zap.String("file_id", id.String()),
		zap.Int64("user_id", userID),
		zap.String("remote_ip", remoteIP),
		zap.String("user_agent", userAgent),
		zap.Bool("has_access_token_query", c.Query("access_token") != ""),
	)

	reader, contentType, size, filename, err := h.drive.DownloadFileByID(c.Request.Context(), userID, id)
	if err != nil {
		h.logger.Warn("drive download failed",
			zap.String("file_id", id.String()),
			zap.Int64("user_id", userID),
			zap.String("remote_ip", remoteIP),
			zap.Duration("elapsed", time.Since(start)),
			zap.Error(err),
		)
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	defer reader.Close()

	c.Header("Content-Type", contentType)
	c.Header("Content-Length", fmt.Sprintf("%d", size))
	c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
	_, _ = io.Copy(c.Writer, reader)

	// Лог после стриминга: позволяет видеть полное время отдачи файла.
	// total_ms включает: DB lookup + MinIO stream + TCP transfer.
	// Если DocumentServer вызывает этот endpoint (s2s), total_ms
	// показывает реальное время server-to-server скачивания.
	h.logger.Info("drive download complete",
		zap.String("file_id", id.String()),
		zap.Int64("user_id", userID),
		zap.String("remote_ip", remoteIP),
		zap.String("user_agent", userAgent),
		zap.String("content_type", contentType),
		zap.Int64("size_bytes", size),
		zap.String("filename", filename),
		zap.Int64("total_ms", time.Since(start).Milliseconds()),
	)
}

type updateFileReq struct {
	Name     *string    `json:"name,omitempty"`
	FolderID *uuid.UUID `json:"folder_id,omitempty"`
}

// UpdateFile godoc
// @Summary Update file
// @Description Rename or move a file
// @Tags drive
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "File UUID"
// @Param body body updateFileReq true "Update payload"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /drive/files/{id} [put]
func (h *DriveHandler) UpdateFile(c *gin.Context) {
	userID := h.getUserID(c)

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid file id"))
		return
	}

	var req updateFileReq
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.BadRequest("invalid body"))
		return
	}

	if err := h.drive.UpdateFile(c.Request.Context(), userID, id, req.Name, req.FolderID); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "file updated")})
}

// DeleteFile godoc
// @Summary Delete file
// @Description Soft delete a file
// @Tags drive
// @Security BearerAuth
// @Produce json
// @Param id path string true "File UUID"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /drive/files/{id} [delete]
func (h *DriveHandler) DeleteFile(c *gin.Context) {
	userID := h.getUserID(c)

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid file id"))
		return
	}

	result, err := h.drive.DeleteFile(c.Request.Context(), userID, id)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, result)
}

type rejectDeleteRequestReq struct {
	Reason string `json:"reason"`
}

func (h *DriveHandler) ApproveDeleteRequest(c *gin.Context) {
	userID := h.getUserID(c)

	requestID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid delete request id"))
		return
	}

	if err := h.drive.ApproveDeleteRequest(c.Request.Context(), userID, requestID); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{
		"status":  "approved",
		"message": apiMessage(c, "Удаление согласовано и выполнено."),
	})
}

func (h *DriveHandler) RejectDeleteRequest(c *gin.Context) {
	userID := h.getUserID(c)

	requestID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid delete request id"))
		return
	}

	var req rejectDeleteRequestReq
	if err := c.ShouldBindJSON(&req); err != nil && err.Error() != "EOF" {
		h.respondError(c, apperrors.BadRequest("invalid body"))
		return
	}

	if err := h.drive.RejectDeleteRequest(c.Request.Context(), userID, requestID, req.Reason); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{
		"status":  "rejected",
		"message": apiMessage(c, "Запрос на удаление отклонён."),
	})
}

type permissionReq struct {
	ResourceType domain.ResourceType   `json:"resource_type" binding:"required,oneof=file folder"`
	ResourceID   uuid.UUID             `json:"resource_id" binding:"required"`
	UserID       []json.RawMessage     `json:"user_id" binding:"required,min=1"`
	Role         domain.PermissionRole `json:"role" binding:"required,oneof=viewer editor owner manager"`
}

// PermissionReqDoc mirrors permissionReq for Swagger generation.
// user_id accepts numeric user IDs and the special string value "public".
type PermissionReqDoc struct {
	ResourceType domain.ResourceType   `json:"resource_type" binding:"required,oneof=file folder"`
	ResourceID   uuid.UUID             `json:"resource_id" binding:"required"`
	UserID       []string              `json:"user_id" binding:"required,min=1" example:"123,public"`
	Role         domain.PermissionRole `json:"role" binding:"required,oneof=viewer editor owner manager"`
}

func parsePermissionTargets(rawTargets []json.RawMessage) ([]int64, bool, error) {
	userIDs := make([]int64, 0, len(rawTargets))
	makePublic := false

	for _, raw := range rawTargets {
		var id int64
		if err := json.Unmarshal(raw, &id); err == nil {
			if id <= 0 {
				return nil, false, fmt.Errorf("invalid user_id")
			}
			userIDs = append(userIDs, id)
			continue
		}

		var target string
		if err := json.Unmarshal(raw, &target); err == nil && target == "public" {
			makePublic = true
			continue
		}

		return nil, false, fmt.Errorf("invalid user_id format")
	}

	return userIDs, makePublic, nil
}

// GrantPermission godoc
// @Summary Grant permission
// @Description Grant viewer/editor permission to multiple users for file/folder
// @Tags drive
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param body body PermissionReqDoc true "Grant permission payload"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 403 {object} map[string]interface{}
// @Router /drive/permissions [post]
func (h *DriveHandler) GrantPermission(c *gin.Context) {
	ownerID := h.getUserID(c)

	var req permissionReq
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.BadRequest("invalid body"))
		return
	}

	userIDs, makePublic, err := parsePermissionTargets(req.UserID)
	if err != nil {
		h.respondError(c, apperrors.BadRequest(err.Error()))
		return
	}

	if len(userIDs) > 0 {
		if err := h.drive.GrantPermissionsBulk(c.Request.Context(), ownerID, req.ResourceType, req.ResourceID, userIDs, req.Role); err != nil {
			h.respondError(c, apperrors.ToAppError(err))
			return
		}
	}

	if makePublic {
		if req.ResourceType == domain.ResourceFolder {
			if err := h.drive.SetFolderPublicStatus(c.Request.Context(), ownerID, req.ResourceID, true); err != nil {
				h.respondError(c, apperrors.ToAppError(err))
				return
			}
		} else {
			if err := h.drive.SetFilePublicStatus(c.Request.Context(), ownerID, req.ResourceID, true); err != nil {
				h.respondError(c, apperrors.ToAppError(err))
				return
			}
		}
	}

	c.Status(http.StatusNoContent)
}

// UpdatePermission godoc
// @Summary Update permission
// @Description Update permission role for users
// @Tags drive
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param body body PermissionReqDoc true "Update permission payload"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 403 {object} map[string]interface{}
// @Router /drive/permissions [put]
func (h *DriveHandler) UpdatePermission(c *gin.Context) {
	ownerID := h.getUserID(c)

	var req permissionReq
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.BadRequest("invalid body"))
		return
	}

	userIDs, makePublic, err := parsePermissionTargets(req.UserID)
	if err != nil {
		h.respondError(c, apperrors.BadRequest(err.Error()))
		return
	}

	if len(userIDs) > 0 {
		if err := h.drive.GrantPermissionsBulk(c.Request.Context(), ownerID, req.ResourceType, req.ResourceID, userIDs, req.Role); err != nil {
			h.respondError(c, apperrors.ToAppError(err))
			return
		}
	}

	if makePublic {
		if req.ResourceType == domain.ResourceFolder {
			if err := h.drive.SetFolderPublicStatus(c.Request.Context(), ownerID, req.ResourceID, true); err != nil {
				h.respondError(c, apperrors.ToAppError(err))
				return
			}
		} else {
			if err := h.drive.SetFilePublicStatus(c.Request.Context(), ownerID, req.ResourceID, true); err != nil {
				h.respondError(c, apperrors.ToAppError(err))
				return
			}
		}
	}

	c.Status(http.StatusNoContent)
}

// GetUsersWithAccess godoc
// @Summary Get users with access
// @Description Get list of users who have access to file/folder
// @Tags drive
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param resource_type query string true "Resource type" Enums(file, folder)
// @Param resource_id query string true "Resource UUID"
// @Param limit query int false "Limit for paginated response"
// @Param offset query int false "Offset for paginated response"
// @Param page query int false "Page number for paginated response, starts from 1"
// @Param paginated query bool false "Return paginated wrapper instead of flat array"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Router /drive/permissions/users-with-access [get]
func (h *DriveHandler) GetUsersWithAccess(c *gin.Context) {
	userID := h.getUserID(c)

	resourceType := domain.ResourceType(c.Query("resource_type"))
	switch resourceType {
	case domain.ResourceFile, domain.ResourceFolder:
		// valid
	default:
		h.respondError(c, apperrors.BadRequest("invalid resource_type"))
		return
	}

	resourceIDStr := c.Query("resource_id")
	resourceID, err := uuid.Parse(resourceIDStr)
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid resource_id"))
		return
	}

	searchQuery := c.Query("q")
	if searchQuery == "" {
		searchQuery = c.Query("search")
	}

	// Keep the legacy flat array unless pagination was explicitly requested.
	rawLimit := c.Query("limit")
	rawOffset := c.Query("offset")
	rawPage := c.Query("page")
	isPaginatedRequest := rawLimit != "" || rawOffset != "" || rawPage != "" || c.Query("paginated") == "true"
	if !isPaginatedRequest && searchQuery == "" {
		users, err := h.drive.GetUsersWithAccess(c.Request.Context(), userID, resourceType, resourceID)
		if err != nil {
			h.respondError(c, apperrors.ToAppError(err))
			return
		}
		respondJSON(c, http.StatusOK, users)
		return
	}

	limit := 20
	if parsedLimit, err := strconv.Atoi(rawLimit); err == nil && parsedLimit > 0 {
		limit = parsedLimit
	}

	offset := 0
	page := 1
	if parsedOffset, err := strconv.Atoi(rawOffset); err == nil && parsedOffset >= 0 {
		offset = parsedOffset
		page = (offset / limit) + 1
	} else if parsedPage, err := strconv.Atoi(rawPage); err == nil && parsedPage > 0 {
		page = parsedPage
		offset = (page - 1) * limit
	}

	users, total, err := h.drive.GetUsersWithAccessPaginated(c.Request.Context(), userID, resourceType, resourceID, limit, offset, searchQuery)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{
		"items":  users,
		"total":  total,
		"page":   page,
		"limit":  limit,
		"offset": offset,
	})
}

// ExportFolderAccessReport godoc
// @Summary Export folder access report
// @Description Export CSV report for a folder and its nested subfolders with users and their roles
// @Tags drive
// @Security BearerAuth
// @Produce application/vnd.openxmlformats-officedocument.spreadsheetml.sheet
// @Param id path string true "Folder UUID"
// @Success 200 {file} binary
// @Failure 400 {object} map[string]interface{}
// @Failure 403 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /drive/folders/{id}/access-report [get]
func (h *DriveHandler) ExportFolderAccessReport(c *gin.Context) {
	userID := h.getUserID(c)

	folderID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid folder id"))
		return
	}

	reportBytes, fileName, err := h.drive.BuildFolderAccessReportCSV(c.Request.Context(), userID, folderID)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	c.Header("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", fileName))
	c.Header("Content-Length", fmt.Sprintf("%d", len(reportBytes)))
	c.Data(http.StatusOK, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", reportBytes)
}

// GetMyPermission godoc
// @Summary Get my permission role
// @Description Get current user's permission role for a file or folder
// @Tags drive
// @Security BearerAuth
// @Produce json
// @Param resource_type query string true "Resource type" Enums(file, folder)
// @Param resource_id query string true "Resource UUID"
// @Success 200 {object} map[string]string
// @Failure 400 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /drive/permissions/my [get]
func (h *DriveHandler) GetMyPermission(c *gin.Context) {
	userID := h.getUserID(c)

	resourceType := domain.ResourceType(c.Query("resource_type"))
	if resourceType != domain.ResourceFile && resourceType != domain.ResourceFolder {
		h.respondError(c, apperrors.BadRequest("invalid resource_type"))
		return
	}

	resourceIDStr := c.Query("resource_id")
	resourceID, err := uuid.Parse(resourceIDStr)
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid resource_id"))
		return
	}

	role, err := h.drive.GetMyRole(c.Request.Context(), userID, resourceType, resourceID)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"role": role, "can_view": role == domain.RoleViewer || role == domain.RoleEditor || role == domain.RoleOwner, "can_edit": role == domain.RoleEditor || role == domain.RoleOwner, "can_manage_access": role == domain.RoleOwner})
}

// GetUsersWithoutAccess godoc
// @Summary Get users without access
// @Description Get list of users who don't have access (available to grant)
// @Tags drive
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param resource_type query string true "Resource type" Enums(file, folder)
// @Param resource_id query string true "Resource UUID"
// @Param query query string false "Search users by name, username, or email"
// @Param q query string false "Search users by name, username, or email"
// @Param search query string false "Search users by name, username, or email"
// @Param limit query int false "Limit for paginated response (default 20, max 100)"
// @Param offset query int false "Offset for paginated response"
// @Param page query int false "Page number for paginated response, starts from 1"
// @Success 200 {object} domain.UserBasicListResponse
// @Failure 400 {object} map[string]interface{}
// @Router /drive/permissions/users-without-access [get]
func (h *DriveHandler) GetUsersWithoutAccess(c *gin.Context) {
	userID := h.getUserID(c)

	resourceType := domain.ResourceType(c.Query("resource_type"))
	switch resourceType {
	case domain.ResourceFile, domain.ResourceFolder:
		// valid
	default:
		h.respondError(c, apperrors.BadRequest("invalid resource_type"))
		return
	}

	resourceIDStr := c.Query("resource_id")
	resourceID, err := uuid.Parse(resourceIDStr)
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid resource_id"))
		return
	}

	searchQuery := strings.TrimSpace(c.Query("query"))
	if searchQuery == "" {
		searchQuery = strings.TrimSpace(c.Query("q"))
	}
	if searchQuery == "" {
		searchQuery = strings.TrimSpace(c.Query("search"))
	}

	const defaultLimit = 20
	const maxLimit = 100

	limit := defaultLimit
	rawLimit := strings.TrimSpace(c.Query("limit"))
	if rawLimit != "" {
		parsedLimit, err := strconv.Atoi(rawLimit)
		if err != nil || parsedLimit <= 0 {
			h.respondError(c, apperrors.BadRequest("invalid limit"))
			return
		}
		if parsedLimit > maxLimit {
			parsedLimit = maxLimit
		}
		limit = parsedLimit
	}

	offset := 0
	page := 1
	rawOffset := strings.TrimSpace(c.Query("offset"))
	rawPage := strings.TrimSpace(c.Query("page"))
	switch {
	case rawOffset != "":
		parsedOffset, err := strconv.Atoi(rawOffset)
		if err != nil || parsedOffset < 0 {
			h.respondError(c, apperrors.BadRequest("invalid offset"))
			return
		}
		offset = parsedOffset
		page = (offset / limit) + 1
	case rawPage != "":
		parsedPage, err := strconv.Atoi(rawPage)
		if err != nil || parsedPage <= 0 {
			h.respondError(c, apperrors.BadRequest("invalid page"))
			return
		}
		page = parsedPage
		offset = (page - 1) * limit
	}

	users, total, err := h.drive.GetUsersWithoutAccessPaginated(
		c.Request.Context(),
		userID,
		resourceType,
		resourceID,
		searchQuery,
		limit,
		offset,
	)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, domain.UserBasicListResponse{
		Items:  users,
		Total:  total,
		Page:   page,
		Limit:  limit,
		Offset: offset,
	})
}

type revokeReq struct {
	ResourceType domain.ResourceType `json:"resource_type" binding:"required,oneof=file folder"`
	ResourceID   uuid.UUID           `json:"resource_id" binding:"required"`
	UserID       []int64             `json:"user_id" binding:"required"`
}

type transferOwnershipReq struct {
	ResourceType domain.ResourceType `json:"resource_type" binding:"required,oneof=file folder"`
	ResourceID   uuid.UUID           `json:"resource_id" binding:"required"`
	NewOwnerID   int64               `json:"new_owner_id" binding:"required"`
}

// RevokePermission godoc
// @Summary Revoke permission
// @Description Revoke permission from users for file/folder. Allows batch revocation.
// @Tags drive
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param body body revokeReq true "Revoke permission payload"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 403 {object} map[string]interface{}
// @Router /drive/permissions [delete]
func (h *DriveHandler) RevokePermission(c *gin.Context) {
	ownerID := h.getUserID(c)

	var req revokeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.BadRequest("invalid body: "+err.Error()))
		return
	}

	if err := h.drive.RevokePermissionsBulk(c.Request.Context(), ownerID, req.ResourceType, req.ResourceID, req.UserID); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "permissions revoked"), "count": len(req.UserID)})
}

// TransferOwnership godoc
// @Summary Transfer ownership
// @Description Transfer file or folder ownership to another user. For folders, ownership is transferred recursively only for nested items owned by the current owner.
// @Tags drive
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param body body transferOwnershipReq true "Transfer ownership payload"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 403 {object} map[string]interface{}
// @Router /drive/ownership/transfer [post]
func (h *DriveHandler) TransferOwnership(c *gin.Context) {
	ownerID := h.getUserID(c)

	var req transferOwnershipReq
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.BadRequest("invalid body"))
		return
	}

	if err := h.drive.TransferOwnership(c.Request.Context(), ownerID, req.ResourceType, req.ResourceID, req.NewOwnerID); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{
		"message":        apiMessage(c, "ownership transferred"),
		"resource_type":  req.ResourceType,
		"resource_id":    req.ResourceID,
		"new_owner_id":   req.NewOwnerID,
		"previous_role":  domain.RoleEditor,
		"transfer_scope": "folders recursively transfer only nested items owned by the previous owner",
	})
}

type onlyOfficeAction struct {
	Type   int    `json:"type"`
	UserID string `json:"userid"`
}

type onlyOfficeCallbackReq struct {
	FileType      string             `json:"filetype,omitempty"`
	Key           string             `json:"key"`
	Status        int                `json:"status"`
	URL           string             `json:"url,omitempty"`
	ChangesURL    string             `json:"changesurl,omitempty"`
	Users         []string           `json:"users,omitempty"`
	Actions       []onlyOfficeAction `json:"actions,omitempty"`
	Token         string             `json:"token,omitempty"`
	ForceSaveType int                `json:"forcesavetype,omitempty"`
	Error         int                `json:"error,omitempty"`
}

// Only signed edit/comment/review sessions for this file may save. Callback users/actions are
// document-server payload, not authentication credentials.
func resolveOnlyOfficeCallbackUserID(fileID uuid.UUID, token, jwtSecret string, documentKey ...string) (int64, error) {
	claims, err := service.ValidateOnlyOfficeCallbackToken(token, jwtSecret)
	if err != nil {
		return 0, err
	}
	if claims.FileID != fileID.String() || claims.UserID <= 0 {
		return 0, apperrors.Unauthorized("invalid callback session")
	}
	if claims.DocumentKey != "" && (len(documentKey) == 0 || claims.DocumentKey != documentKey[0]) {
		return 0, apperrors.Unauthorized("callback document key mismatch")
	}
	if !service.OnlyOfficeModeCanSave(claims.Mode) {
		return 0, apperrors.Forbidden("read-only session cannot save")
	}
	return claims.UserID, nil
}

// OnlyOfficeCallback godoc
// @Summary OnlyOffice callback
// @Description Synchronizes status 2/6 snapshots into the same Drive file. Repeated callbacks are idempotent; stale sessions return 409. Requires signed edit/comment/review-session token in query and DocumentServer JWT when configured. Responses use numeric error (0 on success, 1 on failure).
// @Tags drive
// @Accept json
// @Produce json
// @Param token query string true "Signed edit/comment/review-session callback token"
// @Param file_id query string true "File UUID"
// @Param body body onlyOfficeCallbackReq true "Callback payload"
// @Success 200 {object} map[string]int
// @Failure 400 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /drive/onlyoffice/callback [post]
func (h *DriveHandler) OnlyOfficeCallback(c *gin.Context) {
	start := time.Now()
	userID := h.getUserID(c)

	fileID, err := uuid.Parse(c.Query("file_id"))
	if err != nil {
		respondOnlyOfficeError(c, apperrors.BadRequest("invalid file_id"))
		return
	}

	var req onlyOfficeCallbackReq
	if err := c.ShouldBindJSON(&req); err != nil {
		respondOnlyOfficeError(c, apperrors.BadRequest("invalid body"))
		return
	}

	// Логируем все callback-запросы, чтобы видеть полную картину DocumentServer timeline.
	// status=1: документ открыт / редактирование началось
	// status=2: сохранение завершено
	// status=4: закрыт без изменений
	// status=6: принудительное сохранение
	bodySize := c.Request.ContentLength
	if bodySize < 0 {
		bodySize = 0
	}

	h.logger.Info("onlyoffice_callback_received",
		zap.String("file_id", fileID.String()),
		zap.Int64("user_id", userID),
		zap.Int("status", req.Status),
		zap.String("key", req.Key),
		zap.Strings("users", req.Users),
		zap.Any("actions", req.Actions),
		zap.Int("forcesavetype", req.ForceSaveType),
		zap.Int("error", req.Error),
		zap.String("remote_ip", c.ClientIP()),
		zap.String("user_agent", c.GetHeader("User-Agent")),
		zap.Bool("has_url", req.URL != ""),
		zap.Bool("has_changesurl", req.ChangesURL != ""),
		zap.Int64("request_body_size", bodySize),
		zap.Int64("total_ms", time.Since(start).Milliseconds()),
	)

	if err := validateOnlyOfficeCallbackJWT(c, req); err != nil {
		respondOnlyOfficeError(c, apperrors.Unauthorized("invalid DocumentServer callback signature"))
		return
	}

	if req.Status != 2 && req.Status != 6 && req.Status != 3 && req.Status != 7 {
		h.logger.Info("onlyoffice_callback_no_save",
			zap.String("file_id", fileID.String()),
			zap.Int("status", req.Status),
			zap.String("key", req.Key),
			zap.Int64("total_ms", time.Since(start).Milliseconds()),
		)
		respondJSON(c, http.StatusOK, gin.H{"error": 0})
		return
	}

	// The backend-signed capability lives in callbackUrl. A DocumentServer JWT
	// in the body must not override it.
	if token := strings.TrimSpace(c.Query("token")); token != "" {
		req.Token = token
	}
	userID, err = resolveOnlyOfficeCallbackUserID(fileID, req.Token, h.jwtSecret, req.Key)
	if err != nil {
		respondOnlyOfficeError(c, apperrors.ToAppError(err))
		return
	}

	if req.Status == 3 || req.Status == 7 {
		if err := h.drive.RecordOfficeServerError(c.Request.Context(), userID, fileID, req.Key); err != nil {
			respondOnlyOfficeError(c, apperrors.ToAppError(err))
			return
		}
		// Acknowledge receipt of the error, without treating its URL as a valid snapshot.
		respondJSON(c, http.StatusOK, gin.H{"error": 0})
		return
	}

	h.logger.Info("received onlyoffice callback",
		zap.String("file_id", fileID.String()),
		zap.Int64("user_id", userID),
		zap.Int("status", req.Status),
		zap.String("key", req.Key),
		zap.Bool("has_token", strings.TrimSpace(req.Token) != ""),
	)

	if err := h.drive.SaveOnlyOfficeCallback(c.Request.Context(), userID, fileID, service.OnlyOfficeCallback{
		FileType: req.FileType,
		Status:   req.Status,
		URL:      req.URL,
		Key:      req.Key,
		Token:    "",
	}); err != nil {
		h.logger.Error("onlyoffice callback processing failed",
			zap.String("file_id", fileID.String()),
			zap.Int64("user_id", userID),
			zap.Int("status", req.Status),
			zap.String("key", req.Key),
			zap.Error(err),
		)
		respondOnlyOfficeError(c, apperrors.ToAppError(err))
		return
	}

	h.logger.Info("onlyoffice callback processed successfully",
		zap.String("file_id", fileID.String()),
		zap.Int64("user_id", userID),
		zap.Int("status", req.Status),
		zap.String("key", req.Key),
		zap.Int64("total_ms", time.Since(start).Milliseconds()),
	)

	respondJSON(c, http.StatusOK, gin.H{"error": 0})
}

// ListAllDrive godoc
// @Summary List all drive items (own + shared)
// @Description Returns root own items + items shared with user
// @Tags drive
// @Security BearerAuth
// @Produce json
// @Param limit query int false "Limit"
// @Param offset query int false "Offset"
// @Param type query string false "File type filter"
// @Param owner_id query int false "Owner ID"
// @Param sort_by query string false "Sort by: name, date, size"
// @Param sort_order query string false "Sort order: asc, desc"
// @Success 200 {object} domain.ListAllDriveResp
// @Failure 500 {object} map[string]interface{}
// @Router /drive/items [get]
func (h *DriveHandler) ListAllDrive(c *gin.Context) {
	userID := h.getUserID(c)

	limit, offset := parsePagination(c)
	fileType := c.Query("type")
	ownerIDStr := c.Query("owner_id")
	var ownerID *int64
	if ownerIDStr != "" {
		if oid, err := strconv.ParseInt(ownerIDStr, 10, 64); err == nil && oid > 0 {
			ownerID = &oid
		}
	}
	sortBy := c.DefaultQuery("sort_by", "date")
	sortOrder := c.DefaultQuery("sort_order", "desc")

	own, shared, err := h.drive.ListAllDriveItemsFiltered(c.Request.Context(), userID, limit, offset, fileType, ownerID, sortBy, sortOrder)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, domain.ListAllDriveResp{
		Own:    own,
		Shared: shared,
	})
}

// ListActivity godoc
// @Summary Get drive activity history
// @Description Returns drive activity history for the current user's root drive or for a specific folder subtree
// @Tags drive
// @Security BearerAuth
// @Produce json
// @Param folder_id query string false "Folder UUID to filter by subtree"
// @Param limit query int false "Limit"
// @Param offset query int false "Offset"
// @Success 200 {object} domain.DriveActivityListResponseDoc
// @Failure 400 {object} map[string]interface{}
// @Failure 403 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /drive/activity [get]
func (h *DriveHandler) ListActivity(c *gin.Context) {
	userID := h.getUserID(c)

	var folderID *uuid.UUID
	if rawFolderID := strings.TrimSpace(c.Query("folder_id")); rawFolderID != "" {
		parsed, err := uuid.Parse(rawFolderID)
		if err != nil {
			h.respondError(c, apperrors.BadRequest("invalid folder_id"))
			return
		}
		folderID = &parsed
	}

	limit, offset := parsePagination(c)
	activities, err := h.drive.ListDriveActivities(c.Request.Context(), userID, folderID, limit, offset)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, activities)
}

// DeleteFolder godoc
// @Summary Delete folder
// @Description Delete a folder. Use recursive=true to delete non-empty folders.
// @Tags drive
// @Security BearerAuth
// @Produce json
// @Param id path string true "Folder UUID"
// @Param recursive query boolean false "Recursive delete"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /drive/folders/{id} [delete]
func (h *DriveHandler) DeleteFolder(c *gin.Context) {
	userID := h.getUserID(c)

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid folder id"))
		return
	}

	recursive := c.Query("recursive") == "true" || c.Query("recursive") == "1"

	result, err := h.drive.DeleteFolder(c.Request.Context(), userID, id, recursive)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, result)
}

// ListOwnDrive godoc
// @Summary List own drive root items
// @Description Returns root folders and files owned by user
// @Tags drive
// @Security BearerAuth
// @Produce json
// @Param limit query int false "Limit"
// @Param offset query int false "Offset"
// @Param type query string false "File type filter"
// @Param owner_id query int false "Owner ID"
// @Param sort_by query string false "Sort by: name, date, size"
// @Param sort_order query string false "Sort order: asc, desc"
// @Success 200 {array} domain.DriveItem
// @Failure 500 {object} map[string]interface{}
// @Router /drive/items/own [get]
func (h *DriveHandler) ListOwnDrive(c *gin.Context) {
	userID := h.getUserID(c)

	limit, offset := parsePagination(c)
	fileType := c.Query("type")
	ownerIDStr := c.Query("owner_id")
	var ownerID *int64
	if ownerIDStr != "" {
		if oid, err := strconv.ParseInt(ownerIDStr, 10, 64); err == nil && oid > 0 {
			ownerID = &oid
		}
	}
	sortBy := c.DefaultQuery("sort_by", "date")
	sortOrder := c.DefaultQuery("sort_order", "desc")

	items, err := h.drive.ListOwnItemsFiltered(c.Request.Context(), userID, limit, offset, fileType, ownerID, sortBy, sortOrder)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, items)
}

// ListSharedDrive godoc
// @Summary List shared drive items
// @Description Returns items shared with user with filtering and sorting
// @Tags drive
// @Security BearerAuth
// @Produce json
// @Param limit query int false "Limit"
// @Param offset query int false "Offset"
// @Param type query string false "File type filter (extension like: pdf, xlsx, docx, or category: image, video, document)"
// @Param owner_id query int false "Filter by owner user ID"
// @Param sort_by query string false "Sort by: name, date, size (default: date)"
// @Param sort_order query string false "Sort order: asc, desc (default: desc)"
// @Success 200 {array} domain.DriveItem
// @Failure 500 {object} map[string]interface{}
// @Router /drive/items/shared [get]
func (h *DriveHandler) ListSharedDrive(c *gin.Context) {
	userID := h.getUserID(c)

	limit, offset := parsePagination(c)

	// Фильтры
	fileType := c.Query("type")       // pdf, xlsx, image, video
	ownerIDStr := c.Query("owner_id") // ID владельца

	var ownerID *int64
	if ownerIDStr != "" {
		if oid, err := strconv.ParseInt(ownerIDStr, 10, 64); err == nil && oid > 0 {
			ownerID = &oid
		}
	}

	// Сортировка
	sortBy := c.DefaultQuery("sort_by", "date")       // name, date, size
	sortOrder := c.DefaultQuery("sort_order", "desc") // asc, desc

	items, err := h.drive.ListSharedItemsFiltered(c.Request.Context(), userID, limit, offset, fileType, ownerID, sortBy, sortOrder)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, items)
}

// ListRecent godoc
// @Summary List recent items
// @Description Returns files and folders accessed recently by the user
// @Tags drive
// @Security BearerAuth
// @Produce json
// @Param limit query int false "Limit (default 20)"
// @Param offset query int false "Offset"
// @Param type query string false "File type filter"
// @Param owner_id query int false "Owner ID"
// @Param sort_by query string false "Sort by: name, date, size"
// @Param sort_order query string false "Sort order: asc, desc"
// @Success 200 {array} domain.RecentItem
// @Failure 500 {object} map[string]interface{}
// @Router /drive/recent [get]
func (h *DriveHandler) ListRecent(c *gin.Context) {
	userID := h.getUserID(c)

	limit, offset := parsePagination(c)
	if limit == 0 {
		limit = 20
	}

	// Фильтры пока частично игнорируются для Recent, так как сервис еще не поддерживает их
	// Но мы добавим их в TODO
	// TODO: Add filtered recent items
	items, err := h.drive.ListRecent(c.Request.Context(), userID, limit, offset)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, items)
}

// ListSharingUsers godoc
// @Summary List users who shared files with me
// @Description Get a list of users who have shared files or folders with the current user
// @Tags drive
// @Security BearerAuth
// @Produce json
// @Success 200 {array} domain.UserBasic
// @Failure 401 {object} apperrors.AppError
// @Failure 500 {object} apperrors.AppError
// @Router /drive/shared/users [get]
func (h *DriveHandler) ListSharingUsers(c *gin.Context) {
	userID := h.getUserID(c)
	users, err := h.drive.ListSharingUsers(c.Request.Context(), userID)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, users)
}

func (h *DriveHandler) getUserID(c *gin.Context) int64 {
	if id, exists := middleware.GetUserID(c); exists {
		return id
	}
	return 0
}

func (h *DriveHandler) respondError(c *gin.Context, err *apperrors.AppError) {
	respondAppError(c, err)
}

func parsePagination(c *gin.Context) (int, int) {
	limit, _ := strconv.Atoi(c.Query("limit"))
	offset, _ := strconv.Atoi(c.Query("offset"))
	if limit < 0 {
		limit = 0
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

// SearchItems godoc
// @Summary Search and filter drive items
// @Description Search file/folder names or file tags with type and user filters and pagination
// @Tags drive
// @Security BearerAuth
// @Produce json
// @Param q query string false "Case-insensitive literal substring in names or file tags"
// @Param type query string false "Type filter: folder, image, video, document"
// @Param limit query int false "Limit (default 20)"
// @Param page query int false "Page (default 1)"
// @Param user_id query int false "Filter by user ID (owner or participant)"
// @Param user_filter query string false "User role filter: owner, participant"
// @Success 200 {array} domain.DriveItem
// @Failure 401 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /drive/search [get]
func (h *DriveHandler) SearchItems(c *gin.Context) {
	userID := h.getUserID(c)
	if userID == 0 {
		h.respondError(c, apperrors.Unauthorized("unauthorized"))
		return
	}

	query := c.Query("q")
	fileType := c.Query("type")
	userFilter := c.Query("user_filter") // "owner" или "participant"

	// Опциональный user_id для фильтрации
	var ownerUserID *int64
	if userIDStr := c.Query("user_id"); userIDStr != "" {
		uid, err := strconv.ParseInt(userIDStr, 10, 64)
		if err == nil && uid > 0 {
			ownerUserID = &uid
		}
	}

	page, err := strconv.Atoi(c.DefaultQuery("page", "1"))
	if err != nil || page < 1 {
		page = 1
	}

	limit, err := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if err != nil || limit < 1 {
		limit = 20
	}

	if limit > 100 {
		limit = 100
	}

	offset := (page - 1) * limit

	items, err := h.drive.SearchAndFilter(c.Request.Context(), userID, query, fileType, limit, offset, ownerUserID, userFilter)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	if items == nil {
		items = []domain.DriveItem{}
	}

	respondJSON(c, http.StatusOK, items)
}

type createLinkReq struct {
	ResourceType string `json:"resource_type" binding:"required,oneof=file folder"`
	ResourceID   string `json:"resource_id" binding:"required"`
	Role         string `json:"role" binding:"required,oneof=viewer owner editor manager"`
	ExpiresInSec int64  `json:"expires_in_sec" binding:"gte=0,lte=9223372036"`
}

type acceptPublicLinkResp struct {
	Token      uuid.UUID         `json:"token"`
	AccessRole string            `json:"access_role"`
	ItemType   string            `json:"type"`
	File       *domain.DriveFile `json:"file,omitempty"`
	Folder     *domain.Folder    `json:"folder,omitempty"`
	Accepted   bool              `json:"accepted"`
}

// CreatePublicLink godoc
// @Summary Generate public link
// @Description Create a temporary or permanent public access link
// @Tags drive
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body createLinkReq true "Link creation request"
// @Success 201 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Router /drive/share/link [post]
func (h *DriveHandler) CreatePublicLink(c *gin.Context) {
	setPublicLinkHeaders(c)
	userID := h.getUserID(c)

	var req createLinkReq
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.BadRequest("invalid body"))
		return
	}

	rid, err := uuid.Parse(req.ResourceID)
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid resource_id uuid"))
		return
	}

	ttl := time.Duration(req.ExpiresInSec) * time.Second

	link, err := h.drive.CreatePublicLink(c.Request.Context(), userID, req.ResourceType, rid, req.Role, ttl)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	fullURL := fmt.Sprintf("%s/web/drive/share/%s", h.publicURL, link.Token.String())

	respondJSON(c, http.StatusCreated, gin.H{
		"token":      link.Token,
		"url":        fullURL,
		"expires_at": link.ExpiresAt,
		"role":       link.Role,
	})
}

// AcceptPublicLink godoc
// @Summary Accept public drive link
// @Description Validates the link and returns metadata without granting permanent access
// @Tags drive
// @Security BearerAuth
// @Produce json
// @Param token path string true "Access Token"
// @Success 200 {object} acceptPublicLinkResp
// @Failure 400 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /drive/share/link/{token}/accept [post]
func (h *DriveHandler) AcceptPublicLink(c *gin.Context) {
	setPublicLinkHeaders(c)
	userID := h.getUserID(c)
	tokenStr := c.Param("token")
	token, err := uuid.Parse(tokenStr)
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid token"))
		return
	}

	item, err := h.drive.AcceptPublicLink(c.Request.Context(), userID, token)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, acceptPublicLinkResp{
		Token:      item.Token,
		AccessRole: item.AccessRole,
		ItemType:   item.ItemType,
		File:       item.File,
		Folder:     item.Folder,
		Accepted:   true,
	})
}

// GetPublicItem godoc
// @Summary Get item by public link
// @Description Get file/folder meta using token (No Auth Header required)
// @Tags public
// @Produce json
// @Param token path string true "Access Token"
// @Param resource_type query string false "Target type within shared folder: file or folder"
// @Param resource_id query string false "Target UUID within shared folder"
// @Success 200 {object} domain.PublicDriveItem
// @Failure 404 {object} map[string]interface{}
// @Router /public/drive/items/{token} [get]
func (h *DriveHandler) GetPublicItem(c *gin.Context) {
	setPublicLinkHeaders(c)
	tokenStr := c.Param("token")
	token, err := uuid.Parse(tokenStr)
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid token"))
		return
	}

	target, err := parsePublicLinkTarget(c)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	item, err := h.drive.GetPublicItemByToken(c.Request.Context(), token, target)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, item)
}

// DownloadPublicFile godoc
// @Summary Download file by public link
// @Description Stream file content using public token
// @Tags public
// @Produce application/octet-stream
// @Param token path string true "Access Token"
// @Param resource_type query string false "Target type within shared folder: file or folder"
// @Param resource_id query string false "Target UUID within shared folder"
// @Success 200 {file} file
// @Failure 404 {object} map[string]interface{}
// @Router /public/drive/files/{token}/download [get]
func (h *DriveHandler) DownloadPublicFile(c *gin.Context) {
	setPublicLinkHeaders(c)
	tokenStr := c.Param("token")
	token, err := uuid.Parse(tokenStr)
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid token"))
		return
	}

	target, err := parsePublicLinkTarget(c)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	reader, contentType, size, filename, err := h.drive.DownloadPublicFile(c.Request.Context(), token, target)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	defer reader.Close()

	c.Header("Content-Type", contentType)
	c.Header("Content-Length", fmt.Sprintf("%d", size))
	c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
	_, _ = io.Copy(c.Writer, reader)
}
