package handler

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestResolveActiveCallStatus(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name              string
		call              domain.CallHistory
		participantsCount int
		liveKitErr        error
		wantStatus        string
		wantOK            bool
	}{
		{
			name:              "ongoing room with participants wins",
			call:              domain.CallHistory{CallID: uuid.New(), RoomID: "call_group_room", Status: "missed", CreatedAt: now.Add(-time.Minute), UpdatedAt: now.Add(-time.Minute)},
			participantsCount: 2,
			wantStatus:        "ongoing",
			wantOK:            true,
		},
		{
			name:              "recent initiated direct call stays recoverable",
			call:              domain.CallHistory{CallID: uuid.New(), RoomID: "call_1_2_3", Status: "initiated", CreatedAt: now.Add(-10 * time.Second), UpdatedAt: now.Add(-10 * time.Second)},
			participantsCount: 0,
			wantStatus:        "initiated",
			wantOK:            true,
		},
		{
			name:              "initiated direct call remains visible until 45 second server timeout",
			call:              domain.CallHistory{CallID: uuid.New(), RoomID: "call_1_2_3", Status: "initiated", CreatedAt: now.Add(-40 * time.Second), UpdatedAt: now.Add(-40 * time.Second)},
			participantsCount: 0,
			wantStatus:        "initiated",
			wantOK:            true,
		},
		{
			name:              "accepted call survives temporary livekit outage",
			call:              domain.CallHistory{CallID: uuid.New(), RoomID: "call_1_2_3", Status: "accepted", CreatedAt: now.Add(-5 * time.Minute), UpdatedAt: now.Add(-2 * time.Minute)},
			participantsCount: 0,
			liveKitErr:        errors.New("timeout"),
			wantStatus:        "ongoing",
			wantOK:            true,
		},
		{
			name:              "recent group invite can still ring",
			call:              domain.CallHistory{CallID: uuid.New(), RoomID: "call_group_123e4567-e89b-12d3-a456-426614174000_1711111111", Status: "missed", CreatedAt: now.Add(-5 * time.Second), UpdatedAt: now.Add(-5 * time.Second)},
			participantsCount: 0,
			wantStatus:        "initiated",
			wantOK:            true,
		},
		{
			name:              "stale initiated call is ignored",
			call:              domain.CallHistory{CallID: uuid.New(), RoomID: "call_1_2_3", Status: "initiated", CreatedAt: now.Add(-2 * time.Minute), UpdatedAt: now.Add(-2 * time.Minute)},
			participantsCount: 0,
			wantOK:            false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotStatus, gotOK := resolveActiveCallStatus(tt.call, tt.participantsCount, tt.liveKitErr, now)
			if gotOK != tt.wantOK {
				t.Fatalf("expected ok=%v, got %v", tt.wantOK, gotOK)
			}
			if gotStatus != tt.wantStatus {
				t.Fatalf("expected status %q, got %q", tt.wantStatus, gotStatus)
			}
		})
	}
}

func TestIsGroupCallRoom(t *testing.T) {
	if !isGroupCallRoom("call_group_123e4567-e89b-12d3-a456-426614174000_1711111111") {
		t.Fatal("expected group room to be detected")
	}
	if isGroupCallRoom("call_1_2_3") {
		t.Fatal("direct room must not be treated as group room")
	}
}
