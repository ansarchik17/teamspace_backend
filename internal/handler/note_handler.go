package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
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

type NoteHandler struct {
	service *service.NoteService
	logger  *zap.Logger
}

func NewNoteHandler(service *service.NoteService, logger *zap.Logger) *NoteHandler {
	return &NoteHandler{
		service: service,
		logger:  logger.With(zap.String("handler", "notes")),
	}
}

// CreateNote godoc
// @Summary Create note
// @Description Creates a note. Accepts both application/json and multipart/form-data (for file uploads).
// @Description Use application/json when no file is attached. The content_doc field follows the rich document schema.
// @Tags notes
// @Security BearerAuth
// @Accept multipart/form-data
// @Produce json
// @Param title formData string false "Title"
// @Param content formData string false "Plain text content (legacy)"
// @Param content_doc formData string false "Rich content JSON (content_doc schema)"
// @Param type formData string false "Note type: text|checklist|rich"
// @Param folder_id formData string false "Folder UUID"
// @Param is_pinned formData bool false "Pin the note"
// @Param is_archived formData bool false "Archive the note"
// @Param checklist_items formData string false "Checklist items JSON array (legacy)"
// @Param files formData file false "File attachments (repeat for multiple)"
// @Success 201 {object} domain.Note
// @Failure 400 {object} domain.ErrorResponse
// @Router /notes [post]
func (h *NoteHandler) CreateNote(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	if strings.HasPrefix(c.ContentType(), "multipart/form-data") {
		h.createNoteMultipart(c, userID)
		return
	}

	var req domain.CreateNoteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}
	note, err := h.service.CreateNote(c.Request.Context(), userID, &req)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusCreated, note)
}

// ListNotes godoc
// @Summary List notes
// @Description Returns paginated list of notes. Sorted: pinned first, then by updated_at DESC.
// @Tags notes
// @Security BearerAuth
// @Produce json
// @Param folderId query string false "Filter by folder UUID"
// @Param isDeleted query bool false "Include deleted notes"
// @Param isArchived query bool false "Filter by archived flag"
// @Param q query string false "Full-text search query"
// @Param scope query string false "Access scope: all (default), owned, shared"
// @Param limit query int false "Page size (max 100, default 50)"
// @Param cursor query string false "Cursor for next page"
// @Success 200 {object} domain.NoteListResponse
// @Failure 400 {object} domain.ErrorResponse
// @Router /notes [get]
func (h *NoteHandler) ListNotes(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	filter := domain.NoteListFilter{
		Scope:  c.Query("scope"),
		UserID: userID,
		Query:  c.Query("q"),
		Cursor: c.Query("cursor"),
		Limit:  parseInt(c.Query("limit"), 50),
	}

	rawFolder := c.Query("folderId")
	if rawFolder == "" {
		rawFolder = c.Query("folder_id")
	}
	if rawFolder != "" {
		if rawFolder == "root" || rawFolder == "none" {
			filter.FolderID = &uuid.Nil
		} else {
			id, err := uuid.Parse(rawFolder)
			if err != nil {
				h.respondError(c, apperrors.BadRequest("invalid folder id"))
				return
			}
			filter.FolderID = &id
		}
	}
	if raw := c.Query("isDeleted"); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			h.respondError(c, apperrors.BadRequest("invalid isDeleted"))
			return
		}
		filter.IsDeleted = &value
	}
	if raw := c.Query("isArchived"); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			h.respondError(c, apperrors.BadRequest("invalid isArchived"))
			return
		}
		filter.IsArchived = &value
	}
	rawFav := c.Query("isFavorite")
	if rawFav == "" {
		rawFav = c.Query("is_favorite")
	}
	if rawFav != "" {
		value, err := strconv.ParseBool(rawFav)
		if err != nil {
			h.respondError(c, apperrors.BadRequest("invalid isFavorite"))
			return
		}
		filter.IsFavorite = &value
	}
	if tag := strings.TrimSpace(c.Query("tag")); tag != "" {
		filter.Tag = tag
	}

	resp, err := h.service.ListNotes(c.Request.Context(), filter)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, resp)
}

// SearchNotes godoc
// @Summary Search notes
// @Description Full-text and tag search for notes
// @Tags notes
// @Security BearerAuth
// @Produce json
// @Param q query string true "Search query"
// @Param folderId query string false "Filter by folder UUID"
// @Param tag query string false "Filter by tag"
// @Param isFavorite query bool false "Filter by favorite flag"
// @Param limit query int false "Page size (max 100, default 50)"
// @Param cursor query string false "Cursor for next page"
// @Success 200 {object} domain.NoteListResponse
// @Failure 400 {object} domain.ErrorResponse
// @Router /notes/search [get]
func (h *NoteHandler) SearchNotes(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	query := strings.TrimSpace(c.Query("q"))
	if query == "" {
		query = strings.TrimSpace(c.Query("query"))
	}
	if query == "" {
		h.respondError(c, apperrors.Validation("search query is required"))
		return
	}

	filter := domain.NoteListFilter{
		Scope:  c.Query("scope"),
		UserID: userID,
		Query:  query,
		Cursor: c.Query("cursor"),
		Limit:  parseInt(c.Query("limit"), 50),
		Tag:    strings.TrimSpace(c.Query("tag")),
	}

	rawFolder := c.Query("folderId")
	if rawFolder == "" {
		rawFolder = c.Query("folder_id")
	}
	if rawFolder != "" {
		if rawFolder == "root" || rawFolder == "none" {
			filter.FolderID = &uuid.Nil
		} else {
			id, err := uuid.Parse(rawFolder)
			if err != nil {
				h.respondError(c, apperrors.BadRequest("invalid folder id"))
				return
			}
			filter.FolderID = &id
		}
	}
	if raw := c.Query("isDeleted"); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			h.respondError(c, apperrors.BadRequest("invalid isDeleted"))
			return
		}
		filter.IsDeleted = &value
	}
	if raw := c.Query("isArchived"); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			h.respondError(c, apperrors.BadRequest("invalid isArchived"))
			return
		}
		filter.IsArchived = &value
	}
	rawFav := c.Query("isFavorite")
	if rawFav == "" {
		rawFav = c.Query("is_favorite")
	}
	if rawFav != "" {
		value, err := strconv.ParseBool(rawFav)
		if err != nil {
			h.respondError(c, apperrors.BadRequest("invalid isFavorite"))
			return
		}
		filter.IsFavorite = &value
	}

	resp, err := h.service.SearchNotes(c.Request.Context(), filter)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, resp)
}

