package handler

import (
	"context"
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

type calendarHandlerService interface {
	ListCalendars(ctx context.Context, userID int64) ([]domain.UserCalendar, error)
	CreateCalendar(ctx context.Context, userID int64, req *domain.CreateUserCalendarRequest) (*domain.UserCalendar, error)
	RejoinLiveKitMeetingByRoomName(ctx context.Context, userID int64, userName string, roomName string, tokenCtx service.LiveKitTokenContext) (*service.CalendarMeetingJoinResult, error)
	RejoinLiveKitMeetingByRoomNameBypass(ctx context.Context, userID int64, userName string, roomName string, tokenCtx service.LiveKitTokenContext) (*service.CalendarMeetingJoinResult, error)
	CreateEventWithOptions(ctx context.Context, userID int64, req *domain.CreateEventRequest, force bool) (*domain.Event, error)
	CheckAvailability(ctx context.Context, userID int64, req domain.CalendarAvailabilityRequest) (*domain.CalendarAvailabilityResponse, error)
	SuggestTimes(ctx context.Context, userID int64, req domain.CalendarSuggestTimesRequest) (*domain.CalendarSuggestTimesResponse, error)
	GetFreeBusy(ctx context.Context, userID int64, req domain.CalendarFreeBusyRequest) (*domain.CalendarFreeBusyResponse, error)
	ListEvents(ctx context.Context, viewerID, targetUserID int64, from, to time.Time) ([]domain.Event, error)
	GetEventByOwner(ctx context.Context, viewerID, ownerID int64, eventID uuid.UUID) (*domain.Event, error)
	GetEvent(ctx context.Context, userID int64, eventID uuid.UUID) (*domain.Event, error)
	UpdateEvent(ctx context.Context, userID int64, eventID uuid.UUID, req *domain.UpdateEventRequest) (*domain.Event, error)
	UpdateEventWithOptions(ctx context.Context, userID int64, eventID uuid.UUID, req *domain.UpdateEventRequest, force bool) (*domain.Event, error)
	DeleteEvent(ctx context.Context, userID int64, eventID uuid.UUID) error
	UpdateOccurrence(ctx context.Context, userID int64, eventID uuid.UUID, occurrenceStart time.Time, req domain.UpdateOccurrenceRequest) (*domain.CalendarEventException, error)
	UpdateOccurrenceWithOptions(ctx context.Context, userID int64, eventID uuid.UUID, occurrenceStart time.Time, req domain.UpdateOccurrenceRequest, force bool) (*domain.CalendarEventException, error)
	DeleteOccurrence(ctx context.Context, userID int64, eventID uuid.UUID, occurrenceStart time.Time) (*domain.CalendarEventException, error)
	ListOccurrenceExceptions(ctx context.Context, userID int64, eventID uuid.UUID) ([]domain.CalendarEventException, error)
	RestoreOccurrence(ctx context.Context, userID int64, eventID, exceptionID uuid.UUID) error
	RespondToInvite(ctx context.Context, userID int64, eventID uuid.UUID, req *domain.RespondToInviteRequest) error
	GetEventAttendees(ctx context.Context, userID int64, eventID uuid.UUID) ([]domain.EventAttendee, error)
	BatchCreateEvents(ctx context.Context, organizerID int64, req *domain.BatchCreateEventRequest) ([]*domain.Event, error)
	GetTodaySummary(ctx context.Context, userID int64, timezoneOffsetMinutes int) (*domain.TodaySummaryResponse, error)
	GetUpcomingEvents(ctx context.Context, userID int64, limit int) ([]domain.Event, error)
	SetReminder(ctx context.Context, userID int64, eventID uuid.UUID, minutesBefore int) error
	DeleteReminder(ctx context.Context, userID int64, eventID uuid.UUID) error
	SearchUsers(ctx context.Context, requesterID int64, query string, limit, offset int) ([]*domain.UserResponse, error)
	SearchEvents(ctx context.Context, userID int64, title string, from, to time.Time, limit int) ([]domain.Event, error)
	CreatePublicEvent(ctx context.Context, targetUserID int64, req *domain.CreateEventRequest) (*domain.Event, error)
	GetUserByEmail(ctx context.Context, email string) (*domain.User, error)
	GetCalendarGrid(ctx context.Context, userID int64, req domain.CalendarGridRequest) (*domain.CalendarGridResponse, error)
	GetCalendarsWithSettings(ctx context.Context, userID int64) ([]domain.UserCalendarWithSettings, error)
	UpdateCalendarVisibility(ctx context.Context, userID int64, calendarID uuid.UUID, req domain.UpdateCalendarVisibilityRequest) error
	GetGroupCalendarsWithSettings(ctx context.Context, userID int64) ([]domain.GroupCalendarWithSettings, error)
	UpdateGroupCalendarVisibility(ctx context.Context, userID int64, groupID uuid.UUID, req domain.UpdateCalendarVisibilityRequest) error
	ListRooms(ctx context.Context, filter domain.RoomFilter) ([]domain.MeetingRoom, error)
	GetRoom(ctx context.Context, id uuid.UUID) (*domain.MeetingRoom, error)
	CreateRoom(ctx context.Context, userID int64, req domain.CreateMeetingRoomRequest) (*domain.MeetingRoom, error)
	UpdateRoom(ctx context.Context, userID int64, id uuid.UUID, req domain.UpdateMeetingRoomRequest) (*domain.MeetingRoom, error)
	DeleteRoom(ctx context.Context, userID int64, id uuid.UUID) error
	GetRoomAvailability(ctx context.Context, userID int64, roomID uuid.UUID, query domain.RoomAvailabilityQuery) (*domain.RoomAvailabilityResponse, error)
	GetRoomsAvailability(ctx context.Context, userID int64, query domain.RoomAvailabilityQuery) ([]domain.RoomAvailabilityResponse, error)
	BookRoom(ctx context.Context, userID int64, roomID uuid.UUID, req domain.BookRoomRequest) (*domain.RoomBooking, error)
	CancelRoomBooking(ctx context.Context, userID int64, bookingID uuid.UUID) error
	GenerateQMeetLink(ctx context.Context, userID int64, eventID uuid.UUID) (*domain.CreateQMeetLinkResponse, error)
	GetTimezones(ctx context.Context) ([]domain.CalendarTimezoneItem, error)
	ConvertTimezones(ctx context.Context, userID int64, req domain.TimezoneConvertRequest) (*domain.TimezoneConvertResponse, error)
	GetReminder(ctx context.Context, userID int64, eventID uuid.UUID) (*domain.EventReminderInfo, error)
	GetReminderPresets(ctx context.Context) ([]domain.ReminderPresetItem, error)
}

func (h *CalendarHandler) ListCalendars(c *gin.Context) {
	items, err := h.service.ListCalendars(c.Request.Context(), middleware.MustGetUserID(c))
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, items)
}

