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

// ListRooms godoc
// @Summary List meeting rooms
// @Tags calendar
// @Security BearerAuth
// @Produce json
// @Param search query string false "Search query for room name, location, or description"
// @Param capacity query int false "Minimum capacity"
// @Param floor query int false "Floor number"
// @Param equipment query string false "Comma-separated equipment tags"
// @Param only_active query bool false "Only return active rooms (default: true)"
// @Success 200 {array} domain.MeetingRoom
// @Router /calendar/rooms [get]
func (h *CalendarHandler) ListRooms(c *gin.Context) {
	filter := domain.RoomFilter{
		OnlyActive:  true,
		RequesterID: middleware.MustGetUserID(c),
	}

	if search := strings.TrimSpace(c.Query("search")); search != "" {
		filter.Search = search
	}

	if raw := c.Query("site_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			h.respondError(c, apperrors.Validation("invalid site_id"))
			return
		}
		filter.SiteID = &id
	}
	if raw := c.Query("include_inactive"); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			h.respondError(c, apperrors.Validation("invalid include_inactive"))
			return
		}
		filter.OnlyActive = !value
	}
	capStr := c.Query("min_capacity")
	if capStr == "" {
		capStr = c.Query("capacity")
	}
	if capStr != "" {
		if capVal, err := strconv.Atoi(capStr); err == nil && capVal > 0 {
			filter.MinCapacity = &capVal
		}
	}

	if floorStr := c.Query("floor"); floorStr != "" {
		if floorVal, err := strconv.Atoi(floorStr); err == nil {
			filter.Floor = &floorVal
		}
	}

	if eqStr := strings.TrimSpace(c.Query("equipment")); eqStr != "" {
		parts := strings.Split(eqStr, ",")
		var eqList []string
		for _, p := range parts {
			trimmed := strings.TrimSpace(p)
			if trimmed != "" {
				eqList = append(eqList, trimmed)
			}
		}
		filter.Equipment = eqList
	}

	if activeStr := c.Query("only_active"); activeStr != "" {
		if activeVal, err := strconv.ParseBool(activeStr); err == nil {
			filter.OnlyActive = activeVal
		}
	}

	rooms, err := h.service.ListRooms(c.Request.Context(), filter)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, rooms)
}

// GetRoom godoc
// @Summary Get meeting room by ID
// @Tags calendar
// @Security BearerAuth
// @Produce json
// @Param id path string true "Meeting Room ID"
// @Success 200 {object} domain.MeetingRoom
// @Router /calendar/rooms/{id} [get]
func (h *CalendarHandler) GetRoom(c *gin.Context) {
	roomIDStr := c.Param("id")
	if roomIDStr == "" {
		roomIDStr = c.Param("room_id")
	}
	roomID, err := uuid.Parse(roomIDStr)
	if err != nil {
		h.respondError(c, apperrors.Validation("некорректный ID переговорной"))
		return
	}

	room, err := h.getRoomForRequester(c, roomID)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, room)
}

// CreateRoom godoc
// @Summary Create a meeting room
// @Tags calendar
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body domain.CreateMeetingRoomRequest true "Room details"
// @Success 201 {object} domain.MeetingRoom
// @Router /calendar/rooms [post]
func (h *CalendarHandler) CreateRoom(c *gin.Context) {
	var req domain.CreateMeetingRoomRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("неверный формат запроса: "+err.Error()))
		return
	}

	room, err := h.service.CreateRoom(c.Request.Context(), middleware.MustGetUserID(c), req)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusCreated, room)
}

// UpdateRoom godoc
// @Summary Update meeting room
// @Tags calendar
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "Meeting Room ID"
// @Param request body domain.UpdateMeetingRoomRequest true "Room update details"
// @Success 200 {object} domain.MeetingRoom
// @Router /calendar/rooms/{id} [put]
func (h *CalendarHandler) UpdateRoom(c *gin.Context) {
	roomIDStr := c.Param("id")
	if roomIDStr == "" {
		roomIDStr = c.Param("room_id")
	}
	roomID, err := uuid.Parse(roomIDStr)
	if err != nil {
		h.respondError(c, apperrors.Validation("некорректный ID переговорной"))
		return
	}

	var req domain.UpdateMeetingRoomRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("неверный формат запроса: "+err.Error()))
		return
	}

	room, err := h.service.UpdateRoom(c.Request.Context(), middleware.MustGetUserID(c), roomID, req)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, room)
}