// GetTags godoc
// @Summary List all user tags
// @Description Returns distinct tags with usage counts for the authenticated user
// @Tags notes
// @Security BearerAuth
// @Produce json
// @Success 200 {array} domain.NoteTagSummary
// @Router /notes/tags [get]
func (h *NoteHandler) GetTags(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	tags, err := h.service.GetUserTags(c.Request.Context(), userID)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, tags)
}

// GetNote godoc
// @Summary Get note by ID
// @Description Returns a single note including content_doc and attachments.
// @Tags notes
// @Security BearerAuth
// @Produce json
// @Param id path string true "Note UUID"
// @Success 200 {object} domain.Note
// @Failure 404 {object} domain.ErrorResponse
// @Router /notes/{id} [get]
func (h *NoteHandler) GetNote(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	noteID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid note id"))
		return
	}
	note, err := h.service.GetNote(c.Request.Context(), userID, noteID)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, note)
}

// UpdateNote godoc
// @Summary Update note
// @Description Partially updates a note. Requires version for optimistic concurrency.
// @Description If version is outdated, returns 409 NOTE_VERSION_CONFLICT with server-side note.
// @Tags notes
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "Note UUID"
// @Param request body domain.UpdateNoteRequest true "Update request"
// @Success 200 {object} domain.Note
// @Failure 400 {object} domain.ErrorResponse
// @Failure 409 {object} domain.ErrorResponse
// @Router /notes/{id} [patch]
func (h *NoteHandler) UpdateNote(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	noteID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid note id"))
		return
	}
	var req domain.UpdateNoteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}
	note, err := h.service.UpdateNote(c.Request.Context(), userID, noteID, &req)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, note)
}

// PinNote godoc
// @Summary Pin note
// @Tags notes
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "Note UUID"
// @Param request body domain.VersionedNoteActionRequest true "Version"
// @Success 200 {object} domain.Note
// @Router /notes/{id}/pin [post]
func (h *NoteHandler) PinNote(c *gin.Context) {
	h.setPinned(c, true)
}

// UnpinNote godoc
// @Summary Unpin note
// @Tags notes
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "Note UUID"
// @Param request body domain.VersionedNoteActionRequest true "Version"
// @Success 200 {object} domain.Note
// @Router /notes/{id}/unpin [post]
func (h *NoteHandler) UnpinNote(c *gin.Context) {
	h.setPinned(c, false)
}

// FavoriteNote godoc
// @Summary Mark note as favorite
// @Tags notes
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "Note UUID"
// @Param request body domain.VersionedNoteActionRequest true "Version"
// @Success 200 {object} domain.Note
// @Router /notes/{id}/favorite [post]
func (h *NoteHandler) FavoriteNote(c *gin.Context) {
	h.setFavorite(c, true)
}

// UnfavoriteNote godoc
// @Summary Unmark note as favorite
// @Tags notes
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "Note UUID"
// @Param request body domain.VersionedNoteActionRequest true "Version"
// @Success 200 {object} domain.Note
// @Router /notes/{id}/unfavorite [post]
func (h *NoteHandler) UnfavoriteNote(c *gin.Context) {
	h.setFavorite(c, false)
}

// SetFavoriteNote godoc
// @Summary Toggle or set note favorite status
// @Tags notes
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "Note UUID"
// @Param request body domain.FavoriteNoteRequest true "Favorite request"
// @Success 200 {object} domain.Note
// @Router /notes/{id}/favorite [patch]
func (h *NoteHandler) SetFavoriteNote(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	noteID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid note id"))
		return
	}
	var req domain.FavoriteNoteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}
	note, err := h.service.SetFavorite(c.Request.Context(), userID, noteID, req.Version, req.IsFavorite)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, note)
}

// MoveNote godoc
// @Summary Move note to a folder
// @Tags notes
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "Note UUID"
// @Param request body domain.MoveNoteRequest true "Move request"
// @Success 200 {object} domain.Note
// @Router /notes/{id}/move [post]
func (h *NoteHandler) MoveNote(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	noteID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid note id"))
		return
	}
	var req domain.MoveNoteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}
	note, err := h.service.MoveNote(c.Request.Context(), userID, noteID, &req)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, note)
}