func (h *CalendarHandler) CreateCalendar(c *gin.Context) {
	var req domain.CreateUserCalendarRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}
	item, err := h.service.CreateCalendar(c.Request.Context(), middleware.MustGetUserID(c), &req)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusCreated, item)
}

type CalendarHandler struct {
	service          calendarHandlerService
	jwtSecret        string
	logger           *zap.Logger
	callAccessBypass bool
}

type RejoinCalendarMeetingRequest struct {
	RoomName string `json:"room_name"`
}

type RejoinCalendarMeetingResponse struct {
	Success                  bool      `json:"success"`
	RoomName                 string    `json:"room_name"`
	CallID                   string    `json:"call_id,omitempty"`
	URL                      string    `json:"url"`
	Token                    string    `json:"token"`
	LivekitURL               string    `json:"livekit_url,omitempty"`
	LivekitToken             string    `json:"livekit_token,omitempty"`
	CallType                 string    `json:"call_type"`
	EventID                  uuid.UUID `json:"event_id"`
	Title                    string    `json:"title"`
	MeetingProvider          string    `json:"meeting_provider"`
	OnlineMeetingStatus      string    `json:"online_meeting_status"`
	ParticipantsCount        int       `json:"participants_count"`
	Message                  string    `json:"message,omitempty"`
	AccessBypassed           bool      `json:"access_bypassed,omitempty"`
	AccessValidationDisabled bool      `json:"access_validation_disabled,omitempty"`
	Warning                  string    `json:"warning,omitempty"`
}

func NewCalendarHandler(service *service.CalendarService, jwtSecret string, logger *zap.Logger) *CalendarHandler {
	return &CalendarHandler{
		service:          service,
		jwtSecret:        jwtSecret,
		logger:           logger.With(zap.String("handler", "calendar")),
		callAccessBypass: liveKitCallAccessBypassEnabled(),
	}
}

func (h *CalendarHandler) respondError(c *gin.Context, err *apperrors.AppError) {
	respondAppError(c, err)
}

func parseDateAny(v string) (time.Time, error) {
	if len(v) == 10 {
		return time.Parse("2006-01-02", v)
	}
	return time.Parse(time.RFC3339, v)
}

// RejoinCalendarMeeting godoc
// @Summary Rejoin a calendar LiveKit meeting
// @Tags calendar
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body RejoinCalendarMeetingRequest true "Calendar room details"
// @Success 200 {object} RejoinCalendarMeetingResponse
// @Failure 400 {object} apperrors.AppError
// @Failure 403 {object} apperrors.AppError
// @Failure 404 {object} apperrors.AppError
// @Failure 500 {object} apperrors.AppError
// @Router /calls/calendar/rejoin [post]
func (h *CalendarHandler) RejoinCalendarMeeting(c *gin.Context) {
	h.joinCalendarMeeting(c, "calls.calendar.rejoin")
}

// JoinCalendarCall godoc
// @Summary Join a calendar LiveKit meeting by room name
// @Tags calendar
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body RejoinCalendarMeetingRequest true "Calendar room details"
// @Success 200 {object} RejoinCalendarMeetingResponse
// @Failure 400 {object} apperrors.AppError
// @Failure 403 {object} apperrors.AppError
// @Failure 404 {object} apperrors.AppError
// @Failure 500 {object} apperrors.AppError
// @Router /calendar/calls/join [post]
func (h *CalendarHandler) JoinCalendarCall(c *gin.Context) {
	h.joinCalendarMeeting(c, "calendar.calls.join")
}