// DeleteRoom godoc
// @Summary Delete meeting room
// @Tags calendar
// @Security BearerAuth
// @Produce json
// @Param id path string true "Meeting Room ID"
// @Success 200 {object} map[string]string
// @Router /calendar/rooms/{id} [delete]
func (h *CalendarHandler) DeleteRoom(c *gin.Context) {
	roomIDStr := c.Param("id")
	if roomIDStr == "" {
		roomIDStr = c.Param("room_id")
	}
	roomID, err := uuid.Parse(roomIDStr)
	if err != nil {
		h.respondError(c, apperrors.Validation("некорректный ID переговорной"))
		return
	}

	if err := h.service.DeleteRoom(c.Request.Context(), middleware.MustGetUserID(c), roomID); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "Переговорная комната удалена")})
}

// GetRoomAvailability godoc
// @Summary Get availability, bookings, and free slots for a specific meeting room
// @Tags calendar
// @Security BearerAuth
// @Produce json
// @Param id path string true "Meeting Room ID"
// @Param from query string false "Start time (RFC3339 or YYYY-MM-DD)"
// @Param to query string false "End time (RFC3339 or YYYY-MM-DD)"
// @Param start_time query string false "Alias for from"
// @Param end_time query string false "Alias for to"
// @Param slot_step_minutes query int false "Step for discrete grid slots (default: 30)"
// @Param exclude_booking_id query string false "Exclude specific booking from conflict calculation"
// @Param exclude_event_id query string false "Exclude specific event from conflict calculation"
// @Success 200 {object} domain.RoomAvailabilityResponse
// @Router /calendar/rooms/{id}/availability [get]
func (h *CalendarHandler) GetRoomAvailability(c *gin.Context) {
	roomIDStr := c.Param("id")
	if roomIDStr == "" {
		roomIDStr = c.Param("room_id")
	}
	roomID, err := uuid.Parse(roomIDStr)
	if err != nil {
		h.respondError(c, apperrors.Validation("некорректный ID переговорной"))
		return
	}

	fromStr := c.Query("from")
	if fromStr == "" {
		fromStr = c.Query("start_time")
	}
	toStr := c.Query("to")
	if toStr == "" {
		toStr = c.Query("end_time")
	}

	var startTime, endTime time.Time
	if fromStr != "" {
		st, err := parseDateAny(fromStr)
		if err != nil {
			h.respondError(c, apperrors.Validation("неверный формат начальной даты"))
			return
		}
		startTime = st
	}
	if toStr != "" {
		et, err := parseDateAny(toStr)
		if err != nil {
			h.respondError(c, apperrors.Validation("неверный формат конечной даты"))
			return
		}
		endTime = et
	}

	step := 30
	if stepStr := c.Query("slot_step_minutes"); stepStr != "" {
		if s, err := strconv.Atoi(stepStr); err == nil && s > 0 {
			step = s
		} else {
			h.respondError(c, apperrors.BadRequest("invalid slot_step_minutes"))
			return
		}
	}

	var excludeBookingID, excludeEventID *uuid.UUID
	if exBookStr := c.Query("exclude_booking_id"); exBookStr != "" {
		if bid, err := uuid.Parse(exBookStr); err == nil {
			excludeBookingID = &bid
		} else {
			h.respondError(c, apperrors.BadRequest("invalid exclude_booking_id"))
			return
		}
	}
	if exEventStr := c.Query("exclude_event_id"); exEventStr != "" {
		if eid, err := uuid.Parse(exEventStr); err == nil {
			excludeEventID = &eid
		} else {
			h.respondError(c, apperrors.BadRequest("invalid exclude_event_id"))
			return
		}
	}

	query := domain.RoomAvailabilityQuery{
		StartTime:        startTime,
		EndTime:          endTime,
		SlotStepMinutes:  step,
		ExcludeBookingID: excludeBookingID,
		ExcludeEventID:   excludeEventID,
	}

	resp, err := h.service.GetRoomAvailability(c.Request.Context(), middleware.MustGetUserID(c), roomID, query)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, resp)
}

