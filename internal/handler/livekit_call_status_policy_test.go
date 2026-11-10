package handler

import (
	"testing"
	"time"

	"github.com/west6ide/workspace-backend/internal/domain"
)

func canDeclineCallStatus(status string) bool {
	return domain.IsCallRingingStatus(status)
}

func canEndCallStatus(status string) bool {
	return domain.IsCallAcceptedStatus(status) || domain.IsCallRingingStatus(status)
}

func TestCanDeclineCallStatus(t *testing.T) {
	tests := []struct {
		status string
		want   bool
	}{
		{status: domain.CallStatusInitiated, want: true},
		{status: domain.CallStatusAccepted, want: false},
		{status: domain.CallStatusOngoing, want: false},
		{status: domain.CallStatusDeclined, want: false},
		{status: domain.CallStatusCanceled, want: false},
		{status: domain.CallStatusEnded, want: false},
		{status: domain.CallStatusMissed, want: false},
	}

	for _, tt := range tests {
		if got := canDeclineCallStatus(tt.status); got != tt.want {
			t.Fatalf("canDeclineCallStatus(%q) = %v, want %v", tt.status, got, tt.want)
		}
	}
}

func TestCanEndCallStatus(t *testing.T) {
	tests := []struct {
		status string
		want   bool
	}{
		{status: domain.CallStatusInitiated, want: true},
		{status: domain.CallStatusRinging, want: true},
		{status: domain.CallStatusConnecting, want: true},
		{status: domain.CallStatusAccepted, want: true},
		{status: domain.CallStatusOngoing, want: true},
		{status: domain.CallStatusDeclined, want: false},
		{status: domain.CallStatusCanceled, want: false},
		{status: domain.CallStatusEnded, want: false},
		{status: domain.CallStatusMissed, want: false},
		{status: domain.CallStatusNoAnswer, want: false},
	}

	for _, tt := range tests {
		if got := canEndCallStatus(tt.status); got != tt.want {
			t.Fatalf("canEndCallStatus(%q) = %v, want %v", tt.status, got, tt.want)
		}
	}
}

func TestBuildCallStatusAccessBlocksGroupCallForNonParticipantDevice(t *testing.T) {
	t.Parallel()

	now := time.Unix(1779444000, 0).UTC()
	call := &domain.CallHistory{
		Status:    domain.CallStatusAccepted,
		ToUserID:  0,
		RoomID:    "call_group_123e4567-e89b-12d3-a456-426614174000_1711111111",
		CreatedAt: now.Add(-10 * time.Second),
	}

	access := buildCallStatusAccess(call, 55, "device-a", false, now)
	if access.CallType != "group" {
		t.Fatalf("CallType = %q, want group", access.CallType)
	}
	if access.CanJoin {
		t.Fatal("CanJoin must be false for non-participant group user")
	}
	if access.CanAccept {
		t.Fatal("CanAccept must be false for non-participant group user")
	}
}

func TestBuildCallStatusAccessDetectsAcceptedOnAnotherDevice(t *testing.T) {
	t.Parallel()

	acceptedByUserID := int64(77)
	acceptedByDeviceID := "device-a"
	now := time.Unix(1779444000, 0).UTC()
	call := &domain.CallHistory{
		Status:             domain.CallStatusAccepted,
		ToUserID:           77,
		RoomID:             "call_1_2_3",
		CreatedAt:          now.Add(-5 * time.Second),
		AcceptedByUserID:   &acceptedByUserID,
		AcceptedByDeviceID: &acceptedByDeviceID,
	}

	access := buildCallStatusAccess(call, 77, "device-b", true, now)
	if access.ActiveOnCurrentDevice {
		t.Fatal("ActiveOnCurrentDevice must be false for another accepted device")
	}
	if access.AcceptedByDeviceID != "device-a" {
		t.Fatalf("AcceptedByDeviceID = %q, want device-a", access.AcceptedByDeviceID)
	}
}

func TestBuildCallStatusAccessUsesPrivateCallRingingTimeout(t *testing.T) {
	t.Parallel()

	now := time.Unix(1779444000, 0).UTC()
	call := &domain.CallHistory{
		Status:    domain.CallStatusInitiated,
		ToUserID:  99,
		RoomID:    "call_1_2_3",
		CreatedAt: now.Add(-40 * time.Second),
	}

	access := buildCallStatusAccess(call, 99, "device-a", true, now)
	if access.IsExpired {
		t.Fatal("IsExpired must stay false before 45-second ringing timeout")
	}
	if got := access.ExpiresAt.Sub(call.CreatedAt); got != privateCallRingingTimeout {
		t.Fatalf("ExpiresAt window = %s, want %s", got, privateCallRingingTimeout)
	}
}

func TestBuildCallStatusAccessAllowsAcceptForRingingStatus(t *testing.T) {
	t.Parallel()

	now := time.Unix(1779444000, 0).UTC()
	call := &domain.CallHistory{
		Status:    domain.CallStatusRinging,
		ToUserID:  99,
		RoomID:    "call_1_99_3",
		CreatedAt: now.Add(-5 * time.Second),
	}

	access := buildCallStatusAccess(call, 99, "device-a", true, now)
	if !access.CanAccept {
		t.Fatal("CanAccept must be true for ringing call")
	}
	if !access.CanDecline {
		t.Fatal("CanDecline must be true for ringing call")
	}
}
