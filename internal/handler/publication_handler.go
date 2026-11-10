package handler

import (
	"context"
	"errors"
	"fmt"
	_ "golang.org/x/image/webp"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"path/filepath"
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

type PublicationHandler struct {
	svc    *service.PublicationService
	logger *zap.Logger
}

func NewPublicationHandler(svc *service.PublicationService, logger *zap.Logger) *PublicationHandler {
	return &PublicationHandler{
		svc:    svc,
		logger: logger.With(zap.String("handler", "publication")),
	}
}

func (h *PublicationHandler) respondError(c *gin.Context, err *apperrors.AppError) {
	respondAppError(c, err)
}

func (h *PublicationHandler) mapServiceErr(err error) *apperrors.AppError {
	if appErr := apperrors.GetAppError(err); appErr != nil {
		return appErr
	}
	switch {
	case errors.Is(err, service.ErrForbiddenPublish):
		return apperrors.Forbidden("You are not authorized to publish from this source")
	case errors.Is(err, service.ErrForbiddenView):
		return apperrors.Forbidden("This publication is restricted to another ministry")
	case errors.Is(err, service.ErrForbiddenAction):
		return apperrors.Forbidden("You do not have permissions to perform this action")
	case strings.Contains(strings.ToLower(err.Error()), "not found"):
		return apperrors.NotFound(err.Error())
	case strings.Contains(err.Error(), "invalid publication status"), strings.Contains(err.Error(), "scheduled_at must"), strings.Contains(err.Error(), "invalid content_doc"):
		return apperrors.Validation(err.Error())
	default:
		return apperrors.Internal(err.Error())
	}
}

// POST /api/v1/publications
func (h *PublicationHandler) CreatePublication(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4<<20)
	userID := middleware.MustGetUserID(c)
	var req domain.CreatePublicationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation(err.Error()))
		return
	}

	pub, err := h.svc.CreatePublication(c.Request.Context(), userID, req)
	if err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}

	respondJSON(c, http.StatusCreated, pub)
}

// GET /api/v1/publications/:id
func (h *PublicationHandler) GetPublication(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid publication id"))
		return
	}

	pub, err := h.svc.GetPublication(c.Request.Context(), id, userID)
	if err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}

	respondJSON(c, http.StatusOK, pub)
}

// PUT /api/v1/publications/:id
func (h *PublicationHandler) UpdatePublication(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4<<20)
	userID := middleware.MustGetUserID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid publication id"))
		return
	}

	var req domain.UpdatePublicationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation(err.Error()))
		return
	}

	pub, err := h.svc.UpdatePublication(c.Request.Context(), id, userID, req)
	if err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}

	respondJSON(c, http.StatusOK, pub)
}

// GET /api/v1/publications
func (h *PublicationHandler) ListPublications(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	pubType := c.Query("type")
	sourceType := c.Query("source_type")
	importance := c.Query("importance")
	status := c.Query("status")

	var ministryID *int64
	if mIDStr := c.Query("ministry_id"); mIDStr != "" {
		if val, err := strconv.ParseInt(mIDStr, 10, 64); err == nil {
			ministryID = &val
		}
	}

	var stateBodyID *int64
	if sbIDStr := c.Query("state_body_id"); sbIDStr != "" {
		if val, err := strconv.ParseInt(sbIDStr, 10, 64); err == nil {
			stateBodyID = &val
		}
	}

	var departmentID *int64
	if dIDStr := c.Query("department_id"); dIDStr != "" {
		if val, err := strconv.ParseInt(dIDStr, 10, 64); err == nil {
			departmentID = &val
		}
	}

	page := 1
	if pStr := c.Query("page"); pStr != "" {
		if val, err := strconv.Atoi(pStr); err == nil && val > 0 {
			page = val
		}
	}

	limit := 10
	if lStr := c.Query("limit"); lStr != "" {
		if val, err := strconv.Atoi(lStr); err == nil && val > 0 {
			limit = val
		}
	}

	publications, err := h.svc.ListPublications(c.Request.Context(), userID, pubType, sourceType, ministryID, stateBodyID, departmentID, importance, status, page, limit)
	if err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"publications": publications, "count": len(publications)})
}