func (h *CalendarHandler) joinCalendarMeeting(c *gin.Context, endpoint string) {
	userID := middleware.MustGetUserID(c)
	userName := getUserDisplayName(c, userID)

	var req RejoinCalendarMeetingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	roomName := strings.TrimSpace(req.RoomName)
	h.logger.Info("calendar_call_join_requested",
		zap.String("endpoint", endpoint),
		zap.String("room_name", roomName),
		zap.Int64("user_id", userID),
	)

	result, err := h.service.RejoinLiveKitMeetingByRoomName(
		c.Request.Context(),
		userID,
		userName,
		roomName,
		service.LiveKitTokenContext{
			RequestID: c.GetString("request_id"),
			DeviceID:  c.GetHeader("X-Web-Device-ID"),
			SessionID: middleware.GetTraceID(c),
			UserAgent: c.GetHeader("User-Agent"),
		},
	)
	if err != nil {
		h.logger.Warn("calendar_call_join_failed",
			zap.String("endpoint", endpoint),
			zap.String("room_name", roomName),
			zap.Int64("user_id", userID),
			zap.Error(err),
			zap.Bool("token_generated", false),
		)
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	h.logger.Info("calendar_call_join_succeeded",
		zap.String("endpoint", endpoint),
		zap.String("room_name", result.RoomName),
		zap.String("event_id", result.EventID.String()),
		zap.Int64("user_id", userID),
		zap.String("meeting_provider", result.MeetingProvider),
		zap.String("meeting_url", result.MeetingURL),
		zap.String("livekit_url", result.URL),
		zap.Bool("token_generated", strings.TrimSpace(result.Token) != ""),
	)

	respondJSON(c, http.StatusOK, RejoinCalendarMeetingResponse{
		Success:                  true,
		RoomName:                 result.RoomName,
		CallID:                   result.CallID,
		URL:                      result.URL,
		Token:                    result.Token,
		LivekitURL:               result.URL,
		LivekitToken:             result.Token,
		CallType:                 "calendar",
		EventID:                  result.EventID,
		Title:                    result.Title,
		MeetingProvider:          result.MeetingProvider,
		OnlineMeetingStatus:      result.OnlineMeetingState,
		ParticipantsCount:        result.ParticipantsCount,
		Message:                  apiMessage(c, "Calendar call join token issued"),
		AccessBypassed:           false,
		AccessValidationDisabled: false,
		Warning:                  "",
	})
}

// CreateEvent godoc
// @Summary Create a new calendar event
// @Tags calendar
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param force query bool false "Create event even if calendar conflicts exist"
// @Param request body domain.CreateEventRequest true "Event creation request"
// @Success 201 {object} domain.Event
// @Failure 400 {object} apperrors.AppError
// @Failure 401 {object} apperrors.AppError
// @Failure 403 {object} apperrors.AppError
// @Failure 409 {object} apperrors.AppError
// @Failure 500 {object} apperrors.AppError
// @Router /calendar/events [post]
func (h *CalendarHandler) CreateEvent(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	var req domain.CreateEventRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	force := strings.EqualFold(strings.TrimSpace(c.Query("force")), "true")

	event, err := h.service.CreateEventWithOptions(c.Request.Context(), userID, &req, force)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusCreated, event)
}

// CheckAvailability godoc
// @Summary Check calendar participant availability
// @Tags calendar
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body domain.CalendarAvailabilityRequest true "Availability request"
// @Success 200 {object} domain.CalendarAvailabilityResponse
// @Failure 400 {object} apperrors.AppError
// @Failure 401 {object} apperrors.AppError
// @Failure 403 {object} apperrors.AppError
// @Failure 500 {object} apperrors.AppError
// @Router /calendar/events/check-availability [post]
func (h *CalendarHandler) CheckAvailability(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	var req domain.CalendarAvailabilityRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	response, err := h.service.CheckAvailability(c.Request.Context(), userID, req)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, response)
}

// SuggestTimes godoc
// @Summary Suggest available calendar time slots
// @Description Builds workday slots from `date + workday_start + workday_end`, automatically includes organizer by default, and returns full and partial suggestions for frontend "Предложить время".
// @Description
// @Description Curl example:
// @Description `curl 'https://alem-workspace.gov.kz/api/v1/calendar/events/suggest-times' -X POST -H 'accept: application/json' -H 'content-type: application/json' -H 'authorization: Bearer <ACCESS_TOKEN>' -H 'x-web-device-id: <DEVICE_ID>' --data-raw '{"date":"2026-05-29","timezone":"Asia/Almaty","attendee_ids":[6,36],"duration_minutes":60,"workday_start":"09:00","workday_end":"18:00","slot_step_minutes":30,"include_organizer":true,"exclude_event_id":null,"limit":10,"allow_partial":true}'`
// @Tags calendar
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body domain.CalendarSuggestTimesRequest true "Time suggestion request"
// @Success 200 {object} domain.CalendarSuggestTimesResponse
// @Failure 400 {object} apperrors.AppError
// @Failure 401 {object} apperrors.AppError
// @Failure 403 {object} apperrors.AppError
// @Failure 500 {object} apperrors.AppError
// @Router /calendar/events/suggest-times [post]
func (h *CalendarHandler) SuggestTimes(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	var req domain.CalendarSuggestTimesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	response, err := h.service.SuggestTimes(c.Request.Context(), userID, req)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, response)
}

