package handler

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/middleware"
)

func calendarOccurrenceParams(c *gin.Context) (uuid.UUID, time.Time, error) {
	eventID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return uuid.Nil, time.Time{}, apperrors.BadRequest("invalid event id")
	}
	seconds, err := strconv.ParseInt(c.Param("occurrence_date"), 10, 64)
	if err != nil || seconds <= 0 || seconds > 253402300799 {
		return uuid.Nil, time.Time{}, apperrors.BadRequest("occurrence_date must be a Unix timestamp in seconds")
	}
	return eventID, time.Unix(seconds, 0).UTC(), nil
}

func (h *CalendarHandler) UpdateOccurrence(c *gin.Context) {
	eventID, start, err := calendarOccurrenceParams(c)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	var req domain.UpdateOccurrenceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}
	force := strings.EqualFold(strings.TrimSpace(c.Query("force")), "true") || (req.Force != nil && *req.Force)
	item, err := h.service.UpdateOccurrenceWithOptions(c.Request.Context(), middleware.MustGetUserID(c), eventID, start, req, force)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, item)
}

func (h *CalendarHandler) DeleteOccurrence(c *gin.Context) {
	eventID, start, err := calendarOccurrenceParams(c)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	item, err := h.service.DeleteOccurrence(c.Request.Context(), middleware.MustGetUserID(c), eventID, start)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, item)
}

func (h *CalendarHandler) ListOccurrenceExceptions(c *gin.Context) {
	eventID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid event id"))
		return
	}
	items, err := h.service.ListOccurrenceExceptions(c.Request.Context(), middleware.MustGetUserID(c), eventID)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	if items == nil {
		items = []domain.CalendarEventException{}
	}
	respondJSON(c, http.StatusOK, gin.H{"exceptions": items})
}

func (h *CalendarHandler) RestoreOccurrence(c *gin.Context) {
	eventID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid event id"))
		return
	}
	exceptionID, err := uuid.Parse(c.Param("exception_id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid exception id"))
		return
	}
	if err := h.service.RestoreOccurrence(c.Request.Context(), middleware.MustGetUserID(c), eventID, exceptionID); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "ok")})
}