// POST /api/v1/publications/:id/publish
func (h *PublicationHandler) PublishPublication(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid publication id"))
		return
	}

	var req domain.PublishPublicationRequest
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		h.respondError(c, apperrors.Validation("invalid publication request"))
		return
	}

	pub, err := h.svc.PublishPublication(c.Request.Context(), id, userID, req.ScheduledAt, req.ArchiveAt)
	if err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}

	respondJSON(c, http.StatusOK, pub)
}

// POST /api/v1/publications/:id/unpublish
func (h *PublicationHandler) UnpublishPublication(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid publication id"))
		return
	}

	pub, err := h.svc.UnpublishPublication(c.Request.Context(), id, userID)
	if err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}

	respondJSON(c, http.StatusOK, pub)
}

// POST /api/v1/publications/:id/archive
func (h *PublicationHandler) ArchivePublication(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid publication id"))
		return
	}

	pub, err := h.svc.ArchivePublication(c.Request.Context(), id, userID)
	if err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}

	respondJSON(c, http.StatusOK, pub)
}

// POST /api/v1/publications/:id/unarchive
func (h *PublicationHandler) UnarchivePublication(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid publication id"))
		return
	}

	var req domain.UnarchivePublicationRequest
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		h.respondError(c, apperrors.Validation("invalid unarchive request"))
		return
	}

	pub, err := h.svc.UnarchivePublication(c.Request.Context(), id, userID, req.TargetStatus)
	if err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}

	respondJSON(c, http.StatusOK, pub)
}

// POST /api/v1/publications/archive/obsolete
func (h *PublicationHandler) ArchiveObsoletePublications(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	var req domain.ArchiveObsoleteRequest
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		h.respondError(c, apperrors.Validation("invalid archive obsolete request"))
		return
	}

	resp, err := h.svc.ArchiveObsoletePublications(c.Request.Context(), userID, req)
	if err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}

	respondJSON(c, http.StatusOK, resp)
}

// POST /api/v1/publications/:id/cover
func (h *PublicationHandler) SetCoverImage(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid publication id"))
		return
	}

	var coverURL string
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 10<<20)

	contentType := c.GetHeader("Content-Type")
	if strings.HasPrefix(contentType, "multipart/form-data") {
		editablePublication, err := h.svc.CheckPublicationEditAccess(c.Request.Context(), id, userID)
		if err != nil {
			h.respondError(c, h.mapServiceErr(err))
			return
		}
		if err := c.Request.ParseMultipartForm(1 << 20); err != nil {
			h.respondError(c, apperrors.Validation("invalid or oversized cover upload"))
			return
		}
		defer c.Request.MultipartForm.RemoveAll()
		file, header, err := c.Request.FormFile("cover")
		if err != nil {
			file, header, err = c.Request.FormFile("file")
		}
		if err != nil {
			h.respondError(c, apperrors.BadRequest("cover or file is required in form-data"))
			return
		}
		defer file.Close()
		cfg, format, err := image.DecodeConfig(file)
		if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > 40_000_000 {
			h.respondError(c, apperrors.Validation("invalid cover image"))
			return
		}
		extensions := map[string]string{"jpeg": ".jpg", "png": ".png", "gif": ".gif", "webp": ".webp"}
		ext, ok := extensions[format]
		if !ok {
			h.respondError(c, apperrors.Validation("unsupported cover image format"))
			return
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			h.respondError(c, apperrors.Internal("cannot read cover"))
			return
		}

		fileSvc := h.svc.GetFileService()
		if fileSvc == nil {
			h.respondError(c, apperrors.Internal("file service not available"))
			return
		}

		objectName, err := fileSvc.UploadFile(
			c.Request.Context(),
			editablePublication.AuthorID,
			strings.TrimSuffix(filepath.Base(header.Filename), filepath.Ext(header.Filename))+ext,
			"image",
			header.Size,
			file,
		)
		if err != nil {
			h.respondError(c, apperrors.ToAppError(err))
			return
		}
		attached := false
		defer func() {
			if !attached {
				cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 10*time.Second)
				defer cancel()
				if err := fileSvc.DeleteFile(cleanupCtx, objectName); err != nil {
					h.logger.Warn("failed to remove unattached publication cover", zap.String("object_name", objectName), zap.Error(err))
				}
			}
		}()

		downloadURL, err := fileSvc.GetFileURL(c.Request.Context(), objectName, 24*time.Hour)
		if err != nil {
			h.respondError(c, apperrors.ToAppError(err))
			return
		}
		coverURL = downloadURL
		pub, committed, err := h.svc.SetUploadedCoverImage(c.Request.Context(), id, userID, coverURL)
		attached = committed
		if err != nil {
			h.respondError(c, h.mapServiceErr(err))
			return
		}
		attached = true
		respondJSON(c, http.StatusOK, pub)
		return
	} else {
		var req domain.UpdatePublicationCoverRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			h.respondError(c, apperrors.Validation(err.Error()))
			return
		}
		coverURL = req.CoverImageURL
	}

	pub, err := h.svc.SetCoverImage(c.Request.Context(), id, userID, coverURL)
	if err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}

	respondJSON(c, http.StatusOK, pub)
}

