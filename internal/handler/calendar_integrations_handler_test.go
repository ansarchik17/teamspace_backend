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
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestCalendarHandler_GetTimezones(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	stub := &stubCalendarHandlerService{
		getTimezonesResult: []domain.CalendarTimezoneItem{
			{ID: "Asia/Almaty", Name: "Алматы, Астана (UTC+5)", Offset: "+05:00", OffsetMinutes: 300, IsDefault: true},
			{ID: "Europe/Moscow", Name: "Москва (UTC+3)", Offset: "+03:00", OffsetMinutes: 180, IsDefault: false},
		},
	}
	h := newCalendarHandlerWithStub(stub)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/calendar/timezones", nil)

	h.GetTimezones(ctx)

	assert.Equal(t, http.StatusOK, recorder.Code)
	var items []domain.CalendarTimezoneItem
	err := json.Unmarshal(recorder.Body.Bytes(), &items)
	require.NoError(t, err)
	assert.Len(t, items, 2)
	assert.Equal(t, "Asia/Almaty", items[0].ID)
}

func TestCalendarHandler_ConvertTimezones(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	now := time.Now().UTC()
	stub := &stubCalendarHandlerService{
		convertTZResult: &domain.TimezoneConvertResponse{
			Source: domain.TimezoneConvertedItem{
				Timezone:  "Asia/Almaty",
				Offset:    "+05:00",
				StartTime: now,
				EndTime:   now.Add(time.Hour),
			},
			Targets: []domain.TimezoneConvertedItem{
				{
					Timezone:  "Europe/Moscow",
					Offset:    "+03:00",
					StartTime: now.Add(-2 * time.Hour),
					EndTime:   now.Add(-time.Hour),
				},
			},
		},
	}
	h := newCalendarHandlerWithStub(stub)

	body := domain.TimezoneConvertRequest{
		StartTime:       &now,
		EndTime:         &now,
		SourceTimezone:  "Asia/Almaty",
		TargetTimezones: []string{"Europe/Moscow"},
	}
	bodyBytes, _ := json.Marshal(body)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/calendar/timezone/convert", bytes.NewBuffer(bodyBytes))
	ctx.Request.Header.Set("Content-Type", "application/json")

	ctx.Set("user_id", int64(42))
	h.ConvertTimezones(ctx)

	assert.Equal(t, http.StatusOK, recorder.Code)
	var resp domain.TimezoneConvertResponse
	err := json.Unmarshal(recorder.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Equal(t, "Asia/Almaty", resp.Source.Timezone)
	assert.Len(t, resp.Targets, 1)
	assert.Equal(t, "Europe/Moscow", resp.Targets[0].Timezone)
}

func TestCalendarHandler_GenerateQMeetLink(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	eventID := uuid.New()
	stub := &stubCalendarHandlerService{
		generateQMeetResult: &domain.CreateQMeetLinkResponse{
			EventID:         eventID,
			MeetingProvider: "qmeet",
			RoomName:        "qmeet-test-" + eventID.String()[:8],
			MeetingURL:      "https://meet.qmeet.kz/qmeet-test-" + eventID.String()[:8],
			Status:          "created",
			IsOnline:        true,
		},
	}
	h := newCalendarHandlerWithStub(stub)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/calendar/events/"+eventID.String()+"/qmeet", nil)
	ctx.Params = gin.Params{{Key: "id", Value: eventID.String()}}
	ctx.Set("user_id", int64(42))

	h.GenerateQMeetLink(ctx)

	assert.Equal(t, http.StatusOK, recorder.Code)
	var resp domain.CreateQMeetLinkResponse
	err := json.Unmarshal(recorder.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Equal(t, eventID, resp.EventID)
	assert.Equal(t, "qmeet", resp.MeetingProvider)
	assert.True(t, resp.IsOnline)
	assert.Contains(t, resp.MeetingURL, "qmeet-test-")
}

func TestCalendarHandler_GetReminderPresets(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	stub := &stubCalendarHandlerService{
		getPresetsResult: []domain.ReminderPresetItem{
			{MinutesBefore: 15, Label: "За 15 минут", Description: "Рекомендуется"},
		},
	}
	h := newCalendarHandlerWithStub(stub)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/calendar/reminders/presets", nil)

	h.GetReminderPresets(ctx)

	assert.Equal(t, http.StatusOK, recorder.Code)
	var presets []domain.ReminderPresetItem
	err := json.Unmarshal(recorder.Body.Bytes(), &presets)
	require.NoError(t, err)
	assert.Len(t, presets, 1)
	assert.Equal(t, 15, presets[0].MinutesBefore)
}
