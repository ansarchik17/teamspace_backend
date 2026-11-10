package handler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/livekit/protocol/livekit"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

func TestCleanupPrivateCallRoomDeletesRoomOnce(t *testing.T) {
	t.Parallel()

	deleteCalls := 0
	h := &LiveKitHandler{
		logger: zap.NewNop(),
		deletePrivateCallRoom: func(context.Context, string) (bool, error) {
			deleteCalls++
			return true, nil
		},
		listPrivateCallRoom: func(context.Context, string) ([]*livekit.ParticipantInfo, error) {
			return nil, nil
		},
	}

	h.cleanupPrivateCallRoom(context.Background(), "call-id-1", "call_1_2_3", "decline_before_accept")

	if deleteCalls != 1 {
		t.Fatalf("deletePrivateCallRoom() calls = %d, want 1", deleteCalls)
	}
}

func TestCleanupPrivateCallRoomTreatsMissingRoomAsSafe(t *testing.T) {
	t.Parallel()

	deleteCalls := 0
	h := &LiveKitHandler{
		logger: zap.NewNop(),
		deletePrivateCallRoom: func(context.Context, string) (bool, error) {
			deleteCalls++
			return false, nil
		},
		listPrivateCallRoom: func(context.Context, string) ([]*livekit.ParticipantInfo, error) {
			return nil, nil
		},
	}

	h.cleanupPrivateCallRoom(context.Background(), "call-id-1", "call_1_2_3", "end_after_accept")

	if deleteCalls != 1 {
		t.Fatalf("deletePrivateCallRoom() calls = %d, want 1", deleteCalls)
	}
}

func TestCleanupPrivateCallRoomSwallowsDeleteErrors(t *testing.T) {
	t.Parallel()

	deleteCalls := 0
	h := &LiveKitHandler{
		logger: zap.NewNop(),
		deletePrivateCallRoom: func(context.Context, string) (bool, error) {
			deleteCalls++
			return false, errors.New("livekit unavailable")
		},
		listPrivateCallRoom: func(context.Context, string) ([]*livekit.ParticipantInfo, error) {
			return nil, nil
		},
	}

	h.cleanupPrivateCallRoom(context.Background(), "call-id-1", "call_1_2_3", "start_call_timeout_cleanup")

	if deleteCalls != 1 {
		t.Fatalf("deletePrivateCallRoom() calls = %d, want 1", deleteCalls)
	}
}

func TestCleanupPrivateCallRoomSkipsDeleteWhenParticipantsConnected(t *testing.T) {
	t.Parallel()

	deleteCalls := 0
	h := &LiveKitHandler{
		logger: zap.NewNop(),
		deletePrivateCallRoom: func(context.Context, string) (bool, error) {
			deleteCalls++
			return true, nil
		},
		listPrivateCallRoom: func(context.Context, string) ([]*livekit.ParticipantInfo, error) {
			return []*livekit.ParticipantInfo{{Identity: "9:z5fd12d"}}, nil
		},
		privateCallGrace: time.Second,
		now:              time.Now,
	}

	h.cleanupPrivateCallRoom(context.Background(), "call-id-1", "call_1_2_3", "start_call_timeout_cleanup")

	if deleteCalls != 0 {
		t.Fatalf("deletePrivateCallRoom() calls = %d, want 0", deleteCalls)
	}
}

func TestCleanupPrivateCallRoomSkipsDeleteWithinGracePeriod(t *testing.T) {
	t.Parallel()

	now := time.Unix(1779749000, 0).UTC()
	deleteCalls := 0
	h := &LiveKitHandler{
		logger: zap.NewNop(),
		deletePrivateCallRoom: func(context.Context, string) (bool, error) {
			deleteCalls++
			return true, nil
		},
		listPrivateCallRoom: func(context.Context, string) ([]*livekit.ParticipantInfo, error) {
			return nil, nil
		},
		loadPrivateCallInfo: func(context.Context, string, string) *domain.CallHistory {
			return &domain.CallHistory{
				CallID:    domain.CallHistory{}.CallID,
				RoomID:    "call_1_2_3",
				Status:    domain.CallStatusEnded,
				UpdatedAt: now.Add(-5 * time.Second),
				CreatedAt: now.Add(-20 * time.Second),
			}
		},
		privateCallGrace: 15 * time.Second,
		now:              func() time.Time { return now },
	}

	h.cleanupPrivateCallRoom(context.Background(), "call-id-1", "call_1_2_3", "end_after_accept")

	if deleteCalls != 0 {
		t.Fatalf("deletePrivateCallRoom() calls = %d, want 0", deleteCalls)
	}
}

func TestCleanupPrivateCallRoomDeletesAfterGracePeriod(t *testing.T) {
	t.Parallel()

	now := time.Unix(1779749000, 0).UTC()
	deleteCalls := 0
	h := &LiveKitHandler{
		logger: zap.NewNop(),
		deletePrivateCallRoom: func(context.Context, string) (bool, error) {
			deleteCalls++
			return true, nil
		},
		listPrivateCallRoom: func(context.Context, string) ([]*livekit.ParticipantInfo, error) {
			return nil, nil
		},
		loadPrivateCallInfo: func(context.Context, string, string) *domain.CallHistory {
			return &domain.CallHistory{
				RoomID:    "call_1_2_3",
				Status:    domain.CallStatusEnded,
				UpdatedAt: now.Add(-30 * time.Second),
				CreatedAt: now.Add(-40 * time.Second),
			}
		},
		privateCallGrace: 15 * time.Second,
		now:              func() time.Time { return now },
	}

	h.cleanupPrivateCallRoom(context.Background(), "call-id-1", "call_1_2_3", "end_after_accept")

	if deleteCalls != 1 {
		t.Fatalf("deletePrivateCallRoom() calls = %d, want 1", deleteCalls)
	}
}