// DeleteNote godoc
// @Summary Soft-delete note (moves to trash)
// @Tags notes
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "Note UUID"
// @Param request body domain.VersionedNoteActionRequest true "Version"
// @Success 200 {object} domain.Note
// @Router /notes/{id} [delete]
func (h *NoteHandler) DeleteNote(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	noteID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid note id"))
		return
	}
	var req domain.VersionedNoteActionRequest
	{
		if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
			h.respondError(c, apperrors.Validation("invalid request body"))
			return
		}
	}
	note, err := h.service.SoftDeleteNote(c.Request.Context(), userID, noteID, req.Version)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, note)
}

// DeleteTableColumn godoc
// @Summary Delete a table column from a note table block
// @Description Deletes one column from every row in the target table block. Requires version for optimistic concurrency.
// @Tags notes
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "Note UUID"
// @Param blockId path string true "Table block ID"
// @Param colIndex path int true "Zero-based column index"
// @Param request body domain.VersionedNoteActionRequest true "Version"
// @Success 200 {object} domain.Note
// @Failure 400 {object} domain.ErrorResponse
// @Failure 404 {object} domain.ErrorResponse
// @Failure 409 {object} domain.ErrorResponse
// @Router /notes/{id}/tables/{blockId}/columns/{colIndex} [delete]
func (h *NoteHandler) DeleteTableColumn(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	noteID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid note id"))
		return
	}
	colIndex, err := strconv.Atoi(c.Param("colIndex"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid colIndex"))
		return
	}
	var req domain.VersionedNoteActionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}
	note, err := h.service.DeleteTableColumn(c.Request.Context(), userID, noteID, c.Param("blockId"), colIndex, req.Version)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, note)
}

// ClearTableCell godoc
// @Summary Clear a table cell in a note table block
// @Description Clears the text of a single cell without changing table dimensions. Requires version for optimistic concurrency.
// @Tags notes
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "Note UUID"
// @Param blockId path string true "Table block ID"
// @Param rowIndex path int true "Zero-based row index"
// @Param colIndex path int true "Zero-based column index"
// @Param request body domain.VersionedNoteActionRequest true "Version"
// @Success 200 {object} domain.Note
// @Failure 400 {object} domain.ErrorResponse
// @Failure 404 {object} domain.ErrorResponse
// @Failure 409 {object} domain.ErrorResponse
// @Router /notes/{id}/tables/{blockId}/cells/{rowIndex}/{colIndex} [delete]
func (h *NoteHandler) ClearTableCell(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	noteID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid note id"))
		return
	}
	rowIndex, err := strconv.Atoi(c.Param("rowIndex"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid rowIndex"))
		return
	}
	colIndex, err := strconv.Atoi(c.Param("colIndex"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid colIndex"))
		return
	}
	var req domain.VersionedNoteActionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}
	note, err := h.service.ClearTableCell(c.Request.Context(), userID, noteID, c.Param("blockId"), rowIndex, colIndex, req.Version)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, note)
}

// RestoreNote godoc
// @Summary Restore note from trash
// @Tags notes
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "Note UUID"
// @Param request body domain.VersionedNoteActionRequest true "Version"
// @Success 200 {object} domain.Note
// @Router /notes/{id}/restore [post]
func (h *NoteHandler) RestoreNote(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	noteID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid note id"))
		return
	}
	var req domain.VersionedNoteActionRequest
	{
		if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
			h.respondError(c, apperrors.Validation("invalid request body"))
			return
		}
	}
	note, err := h.service.RestoreNote(c.Request.Context(), userID, noteID, req.Version)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, note)
}

// HardDeleteNote godoc
// @Summary Permanently delete note
// @Description Removes the note and all its attachments from storage. Cannot be undone.
// @Tags notes
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "Note UUID"
// @Param request body domain.VersionedNoteActionRequest false "Version"
// @Success 200 {object} map[string]string
// @Router /notes/{id}/hard [delete]
func (h *NoteHandler) HardDeleteNote(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	noteID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid note id"))
		return
	}
	var req domain.VersionedNoteActionRequest
	{
		if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
			h.respondError(c, apperrors.Validation("invalid request body"))
			return
		}
	}
	if err := h.service.HardDeleteNote(c.Request.Context(), userID, noteID, req.Version); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, gin.H{"status": "deleted"})
}

// AutosaveNote godoc
// @Summary Autosave note (draft creation or update)
// @Description Creates a new note if ID is omitted/nil, or updates existing note with version check.
// @Tags notes
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string false "Optional note UUID in path"
// @Param request body domain.AutosaveNoteRequest true "Autosave payload"
// @Success 200 {object} domain.Note "Updated note"
// @Success 201 {object} domain.Note "Created note"
// @Failure 400 {object} domain.ErrorResponse
// @Failure 404 {object} domain.ErrorResponse
// @Failure 409 {object} domain.ErrorResponse
// @Router /notes/autosave [post]
func (h *NoteHandler) AutosaveNote(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4<<20)
	var req domain.AutosaveNoteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			respondJSON(c, http.StatusRequestEntityTooLarge, gin.H{"error": "autosave payload exceeds 4 MiB"})
			return
		}
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}
	if rawID := c.Param("id"); rawID != "" {
		noteID, err := uuid.Parse(rawID)
		if err != nil {
			h.respondError(c, apperrors.BadRequest("invalid note id"))
			return
		}
		req.ID = &noteID
	}
	isCreate := req.ID == nil || *req.ID == uuid.Nil
	note, err := h.service.AutosaveNote(c.Request.Context(), userID, &req)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	if isCreate {
		respondJSON(c, http.StatusCreated, note)
	} else {
		respondJSON(c, http.StatusOK, note)
	}
}