// DELETE /api/v1/publications/:id/cover
func (h *PublicationHandler) RemoveCoverImage(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid publication id"))
		return
	}

	pub, err := h.svc.RemoveCoverImage(c.Request.Context(), id, userID)
	if err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}

	respondJSON(c, http.StatusOK, pub)
}

// DELETE /api/v1/publications/:id
func (h *PublicationHandler) DeletePublication(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid publication id"))
		return
	}

	err = h.svc.DeletePublication(c.Request.Context(), id, userID)
	if err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}

	c.Status(http.StatusNoContent)
}

// POST /api/v1/publications/:id/like
func (h *PublicationHandler) LikePublication(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid publication id"))
		return
	}

	err = h.svc.LikePublication(c.Request.Context(), id, userID)
	if err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"status": "liked"})
}

// DELETE /api/v1/publications/:id/like
func (h *PublicationHandler) UnlikePublication(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid publication id"))
		return
	}

	err = h.svc.UnlikePublication(c.Request.Context(), id, userID)
	if err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"status": "unliked"})
}

// ListPublicationLikes godoc
// @Summary Get publication likes
// @Description Returns users who liked the publication. Available only to the publication author or super admin.
// @Tags publications
// @Produce json
// @Security BearerAuth
// @Param id path string true "Publication ID"
// @Success 200 {object} domain.PublicationLikesResponse
// @Failure 400 {object} apperrors.ErrorResponse
// @Failure 403 {object} apperrors.ErrorResponse
// @Failure 404 {object} apperrors.ErrorResponse
// @Failure 500 {object} apperrors.ErrorResponse
// @Router /publications/{id}/likes [get]
func (h *PublicationHandler) ListPublicationLikes(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid publication id"))
		return
	}

	likes, err := h.svc.ListPublicationLikes(c.Request.Context(), id, userID)
	if err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}

	respondJSON(c, http.StatusOK, domain.PublicationLikesResponse{
		Likes: likes,
		Count: len(likes),
	})
}

// ListPublicationActivity godoc
// @Summary Get publication activity
// @Description Returns users who liked and viewed the publication. Available only to the publication author or super admin.
// @Tags publications
// @Produce json
// @Security BearerAuth
// @Param id path string true "Publication ID"
// @Success 200 {object} domain.PublicationActivityResponse
// @Failure 400 {object} apperrors.ErrorResponse
// @Failure 403 {object} apperrors.ErrorResponse
// @Failure 404 {object} apperrors.ErrorResponse
// @Failure 500 {object} apperrors.ErrorResponse
// @Router /publications/{id}/activity [get]
func (h *PublicationHandler) ListPublicationActivity(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid publication id"))
		return
	}

	activity, err := h.svc.ListPublicationActivity(c.Request.Context(), id, userID)
	if err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}

	respondJSON(c, http.StatusOK, activity)
}

// POST /api/v1/publications/:id/view
func (h *PublicationHandler) RecordView(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid publication id"))
		return
	}

	err = h.svc.RecordView(c.Request.Context(), id, userID)
	if err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"status": "viewed"})
}