// GetFreeBusy godoc
// @Summary Get participant Free/Busy schedule and timeline grid
// @Description Returns busy intervals, timeline occupancy grid, mutual free slots, and conflict alerts
// @Tags calendar
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body domain.CalendarFreeBusyRequest false "Free/Busy request"
// @Success 200 {object} domain.CalendarFreeBusyResponse
// @Failure 400 {object} apperrors.AppError
// @Failure 401 {object} apperrors.AppError
// @Failure 403 {object} apperrors.AppError
// @Failure 500 {object} apperrors.AppError
// @Router /calendar/events/free-busy [post]
func (h *CalendarHandler) GetFreeBusy(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	var req domain.CalendarFreeBusyRequest
	if c.Request.Method == http.MethodPost && c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			h.respondError(c, apperrors.Validation("invalid request body"))
			return
		}
	}

	if req.StartTime.IsZero() {
		if val := c.Query("start_time"); val != "" {
			if t, err := time.Parse(time.RFC3339, val); err == nil {
				req.StartTime = t
			} else {
				h.respondError(c, apperrors.BadRequest("invalid start_time"))
				return
			}
		} else if val := c.Query("from"); val != "" {
			if t, err := time.Parse(time.RFC3339, val); err == nil {
				req.StartTime = t
			} else if t, err := time.Parse("2006-01-02", val); err == nil {
				req.StartTime = t
			} else {
				h.respondError(c, apperrors.BadRequest("invalid from"))
				return
			}
		}
	}
	if req.EndTime.IsZero() {
		if val := c.Query("end_time"); val != "" {
			if t, err := time.Parse(time.RFC3339, val); err == nil {
				req.EndTime = t
			} else {
				h.respondError(c, apperrors.BadRequest("invalid end_time"))
				return
			}
		} else if val := c.Query("to"); val != "" {
			if t, err := time.Parse(time.RFC3339, val); err == nil {
				req.EndTime = t
			} else if t, err := time.Parse("2006-01-02", val); err == nil {
				req.EndTime = t.Add(24*time.Hour - time.Nanosecond)
			} else {
				h.respondError(c, apperrors.BadRequest("invalid to"))
				return
			}
		}
	}
	if val := c.Query("timezone"); val != "" && req.Timezone == "" {
		req.Timezone = val
	}
	if val := c.Query("slot_step_minutes"); val != "" && req.SlotStepMinutes == 0 {
		if step, err := strconv.Atoi(val); err == nil {
			req.SlotStepMinutes = step
		} else {
			h.respondError(c, apperrors.BadRequest("invalid slot_step_minutes"))
			return
		}
	}
	if val := c.Query("user_ids"); val != "" && len(req.AttendeeIDs) == 0 && len(req.UserIDs) == 0 {
		for _, idStr := range strings.Split(val, ",") {
			if id, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64); err == nil && id > 0 {
				req.AttendeeIDs = append(req.AttendeeIDs, id)
			} else {
				h.respondError(c, apperrors.BadRequest("invalid user_ids"))
				return
			}
		}
	}
	if val := c.Query("attendee_ids"); val != "" && len(req.AttendeeIDs) == 0 && len(req.UserIDs) == 0 {
		for _, idStr := range strings.Split(val, ",") {
			if id, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64); err == nil && id > 0 {
				req.AttendeeIDs = append(req.AttendeeIDs, id)
			} else {
				h.respondError(c, apperrors.BadRequest("invalid attendee_ids"))
				return
			}
		}
	}
	if val := c.Query("exclude_event_id"); val != "" && req.ExcludeEventID == nil {
		if id, err := uuid.Parse(val); err == nil {
			req.ExcludeEventID = &id
		} else {
			h.respondError(c, apperrors.BadRequest("invalid exclude_event_id"))
			return
		}
	}
	if val := c.Query("include_organizer"); val != "" && req.IncludeOrganizer == nil {
		b, err := strconv.ParseBool(val)
		if err != nil {
			h.respondError(c, apperrors.BadRequest("invalid include_organizer"))
			return
		}
		req.IncludeOrganizer = &b
	}

	response, err := h.service.GetFreeBusy(c.Request.Context(), userID, req)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, response)
}

// ListEvents godoc
// @Summary List calendar events
// @Tags calendar
// @Security BearerAuth
// @Produce json
// @Param from query string true "Start date (RFC3339 or YYYY-MM-DD)"
// @Param to query string true "End date (RFC3339 or YYYY-MM-DD)"
// @Success 200 {array} domain.Event
// @Router /calendar/events [get]
func (h *CalendarHandler) ListEvents(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	fromStr := c.Query("from")
	toStr := c.Query("to")

	from, err := parseDateAny(fromStr)
	if err != nil {
		h.respondError(c, apperrors.Validation("invalid 'from' date format"))
		return
	}
	to, err := parseDateAny(toStr)
	if err != nil {
		h.respondError(c, apperrors.Validation("invalid 'to' date format"))
		return
	}

	events, err := h.service.ListEvents(c.Request.Context(), userID, userID, from, to)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	requestedTZ := c.DefaultQuery("timezone", c.Query("tz"))
	if requestedTZ != "" {
		if err := service.ValidateCalendarTimezone(requestedTZ); err != nil {
			h.respondError(c, apperrors.ToAppError(err))
			return
		}
		for i := range events {
			service.FormatEventLocalTimes(&events[i], requestedTZ)
		}
	}

	respondJSON(c, http.StatusOK, events)
}

// GetUserEvents godoc
// @Summary List calendar events for a specific user
// @Tags calendar
// @Security BearerAuth
// @Produce json
// @Param id path int true "User ID"
// @Param from query string true "Start date (RFC3339 or YYYY-MM-DD)"
// @Param to query string true "End date (RFC3339 or YYYY-MM-DD)"
// @Success 200 {array} domain.Event
// @Router /calendar/users/{id}/events [get]
func (h *CalendarHandler) GetUserEvents(c *gin.Context) {
	viewerID := middleware.MustGetUserID(c)

	targetUserIDStr := c.Param("id")
	targetUserID, err := strconv.ParseInt(targetUserIDStr, 10, 64)
	if err != nil {
		h.respondError(c, apperrors.Validation("invalid user id"))
		return
	}

	fromStr := c.Query("from")
	toStr := c.Query("to")

	from, err := parseDateAny(fromStr)
	if err != nil {
		h.respondError(c, apperrors.Validation("invalid 'from' date format"))
		return
	}
	to, err := parseDateAny(toStr)
	if err != nil {
		h.respondError(c, apperrors.Validation("invalid 'to' date format"))
		return
	}

	events, err := h.service.ListEvents(c.Request.Context(), viewerID, targetUserID, from, to)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	requestedTZ := c.DefaultQuery("timezone", c.Query("tz"))
	if requestedTZ != "" {
		if err := service.ValidateCalendarTimezone(requestedTZ); err != nil {
			h.respondError(c, apperrors.ToAppError(err))
			return
		}
		for i := range events {
			service.FormatEventLocalTimes(&events[i], requestedTZ)
		}
	}

	respondJSON(c, http.StatusOK, events)
}