// ListTrash godoc
// @Summary List notes in trash
// @Description Returns paginated list of notes in trash, ordered by deleted_at DESC.
// @Tags notes
// @Security BearerAuth
// @Produce json
// @Param limit query int false "Page size (max 100, default 50)"
// @Param cursor query string false "Cursor for next page"
// @Param q query string false "Search query inside trash"
// @Success 200 {object} domain.NoteListResponse
// @Failure 400 {object} domain.ErrorResponse
// @Router /notes/trash [get]
func (h *NoteHandler) ListTrash(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	filter := domain.NoteListFilter{
		UserID: userID,
		Query:  c.Query("q"),
		Cursor: c.Query("cursor"),
		Limit:  parseInt(c.Query("limit"), 50),
	}
	resp, err := h.service.ListTrash(c.Request.Context(), filter)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, resp)
}

// GetTrashCount godoc
// @Summary Get count of active notes in trash
// @Tags notes
// @Security BearerAuth
// @Produce json
// @Success 200 {object} domain.TrashSummaryResponse
// @Router /notes/trash/count [get]
func (h *NoteHandler) GetTrashCount(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	resp, err := h.service.GetTrashCount(c.Request.Context(), userID)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, resp)
}

// EmptyTrash godoc
// @Summary Empty trash (permanently delete all trashed notes)
// @Tags notes
// @Security BearerAuth
// @Produce json
// @Success 200 {object} domain.EmptyTrashResponse
// @Router /notes/trash [delete]
func (h *NoteHandler) EmptyTrash(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	resp, err := h.service.EmptyTrash(c.Request.Context(), userID)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, resp)
}

// CreateFolder godoc
// @Summary Create notes folder
// @Tags notes
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body domain.CreateNoteFolderRequest true "Request"
// @Success 201 {object} domain.NoteFolder
// @Router /notes/folders [post]
func (h *NoteHandler) CreateFolder(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	var req domain.CreateNoteFolderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}
	folder, err := h.service.CreateFolder(c.Request.Context(), userID, &req)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusCreated, folder)
}

// ListFolders godoc
// @Summary List notes folders
// @Tags notes
// @Security BearerAuth
// @Produce json
// @Success 200 {array} domain.NoteFolder
// @Router /notes/folders [get]
func (h *NoteHandler) ListFolders(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	folders, err := h.service.ListFolders(c.Request.Context(), userID)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, folders)
}

// UpdateFolder godoc
// @Summary Update notes folder
// @Tags notes
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "Folder UUID"
// @Param request body domain.UpdateNoteFolderRequest true "Request"
// @Success 200 {object} domain.NoteFolder
// @Router /notes/folders/{id} [patch]
func (h *NoteHandler) UpdateFolder(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	folderID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid folder id"))
		return
	}
	var req domain.UpdateNoteFolderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}
	folder, err := h.service.UpdateFolder(c.Request.Context(), userID, folderID, &req)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, folder)
}

// DeleteFolder godoc
// @Summary Delete notes folder
// @Description Deletes folder; notes inside are un-foldered (folder_id set to NULL).
// @Tags notes
// @Security BearerAuth
// @Produce json
// @Param id path string true "Folder UUID"
// @Success 200 {object} map[string]string
// @Router /notes/folders/{id} [delete]
func (h *NoteHandler) DeleteFolder(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	folderID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid folder id"))
		return
	}
	if err := h.service.DeleteFolder(c.Request.Context(), userID, folderID); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, gin.H{"status": "deleted"})
}

// SyncNotes godoc
// @Summary Get changed notes since a timestamp
// @Description Returns all notes updated after the given ISO timestamp. Use cursor for pagination.
// @Description Includes deleted notes (is_deleted=true) for device consistency.
// @Tags notes
// @Security BearerAuth
// @Produce json
// @Param since query string true "ISO 8601 timestamp (e.g. 2026-01-01T00:00:00Z)"
// @Param cursor query string false "Pagination cursor"
// @Param limit query int false "Page size (default 100)"
// @Success 200 {object} domain.NoteSyncResponse
// @Router /notes/sync [get]
func (h *NoteHandler) SyncNotes(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	since, err := time.Parse(time.RFC3339, c.Query("since"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid since parameter"))
		return
	}
	resp, err := h.service.ListSync(c.Request.Context(), domain.NoteSyncFilter{
		UserID: userID,
		Since:  since,
		Cursor: c.Query("cursor"),
		Limit:  parseInt(c.Query("limit"), 100),
	})
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, resp)
}

// SyncBatch godoc
// @Summary Apply offline sync batch
// @Description Applies a batch of upserts, deletes, restores, and hard-deletes from an offline client.
// @Description Returns 409 on version conflict.
// @Tags notes
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body domain.SyncBatchRequest true "Sync batch"
// @Success 200 {object} domain.SyncBatchResponse
// @Failure 409 {object} domain.ErrorResponse
// @Router /notes/sync/batch [post]
func (h *NoteHandler) SyncBatch(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	var req domain.SyncBatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}
	resp, err := h.service.ApplySyncBatch(c.Request.Context(), userID, &req)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, resp)
}

// -------- Legacy direct-upload attachments --------

