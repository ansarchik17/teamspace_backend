package service

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

func TestCalendarMeetingProviderService_GenerateQMeetRoomInfo(t *testing.T) {
	t.Parallel()

	providerService := NewCalendarMeetingProviderService(
		config.CalendarMeetingsConfig{
			MeetingBaseURL: "https://meet.qmeet.kz",
		},
		nil,
		zap.NewNop(),
	)

	eventID := uuid.MustParse("12345678-1234-1234-1234-123456789abc")
	title := "Встреча по архитектуре CAL-007"

	info := providerService.GenerateQMeetRoomInfo(eventID, title)

	assert.Equal(t, "qmeet", info.Provider)
	assert.Equal(t, "created", info.Status)
	require.NotNil(t, info.MeetingRoomID)
	require.NotNil(t, info.MeetingURL)
	require.NotNil(t, info.LiveKitRoomName)

	assert.True(t, strings.HasPrefix(*info.MeetingRoomID, "qmeet-"))
	assert.Equal(t, "qmeet-"+eventID.String(), *info.MeetingRoomID)
	assert.Contains(t, *info.MeetingURL, "https://meet.qmeet.kz/web/calls/calendar/qmeet-")
	assert.Equal(t, *info.MeetingRoomID, *info.LiveKitRoomName)
}

func TestCalendarMeetingProviderService_NormalizeEventMeetingInfo_QMeet(t *testing.T) {
	t.Parallel()

	providerService := NewCalendarMeetingProviderService(
		config.CalendarMeetingsConfig{
			MeetingBaseURL: "https://meet.qmeet.kz",
		},
		nil,
		zap.NewNop(),
	)

	roomName := "qmeet-sync-planning-abcd1234"
	event := &domain.Event{
		ID:              uuid.New(),
		Title:           "Sync Planning",
		StartTime:       time.Now(),
		EndTime:         time.Now().Add(time.Hour),
		IsOnline:        true,
		MeetingProvider: "qmeet",
		MeetingRoomID:   &roomName,
	}

	providerService.NormalizeEventMeetingInfo(event)

	assert.Equal(t, "qmeet", event.MeetingProvider)
	assert.Equal(t, "created", event.MeetingStatus)
	require.NotNil(t, event.LiveKitRoomName)
	assert.Equal(t, roomName, *event.LiveKitRoomName)
	require.NotNil(t, event.MeetingURL)
	assert.Contains(t, *event.MeetingURL, roomName)
}