// POST /api/v1/publications/:id/comments
func (h *PublicationHandler) AddComment(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid publication id"))
		return
	}

	var req domain.CreateCommentRequest
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation(err.Error()))
		return
	}

	comment, err := h.svc.AddComment(c.Request.Context(), id, userID, req)
	if err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}

	respondJSON(c, http.StatusCreated, comment)
}

// GET /api/v1/publications/:id/comments
func (h *PublicationHandler) ListComments(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid publication id"))
		return
	}

	comments, err := h.svc.ListComments(c.Request.Context(), id, middleware.MustGetUserID(c))
	if err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"comments": comments, "count": len(comments)})
}

// GET /api/v1/publications/:id/comments/:comment_id/replies
func (h *PublicationHandler) ListReplies(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	pubID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid publication id"))
		return
	}
	commentID, err := uuid.Parse(c.Param("comment_id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid comment id"))
		return
	}

	replies, err := h.svc.ListReplies(c.Request.Context(), pubID, commentID, userID)
	if err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"replies": replies, "count": len(replies)})
}

// PUT /api/v1/publications/:id/comments/:comment_id/pin
func (h *PublicationHandler) PinComment(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	pubID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid publication id"))
		return
	}
	commentID, err := uuid.Parse(c.Param("comment_id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid comment id"))
		return
	}

	comment, err := h.svc.PinComment(c.Request.Context(), pubID, commentID, userID, true)
	if err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}

	respondJSON(c, http.StatusOK, comment)
}

// DELETE /api/v1/publications/:id/comments/:comment_id/pin
func (h *PublicationHandler) UnpinComment(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	pubID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid publication id"))
		return
	}
	commentID, err := uuid.Parse(c.Param("comment_id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid comment id"))
		return
	}

	comment, err := h.svc.PinComment(c.Request.Context(), pubID, commentID, userID, false)
	if err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}

	respondJSON(c, http.StatusOK, comment)
}

// PUT /api/v1/publications/:id/comments/:comment_id/moderate
func (h *PublicationHandler) ModerateComment(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	pubID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid publication id"))
		return
	}
	commentID, err := uuid.Parse(c.Param("comment_id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid comment id"))
		return
	}

	var req domain.ModerateCommentRequest
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation(err.Error()))
		return
	}

	comment, err := h.svc.ModerateComment(c.Request.Context(), pubID, commentID, userID, req)
	if err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}

	respondJSON(c, http.StatusOK, comment)
}

// PUT /api/v1/publications/:id/comments/lock
func (h *PublicationHandler) LockComments(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	pubID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid publication id"))
		return
	}

	var req domain.LockCommentsRequest
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation(err.Error()))
		return
	}

	pub, err := h.svc.LockComments(c.Request.Context(), pubID, userID, req)
	if err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}

	respondJSON(c, http.StatusOK, pub)
}

// POST /api/v1/publications/:id/comments/:comment_id/reactions
func (h *PublicationHandler) AddCommentReaction(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	pubID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid publication id"))
		return
	}
	commentID, err := uuid.Parse(c.Param("comment_id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid comment id"))
		return
	}

	var req domain.SetReactionRequest
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4<<10)
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation(err.Error()))
		return
	}

	reactions, err := h.svc.AddCommentReaction(c.Request.Context(), pubID, commentID, userID, req.Reaction)
	if err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"status": "reaction_added", "reactions": reactions})
}

// DELETE /api/v1/publications/:id/comments/:comment_id/reactions/:reaction
func (h *PublicationHandler) RemoveCommentReaction(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	pubID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid publication id"))
		return
	}
	commentID, err := uuid.Parse(c.Param("comment_id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid comment id"))
		return
	}

	reaction := c.Param("reaction")
	if strings.TrimSpace(reaction) == "" {
		h.respondError(c, apperrors.BadRequest("reaction is required"))
		return
	}

	reactions, err := h.svc.RemoveCommentReaction(c.Request.Context(), pubID, commentID, userID, reaction)
	if err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"status": "reaction_removed", "reactions": reactions})
}

// POST /api/v1/publications/:id/reactions
func (h *PublicationHandler) AddReaction(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid publication id"))
		return
	}

	var req domain.SetReactionRequest
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4<<10)
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation(err.Error()))
		return
	}

	reactions, err := h.svc.AddReaction(c.Request.Context(), id, userID, req.Reaction)
	if err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"status": "reaction_added", "reactions": reactions})
}