// UploadAttachment godoc
// @Summary Upload a file attachment to note (legacy direct upload)
// @Tags notes
// @Security BearerAuth
// @Accept multipart/form-data
// @Produce json
// @Param id path string true "Note UUID"
// @Param file formData file true "File to upload"
// @Param type formData string false "Attachment type hint"
// @Success 201 {object} domain.NoteAttachment
// @Router /notes/{id}/attachments [post]
func (h *NoteHandler) UploadAttachment(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	noteID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid note id"))
		return
	}

	file, header, err := c.Request.FormFile("file")
	if err != nil {
		h.respondError(c, apperrors.BadRequest("file is required"))
		return
	}
	defer file.Close()

	item, err := h.service.UploadAttachment(c.Request.Context(), userID, noteID, c.PostForm("type"), header.Filename, header.Size, file)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusCreated, item)
}

// ListAttachments godoc
// @Summary List note attachments (legacy)
// @Tags notes
// @Security BearerAuth
// @Produce json
// @Param id path string true "Note UUID"
// @Success 200 {array} domain.NoteAttachment
// @Router /notes/{id}/attachments [get]
func (h *NoteHandler) ListAttachments(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	noteID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid note id"))
		return
	}
	items, err := h.service.ListAttachments(c.Request.Context(), userID, noteID)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, items)
}

// DeleteAttachment godoc
// @Summary Delete note attachment (legacy)
// @Tags notes
// @Security BearerAuth
// @Produce json
// @Param id path string true "Note UUID"
// @Param fileId path string true "File UUID"
// @Success 200 {object} map[string]string
// @Router /notes/{id}/attachments/{fileId} [delete]
func (h *NoteHandler) DeleteAttachment(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	noteID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid note id"))
		return
	}
	fileID, err := uuid.Parse(c.Param("fileId"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid file id"))
		return
	}
	if err := h.service.DeleteAttachment(c.Request.Context(), userID, noteID, fileID); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, gin.H{"status": "deleted"})
}

// -------- Presigned-URL photo attachments (TZ 5.2) --------

// PresignPhotoUpload godoc
// @Summary Get presigned URL to upload a photo attachment
// @Description Step 1 of the photo upload flow. Returns a presigned PUT URL and attachmentId.
// @Description The client uploads the file directly to storage using the returned uploadUrl.
// @Description After successful upload, call POST /notes/{id}/attachments/{attachmentId}/complete.
// @Tags notes
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "Note UUID"
// @Param request body domain.PresignRequest true "MIME type and size"
// @Success 201 {object} domain.PresignResponse
// @Failure 400 {object} domain.ErrorResponse
// @Router /notes/{id}/attachments/presign [post]
func (h *NoteHandler) PresignPhotoUpload(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	noteID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid note id"))
		return
	}
	var req domain.PresignRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}
	if domain.IsAllowedAudioMimeType(req.MimeType) {
		audioReq := domain.PresignAudioRequest{
			MimeType:  req.MimeType,
			SizeBytes: req.SizeBytes,
		}
		resp, err := h.service.PresignAudioUpload(c.Request.Context(), userID, noteID, &audioReq)
		if err != nil {
			h.respondError(c, apperrors.ToAppError(err))
			return
		}
		respondJSON(c, http.StatusCreated, resp)
		return
	}
	resp, err := h.service.PresignPhotoUpload(c.Request.Context(), userID, noteID, &req)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusCreated, resp)
}

// CompletePhotoUpload godoc
// @Summary Confirm that a photo was successfully uploaded
// @Description Step 2 of the photo upload flow. Call after the client has PUT the file to the presigned URL.
// @Description Marks attachment status as 'uploaded' and returns the full updated note.
// @Tags notes
// @Security BearerAuth
// @Produce json
// @Param id path string true "Note UUID"
// @Param attachmentId path string true "Attachment UUID returned by presign"
// @Success 200 {object} domain.Note
// @Failure 404 {object} domain.ErrorResponse
// @Router /notes/{id}/attachments/{attachmentId}/complete [post]
func (h *NoteHandler) CompletePhotoUpload(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	noteID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid note id"))
		return
	}
	attachmentID, err := uuid.Parse(c.Param("attachmentId"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid attachment id"))
		return
	}
	result, err := h.service.CompletePhotoUpload(c.Request.Context(), userID, noteID, attachmentID)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, result)
}

// UploadPhoto godoc
// @Summary Upload a photo attachment directly to the server
// @Description Direct multipart upload alternative to the presigned URL flow.
// @Description Use this if the presigned URL flow (PUT) fails due to proxy/CORS/Nginx (405 Not Allowed) issues.
// @Tags notes
// @Security BearerAuth
// @Accept multipart/form-data
// @Produce json
// @Param id path string true "Note UUID"
// @Param file formData file true "Photo to upload"
// @Success 201 {object} domain.Note
// @Router /notes/{id}/attachments/upload [post]
func (h *NoteHandler) UploadPhoto(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	noteID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid note id"))
		return
	}

	file, header, err := c.Request.FormFile("file")
	if err != nil {
		h.respondError(c, apperrors.BadRequest("file is required"))
		return
	}
	defer file.Close()

	// We use the existing UploadAttachment but then we need the attachment ID.
	// Let's call a new service method that returns both.
	note, attachmentID, err := h.service.UploadPhotoWithID(c.Request.Context(), userID, noteID, header.Filename, header.Size, file)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusCreated, gin.H{
		"note":          note,
		"attachment_id": attachmentID,
	})
}