// GetUserEvent godoc
// @Summary Get a specific calendar event for a specific user (masked if private)
// @Tags calendar
// @Security BearerAuth
// @Produce json
// @Param id path int true "User ID (Calendar Owner)"
// @Param event_id path string true "Event ID"
// @Success 200 {object} domain.Event
// @Router /calendar/users/{id}/events/{event_id} [get]
func (h *CalendarHandler) GetUserEvent(c *gin.Context) {
	viewerID := middleware.MustGetUserID(c)

	targetUserIDStr := c.Param("id")
	targetUserID, err := strconv.ParseInt(targetUserIDStr, 10, 64)
	if err != nil {
		h.respondError(c, apperrors.Validation("invalid user id"))
		return
	}

	eventIDStr := c.Param("event_id")
	eventID, err := uuid.Parse(eventIDStr)
	if err != nil {
		h.respondError(c, apperrors.Validation("invalid event id"))
		return
	}

	// Передаем viewerID первым аргументом
	event, err := h.service.GetEventByOwner(c.Request.Context(), viewerID, targetUserID, eventID)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, event)
}

// GetEvent godoc
// @Summary Get event details
// @Tags calendar
// @Security BearerAuth
// @Produce json
// @Param id path string true "Event ID"
// @Success 200 {object} domain.Event
// @Router /calendar/events/{id} [get]
func (h *CalendarHandler) GetEvent(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	eventID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.Validation("invalid event id"))
		return
	}

	event, err := h.service.GetEvent(c.Request.Context(), userID, eventID)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	requestedTZ := c.DefaultQuery("timezone", c.Query("tz"))
	if requestedTZ != "" {
		if err := service.ValidateCalendarTimezone(requestedTZ); err != nil {
			h.respondError(c, apperrors.ToAppError(err))
			return
		}
		service.FormatEventLocalTimes(event, requestedTZ)
	}

	respondJSON(c, http.StatusOK, event)
}

// UpdateEvent godoc
// @Summary Update an event
// @Tags calendar
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "Event ID"
// @Param force query bool false "Update event even if calendar conflicts exist"
// @Param request body domain.UpdateEventRequest true "Update request"
// @Success 200 {object} domain.Event
// @Failure 400 {object} apperrors.AppError
// @Failure 401 {object} apperrors.AppError
// @Failure 403 {object} apperrors.AppError
// @Failure 409 {object} apperrors.AppError
// @Failure 500 {object} apperrors.AppError
// @Router /calendar/events/{id} [put]
func (h *CalendarHandler) UpdateEvent(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	eventID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.Validation("invalid event id"))
		return
	}

	var req domain.UpdateEventRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	force := strings.EqualFold(strings.TrimSpace(c.Query("force")), "true") || (req.Force != nil && *req.Force)

	event, err := h.service.UpdateEventWithOptions(c.Request.Context(), userID, eventID, &req, force)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, event)
}

// DeleteEvent godoc
// @Summary Delete an event
// @Tags calendar
// @Security BearerAuth
// @Param id path string true "Event ID"
// @Success 200 {object} map[string]string
// @Router /calendar/events/{id} [delete]
func (h *CalendarHandler) DeleteEvent(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	eventID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.Validation("invalid event id"))
		return
	}

	if err := h.service.DeleteEvent(c.Request.Context(), userID, eventID); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "event deleted")})
}

// HandleCalendarInviteAction processes the GET requests from email buttons
func (h *CalendarHandler) HandleCalendarInviteAction(c *gin.Context) {
	tokenStr := c.Query("token")
	if tokenStr == "" {
		c.Data(http.StatusBadRequest, "text/html; charset=utf-8", []byte(apiMessage(c, "Отсутствует токен")))
		return
	}

	token, err := service.ParseCalendarActionToken(h.jwtSecret, tokenStr)
	if err != nil {
		h.logger.Warn("Failed to parse action token", zap.Error(err))
		c.Data(http.StatusBadRequest, "text/html; charset=utf-8", []byte(apiMessage(c, "Недействительный или истекший токен")))
		return
	}

	userID := token.UserID
	eventID := token.EventID
	action := token.Action

	req := &domain.RespondToInviteRequest{
		Status: action, // "accepted", "declined", "tentative"
	}

	if err := h.service.RespondToInvite(c.Request.Context(), userID, eventID, req); err != nil {
		h.logger.Error("Failed to apply calendar action", zap.Error(err))
		c.Data(http.StatusInternalServerError, "text/html; charset=utf-8", []byte(apiMessage(c, "Произошла ошибка при обработке вашего ответа")))
		return
	}

	actionTitle := apiMessage(c, "Вы приняли приглашение.")
	switch action {
	case "declined":
		actionTitle = apiMessage(c, "Вы отклонили приглашение.")
	case "tentative":
		actionTitle = apiMessage(c, "Вы отметили приглашение как «возможно».")
	}

	html := `<!DOCTYPE html>
<html>
<head>
    <meta charset="utf-8">
    <title>` + apiMessage(c, "Ответ записан") + `</title>
    <style>
        body { font-family: Arial, sans-serif; text-align: center; padding: 50px; background-color: #f8f9fa; }
        .message { background-color: #ffffff; padding: 30px; border-radius: 8px; box-shadow: 0 4px 6px rgba(0,0,0,0.1); display: inline-block; }
        h2 { color: #28a745; }
    </style>
</head>
<body>
    <div class="message">
        <h2>` + apiMessage(c, "Успешно!") + `</h2>
        <p>` + actionTitle + `</p>
        <p>` + apiMessage(c, "Можете закрыть эту вкладку.") + `</p>
    </div>
</body>
</html>`
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(html))
}