// DELETE /api/v1/publications/:id/reactions/:reaction
func (h *PublicationHandler) RemoveReaction(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid publication id"))
		return
	}

	reaction := c.Param("reaction")
	if strings.TrimSpace(reaction) == "" {
		h.respondError(c, apperrors.BadRequest("reaction is required"))
		return
	}

	reactions, err := h.svc.RemoveReaction(c.Request.Context(), id, userID, reaction)
	if err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"status": "reaction_removed", "reactions": reactions})
}

// GET /api/v1/publications/:id/reactions
func (h *PublicationHandler) ListReactions(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid publication id"))
		return
	}

	reactions, err := h.svc.GetReactions(c.Request.Context(), id, userID)
	if err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"reactions": reactions})
}

// DELETE /api/v1/publications/comments/:comment_id
func (h *PublicationHandler) DeleteComment(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	id, err := uuid.Parse(c.Param("comment_id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid comment id"))
		return
	}

	err = h.svc.DeleteComment(c.Request.Context(), id, userID)
	if err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}

	c.Status(http.StatusNoContent)
}

// POST /api/v1/publications/:id/acknowledge
func (h *PublicationHandler) AcknowledgePublication(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid publication id"))
		return
	}

	resp, err := h.svc.AcknowledgePublication(c.Request.Context(), id, userID)
	if err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}

	respondJSON(c, http.StatusOK, resp)
}

// POST /api/v1/publications/:id/unacknowledge or DELETE /api/v1/publications/:id/acknowledge
func (h *PublicationHandler) UnacknowledgePublication(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid publication id"))
		return
	}

	resp, err := h.svc.UnacknowledgePublication(c.Request.Context(), id, userID)
	if err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}

	respondJSON(c, http.StatusOK, resp)
}

// POST /api/v1/publications/:id/share
func (h *PublicationHandler) SharePublication(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid publication id"))
		return
	}

	err = h.svc.SharePublication(c.Request.Context(), id, middleware.MustGetUserID(c))
	if err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"status": "shared"})
}

// POST /api/v1/publications/:id/attachments
func (h *PublicationHandler) UploadAttachments(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	pubID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid publication id"))
		return
	}

	contentType := c.GetHeader("Content-Type")
	if strings.HasPrefix(contentType, "multipart/form-data") {
		if _, err := h.svc.CheckPublicationEditAccess(c.Request.Context(), pubID, userID); err != nil {
			h.respondError(c, h.mapServiceErr(err))
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 100<<20)
		form, err := c.MultipartForm()
		if err != nil {
			h.respondError(c, apperrors.BadRequest("invalid multipart form: "+err.Error()))
			return
		}
		defer func() { _ = form.RemoveAll() }()

		files := form.File["files"]
		if len(files) == 0 {
			files = form.File["file"]
		}
		if len(files) == 0 {
			h.respondError(c, apperrors.BadRequest("no files found in request"))
			return
		}

		captions := form.Value["captions"]
		if len(captions) == 0 {
			captions = form.Value["caption"]
		}

		created, err := h.svc.UploadAttachments(c.Request.Context(), pubID, userID, files, captions)
		if err != nil {
			h.respondError(c, h.mapServiceErr(err))
			return
		}

		respondJSON(c, http.StatusCreated, gin.H{"attachments": created, "count": len(created)})
		return
	}

	h.respondError(c, apperrors.BadRequest("expected multipart/form-data with 'files' or 'file'"))
}