// DeletePhotoAttachment godoc
// @Summary Delete a photo attachment
// @Description Soft-deletes the photo attachment and removes the object from storage.
// @Description Remove any image block referencing this attachmentId from content_doc before calling this.
// @Tags notes
// @Security BearerAuth
// @Produce json
// @Param id path string true "Note UUID"
// @Param attachmentId path string true "Attachment UUID"
// @Success 200 {object} map[string]string
// @Failure 404 {object} domain.ErrorResponse
// @Router /notes/{id}/attachments/{attachmentId} [delete]
func (h *NoteHandler) DeletePhotoAttachment(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	noteID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid note id"))
		return
	}
	attachmentID, err := uuid.Parse(c.Param("attachmentId"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid attachment id"))
		return
	}
	if err := h.service.DeletePhotoAttachment(c.Request.Context(), userID, noteID, attachmentID); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, gin.H{"status": "deleted"})
}

// ---------------------------------------------------------------------------
// Audio notes & attachments (NTS-004)
// ---------------------------------------------------------------------------

// CreateAudioNote godoc
// @Summary Create a new audio note with attached recording or audio file
// @Description Direct multipart creation of an audio note. Supports web voice recorder (webm, ogg, wav) and uploaded audio files (mp3, m4a, wav).
// @Tags notes
// @Security BearerAuth
// @Accept multipart/form-data
// @Produce json
// @Param file formData file true "Audio file or voice recording"
// @Param title formData string false "Note title"
// @Param content formData string false "Note description or transcript"
// @Param folder_id formData string false "Folder UUID"
// @Param color formData string false "Hex color (#RRGGBB)"
// @Param tags formData string false "Tags (comma-separated or JSON array)"
// @Param duration formData number false "Duration in seconds"
// @Param waveform formData string false "Waveform amplitude array as JSON"
// @Param is_recording formData boolean false "True if recorded via built-in microphone"
// @Param is_pinned formData boolean false "Pin note"
// @Param is_favorite formData boolean false "Mark favorite"
// @Success 201 {object} domain.Note
// @Failure 400 {object} domain.ErrorResponse
// @Router /notes/audio [post]
func (h *NoteHandler) CreateAudioNote(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, domain.NoteMaxAudioSizeBytes+1<<20)

	file, header, err := c.Request.FormFile("file")
	if err != nil {
		h.respondError(c, apperrors.BadRequest("file is required"))
		return
	}
	defer file.Close()

	var duration *float64
	if rawDur := c.PostForm("duration"); rawDur != "" {
		if d, err := strconv.ParseFloat(rawDur, 64); err == nil {
			duration = &d
		}
	}

	var waveform []float64
	if rawWf := c.PostForm("waveform"); rawWf != "" {
		_ = json.Unmarshal([]byte(rawWf), &waveform)
	}

	var folderID *uuid.UUID
	if rawFolder := c.PostForm("folder_id"); rawFolder != "" {
		if fid, err := uuid.Parse(rawFolder); err == nil {
			folderID = &fid
		}
	}

	var tags []string
	if rawTags := c.PostForm("tags"); rawTags != "" {
		if err := json.Unmarshal([]byte(rawTags), &tags); err != nil {
			tags = strings.Split(rawTags, ",")
		}
	} else if formTags := c.PostFormArray("tags"); len(formTags) > 0 {
		tags = formTags
	}

	req := &domain.CreateAudioNoteRequest{
		Title:       c.PostForm("title"),
		Content:     c.PostForm("content"),
		FolderID:    folderID,
		Color:       c.PostForm("color"),
		Tags:        tags,
		Duration:    duration,
		Waveform:    waveform,
		IsRecording: parseBoolForm(c.PostForm("is_recording")),
		IsPinned:    parseBoolForm(c.PostForm("is_pinned")),
		IsArchived:  parseBoolForm(c.PostForm("is_archived")),
		IsFavorite:  parseBoolForm(c.PostForm("is_favorite")),
	}

	note, err := h.service.CreateAudioNote(c.Request.Context(), userID, req, header.Filename, header.Size, file)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusCreated, note)
}

// UploadAudio godoc
// @Summary Upload audio file or voice recording to existing note
// @Description Direct multipart upload of an audio attachment to an existing note.
// @Tags notes
// @Security BearerAuth
// @Accept multipart/form-data
// @Produce json
// @Param id path string true "Note UUID"
// @Param file formData file true "Audio file or voice recording"
// @Param duration formData number false "Duration in seconds"
// @Param waveform formData string false "Waveform amplitude array as JSON"
// @Param is_recording formData boolean false "True if recorded via built-in microphone"
// @Success 201 {object} map[string]interface{}
// @Failure 400 {object} domain.ErrorResponse
// @Router /notes/{id}/audio [post]
func (h *NoteHandler) UploadAudio(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, domain.NoteMaxAudioSizeBytes+1<<20)
	noteID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid note id"))
		return
	}

	file, header, err := c.Request.FormFile("file")
	if err != nil {
		h.respondError(c, apperrors.BadRequest("file is required"))
		return
	}
	defer file.Close()

	var duration *float64
	if rawDur := c.PostForm("duration"); rawDur != "" {
		if d, err := strconv.ParseFloat(rawDur, 64); err == nil {
			duration = &d
		}
	}

	var waveform []float64
	if rawWf := c.PostForm("waveform"); rawWf != "" {
		_ = json.Unmarshal([]byte(rawWf), &waveform)
	}

	isRecording := parseBoolForm(c.PostForm("is_recording"))

	note, audioAtt, err := h.service.UploadAudio(c.Request.Context(), userID, noteID, header.Filename, header.Size, file, duration, waveform, isRecording)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusCreated, gin.H{
		"note":  note,
		"audio": audioAtt,
	})
}

