package service

import (
	"context"
	"errors"
	"testing"

	"github.com/livekit/protocol/livekit"
	"go.uber.org/zap"
)

type roomCleanupFakeRoomClient struct {
	listParticipantsResponse *livekit.ListParticipantsResponse
	listParticipantsErr      error
	removeParticipantCalls   int
	deleteRoomCalls          int
	deleteRoomErr            error
}

func (f *roomCleanupFakeRoomClient) RemoveParticipant(context.Context, *livekit.RoomParticipantIdentity) (*livekit.RemoveParticipantResponse, error) {
	f.removeParticipantCalls++
	return &livekit.RemoveParticipantResponse{}, nil
}

func (f *roomCleanupFakeRoomClient) ListParticipants(context.Context, *livekit.ListParticipantsRequest) (*livekit.ListParticipantsResponse, error) {
	if f.listParticipantsErr != nil {
		return nil, f.listParticipantsErr
	}
	if f.listParticipantsResponse == nil {
		return &livekit.ListParticipantsResponse{}, nil
	}
	return f.listParticipantsResponse, nil
}

func (f *roomCleanupFakeRoomClient) ListRooms(context.Context, *livekit.ListRoomsRequest) (*livekit.ListRoomsResponse, error) {
	return &livekit.ListRoomsResponse{}, nil
}

func (f *roomCleanupFakeRoomClient) UpdateParticipant(context.Context, *livekit.UpdateParticipantRequest) (*livekit.ParticipantInfo, error) {
	return &livekit.ParticipantInfo{}, nil
}

func (f *roomCleanupFakeRoomClient) MutePublishedTrack(context.Context, *livekit.MuteRoomTrackRequest) (*livekit.MuteRoomTrackResponse, error) {
	return &livekit.MuteRoomTrackResponse{}, nil
}

func (f *roomCleanupFakeRoomClient) DeleteRoom(context.Context, *livekit.DeleteRoomRequest) (*livekit.DeleteRoomResponse, error) {
	f.deleteRoomCalls++
	if f.deleteRoomErr != nil {
		return nil, f.deleteRoomErr
	}
	return &livekit.DeleteRoomResponse{}, nil
}

func TestDeleteRoomIfExistsDeletesRoomWithoutRemovingParticipants(t *testing.T) {
	t.Parallel()

	roomClient := &roomCleanupFakeRoomClient{}
	svc := &LiveKitService{roomClient: roomClient, logger: zap.NewNop()}

	deleted, err := svc.DeleteRoomIfExists(context.Background(), "call_1_2_3")
	if err != nil {
		t.Fatalf("DeleteRoomIfExists() error = %v", err)
	}
	if !deleted {
		t.Fatal("DeleteRoomIfExists() reported room as not deleted")
	}
	if roomClient.deleteRoomCalls != 1 {
		t.Fatalf("DeleteRoom() calls = %d, want 1", roomClient.deleteRoomCalls)
	}
	if roomClient.removeParticipantCalls != 0 {
		t.Fatalf("RemoveParticipant() calls = %d, want 0", roomClient.removeParticipantCalls)
	}
}

func TestDeleteRoomIfExistsTreatsRoomNotFoundAsIdempotent(t *testing.T) {
	t.Parallel()

	roomClient := &roomCleanupFakeRoomClient{deleteRoomErr: errors.New("twirp error not_found: room does not exist")}
	svc := &LiveKitService{roomClient: roomClient, logger: zap.NewNop()}

	deleted, err := svc.DeleteRoomIfExists(context.Background(), "call_1_2_3")
	if err != nil {
		t.Fatalf("DeleteRoomIfExists() error = %v, want nil", err)
	}
	if deleted {
		t.Fatal("DeleteRoomIfExists() reported deleted=true for missing room")
	}
	if roomClient.deleteRoomCalls != 1 {
		t.Fatalf("DeleteRoom() calls = %d, want 1", roomClient.deleteRoomCalls)
	}
}

func TestKickAllParticipantsStillRemovesParticipantsForTargetedCleanup(t *testing.T) {
	t.Parallel()

	roomClient := &roomCleanupFakeRoomClient{
		listParticipantsResponse: &livekit.ListParticipantsResponse{
			Participants: []*livekit.ParticipantInfo{
				{Identity: "user-1"},
				{Identity: "user-2"},
			},
		},
	}
	svc := &LiveKitService{roomClient: roomClient, logger: zap.NewNop()}

	kicked, err := svc.KickAllParticipants(context.Background(), "call_group_1")
	if err != nil {
		t.Fatalf("KickAllParticipants() error = %v", err)
	}
	if kicked != 2 {
		t.Fatalf("KickAllParticipants() kicked = %d, want 2", kicked)
	}
	if roomClient.removeParticipantCalls != 2 {
		t.Fatalf("RemoveParticipant() calls = %d, want 2", roomClient.removeParticipantCalls)
	}
	if roomClient.deleteRoomCalls != 0 {
		t.Fatalf("DeleteRoom() calls = %d, want 0", roomClient.deleteRoomCalls)
	}
}
