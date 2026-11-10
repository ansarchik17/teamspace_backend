package handler

import (
	"context"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/middleware"
)

func calendarResourceID(c *gin.Context) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return id, apperrors.Validation("invalid calendar resource ID")
	}
	return id, nil
}
func (h *CalendarHandler) ListSites(c *gin.Context) {
	service := h.siteService(c)
	if service == nil {
		return
	}
	include := false
	if raw := c.Query("include_inactive"); raw != "" {
		var err error
		include, err = strconv.ParseBool(raw)
		if err != nil {
			h.respondError(c, apperrors.Validation("invalid include_inactive"))
			return
		}
	}
	items, err := service.ListSites(c.Request.Context(), middleware.MustGetUserID(c), include)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	c.Header("Cache-Control", "no-store")
	respondJSON(c, 200, items)
}
func (h *CalendarHandler) GetSite(c *gin.Context) {
	service := h.siteService(c)
	if service == nil {
		return
	}
	id, err := calendarResourceID(c)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	item, err := service.GetSite(c.Request.Context(), middleware.MustGetUserID(c), id)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, 200, item)
}
func (h *CalendarHandler) CreateSite(c *gin.Context) {
	service := h.siteService(c)
	if service == nil {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8192)
	var req domain.CreateMeetingSiteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid site"))
		return
	}
	item, err := service.CreateSite(c.Request.Context(), middleware.MustGetUserID(c), req)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, 201, item)
}
func (h *CalendarHandler) UpdateSite(c *gin.Context) {
	service := h.siteService(c)
	if service == nil {
		return
	}
	id, err := calendarResourceID(c)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8192)
	var req domain.UpdateMeetingSiteRequest
	if err = c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid site"))
		return
	}
	item, err := service.UpdateSite(c.Request.Context(), middleware.MustGetUserID(c), id, req)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, 200, item)
}
func (h *CalendarHandler) DeleteSite(c *gin.Context) {
	service := h.siteService(c)
	if service == nil {
		return
	}
	id, err := calendarResourceID(c)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	if err = service.DeleteSite(c.Request.Context(), middleware.MustGetUserID(c), id); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	c.Status(204)
}
func (h *CalendarHandler) ListUnassignedRooms(c *gin.Context) {
	service := h.siteService(c)
	if service == nil {
		return
	}
	items, err := service.ListUnassignedRooms(c.Request.Context(), middleware.MustGetUserID(c))
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, 200, items)
}

type calendarSiteService interface {
	ListSites(context.Context, int64, bool) ([]domain.MeetingSite, error)
	GetSite(context.Context, int64, uuid.UUID) (*domain.MeetingSite, error)
	CreateSite(context.Context, int64, domain.CreateMeetingSiteRequest) (*domain.MeetingSite, error)
	UpdateSite(context.Context, int64, uuid.UUID, domain.UpdateMeetingSiteRequest) (*domain.MeetingSite, error)
	DeleteSite(context.Context, int64, uuid.UUID) error
	ListUnassignedRooms(context.Context, int64) ([]domain.MeetingRoom, error)
}

func (h *CalendarHandler) siteService(c *gin.Context) calendarSiteService {
	service, ok := h.service.(calendarSiteService)
	if !ok {
		h.respondError(c, apperrors.Internal("site service unavailable"))
		return nil
	}
	return service
}
func (h *CalendarHandler) getRoomForRequester(c *gin.Context, id uuid.UUID) (*domain.MeetingRoom, error) {
	if service, ok := h.service.(interface {
		GetRoomForUser(context.Context, int64, uuid.UUID) (*domain.MeetingRoom, error)
	}); ok {
		return service.GetRoomForUser(c.Request.Context(), middleware.MustGetUserID(c), id)
	}
	return h.service.GetRoom(c.Request.Context(), id)
}
