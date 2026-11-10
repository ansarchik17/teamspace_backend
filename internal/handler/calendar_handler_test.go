package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

func TestCalendarHandler_CreatePublicEvent_InvalidJSON(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	// Since we are passing nil for the service, any valid request will panic/fail,
	// but invalid JSON will fail early at ShouldBindJSON.
	h := NewCalendarHandler(nil, "test_secret", zap.NewNop())

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/calendar/public/events", bytes.NewBufferString("{invalid-json}"))
	ctx.Request.Header.Set("Content-Type", "application/json")

	h.CreatePublicEvent(ctx)

	assert.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestSummarizeCalendarRSVP(t *testing.T) {
	got := summarizeCalendarRSVP([]domain.EventAttendee{
		{Status: "accepted"}, {Status: "accepted"}, {Status: "tentative"},
		{Status: "pending"}, {Status: "declined"},
	})
	require.Equal(t, 5, got["total"])
	require.Equal(t, 2, got["confirmed"])
	require.Equal(t, 1, got["tentative"])
	require.Equal(t, 1, got["pending"])
	require.Equal(t, 1, got["declined"])
}

func TestCalendarHandler_CreatePublicEvent_MissingUserIdentity(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	h := NewCalendarHandler(nil, "test_secret", zap.NewNop())

	reqBody := domain.CreatePublicEventRequest{
		CreateEventRequest: domain.CreateEventRequest{
			Title:     "Test public event",
			StartTime: time.Now(),
			EndTime:   time.Now().Add(time.Hour),
		},
	}
	jsonBytes, _ := json.Marshal(reqBody)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/calendar/public/events", bytes.NewBuffer(jsonBytes))
	ctx.Request.Header.Set("Content-Type", "application/json")

	h.CreatePublicEvent(ctx)

	assert.Equal(t, http.StatusBadRequest, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"code":"VALIDATION_ERROR"`)
}

// ---------------------------------------------------------------------------
// Stub implementation of calendarHandlerService for join-endpoint tests
// ---------------------------------------------------------------------------

type stubCalendarHandlerService struct {
	// Fields set per test to control behaviour
	joinResult            *service.CalendarMeetingJoinResult
	joinErr               error
	gridResult            *domain.CalendarGridResponse
	gridErr               error
	personalVisibilityErr error
	groupVisibilityErr    error
	freeBusyResult        *domain.CalendarFreeBusyResponse
	freeBusyErr           error
	updateEventResult     *domain.Event
	updateEventErr        error
	lastUpdateForce       bool
	listRoomsResult       []domain.MeetingRoom
	listRoomsErr          error
	getRoomResult         *domain.MeetingRoom
	getRoomErr            error
	createRoomResult      *domain.MeetingRoom
	createRoomErr         error
	updateRoomResult      *domain.MeetingRoom
	updateRoomErr         error
	deleteRoomErr         error
	getRoomAvailResult    *domain.RoomAvailabilityResponse
	getRoomAvailErr       error
	getRoomsAvailResult   []domain.RoomAvailabilityResponse
	getRoomsAvailErr      error
	bookRoomResult        *domain.RoomBooking
	bookRoomErr           error
	cancelRoomBookingErr  error
	generateQMeetResult   *domain.CreateQMeetLinkResponse
	generateQMeetErr      error
	getTimezonesResult    []domain.CalendarTimezoneItem
	getTimezonesErr       error
	convertTZResult       *domain.TimezoneConvertResponse
	convertTZErr          error
	getReminderResult     *domain.EventReminderInfo
	getReminderErr        error
	getPresetsResult      []domain.ReminderPresetItem
	getPresetsErr         error
}

func (s *stubCalendarHandlerService) ListCalendars(_ context.Context, _ int64) ([]domain.UserCalendar, error) {
	return []domain.UserCalendar{}, nil
}
func (s *stubCalendarHandlerService) CreateCalendar(_ context.Context, _ int64, _ *domain.CreateUserCalendarRequest) (*domain.UserCalendar, error) {
	return &domain.UserCalendar{}, nil
}

// Implement only the methods the join endpoint touches; the rest panic if called.

func (s *stubCalendarHandlerService) RejoinLiveKitMeetingByRoomName(
	_ context.Context, _ int64, _ string, _ string, _ service.LiveKitTokenContext,
) (*service.CalendarMeetingJoinResult, error) {
	return s.joinResult, s.joinErr
}

func (s *stubCalendarHandlerService) RejoinLiveKitMeetingByRoomNameBypass(
	_ context.Context, _ int64, _ string, _ string, _ service.LiveKitTokenContext,
) (*service.CalendarMeetingJoinResult, error) {
	return s.joinResult, s.joinErr
}

func (s *stubCalendarHandlerService) CreateEventWithOptions(_ context.Context, _ int64, _ *domain.CreateEventRequest, _ bool) (*domain.Event, error) {
	panic("not implemented")
}
func (s *stubCalendarHandlerService) CheckAvailability(_ context.Context, _ int64, _ domain.CalendarAvailabilityRequest) (*domain.CalendarAvailabilityResponse, error) {
	panic("not implemented")
}
func (s *stubCalendarHandlerService) SuggestTimes(_ context.Context, _ int64, _ domain.CalendarSuggestTimesRequest) (*domain.CalendarSuggestTimesResponse, error) {
	panic("not implemented")
}
func (s *stubCalendarHandlerService) GetFreeBusy(_ context.Context, _ int64, _ domain.CalendarFreeBusyRequest) (*domain.CalendarFreeBusyResponse, error) {
	if s.freeBusyErr != nil {
		return nil, s.freeBusyErr
	}
	if s.freeBusyResult != nil {
		return s.freeBusyResult, nil
	}
	return &domain.CalendarFreeBusyResponse{}, nil
}
func (s *stubCalendarHandlerService) ListEvents(_ context.Context, _, _ int64, _, _ time.Time) ([]domain.Event, error) {
	panic("not implemented")
}
func (s *stubCalendarHandlerService) GetEventByOwner(_ context.Context, _, _ int64, _ uuid.UUID) (*domain.Event, error) {
	panic("not implemented")
}
func (s *stubCalendarHandlerService) GetEvent(_ context.Context, _ int64, _ uuid.UUID) (*domain.Event, error) {
	panic("not implemented")
}
func (s *stubCalendarHandlerService) UpdateEvent(_ context.Context, _ int64, _ uuid.UUID, _ *domain.UpdateEventRequest) (*domain.Event, error) {
	panic("not implemented")
}
func (s *stubCalendarHandlerService) UpdateEventWithOptions(_ context.Context, _ int64, _ uuid.UUID, _ *domain.UpdateEventRequest, force bool) (*domain.Event, error) {
	s.lastUpdateForce = force
	if s.updateEventErr != nil {
		return nil, s.updateEventErr
	}
	if s.updateEventResult != nil {
		return s.updateEventResult, nil
	}
	return &domain.Event{}, nil
}
func (s *stubCalendarHandlerService) DeleteEvent(_ context.Context, _ int64, _ uuid.UUID) error {
	panic("not implemented")
}
func (s *stubCalendarHandlerService) UpdateOccurrence(_ context.Context, _ int64, _ uuid.UUID, _ time.Time, _ domain.UpdateOccurrenceRequest) (*domain.CalendarEventException, error) {
	panic("not implemented")
}
func (s *stubCalendarHandlerService) UpdateOccurrenceWithOptions(_ context.Context, _ int64, _ uuid.UUID, _ time.Time, _ domain.UpdateOccurrenceRequest, _ bool) (*domain.CalendarEventException, error) {
	panic("not implemented")
}
func (s *stubCalendarHandlerService) DeleteOccurrence(_ context.Context, _ int64, _ uuid.UUID, _ time.Time) (*domain.CalendarEventException, error) {
	panic("not implemented")
}
func (s *stubCalendarHandlerService) ListOccurrenceExceptions(_ context.Context, _ int64, _ uuid.UUID) ([]domain.CalendarEventException, error) {
	panic("not implemented")
}
func (s *stubCalendarHandlerService) RestoreOccurrence(_ context.Context, _ int64, _, _ uuid.UUID) error {
	panic("not implemented")
}
func (s *stubCalendarHandlerService) RespondToInvite(_ context.Context, _ int64, _ uuid.UUID, _ *domain.RespondToInviteRequest) error {
	panic("not implemented")
}
func (s *stubCalendarHandlerService) GetEventAttendees(_ context.Context, _ int64, _ uuid.UUID) ([]domain.EventAttendee, error) {
	panic("not implemented")
}
func (s *stubCalendarHandlerService) BatchCreateEvents(_ context.Context, _ int64, _ *domain.BatchCreateEventRequest) ([]*domain.Event, error) {
	panic("not implemented")
}
func (s *stubCalendarHandlerService) GetTodaySummary(_ context.Context, _ int64, _ int) (*domain.TodaySummaryResponse, error) {
	panic("not implemented")
}
func (s *stubCalendarHandlerService) GetUpcomingEvents(_ context.Context, _ int64, _ int) ([]domain.Event, error) {
	panic("not implemented")
}
func (s *stubCalendarHandlerService) SetReminder(_ context.Context, _ int64, _ uuid.UUID, _ int) error {
	panic("not implemented")
}
func (s *stubCalendarHandlerService) DeleteReminder(_ context.Context, _ int64, _ uuid.UUID) error {
	panic("not implemented")
}
func (s *stubCalendarHandlerService) SearchUsers(_ context.Context, _ int64, _ string, _, _ int) ([]*domain.UserResponse, error) {
	panic("not implemented")
}
func (s *stubCalendarHandlerService) SearchEvents(_ context.Context, _ int64, _ string, _, _ time.Time, _ int) ([]domain.Event, error) {
	panic("not implemented")
}
func (s *stubCalendarHandlerService) CreatePublicEvent(_ context.Context, _ int64, _ *domain.CreateEventRequest) (*domain.Event, error) {
	panic("not implemented")
}
func (s *stubCalendarHandlerService) GetUserByEmail(_ context.Context, _ string) (*domain.User, error) {
	panic("not implemented")
}
func (s *stubCalendarHandlerService) GetCalendarGrid(_ context.Context, _ int64, _ domain.CalendarGridRequest) (*domain.CalendarGridResponse, error) {
	return s.gridResult, s.gridErr
}
func (s *stubCalendarHandlerService) GetCalendarsWithSettings(_ context.Context, _ int64) ([]domain.UserCalendarWithSettings, error) {
	panic("not implemented")
}
func (s *stubCalendarHandlerService) UpdateCalendarVisibility(_ context.Context, _ int64, _ uuid.UUID, _ domain.UpdateCalendarVisibilityRequest) error {
	return s.personalVisibilityErr
}
func (s *stubCalendarHandlerService) GetGroupCalendarsWithSettings(_ context.Context, _ int64) ([]domain.GroupCalendarWithSettings, error) {
	panic("not implemented")
}
func (s *stubCalendarHandlerService) UpdateGroupCalendarVisibility(_ context.Context, _ int64, _ uuid.UUID, _ domain.UpdateCalendarVisibilityRequest) error {
	return s.groupVisibilityErr
}
func (s *stubCalendarHandlerService) ListRooms(_ context.Context, _ domain.RoomFilter) ([]domain.MeetingRoom, error) {
	if s.listRoomsErr != nil {
		return nil, s.listRoomsErr
	}
	return s.listRoomsResult, nil
}
func (s *stubCalendarHandlerService) GetRoom(_ context.Context, _ uuid.UUID) (*domain.MeetingRoom, error) {
	if s.getRoomErr != nil {
		return nil, s.getRoomErr
	}
	return s.getRoomResult, nil
}
func (s *stubCalendarHandlerService) CreateRoom(_ context.Context, _ int64, _ domain.CreateMeetingRoomRequest) (*domain.MeetingRoom, error) {
	if s.createRoomErr != nil {
		return nil, s.createRoomErr
	}
	return s.createRoomResult, nil
}
func (s *stubCalendarHandlerService) UpdateRoom(_ context.Context, _ int64, _ uuid.UUID, _ domain.UpdateMeetingRoomRequest) (*domain.MeetingRoom, error) {
	if s.updateRoomErr != nil {
		return nil, s.updateRoomErr
	}
	return s.updateRoomResult, nil
}
func (s *stubCalendarHandlerService) DeleteRoom(_ context.Context, _ int64, _ uuid.UUID) error {
	return s.deleteRoomErr
}
func (s *stubCalendarHandlerService) GetRoomAvailability(_ context.Context, _ int64, _ uuid.UUID, _ domain.RoomAvailabilityQuery) (*domain.RoomAvailabilityResponse, error) {
	if s.getRoomAvailErr != nil {
		return nil, s.getRoomAvailErr
	}
	return s.getRoomAvailResult, nil
}
func (s *stubCalendarHandlerService) GetRoomsAvailability(_ context.Context, _ int64, _ domain.RoomAvailabilityQuery) ([]domain.RoomAvailabilityResponse, error) {
	if s.getRoomsAvailErr != nil {
		return nil, s.getRoomsAvailErr
	}
	return s.getRoomsAvailResult, nil
}
func (s *stubCalendarHandlerService) BookRoom(_ context.Context, _ int64, _ uuid.UUID, _ domain.BookRoomRequest) (*domain.RoomBooking, error) {
	if s.bookRoomErr != nil {
		return nil, s.bookRoomErr
	}
	return s.bookRoomResult, nil
}
func (s *stubCalendarHandlerService) CancelRoomBooking(_ context.Context, _ int64, _ uuid.UUID) error {
	return s.cancelRoomBookingErr
}
func (s *stubCalendarHandlerService) GenerateQMeetLink(_ context.Context, _ int64, _ uuid.UUID) (*domain.CreateQMeetLinkResponse, error) {
	if s.generateQMeetErr != nil {
		return nil, s.generateQMeetErr
	}
	return s.generateQMeetResult, nil
}
func (s *stubCalendarHandlerService) GetTimezones(_ context.Context) ([]domain.CalendarTimezoneItem, error) {
	if s.getTimezonesErr != nil {
		return nil, s.getTimezonesErr
	}
	return s.getTimezonesResult, nil
}
func (s *stubCalendarHandlerService) ConvertTimezones(_ context.Context, _ int64, _ domain.TimezoneConvertRequest) (*domain.TimezoneConvertResponse, error) {
	if s.convertTZErr != nil {
		return nil, s.convertTZErr
	}
	return s.convertTZResult, nil
}
func (s *stubCalendarHandlerService) GetReminder(_ context.Context, _ int64, _ uuid.UUID) (*domain.EventReminderInfo, error) {
	if s.getReminderErr != nil {
		return nil, s.getReminderErr
	}
	return s.getReminderResult, nil
}
func (s *stubCalendarHandlerService) GetReminderPresets(_ context.Context) ([]domain.ReminderPresetItem, error) {
	if s.getPresetsErr != nil {
		return nil, s.getPresetsErr
	}
	return s.getPresetsResult, nil
}

func TestCalendarHandler_GetCalendarGridRejectsInvalidBody(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	h := newCalendarHandlerWithStub(&stubCalendarHandlerService{})
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/calendar/grid", bytes.NewBufferString(`{"view":"agenda"}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	setGinUserID(ctx, 1)

	h.GetCalendarGrid(ctx)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestCalendarHandler_UpdateCalendarVisibilityRejectsInvalidID(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	h := newCalendarHandlerWithStub(&stubCalendarHandlerService{})
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "id", Value: "not-a-uuid"}}
	ctx.Request = httptest.NewRequest(http.MethodPut, "/api/v1/calendar/calendars/not-a-uuid/visibility", bytes.NewBufferString(`{"is_visible":true}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	setGinUserID(ctx, 1)

	h.UpdateCalendarVisibility(ctx)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

// newCalendarHandlerWithStub wires a CalendarHandler whose service field is the stub.
// We bypass NewCalendarHandler (which only accepts *CalendarService) by setting the
// interface directly via the exported-enough struct field approach.
func newCalendarHandlerWithStub(svc calendarHandlerService) *CalendarHandler {
	return &CalendarHandler{
		service: svc,
		logger:  zap.NewNop(),
	}
}

// setGinUserID injects a userID into the gin context (simulates auth middleware).
func setGinUserID(c *gin.Context, userID int64) {
	c.Set("user_id", userID)
}

// ---------------------------------------------------------------------------
// JoinCalendarCall tests
// ---------------------------------------------------------------------------

func TestCalendarHandler_JoinCalendarCall_InvalidJSON(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	h := newCalendarHandlerWithStub(&stubCalendarHandlerService{})

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/calendar/calls/join", bytes.NewBufferString("{bad json"))
	ctx.Request.Header.Set("Content-Type", "application/json")
	setGinUserID(ctx, 42)

	h.JoinCalendarCall(ctx)

	assert.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestCalendarHandler_JoinCalendarCall_AccessDenied_Returns403(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	stub := &stubCalendarHandlerService{
		joinErr: apperrors.New(apperrors.ErrCodeForbidden, "У вас нет доступа к этому событию", http.StatusForbidden),
	}
	h := newCalendarHandlerWithStub(stub)

	body, _ := json.Marshal(map[string]string{"room_name": "call_35_38_1779791721"})

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/calendar/calls/join", bytes.NewBuffer(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	setGinUserID(ctx, 99)

	h.JoinCalendarCall(ctx)

	assert.Equal(t, http.StatusForbidden, recorder.Code)
}

func TestCalendarHandler_JoinCalendarCall_Success_ReturnsLiveKitURLAndToken(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	eventID := uuid.New()
	stub := &stubCalendarHandlerService{
		joinResult: &service.CalendarMeetingJoinResult{
			EventID:            eventID,
			RoomName:           "call_35_38_1779791721",
			Token:              "jwt.livekit.token",
			URL:                "wss://alem-workspace.gov.kz/livekit",
			MeetingURL:         "https://alem-workspace.gov.kz/web/calls/calendar/call_35_38_1779791721",
			MeetingProvider:    "livekit",
			OnlineMeetingState: "created",
			ParticipantsCount:  2,
		},
	}
	h := newCalendarHandlerWithStub(stub)

	body, _ := json.Marshal(map[string]string{"room_name": "call_35_38_1779791721"})

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/calendar/calls/join", bytes.NewBuffer(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	setGinUserID(ctx, 35)

	h.JoinCalendarCall(ctx)

	assert.Equal(t, http.StatusOK, recorder.Code)

	var resp RejoinCalendarMeetingResponse
	require := assert.New(t)
	require.NoError(json.Unmarshal(recorder.Body.Bytes(), &resp))

	require.True(resp.Success)
	require.Equal("call_35_38_1779791721", resp.RoomName)
	// livekit_url must be the WSS client URL, not the backend API URL
	require.Equal("wss://alem-workspace.gov.kz/livekit", resp.LivekitURL)
	require.Equal("jwt.livekit.token", resp.LivekitToken)
	require.Equal("livekit", resp.MeetingProvider)
	require.Equal(eventID, resp.EventID)
}

func TestCalendarHandler_JoinCalendarCall_LivekitURL_IsNotAPIURL(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	// LIVEKIT_API_URL (http://100.72.193.179:7880) must never reach the client.
	// The response livekit_url must always be the public WSS client URL.
	liveKitAPIURL := "http://100.72.193.179:7880"

	stub := &stubCalendarHandlerService{
		joinResult: &service.CalendarMeetingJoinResult{
			EventID:         uuid.New(),
			RoomName:        "calendar-room-abc",
			Token:           "some-token",
			URL:             "wss://alem-workspace.gov.kz/livekit",
			MeetingProvider: "livekit",
		},
	}
	h := newCalendarHandlerWithStub(stub)

	body, _ := json.Marshal(map[string]string{"room_name": "calendar-room-abc"})

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/calendar/calls/join", bytes.NewBuffer(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	setGinUserID(ctx, 1)

	h.JoinCalendarCall(ctx)

	assert.Equal(t, http.StatusOK, recorder.Code)

	respBody := recorder.Body.String()
	assert.NotContains(t, respBody, liveKitAPIURL,
		"response must not expose LIVEKIT_API_URL to clients")
}

func TestCalendarHandler_GetFreeBusy_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)

	now := time.Now().UTC()
	start := now.Truncate(time.Hour)
	end := start.Add(4 * time.Hour)

	stub := &stubCalendarHandlerService{
		freeBusyResult: &domain.CalendarFreeBusyResponse{
			StartTime:       start,
			EndTime:         end,
			Timezone:        "Asia/Almaty",
			SlotStepMinutes: 30,
			Users: []domain.CalendarUserFreeBusy{
				{
					UserID:   1,
					UserName: "Test User",
					BusyIntervals: []domain.CalendarBusyIntervalItem{
						{
							StartTime:  start.Add(time.Hour),
							EndTime:    start.Add(2 * time.Hour),
							Status:     "busy",
							EventTitle: "Important Sync",
						},
					},
					TotalBusyMinutes:   60,
					BusyIntervalsCount: 1,
				},
			},
			Summary: domain.CalendarFreeBusySummary{
				TotalUsers: 1,
				AllFree:    false,
			},
		},
	}
	h := newCalendarHandlerWithStub(stub)

	reqBody := domain.CalendarFreeBusyRequest{
		StartTime:       start,
		EndTime:         end,
		AttendeeIDs:     []int64{1, 2},
		Timezone:        "Asia/Almaty",
		SlotStepMinutes: 30,
	}
	bodyBytes, _ := json.Marshal(reqBody)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/calendar/events/free-busy", bytes.NewBuffer(bodyBytes))
	ctx.Request.Header.Set("Content-Type", "application/json")
	setGinUserID(ctx, 1)

	h.GetFreeBusy(ctx)

	assert.Equal(t, http.StatusOK, recorder.Code)
	var resp domain.CalendarFreeBusyResponse
	err := json.Unmarshal(recorder.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Equal(t, 1, len(resp.Users))
	assert.Equal(t, "Important Sync", resp.Users[0].BusyIntervals[0].EventTitle)
}

func TestCalendarHandler_GetFreeBusy_InvalidQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, query := range []string{"from=invalid", "to=invalid", "start_time=invalid", "end_time=invalid", "user_ids=1,invalid", "attendee_ids=-1", "slot_step_minutes=abc", "exclude_event_id=invalid", "include_organizer=maybe"} {
		t.Run(query, func(t *testing.T) {
			h := newCalendarHandlerWithStub(nil)
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/calendar/free-busy?"+query, nil)
			setGinUserID(ctx, 1)
			h.GetFreeBusy(ctx)
			require.Equal(t, http.StatusBadRequest, recorder.Code)
		})
	}
}

func TestCalendarHandler_UpdateEvent_ConflictAndForce(t *testing.T) {
	gin.SetMode(gin.TestMode)

	eventID := uuid.New()
	newStart := time.Now().Add(time.Hour)
	newEnd := newStart.Add(time.Hour)

	// When conflict occurs and force=false -> returns 409
	conflictErr := apperrors.Conflict("calendar_conflict").WithDetails(map[string]any{
		"conflicts": []map[string]any{{"user_id": 2, "overlap_minutes": 30}},
	})

	stub := &stubCalendarHandlerService{
		updateEventErr: conflictErr,
	}
	h := newCalendarHandlerWithStub(stub)

	updateReq := domain.UpdateEventRequest{
		StartTime: &newStart,
		EndTime:   &newEnd,
	}
	bodyBytes, _ := json.Marshal(updateReq)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/api/v1/calendar/events/"+eventID.String(), bytes.NewBuffer(bodyBytes))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Params = gin.Params{{Key: "id", Value: eventID.String()}}
	setGinUserID(ctx, 1)

	h.UpdateEvent(ctx)

	assert.Equal(t, http.StatusConflict, recorder.Code)
	assert.False(t, stub.lastUpdateForce)

	// When force=true is passed via query param -> passes force=true to service
	stub.updateEventErr = nil
	stub.updateEventResult = &domain.Event{
		ID:        eventID,
		Title:     "Updated Event",
		StartTime: newStart,
		EndTime:   newEnd,
	}

	recorderForce := httptest.NewRecorder()
	ctxForce, _ := gin.CreateTestContext(recorderForce)
	ctxForce.Request = httptest.NewRequest(http.MethodPut, "/api/v1/calendar/events/"+eventID.String()+"?force=true", bytes.NewBuffer(bodyBytes))
	ctxForce.Request.Header.Set("Content-Type", "application/json")
	ctxForce.Params = gin.Params{{Key: "id", Value: eventID.String()}}
	setGinUserID(ctxForce, 1)

	h.UpdateEvent(ctxForce)

	assert.Equal(t, http.StatusOK, recorderForce.Code)
	assert.True(t, stub.lastUpdateForce)
}
