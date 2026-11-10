package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/middleware"
)

type SaveMeetingNoteRequest struct {
	JobID string `json:"job_id" binding:"required"`
	domain.CreateNoteRequest
}

// SaveMeetingNote imports a completed analysis once per user and job ID.
// @Summary Save meeting analysis as a note without duplicates
// @Tags notes
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body SaveMeetingNoteRequest true "Analysis note and source job ID"
// @Success 200 {object} domain.Note
// @Failure 400 {object} domain.ErrorResponse
// @Failure 409 {object} domain.ErrorResponse
// @Router /notes/meeting-result [post]
func (h *NoteHandler) SaveMeetingNote(c *gin.Context) {
	// Limit imported transcripts before JSON decoding (also handles chunked bodies).
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4<<20)
	var req SaveMeetingNoteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.BadRequest("invalid or oversized meeting result"))
		return
	}
	note, err := h.service.SaveMeetingNote(c.Request.Context(), middleware.MustGetUserID(c), req.JobID, &req.CreateNoteRequest)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, note)
}