// PresignAudioUpload godoc
// @Summary Get presigned URL to upload an audio attachment
// @Description Step 1 of the presigned audio upload flow. Returns a presigned PUT URL and attachmentId.
// @Tags notes
// @Security BearerAuth
// @Produce json
// @Param id path string true "Note UUID"
// @Param request body domain.PresignAudioRequest true "Audio metadata"
// @Success 201 {object} domain.PresignResponse
// @Failure 400 {object} domain.ErrorResponse
// @Router /notes/{id}/audio/presign [post]
func (h *NoteHandler) PresignAudioUpload(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	noteID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid note id"))
		return
	}
	var req domain.PresignAudioRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}
	resp, err := h.service.PresignAudioUpload(c.Request.Context(), userID, noteID, &req)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusCreated, resp)
}

// CompleteAudioUpload godoc
// @Summary Confirm that an audio file was uploaded to the presigned URL
// @Description Step 2 of the presigned audio upload flow. Marks attachment status as 'uploaded' and returns the full updated note.
// @Tags notes
// @Security BearerAuth
// @Produce json
// @Param id path string true "Note UUID"
// @Param attachmentId path string true "Attachment UUID"
// @Param request body domain.CompleteAudioRequest false "Optional duration and waveform"
// @Success 200 {object} domain.Note
// @Failure 400 {object} domain.ErrorResponse
// @Router /notes/{id}/audio/{attachmentId}/complete [post]
func (h *NoteHandler) CompleteAudioUpload(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	noteID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid note id"))
		return
	}
	attachmentID, err := uuid.Parse(c.Param("attachmentId"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid attachment id"))
		return
	}

	var req domain.CompleteAudioRequest
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			h.respondError(c, apperrors.Validation("invalid request body"))
			return
		}
	}

	result, err := h.service.CompleteAudioUpload(c.Request.Context(), userID, noteID, attachmentID, &req)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, result)
}

// ListAudioAttachments godoc
// @Summary List audio attachments for a note
// @Tags notes
// @Security BearerAuth
// @Produce json
// @Param id path string true "Note UUID"
// @Success 200 {array} domain.NoteAudioAttachment
// @Router /notes/{id}/audio [get]
func (h *NoteHandler) ListAudioAttachments(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	noteID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid note id"))
		return
	}
	items, err := h.service.ListAudioAttachments(c.Request.Context(), userID, noteID)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, items)
}

// GetAudioAttachment godoc
// @Summary Get audio attachment details
// @Tags notes
// @Security BearerAuth
// @Produce json
// @Param id path string true "Note UUID"
// @Param attachmentId path string true "Attachment UUID"
// @Success 200 {object} domain.NoteAudioAttachment
// @Router /notes/{id}/audio/{attachmentId} [get]
func (h *NoteHandler) GetAudioAttachment(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	noteID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid note id"))
		return
	}
	attachmentID, err := uuid.Parse(c.Param("attachmentId"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid attachment id"))
		return
	}
	item, err := h.service.GetAudioAttachment(c.Request.Context(), userID, noteID, attachmentID)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, item)
}

// StreamAudioAttachment godoc
// @Summary Stream audio attachment with range request support
// @Description Streams audio content directly from storage with HTTP 206 Partial Content support for player scrubbing.
// @Tags notes
// @Security BearerAuth
// @Param id path string true "Note UUID"
// @Param attachmentId path string true "Attachment UUID"
// @Produce audio/mpeg
// @Success 200 {file} file
// @Success 206 {file} file
// @Router /notes/{id}/audio/{attachmentId}/stream [get]
func (h *NoteHandler) StreamAudioAttachment(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	noteID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid note id"))
		return
	}
	attachmentID, err := uuid.Parse(c.Param("attachmentId"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid attachment id"))
		return
	}

	obj, contentType, size, fileName, modTime, err := h.service.OpenAudioObject(c.Request.Context(), userID, noteID, attachmentID)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	defer obj.Close()

	c.Header("Content-Type", contentType)
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Length", strconv.FormatInt(size, 10))
	c.Header("Accept-Ranges", "bytes")
	c.Header("Content-Disposition", fmt.Sprintf(`inline; filename="%s"`, fileName))

	http.ServeContent(c.Writer, c.Request, fileName, modTime, obj)
}

// DeleteAudioAttachment godoc
// @Summary Delete an audio attachment
// @Description Soft-deletes audio attachment and removes it from storage.
// @Tags notes
// @Security BearerAuth
// @Produce json
// @Param id path string true "Note UUID"
// @Param attachmentId path string true "Attachment UUID"
// @Success 200 {object} map[string]string
// @Router /notes/{id}/audio/{attachmentId} [delete]
func (h *NoteHandler) DeleteAudioAttachment(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	noteID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid note id"))
		return
	}
	attachmentID, err := uuid.Parse(c.Param("attachmentId"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid attachment id"))
		return
	}
	if err := h.service.DeleteAudioAttachment(c.Request.Context(), userID, noteID, attachmentID); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, gin.H{"status": "deleted"})
}