// GetRoomsAvailability godoc
// @Summary Check availability of all rooms in a given time window
// @Tags calendar
// @Security BearerAuth
// @Produce json
// @Param from query string true "Start time (RFC3339 or YYYY-MM-DD)"
// @Param to query string true "End time (RFC3339 or YYYY-MM-DD)"
// @Param start_time query string false "Alias for from"
// @Param end_time query string false "Alias for to"
// @Param capacity query int false "Minimum capacity filter"
// @Param equipment query string false "Comma-separated equipment filter"
// @Success 200 {array} domain.RoomAvailabilityResponse
// @Router /calendar/rooms/availability [get]
func (h *CalendarHandler) GetRoomsAvailability(c *gin.Context) {
	fromStr := c.Query("from")
	if fromStr == "" {
		fromStr = c.Query("start_time")
	}
	toStr := c.Query("to")
	if toStr == "" {
		toStr = c.Query("end_time")
	}

	if fromStr == "" || toStr == "" {
		h.respondError(c, apperrors.Validation("параметры from и to обязательны"))
		return
	}

	startTime, err := parseDateAny(fromStr)
	if err != nil {
		h.respondError(c, apperrors.Validation("неверный формат начальной даты"))
		return
	}
	endTime, err := parseDateAny(toStr)
	if err != nil {
		h.respondError(c, apperrors.Validation("неверный формат конечной даты"))
		return
	}

	var capacity *int
	if capStr := c.Query("capacity"); capStr != "" {
		if capVal, err := strconv.Atoi(capStr); err == nil && capVal > 0 {
			capacity = &capVal
		}
	}

	var eqList []string
	if eqStr := strings.TrimSpace(c.Query("equipment")); eqStr != "" {
		parts := strings.Split(eqStr, ",")
		for _, p := range parts {
			trimmed := strings.TrimSpace(p)
			if trimmed != "" {
				eqList = append(eqList, trimmed)
			}
		}
	}

	query := domain.RoomAvailabilityQuery{
		StartTime: startTime,
		EndTime:   endTime,
		Capacity:  capacity,
		Equipment: eqList,
	}

	resp, err := h.service.GetRoomsAvailability(c.Request.Context(), middleware.MustGetUserID(c), query)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, resp)
}

// BookRoom godoc
// @Summary Book a meeting room directly with atomic locking
// @Tags calendar
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "Meeting Room ID"
// @Param request body domain.BookRoomRequest true "Booking details"
// @Success 201 {object} domain.RoomBooking
// @Failure 409 {object} apperrors.AppError "Room is already booked (conflict)"
// @Router /calendar/rooms/{id}/book [post]
func (h *CalendarHandler) BookRoom(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	roomIDStr := c.Param("id")
	if roomIDStr == "" {
		roomIDStr = c.Param("room_id")
	}
	roomID, err := uuid.Parse(roomIDStr)
	if err != nil {
		h.respondError(c, apperrors.Validation("некорректный ID переговорной"))
		return
	}

	var req domain.BookRoomRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("неверный формат запроса: "+err.Error()))
		return
	}

	booking, err := h.service.BookRoom(c.Request.Context(), userID, roomID, req)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusCreated, booking)
}

// CancelRoomBooking godoc
// @Summary Cancel a room booking
// @Tags calendar
// @Security BearerAuth
// @Produce json
// @Param booking_id path string true "Booking ID"
// @Success 200 {object} map[string]string
// @Router /calendar/rooms/bookings/{booking_id} [delete]
func (h *CalendarHandler) CancelRoomBooking(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	bookingIDStr := c.Param("booking_id")
	bookingID, err := uuid.Parse(bookingIDStr)
	if err != nil {
		h.respondError(c, apperrors.Validation("некорректный ID бронирования"))
		return
	}

	if err := h.service.CancelRoomBooking(c.Request.Context(), userID, bookingID); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "Бронирование отменено")})
}
