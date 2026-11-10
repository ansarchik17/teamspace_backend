package repository

import (
	"testing"

	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestIsSameAcceptEndpoint(t *testing.T) {
	t.Parallel()

	deviceID := "device-a"
	sessionID := "session-a"

	tests := []struct {
		name      string
		call      *domain.CallHistory
		deviceID  string
		sessionID string
		want      bool
	}{
		{
			name:      "same device is idempotent",
			call:      &domain.CallHistory{AcceptedByDeviceID: &deviceID},
			deviceID:  "device-a",
			sessionID: "session-b",
			want:      true,
		},
		{
			name:      "same session is idempotent",
			call:      &domain.CallHistory{AcceptedBySessionID: &sessionID},
			deviceID:  "device-b",
			sessionID: "session-a",
			want:      true,
		},
		{
			name:      "missing request identifiers falls back to idempotent for old clients",
			call:      &domain.CallHistory{AcceptedByDeviceID: &deviceID},
			deviceID:  "",
			sessionID: "",
			want:      true,
		},
		{
			name:      "missing stored identifiers falls back to idempotent",
			call:      &domain.CallHistory{},
			deviceID:  "device-b",
			sessionID: "session-b",
			want:      true,
		},
		{
			name:      "different device and session is another endpoint",
			call:      &domain.CallHistory{AcceptedByDeviceID: &deviceID, AcceptedBySessionID: &sessionID},
			deviceID:  "device-b",
			sessionID: "session-b",
			want:      false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isSameAcceptEndpoint(tt.call, tt.deviceID, tt.sessionID); got != tt.want {
				t.Fatalf("isSameAcceptEndpoint() = %v, want %v", got, tt.want)
			}
		})
	}
}