// POST /api/v1/publications/:id/gallery
func (h *PublicationHandler) UploadGallery(c *gin.Context) {
	if c.Request.ContentLength > 100<<20 {
		respondJSON(c, http.StatusRequestEntityTooLarge, gin.H{"error": "upload exceeds 100 MiB"})
		return
	}
	userID := middleware.MustGetUserID(c)
	pubID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid publication id"))
		return
	}

	if _, err := h.svc.CheckPublicationEditAccess(c.Request.Context(), pubID, userID); err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 100<<20)
	form, err := c.MultipartForm()
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid multipart form: "+err.Error()))
		return
	}
	defer func() { _ = form.RemoveAll() }()

	files := form.File["images"]
	if len(files) == 0 {
		files = form.File["files"]
	}
	if len(files) == 0 {
		files = form.File["image"]
	}
	if len(files) == 0 {
		h.respondError(c, apperrors.BadRequest("no images provided for gallery"))
		return
	}

	captions := form.Value["captions"]
	if len(captions) == 0 {
		captions = form.Value["caption"]
	}

	gallery, err := h.svc.UploadGallery(c.Request.Context(), pubID, userID, files, captions)
	if err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}

	respondJSON(c, http.StatusCreated, gin.H{"gallery": gallery, "count": len(gallery)})
}

// POST /api/v1/publications/:id/videos
func (h *PublicationHandler) AddVideoEmbed(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	userID := middleware.MustGetUserID(c)
	pubID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid publication id"))
		return
	}

	var req domain.AddVideoEmbedRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation(err.Error()))
		return
	}

	att, err := h.svc.AddVideoEmbed(c.Request.Context(), pubID, userID, req)
	if err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}

	respondJSON(c, http.StatusCreated, att)
}

// DELETE /api/v1/publications/:id/attachments/:attachment_id
func (h *PublicationHandler) DeleteAttachment(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	pubID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid publication id"))
		return
	}
	attachmentID, err := uuid.Parse(c.Param("attachment_id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid attachment id"))
		return
	}

	if err := h.svc.DeleteAttachment(c.Request.Context(), pubID, attachmentID, userID); err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}

	c.Status(http.StatusNoContent)
}

// PUT /api/v1/publications/:id/attachments/reorder
func (h *PublicationHandler) ReorderAttachments(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	userID := middleware.MustGetUserID(c)
	pubID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid publication id"))
		return
	}

	var req domain.ReorderAttachmentsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation(err.Error()))
		return
	}

	attachments, err := h.svc.ReorderAttachments(c.Request.Context(), pubID, userID, req.AttachmentIDs)
	if err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"attachments": attachments, "count": len(attachments)})
}

// GET /api/v1/publications/banners
func (h *PublicationHandler) ListBanners(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	banners, err := h.svc.ListBanners(c.Request.Context(), userID)
	if err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"banners": banners, "count": len(banners)})
}

// POST /api/v1/publications/:id/banner/dismiss
func (h *PublicationHandler) DismissBanner(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid publication id"))
		return
	}

	if err := h.svc.DismissBanner(c.Request.Context(), id, userID); err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"status": "dismissed"})
}

// PUT /api/v1/publications/:id/banner
func (h *PublicationHandler) SetBanner(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid publication id"))
		return
	}

	var req domain.SetBannerRequest
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation(err.Error()))
		return
	}

	pub, err := h.svc.SetBanner(c.Request.Context(), id, userID, req)
	if err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}

	respondJSON(c, http.StatusOK, pub)
}

// DELETE /api/v1/publications/:id/banner
func (h *PublicationHandler) RemoveBanner(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid publication id"))
		return
	}

	req := domain.SetBannerRequest{IsBanner: false}
	pub, err := h.svc.SetBanner(c.Request.Context(), id, userID, req)
	if err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}

	respondJSON(c, http.StatusOK, pub)
}

// PUT /api/v1/publications/:id/pin
func (h *PublicationHandler) PinPublication(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid publication id"))
		return
	}

	var req domain.PinPublicationRequest
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		h.respondError(c, apperrors.Validation(err.Error()))
		return
	}

	pub, err := h.svc.PinPublication(c.Request.Context(), id, userID, req)
	if err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}

	respondJSON(c, http.StatusOK, pub)
}

// DELETE /api/v1/publications/:id/pin
func (h *PublicationHandler) UnpinPublication(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid publication id"))
		return
	}

	pub, err := h.svc.UnpinPublication(c.Request.Context(), id, userID)
	if err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}

	respondJSON(c, http.StatusOK, pub)
}