// -------- internal helpers --------

func (h *NoteHandler) setPinned(c *gin.Context, pinned bool) {
	userID := middleware.MustGetUserID(c)
	noteID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid note id"))
		return
	}
	var req domain.VersionedNoteActionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}
	note, err := h.service.SetPinned(c.Request.Context(), userID, noteID, req.Version, pinned)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, note)
}

func (h *NoteHandler) setFavorite(c *gin.Context, favorite bool) {
	userID := middleware.MustGetUserID(c)
	noteID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid note id"))
		return
	}
	var req domain.VersionedNoteActionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}
	note, err := h.service.SetFavorite(c.Request.Context(), userID, noteID, req.Version, favorite)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, note)
}

func parseInt(raw string, fallback int) int {
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

type multipartCloser struct {
	multipart.File
}

func (h *NoteHandler) createNoteMultipart(c *gin.Context, userID int64) {
	req, appErr := parseCreateNoteMultipartRequest(c)
	if appErr != nil {
		h.respondError(c, appErr)
		return
	}

	form, err := c.MultipartForm()
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid multipart form"))
		return
	}

	files := form.File["files"]
	if len(files) == 0 {
		note, err := h.service.CreateNote(c.Request.Context(), userID, req)
		if err != nil {
			h.respondError(c, apperrors.ToAppError(err))
			return
		}
		respondJSON(c, http.StatusCreated, note)
		return
	}

	inputs := make([]service.CreateNoteAttachmentInput, 0, len(files))
	opened := make([]multipartCloser, 0, len(files))
	for _, fileHeader := range files {
		file, openErr := fileHeader.Open()
		if openErr != nil {
			for _, closer := range opened {
				_ = closer.Close()
			}
			h.respondError(c, apperrors.BadRequest("failed to read uploaded file"))
			return
		}
		opened = append(opened, multipartCloser{file})
		inputs = append(inputs, service.CreateNoteAttachmentInput{
			FileType: detectAttachmentType(fileHeader.Header.Get("Content-Type"), fileHeader.Filename),
			Filename: fileHeader.Filename,
			Size:     fileHeader.Size,
			Reader:   file,
		})
	}
	defer func() {
		for _, closer := range opened {
			_ = closer.Close()
		}
	}()

	note, err := h.service.CreateNoteWithAttachments(c.Request.Context(), userID, req, inputs)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusCreated, note)
}

func parseCreateNoteMultipartRequest(c *gin.Context) (*domain.CreateNoteRequest, *apperrors.AppError) {
	req := &domain.CreateNoteRequest{
		Color:      c.PostForm("color"),
		Title:      c.PostForm("title"),
		Content:    c.PostForm("content"),
		Type:       c.PostForm("type"),
		IsPinned:   parseBoolForm(c.PostForm("is_pinned")),
		IsArchived: parseBoolForm(c.PostForm("is_archived")),
		IsFavorite: parseBoolForm(c.PostForm("is_favorite")),
	}

	if raw := c.PostForm("tags"); raw != "" {
		var tags []string
		if err := json.Unmarshal([]byte(raw), &tags); err == nil {
			req.Tags = tags
		} else {
			parts := strings.Split(raw, ",")
			req.Tags = parts
		}
	} else if formTags := c.PostFormArray("tags"); len(formTags) > 0 {
		req.Tags = formTags
	}

	if raw := c.PostForm("folder_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			return nil, apperrors.BadRequest("invalid folder_id")
		}
		req.FolderID = &id
	}

	if raw := c.PostForm("checklist_items"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &req.ChecklistItems); err != nil {
			return nil, apperrors.BadRequest("invalid checklist_items json")
		}
	}

	if raw := c.PostForm("content_doc"); raw != "" {
		var doc domain.ContentDoc
		if err := json.Unmarshal([]byte(raw), &doc); err != nil {
			return nil, apperrors.BadRequest("invalid content_doc json")
		}
		req.ContentDoc = &doc
	}

	return req, nil
}

func parseBoolForm(raw string) bool {
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false
	}
	return value
}

func detectAttachmentType(contentType string, filename string) string {
	switch {
	case len(contentType) >= 6 && contentType[:6] == "image/":
		return "image"
	case len(contentType) >= 6 && contentType[:6] == "video/":
		return "video"
	case len(contentType) >= 6 && contentType[:6] == "audio/":
		return "audio"
	case len(contentType) >= 12 && contentType[:12] == "application/":
		return "document"
	default:
		switch {
		case hasSuffixAny(filename, ".jpg", ".jpeg", ".png", ".gif", ".webp"):
			return "image"
		case hasSuffixAny(filename, ".mp4", ".mov", ".avi", ".mkv"):
			return "video"
		case hasSuffixAny(filename, ".mp3", ".m4a", ".wav", ".webm", ".ogg", ".aac", ".flac"):
			return "audio"
		case hasSuffixAny(filename, ".pdf", ".doc", ".docx", ".xls", ".xlsx", ".txt", ".ppt", ".pptx"):
			return "document"
		default:
			return "file"
		}
	}
}

func hasSuffixAny(value string, suffixes ...string) bool {
	value = strings.ToLower(value)
	for _, suffix := range suffixes {
		if strings.HasSuffix(value, suffix) {
			return true
		}
	}
	return false
}

func (h *NoteHandler) respondError(c *gin.Context, err *apperrors.AppError) {
	respondAppError(c, err)
}
