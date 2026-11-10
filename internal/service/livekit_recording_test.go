package service

import (
	"github.com/livekit/protocol/livekit"
	"testing"
)

func TestIsParticipantOwnedByUser(t *testing.T) {
	s := &LiveKitService{}

	tests := []struct {
		name     string
		identity string
		metadata string
		userID   int64
		want     bool
	}{
		{"Exact match", "9", "", 9, true},
		{"Session match", "9:abc", "", 9, true},
		{"Metadata match", "user_9", `{"user_id":"9"}`, 9, true},
		{"Mismatch", "10", "", 9, false},
		{"Metadata mismatch", "user_10", `{"user_id":"10"}`, 9, false},
		{"Complex metadata", "user_9", `{"user_id":"9", "other":"value"}`, 9, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &livekit.ParticipantInfo{
				Identity: tt.identity,
				Metadata: tt.metadata,
			}
			if got := s.isParticipantOwnedByUser(p, tt.userID); got != tt.want {
				t.Errorf("isParticipantOwnedByUser() = %v, want %v", got, tt.want)
			}
		})
	}
}
