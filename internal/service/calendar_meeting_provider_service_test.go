package service

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

func TestCalendarMeetingProviderServiceDetermineMeetingInfoUsesLiveKitForSmallMeetings(t *testing.T) {
	service := NewCalendarMeetingProviderService(config.CalendarMeetingsConfig{
		LiveKitMaxParticipants: 5,
	}, nil, zap.NewNop())

	info := service.DetermineMeetingInfo(CalendarMeetingCreateRequest{
		EventID:           uuid.New(),
		Title:             "Daily sync",
		StartTime:         time.Now(),
		EndTime:           time.Now().Add(time.Hour),
		Organizer:         &domain.User{ID: 1, Username: "organizer"},
		TotalParticipants: 5,
	})

	if info.Provider != "livekit" {
		t.Fatalf("expected livekit provider, got %q", info.Provider)
	}
	if info.Status != "created" {
		t.Fatalf("expected created status, got %q", info.Status)
	}
	if info.LiveKitRoomName == nil || *info.LiveKitRoomName == "" {
		t.Fatalf("expected livekit room name to be populated")
	}
}

func TestCalendarMeetingProviderServiceDetermineMeetingInfoUsesCiscoForLargeMeetings(t *testing.T) {
	service := NewCalendarMeetingProviderService(config.CalendarMeetingsConfig{
		LiveKitMaxParticipants: 5,
	}, nil, zap.NewNop())

	info := service.DetermineMeetingInfo(CalendarMeetingCreateRequest{
		EventID:           uuid.New(),
		Title:             "Townhall",
		StartTime:         time.Now(),
		EndTime:           time.Now().Add(time.Hour),
		Organizer:         &domain.User{ID: 1, Username: "organizer"},
		TotalParticipants: 6,
	})

	if info.Provider != "cisco" {
		t.Fatalf("expected cisco provider, got %q", info.Provider)
	}
	if info.Status != "pending" {
		t.Fatalf("expected pending status, got %q", info.Status)
	}
}

