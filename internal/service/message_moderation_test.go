package service

import (
	"testing"
	"time"
)

func TestIsWithinModerationWindow(t *testing.T) {
	createdAt := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		now  time.Time
		want bool
	}{
		{name: "inside", now: createdAt.Add(MessageEditWindow - time.Nanosecond), want: true},
		{name: "boundary", now: createdAt.Add(MessageEditWindow), want: true},
		{name: "expired", now: createdAt.Add(MessageEditWindow + time.Nanosecond), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isWithinModerationWindow(createdAt, tt.now, MessageEditWindow); got != tt.want {
				t.Fatalf("isWithinModerationWindow() = %v, want %v", got, tt.want)
			}
		})
	}
}
