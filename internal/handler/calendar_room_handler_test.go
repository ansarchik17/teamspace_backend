package handler

import (
	"bytes"
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
)

func TestCalendarHandler_ListRooms(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	roomID := uuid.New()
	color := "#3B82F6"
	stub := &stubCalendarHandlerService{
		listRoomsResult: []domain.MeetingRoom{
			{
				ID:        roomID,
				Name:      "Переговорная 1",
				Capacity:  8,
				Color:     &color,
				Equipment: []string{"tv", "whiteboard"},
				IsActive:  true,
			},
		},
	}

	h := newCalendarHandlerWithStub(stub)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/calendar/rooms?search=Переговорная&capacity=5", nil)
	setGinUserID(ctx, 42)

	h.ListRooms(ctx)

	assert.Equal(t, http.StatusOK, recorder.Code)
	var resp []domain.MeetingRoom
	err := json.Unmarshal(recorder.Body.Bytes(), &resp)
	require.NoError(t, err)
	require.Len(t, resp, 1)
	assert.Equal(t, "Переговорная 1", resp[0].Name)
	assert.Equal(t, 8, resp[0].Capacity)
}

func TestCalendarHandler_GetRoom_NotFound(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	stub := &stubCalendarHandlerService{
		getRoomErr: apperrors.RoomNotFound(),
	}

	h := newCalendarHandlerWithStub(stub)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "id", Value: uuid.New().String()}}
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/calendar/rooms/"+ctx.Params[0].Value, nil)
	setGinUserID(ctx, 42)

	h.GetRoom(ctx)

	assert.Equal(t, http.StatusNotFound, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "ROOM_NOT_FOUND")
}

func TestCalendarHandler_CreateRoom_Success(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	roomID := uuid.New()
	color := "#10B981"
	stub := &stubCalendarHandlerService{
		createRoomResult: &domain.MeetingRoom{
			ID:        roomID,
			Name:      "Астана",
			Capacity:  14,
			Color:     &color,
			Equipment: []string{"video_call"},
			IsActive:  true,
		},
	}

	h := newCalendarHandlerWithStub(stub)

	reqBody := domain.CreateMeetingRoomRequest{
		Name:      "Астана",
		Capacity:  14,
		Color:     &color,
		Equipment: []string{"video_call"},
	}
	bodyBytes, _ := json.Marshal(reqBody)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/calendar/rooms", bytes.NewBuffer(bodyBytes))
	ctx.Request.Header.Set("Content-Type", "application/json")
	setGinUserID(ctx, 42)

	h.CreateRoom(ctx)

	assert.Equal(t, http.StatusCreated, recorder.Code)
	var resp domain.MeetingRoom
	err := json.Unmarshal(recorder.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Equal(t, "Астана", resp.Name)
	assert.Equal(t, 14, resp.Capacity)
}

func TestCalendarHandler_BookRoom_Conflict409(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	roomID := uuid.New()
	stub := &stubCalendarHandlerService{
		bookRoomErr: apperrors.RoomAlreadyBooked("Переговорная уже занята на выбранное время").
			WithDetail("conflicting_title", "Стратегический синк"),
	}

	h := newCalendarHandlerWithStub(stub)

	reqBody := domain.BookRoomRequest{
		Title:     "Планирование спринта",
		StartTime: time.Date(2026, 9, 28, 14, 0, 0, 0, time.UTC),
		EndTime:   time.Date(2026, 9, 28, 15, 0, 0, 0, time.UTC),
	}
	bodyBytes, _ := json.Marshal(reqBody)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "id", Value: roomID.String()}}
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/calendar/rooms/"+roomID.String()+"/book", bytes.NewBuffer(bodyBytes))
	ctx.Request.Header.Set("Content-Type", "application/json")
	setGinUserID(ctx, 42)

	h.BookRoom(ctx)

	assert.Equal(t, http.StatusConflict, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "ROOM_ALREADY_BOOKED")
	assert.Contains(t, recorder.Body.String(), "Переговорная комната уже занята в выбранное время.")
}

func TestCalendarHandler_GetRoomAvailability(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	roomID := uuid.New()
	start := time.Date(2026, 9, 28, 9, 0, 0, 0, time.UTC)
	end := time.Date(2026, 9, 28, 18, 0, 0, 0, time.UTC)

	stub := &stubCalendarHandlerService{
		getRoomAvailResult: &domain.RoomAvailabilityResponse{
			Room: domain.MeetingRoom{
				ID:       roomID,
				Name:     "Алматы",
				Capacity: 10,
				IsActive: true,
			},
			StartTime:   start,
			EndTime:     end,
			IsAvailable: false,
			Bookings: []domain.RoomBooking{
				{
					ID:        uuid.New(),
					RoomID:    roomID,
					Title:     "Обсуждение CAL-006",
					StartTime: start.Add(2 * time.Hour),
					EndTime:   start.Add(3 * time.Hour),
					Status:    "confirmed",
				},
			},
			FreeSlots: []domain.RoomFreeSlot{
				{
					StartTime:       start,
					EndTime:         start.Add(2 * time.Hour),
					DurationMinutes: 120,
				},
				{
					StartTime:       start.Add(3 * time.Hour),
					EndTime:         end,
					DurationMinutes: 300,
				},
			},
		},
	}

	h := newCalendarHandlerWithStub(stub)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "id", Value: roomID.String()}}
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/calendar/rooms/"+roomID.String()+"/availability?from=2026-09-28T09:00:00Z&to=2026-09-28T18:00:00Z", nil)
	setGinUserID(ctx, 42)

	h.GetRoomAvailability(ctx)

	assert.Equal(t, http.StatusOK, recorder.Code)
	var resp domain.RoomAvailabilityResponse
	err := json.Unmarshal(recorder.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.False(t, resp.IsAvailable)
	require.Len(t, resp.Bookings, 1)
	assert.Equal(t, "Обсуждение CAL-006", resp.Bookings[0].Title)
	require.Len(t, resp.FreeSlots, 2)
}