func TestNormalizeCalendarMeetingBaseURL(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "empty", raw: "", want: ""},
		{name: "already canonical", raw: "https://alem-workspace.gov.kz/web/calls/calendar", want: "https://alem-workspace.gov.kz/web/calls/calendar"},
		{name: "app public url", raw: "https://alem-workspace.gov.kz", want: "https://alem-workspace.gov.kz/web/calls/calendar"},
		{name: "web root", raw: "https://alem-workspace.gov.kz/web", want: "https://alem-workspace.gov.kz/web/calls/calendar"},
		{name: "legacy without web", raw: "https://alem-workspace.gov.kz/calls/calendar", want: "https://alem-workspace.gov.kz/calls/calendar"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeCalendarMeetingBaseURL(tc.raw); got != tc.want {
				t.Fatalf("normalizeCalendarMeetingBaseURL(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// BuildMeetingURL
// ---------------------------------------------------------------------------

func TestBuildMeetingURLReturnsCalendarPath(t *testing.T) {
	svc := NewCalendarMeetingProviderService(config.CalendarMeetingsConfig{
		MeetingBaseURL: "https://alem-workspace.gov.kz",
	}, nil, zap.NewNop())

	roomName := "call_35_38_1779791721"
	got := svc.BuildMeetingURL(roomName)
	if got == nil {
		t.Fatal("expected non-nil meeting_url, got nil")
	}
	want := "https://alem-workspace.gov.kz/web/calls/calendar/call_35_38_1779791721"
	if *got != want {
		t.Fatalf("BuildMeetingURL(%q) = %q, want %q", roomName, *got, want)
	}
}

func TestBuildMeetingURLContainsWebCallsCalendarPath(t *testing.T) {
	svc := NewCalendarMeetingProviderService(config.CalendarMeetingsConfig{
		MeetingBaseURL: "https://alem-workspace.gov.kz",
	}, nil, zap.NewNop())

	got := svc.BuildMeetingURL("my-room-123")
	if got == nil {
		t.Fatal("expected non-nil meeting_url, got nil")
	}
	if !strings.Contains(*got, "/web/calls/calendar/") {
		t.Fatalf("meeting_url %q must contain /web/calls/calendar/", *got)
	}
}

func TestBuildMeetingURLDoesNotContainLiveKitAPIURL(t *testing.T) {
	// LIVEKIT_API_URL is a backend-only server-to-server URL (e.g. http://100.72.193.179:7880).
	// It must NEVER appear in a meeting_url that clients receive.
	liveKitAPIURL := "http://100.72.193.179:7880"

	svc := NewCalendarMeetingProviderService(config.CalendarMeetingsConfig{
		// Intentionally set the base URL to the public app URL, not the API URL.
		MeetingBaseURL: "https://alem-workspace.gov.kz",
	}, nil, zap.NewNop())

	got := svc.BuildMeetingURL("calendar-room-1")
	if got == nil {
		t.Fatal("expected non-nil meeting_url")
	}
	if strings.Contains(*got, liveKitAPIURL) {
		t.Fatalf("meeting_url %q must not contain LIVEKIT_API_URL %q", *got, liveKitAPIURL)
	}
	if strings.Contains(*got, "wss://") {
		t.Fatalf("meeting_url %q must not be a WebSocket URL", *got)
	}
}

func TestBuildMeetingURLReturnsNilForEmptyRoom(t *testing.T) {
	svc := NewCalendarMeetingProviderService(config.CalendarMeetingsConfig{
		MeetingBaseURL: "https://alem-workspace.gov.kz",
	}, nil, zap.NewNop())

	if got := svc.BuildMeetingURL(""); got != nil {
		t.Fatalf("expected nil for empty room, got %q", *got)
	}
	if got := svc.BuildMeetingURL("   "); got != nil {
		t.Fatalf("expected nil for blank room, got %q", *got)
	}
}

func TestBuildMeetingURLReturnsNilWhenBaseURLNotConfigured(t *testing.T) {
	svc := NewCalendarMeetingProviderService(config.CalendarMeetingsConfig{
		MeetingBaseURL: "",
	}, nil, zap.NewNop())

	if got := svc.BuildMeetingURL("some-room"); got != nil {
		t.Fatalf("expected nil when base URL is empty, got %q", *got)
	}
}

// ---------------------------------------------------------------------------
// Participant threshold: <= 5 → livekit, > 5 → cisco
// ---------------------------------------------------------------------------

func TestDetermineMeetingInfoLargeEventDoesNotUseLiveKit(t *testing.T) {
	// Events with > 5 participants must not be assigned the livekit provider.
	// The migration logic and provisioning must not touch those events.
	svc := NewCalendarMeetingProviderService(config.CalendarMeetingsConfig{
		LiveKitMaxParticipants: 5,
	}, nil, zap.NewNop())

	for _, total := range []int{6, 7, 10, 50} {
		info := svc.DetermineMeetingInfo(CalendarMeetingCreateRequest{
			EventID:           uuid.New(),
			Title:             "Townhall",
			StartTime:         time.Now(),
			EndTime:           time.Now().Add(time.Hour),
			Organizer:         &domain.User{ID: 1, Username: "org"},
			TotalParticipants: total,
		})
		if info.Provider == "livekit" {
			t.Fatalf("total=%d: expected non-livekit provider for large meeting, got livekit", total)
		}
	}
}

func TestDetermineMeetingInfoSmallEventUsesLiveKit(t *testing.T) {
	svc := NewCalendarMeetingProviderService(config.CalendarMeetingsConfig{
		LiveKitMaxParticipants: 5,
	}, nil, zap.NewNop())

	for _, total := range []int{1, 2, 3, 4, 5} {
		info := svc.DetermineMeetingInfo(CalendarMeetingCreateRequest{
			EventID:           uuid.New(),
			Title:             "Team sync",
			StartTime:         time.Now(),
			EndTime:           time.Now().Add(time.Hour),
			Organizer:         &domain.User{ID: 1, Username: "org"},
			TotalParticipants: total,
		})
		if info.Provider != "livekit" {
			t.Fatalf("total=%d: expected livekit provider, got %q", total, info.Provider)
		}
	}
}

// ---------------------------------------------------------------------------
// NormalizeEventMeetingInfo — canonical URL always rebuilt from room name
// ---------------------------------------------------------------------------

func TestNormalizeEventMeetingInfoRebuildsLiveKitMeetingURL(t *testing.T) {
	service := NewCalendarMeetingProviderService(config.CalendarMeetingsConfig{
		MeetingBaseURL:         "https://alem-workspace.gov.kz",
		LiveKitMaxParticipants: 5,
	}, nil, zap.NewNop())

	roomName := "calendar-room-123"
	staleURL := "https://old.example.com/calendar-room-123"
	event := &domain.Event{
		IsOnline:        true,
		MeetingProvider: "livekit",
		LiveKitRoomName: &roomName,
		MeetingURL:      &staleURL,
	}

	service.NormalizeEventMeetingInfo(event)

	want := "https://alem-workspace.gov.kz/web/calls/calendar/calendar-room-123"
	if event.MeetingURL == nil || *event.MeetingURL != want {
		t.Fatalf("meeting_url = %v, want %q", event.MeetingURL, want)
	}
}