// BatchCreateEvents godoc
// @Summary Batch create calendar events with individual details per user
// @Tags calendar
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body domain.BatchCreateEventRequest true "Batch event creation request"
// @Success 201 {array} domain.Event
// @Failure 400 {object} map[string]any
// @Failure 401 {object} map[string]any
// @Failure 403 {object} map[string]any
// @Failure 409 {object} map[string]any "conflicts in details.conflicts"
// @Failure 500 {object} map[string]any
// @Router /calendar/events/batch [post]
func (h *CalendarHandler) BatchCreateEvents(c *gin.Context) {
	var req domain.BatchCreateEventRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	userID, _ := middleware.GetUserID(c)

	for i := range req.Items {
		req.Items[i].Mail = strings.ToLower(strings.TrimSpace(req.Items[i].Mail))
	}

	events, err := h.service.BatchCreateEvents(c.Request.Context(), userID, &req)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusCreated, events)
}

// RespondToInvite godoc
// @Summary Accept/Decline calendar invite
// @Tags calendar
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "Event ID"
// @Param request body domain.RespondToInviteRequest true "RSVP request"
// @Success 200 {object} map[string]string
// @Router /calendar/events/{id}/respond [post]
func (h *CalendarHandler) RespondToInvite(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	eventID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.Validation("invalid event id"))
		return
	}

	var req domain.RespondToInviteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	if err := h.service.RespondToInvite(c.Request.Context(), userID, eventID, &req); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "ok")})
}

// GetEventAttendees godoc
// @Summary Get event attendees with RSVP status
// @Tags calendar
// @Security BearerAuth
// @Produce json
// @Param id path string true "Event ID"
// @Success 200 {object} map[string]interface{} "attendees: array of event attendees with user info"
// @Failure 400 {object} map[string]interface{}
// @Failure 403 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /calendar/events/{id}/attendees [get]
func (h *CalendarHandler) GetEventAttendees(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	eventID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.Validation("invalid event id"))
		return
	}

	attendees, err := h.service.GetEventAttendees(c.Request.Context(), userID, eventID)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	if attendees == nil {
		attendees = []domain.EventAttendee{}
	}
	respondJSON(c, http.StatusOK, gin.H{"attendees": attendees, "summary": summarizeCalendarRSVP(attendees)})
}

func summarizeCalendarRSVP(attendees []domain.EventAttendee) gin.H {
	summary := gin.H{"total": len(attendees), "confirmed": 0, "pending": 0, "tentative": 0, "declined": 0}
	for _, attendee := range attendees {
		switch attendee.Status {
		case "accepted":
			summary["confirmed"] = summary["confirmed"].(int) + 1
		case "pending", "tentative", "declined":
			summary[attendee.Status] = summary[attendee.Status].(int) + 1
		}
	}
	return summary
}

// GetTodaySummary godoc
// @Summary Get summary of today's tasks and events
// @Tags calendar
// @Security BearerAuth
// @Produce json
// @Param timezone_offset query int false "Timezone offset in minutes (e.g. 300 for UTC+5, -300 for UTC-5). Defaults to 300 (UTC+5/KZ) if not provided."
// @Success 200 {object} domain.TodaySummaryResponse
// @Router /calendar/today [get]
func (h *CalendarHandler) GetTodaySummary(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	// Default to UTC+5 (300 minutes) as requested by the primary user base (KZ)
	// Usually this should default to 0, but to fix the immediate issue for the user without
	// backend client changes, we default to their timezone.
	offset := 300
	if offsetStr := c.Query("timezone_offset"); offsetStr != "" {
		if parsed, err := strconv.Atoi(offsetStr); err == nil {
			offset = parsed
		}
	}

	summary, err := h.service.GetTodaySummary(c.Request.Context(), userID, offset)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, summary)
}

// GetUpcomingEvents godoc
// @Summary Get upcoming events
// @Tags calendar
// @Security BearerAuth
// @Produce json
// @Param limit query int false "Number of events to return (default: 3, max: 100)"
// @Success 200 {array} domain.Event
// @Router /calendar/events/upcoming [get]
func (h *CalendarHandler) GetUpcomingEvents(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	limit := 3
	if limitStr := c.Query("limit"); limitStr != "" {
		if parsed, err := strconv.Atoi(limitStr); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	events, err := h.service.GetUpcomingEvents(c.Request.Context(), userID, limit)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, events)
}

// GetThisWeekEvents godoc
// @Summary Get events for this week
// @Description Возвращает события на текущую неделю (с понедельника по воскресенье)
// @Tags calendar
// @Security BearerAuth
// @Produce json
// @Param timezone_offset query int false "Timezone offset in minutes (e.g. 300 for UTC+5, -300 for UTC-5). Defaults to 300 (UTC+5/KZ) if not provided."
// @Success 200 {array} domain.Event
// @Router /calendar/events/this-week [get]
func (h *CalendarHandler) GetThisWeekEvents(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	offset := 300
	if offsetStr := c.Query("timezone_offset"); offsetStr != "" {
		if parsed, err := strconv.Atoi(offsetStr); err == nil {
			offset = parsed
		}
	}

	nowUTC := time.Now().UTC()
	userNow := nowUTC.Add(time.Duration(offset) * time.Minute)

	// Calculate Monday of this week
	weekday := int(userNow.Weekday())
	if weekday == 0 { // Sunday in Go is 0, we want it to be 7 for Monday-starting week
		weekday = 7
	}
	monday := userNow.AddDate(0, 0, -weekday+1)
	startOfWeek := time.Date(monday.Year(), monday.Month(), monday.Day(), 0, 0, 0, 0, time.UTC).Add(-time.Duration(offset) * time.Minute)

	// Calculate end of Sunday (next Monday midnight)
	endOfWeek := startOfWeek.AddDate(0, 0, 7)

	events, err := h.service.ListEvents(c.Request.Context(), userID, userID, startOfWeek, endOfWeek)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, events)
}

// ===== Reminders =====