// POST /api/v1/publications/:id/broadcast
func (h *PublicationHandler) BroadcastPublication(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid publication id"))
		return
	}

	var req domain.BroadcastPublicationRequest
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		h.respondError(c, apperrors.Validation(err.Error()))
		return
	}

	stats, err := h.svc.BroadcastPublication(c.Request.Context(), id, userID, req)
	if err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}

	respondJSON(c, http.StatusOK, stats)
}

// GET /api/v1/publications/:id/broadcast/stats
func (h *PublicationHandler) GetBroadcastStats(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid publication id"))
		return
	}

	stats, err := h.svc.GetBroadcastStats(c.Request.Context(), id, userID)
	if err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}

	respondJSON(c, http.StatusOK, stats)
}

// GET /api/v1/publications/:id/acknowledgements
func (h *PublicationHandler) ListAcknowledgements(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid publication id"))
		return
	}

	page := 1
	if pStr := c.Query("page"); pStr != "" {
		if val, err := strconv.Atoi(pStr); err == nil && val > 0 {
			page = val
		}
	}

	limit := 20
	if lStr := c.Query("limit"); lStr != "" {
		if val, err := strconv.Atoi(lStr); err == nil && val > 0 {
			if val > 100 {
				val = 100
			}
			limit = val
		}
	}

	acks, err := h.svc.ListAcknowledgements(c.Request.Context(), id, userID, page, limit)
	if err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}

	respondJSON(c, http.StatusOK, acks)
}

// GET /api/v1/publications/:id/analytics and /api/v1/publications/:id/read-stats
func (h *PublicationHandler) GetPublicationAnalytics(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid publication id"))
		return
	}

	analytics, err := h.svc.GetPublicationAnalytics(c.Request.Context(), id, userID)
	if err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}

	respondJSON(c, http.StatusOK, analytics)
}

// GET /api/v1/publications/:id/readers
func (h *PublicationHandler) ListPublicationReaders(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid publication id"))
		return
	}

	page := 1
	if pStr := c.Query("page"); pStr != "" {
		if val, err := strconv.Atoi(pStr); err == nil && val > 0 {
			page = val
		}
	}

	limit := 50
	if lStr := c.Query("limit"); lStr != "" {
		if val, err := strconv.Atoi(lStr); err == nil && val > 0 {
			if val > 100 {
				val = 100
			}
			limit = val
		}
	}

	var deptID *int64
	if dStr := c.Query("department_id"); dStr != "" {
		if val, err := strconv.ParseInt(dStr, 10, 64); err == nil {
			deptID = &val
		}
	}

	var minID *int64
	if mStr := c.Query("ministry_id"); mStr != "" {
		if val, err := strconv.ParseInt(mStr, 10, 64); err == nil {
			minID = &val
		}
	}

	filter := domain.PublicationReadersFilter{
		Status:       c.Query("status"),
		DepartmentID: deptID,
		MinistryID:   minID,
		Search:       c.Query("search"),
		Page:         page,
		Limit:        limit,
	}

	readers, err := h.svc.ListPublicationReaders(c.Request.Context(), id, userID, filter)
	if err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}

	respondJSON(c, http.StatusOK, readers)
}

// GET /api/v1/publications/:id/readers/export and /api/v1/publications/:id/export/readers
func (h *PublicationHandler) ExportPublicationReaders(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid publication id"))
		return
	}

	var deptID *int64
	if dStr := c.Query("department_id"); dStr != "" {
		if val, err := strconv.ParseInt(dStr, 10, 64); err == nil {
			deptID = &val
		}
	}

	var minID *int64
	if mStr := c.Query("ministry_id"); mStr != "" {
		if val, err := strconv.ParseInt(mStr, 10, 64); err == nil {
			minID = &val
		}
	}

	format := c.DefaultQuery("format", "xlsx")

	filter := domain.PublicationReadersExportFilter{
		Format:       format,
		Status:       c.Query("status"),
		DepartmentID: deptID,
		MinistryID:   minID,
		Search:       c.Query("search"),
	}

	fileBytes, fileName, contentType, err := h.svc.ExportPublicationReaders(c.Request.Context(), id, userID, filter)
	if err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}

	c.Header("Content-Type", contentType)
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", fileName))
	c.Header("Content-Length", fmt.Sprintf("%d", len(fileBytes)))
	c.Data(http.StatusOK, contentType, fileBytes)
}