// SetReminder godoc
// @Summary Set reminder for event (minutes_before)
// @Tags calendar
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "Event ID"
// @Param request body domain.SetReminderRequest true "Reminder request"
// @Success 200 {object} map[string]string
// @Failure 400 {object} map[string]any
// @Failure 401 {object} map[string]any
// @Failure 403 {object} map[string]any
// @Failure 404 {object} map[string]any
// @Failure 500 {object} map[string]any
// @Router /calendar/events/{id}/reminder [put]
func (h *CalendarHandler) SetReminder(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	eventID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.Validation("invalid event id"))
		return
	}

	var req domain.SetReminderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	if req.MinutesBefore < 0 {
		h.respondError(c, apperrors.Validation("minutes_before must be >= 0"))
		return
	}

	if err := h.service.SetReminder(c.Request.Context(), userID, eventID, req.MinutesBefore); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "ok")})
}

// DeleteReminder godoc
// @Summary Delete reminder for event
// @Tags calendar
// @Security BearerAuth
// @Produce json
// @Param id path string true "Event ID"
// @Success 200 {object} map[string]string
// @Failure 400 {object} map[string]any
// @Failure 401 {object} map[string]any
// @Failure 403 {object} map[string]any
// @Failure 404 {object} map[string]any
// @Failure 500 {object} map[string]any
// @Router /calendar/events/{id}/reminder [delete]
func (h *CalendarHandler) DeleteReminder(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	eventID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.Validation("invalid event id"))
		return
	}

	if err := h.service.DeleteReminder(c.Request.Context(), userID, eventID); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "ok")})
}

// SearchUsers godoc
// @Summary Search users for calendar
// @Description Search users visible in calendar scope for viewing calendars and inviting to meetings
// @Tags calendar
// @Security BearerAuth
// @Param q query string true "Search query"
// @Param limit query int false "Limit"
// @Param offset query int false "Offset"
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /calendar/users/search [get]
func (h *CalendarHandler) SearchUsers(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	query := c.Query("q")

	var params struct {
		Limit  int `form:"limit" binding:"omitempty,min=1,max=50"`
		Offset int `form:"offset" binding:"omitempty,min=0"`
	}

	if err := c.ShouldBindQuery(&params); err != nil {
		h.respondError(c, apperrors.Validation("invalid query parameters"))
		return
	}

	if params.Limit == 0 {
		params.Limit = 20
	}

	users, err := h.service.SearchUsers(c.Request.Context(), userID, query, params.Limit, params.Offset)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{
		"query": query,
		"users": users,
		"count": len(users),
	})
}

// SearchEvents godoc
// @Summary Search events by title and time window (no mutations)
// @Tags calendar
// @Security BearerAuth
// @Produce json
// @Param title query string true "Title search (ILIKE)"
// @Param from query string true "Start window (RFC3339 or YYYY-MM-DD)"
// @Param to query string true "End window (RFC3339 or YYYY-MM-DD)"
// @Param limit query int false "Limit (default 20, max 100)"
// @Success 200 {array} domain.Event
// @Router /calendar/events/search [get]
func (h *CalendarHandler) SearchEvents(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	title := c.Query("title")
	fromStr := c.Query("from")
	toStr := c.Query("to")

	from, err := parseDateAny(fromStr)
	if err != nil {
		h.respondError(c, apperrors.Validation("invalid 'from' date format"))
		return
	}
	to, err := parseDateAny(toStr)
	if err != nil {
		h.respondError(c, apperrors.Validation("invalid 'to' date format"))
		return
	}

	limit := 20
	if limitStr := c.Query("limit"); limitStr != "" {
		if parsed, err := strconv.Atoi(limitStr); err == nil {
			limit = parsed
		}
	}

	events, err := h.service.SearchEvents(c.Request.Context(), userID, title, from, to, limit)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, events)
}

// CreatePublicEvent godoc
// @Summary Create a new calendar event via public API (no auth required)
// @Tags calendar
// @Accept json
// @Produce json
// @Param request body domain.CreatePublicEventRequest true "Public Event creation request"
// @Success 201 {object} domain.Event
// @Failure 400 {object} map[string]any
// @Failure 404 {object} map[string]any
// @Failure 500 {object} map[string]any
// @Router /calendar/public/events [post]
func (h *CalendarHandler) CreatePublicEvent(c *gin.Context) {
	var req domain.CreatePublicEventRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	userID, err := h.resolvePublicEventUserID(c, req)
	if err != nil {
		h.respondError(c, err)
		return
	}

	if userID == 0 {
		h.respondError(c, apperrors.Validation("user identity (user_id or email) is required for public event creation"))
		return
	}

	event, serviceErr := h.service.CreatePublicEvent(c.Request.Context(), userID, &req.CreateEventRequest)
	if serviceErr != nil {
		h.respondError(c, apperrors.ToAppError(serviceErr))
		return
	}

	respondJSON(c, http.StatusCreated, event)
}

func (h *CalendarHandler) resolvePublicEventUserID(c *gin.Context, req domain.CreatePublicEventRequest) (int64, *apperrors.AppError) {
	if req.UserID != 0 {
		return req.UserID, nil
	}

	if email := normalizeCalendarEmail(req.Email); email != "" {
		u, err := h.service.GetUserByEmail(c.Request.Context(), email)
		if err != nil || u == nil {
			return 0, apperrors.Validation("user not found by email: " + email)
		}
		return u.ID, nil
	}

	if uIDStr := strings.TrimSpace(c.Query("user_id")); uIDStr != "" {
		if parsed, err := strconv.ParseInt(uIDStr, 10, 64); err == nil {
			return parsed, nil
		}
	}

	if email := normalizeCalendarEmail(c.Query("email")); email != "" {
		u, err := h.service.GetUserByEmail(c.Request.Context(), email)
		if err == nil && u != nil {
			return u.ID, nil
		}
	}

	if uIDStr := strings.TrimSpace(c.GetHeader("X-User-Id")); uIDStr != "" {
		if parsed, err := strconv.ParseInt(uIDStr, 10, 64); err == nil {
			return parsed, nil
		}
	}

	if email := normalizeCalendarEmail(c.GetHeader("X-User-Email")); email != "" {
		u, err := h.service.GetUserByEmail(c.Request.Context(), email)
		if err == nil && u != nil {
			return u.ID, nil
		}
	}

	return 0, nil
}

func normalizeCalendarEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// GetCalendarGrid godoc
// @Summary Get calendar grid view (day/week/month)
// @Tags calendar
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body domain.CalendarGridRequest true "Calendar grid request"
// @Success 200 {object} domain.CalendarGridResponse
// @Failure 400 {object} apperrors.AppError
// @Failure 500 {object} apperrors.AppError
// @Router /calendar/grid [post]
func (h *CalendarHandler) GetCalendarGrid(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	var req domain.CalendarGridRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	grid, err := h.service.GetCalendarGrid(c.Request.Context(), userID, req)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, grid)
}

// GetCalendarsWithSettings godoc
// @Summary Get user calendars with visibility settings
// @Tags calendar
// @Security BearerAuth
// @Produce json
// @Success 200 {array} domain.UserCalendarWithSettings
// @Failure 500 {object} apperrors.AppError
// @Router /calendar/calendars/settings [get]
func (h *CalendarHandler) GetCalendarsWithSettings(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	calendars, err := h.service.GetCalendarsWithSettings(c.Request.Context(), userID)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, calendars)
}

// UpdateCalendarVisibility godoc
// @Summary Update calendar visibility settings
// @Tags calendar
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "Calendar ID"
// @Param request body domain.UpdateCalendarVisibilityRequest true "Visibility settings"
// @Success 200 {object} map[string]string
// @Failure 400 {object} apperrors.AppError
// @Failure 404 {object} apperrors.AppError
// @Failure 500 {object} apperrors.AppError
// @Router /calendar/calendars/{id}/visibility [put]
func (h *CalendarHandler) UpdateCalendarVisibility(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	calendarID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.Validation("invalid calendar id"))
		return
	}

	var req domain.UpdateCalendarVisibilityRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	if err := h.service.UpdateCalendarVisibility(c.Request.Context(), userID, calendarID, req); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "ok")})
}

func (h *CalendarHandler) GetGroupCalendarsWithSettings(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	calendars, err := h.service.GetGroupCalendarsWithSettings(c.Request.Context(), userID)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, calendars)
}

func (h *CalendarHandler) UpdateGroupCalendarVisibility(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	groupID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.Validation("invalid group id"))
		return
	}
	var req domain.UpdateCalendarVisibilityRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}
	if err := h.service.UpdateGroupCalendarVisibility(c.Request.Context(), userID, groupID, req); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "ok")})
}

// GenerateQMeetLink godoc
// @Summary Generate QMeet video meeting link for event
// @Tags calendar
// @Security BearerAuth
// @Produce json
// @Param id path string true "Event ID"
// @Success 200 {object} domain.CreateQMeetLinkResponse
// @Failure 400 {object} apperrors.AppError
// @Failure 403 {object} apperrors.AppError
// @Failure 404 {object} apperrors.AppError
// @Failure 500 {object} apperrors.AppError
// @Router /calendar/events/{id}/qmeet [post]
func (h *CalendarHandler) GenerateQMeetLink(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	eventID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.Validation("invalid event id"))
		return
	}

	res, err := h.service.GenerateQMeetLink(c.Request.Context(), userID, eventID)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, res)
}

// GetTimezones godoc
// @Summary List supported timezones
// @Tags calendar
// @Produce json
// @Success 200 {array} domain.CalendarTimezoneItem
// @Failure 500 {object} apperrors.AppError
// @Router /calendar/timezones [get]
func (h *CalendarHandler) GetTimezones(c *gin.Context) {
	timezones, err := h.service.GetTimezones(c.Request.Context())
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, timezones)
}

// ConvertTimezones godoc
// @Summary Convert event or time range across timezones
// @Tags calendar
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body domain.TimezoneConvertRequest true "Timezone convert request"
// @Success 200 {object} domain.TimezoneConvertResponse
// @Failure 400 {object} apperrors.AppError
// @Failure 500 {object} apperrors.AppError
// @Router /calendar/timezone/convert [post]
func (h *CalendarHandler) ConvertTimezones(c *gin.Context) {
	var req domain.TimezoneConvertRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("некорректные параметры запроса: "+err.Error()))
		return
	}

	res, err := h.service.ConvertTimezones(c.Request.Context(), middleware.MustGetUserID(c), req)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, res)
}

// GetReminder godoc
// @Summary Get event reminder
// @Tags calendar
// @Security BearerAuth
// @Produce json
// @Param id path string true "Event ID"
// @Success 200 {object} domain.EventReminderInfo
// @Failure 400 {object} apperrors.AppError
// @Failure 404 {object} apperrors.AppError
// @Failure 500 {object} apperrors.AppError
// @Router /calendar/events/{id}/reminder [get]
func (h *CalendarHandler) GetReminder(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	eventID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.Validation("invalid event id"))
		return
	}

	rem, err := h.service.GetReminder(c.Request.Context(), userID, eventID)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, rem)
}

// GetReminderPresets godoc
// @Summary Get reminder presets
// @Tags calendar
// @Security BearerAuth
// @Produce json
// @Success 200 {array} domain.ReminderPresetItem
// @Failure 500 {object} apperrors.AppError
// @Router /calendar/reminders/presets [get]
func (h *CalendarHandler) GetReminderPresets(c *gin.Context) {
	presets, err := h.service.GetReminderPresets(c.Request.Context())
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, presets)
}
