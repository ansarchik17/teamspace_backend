package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/livekit/protocol/livekit"
	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

type fakeRoomClient struct {
	responses []*livekit.ListParticipantsResponse
	err       error
	calls     int
}

func (f *fakeRoomClient) ListParticipants(context.Context, *livekit.ListParticipantsRequest) (*livekit.ListParticipantsResponse, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	if len(f.responses) == 0 {
		return &livekit.ListParticipantsResponse{}, nil
	}
	idx := f.calls - 1
	if idx >= len(f.responses) {
		idx = len(f.responses) - 1
	}
	return f.responses[idx], nil
}

func (f *fakeRoomClient) ListRooms(context.Context, *livekit.ListRoomsRequest) (*livekit.ListRoomsResponse, error) {
	return &livekit.ListRoomsResponse{}, nil
}

func (f *fakeRoomClient) RemoveParticipant(context.Context, *livekit.RoomParticipantIdentity) (*livekit.RemoveParticipantResponse, error) {
	return &livekit.RemoveParticipantResponse{}, nil
}

func (f *fakeRoomClient) UpdateParticipant(context.Context, *livekit.UpdateParticipantRequest) (*livekit.ParticipantInfo, error) {
	return &livekit.ParticipantInfo{}, nil
}

func (f *fakeRoomClient) MutePublishedTrack(context.Context, *livekit.MuteRoomTrackRequest) (*livekit.MuteRoomTrackResponse, error) {
	return &livekit.MuteRoomTrackResponse{}, nil
}

func (f *fakeRoomClient) DeleteRoom(context.Context, *livekit.DeleteRoomRequest) (*livekit.DeleteRoomResponse, error) {
	return &livekit.DeleteRoomResponse{}, nil
}

type fakeEgressClient struct {
	startTrackErr  error
	startRoomErr   error
	status         livekit.EgressStatus
	statusError    string
	listErr        error
	stopErr        error
	preserveStatus bool
	request        *livekit.TrackEgressRequest
	roomRequest    *livekit.RoomCompositeEgressRequest
	startCalls     int
	startRoomCalls int
	listCalls      int
	stopCalls      int
}

func (f *fakeEgressClient) StartRoomCompositeEgress(_ context.Context, req *livekit.RoomCompositeEgressRequest) (*livekit.EgressInfo, error) {
	f.startRoomCalls++
	f.roomRequest = req
	if f.startRoomErr != nil {
		return nil, f.startRoomErr
	}
	return &livekit.EgressInfo{EgressId: "room-egress", Status: livekit.EgressStatus_EGRESS_ACTIVE}, nil
}

func (f *fakeEgressClient) StartTrackEgress(_ context.Context, req *livekit.TrackEgressRequest) (*livekit.EgressInfo, error) {
	f.startCalls++
	f.request = req
	if f.startTrackErr != nil {
		return nil, f.startTrackErr
	}
	return &livekit.EgressInfo{EgressId: "track-egress", RoomName: req.RoomName, Status: livekit.EgressStatus_EGRESS_STARTING}, nil
}

func (f *fakeEgressClient) ListEgress(context.Context, *livekit.ListEgressRequest) (*livekit.ListEgressResponse, error) {
	f.listCalls++
	if f.listErr != nil {
		return nil, f.listErr
	}
	status := f.status
	if status == livekit.EgressStatus_EGRESS_STARTING && !f.preserveStatus {
		status = livekit.EgressStatus_EGRESS_ACTIVE
	}
	return &livekit.ListEgressResponse{Items: []*livekit.EgressInfo{
		{EgressId: "room-egress", Status: status, Error: f.statusError},
	}}, nil
}

func (f *fakeEgressClient) StopEgress(context.Context, *livekit.StopEgressRequest) (*livekit.EgressInfo, error) {
	f.stopCalls++
	if f.stopErr != nil {
		return nil, f.stopErr
	}
	return &livekit.EgressInfo{}, nil
}

func personalRecordingTestService(room *fakeRoomClient, egress *fakeEgressClient) *LiveKitService {
	return &LiveKitService{
		roomClient:   room,
		egressClient: egress,
		minioCfg: config.MinIOConfig{
			Endpoint:  "http://minio:9000",
			Bucket:    "recordings",
			AccessKey: "test",
			SecretKey: "test",
		},
		logger: zap.NewNop(),
	}
}

func withFastPersonalRecordingRetries(t *testing.T) {
	t.Helper()
	oldRetries := personalRecordingMaxRetries
	oldInterval := personalRecordingRetryInterval
	oldVerifyAttempts := egressStartVerifyAttempts
	oldVerifyInterval := egressStartVerifyInterval
	personalRecordingMaxRetries = 3
	personalRecordingRetryInterval = time.Millisecond
	egressStartVerifyAttempts = 1
	egressStartVerifyInterval = time.Millisecond
	t.Cleanup(func() {
		personalRecordingMaxRetries = oldRetries
		personalRecordingRetryInterval = oldInterval
		egressStartVerifyAttempts = oldVerifyAttempts
		egressStartVerifyInterval = oldVerifyInterval
	})
}

func audioParticipant(identity string, tracks ...*livekit.TrackInfo) *livekit.ParticipantInfo {
	return &livekit.ParticipantInfo{
		Identity: identity,
		Name:     "User " + identity,
		Tracks:   tracks,
	}
}

func audioTrack(sid string, muted bool) *livekit.TrackInfo {
	return &livekit.TrackInfo{
		Sid:    sid,
		Name:   "microphone",
		Type:   livekit.TrackType_AUDIO,
		Source: livekit.TrackSource_MICROPHONE,
		Muted:  muted,
	}
}

func TestStartPersonalAudioRecordingParticipantStartsTrackAudioEgress(t *testing.T) {
	withFastPersonalRecordingRetries(t)

	room := &fakeRoomClient{responses: []*livekit.ListParticipantsResponse{
		{Participants: []*livekit.ParticipantInfo{audioParticipant("9:session-a", audioTrack("TR_AUDIO", false))}},
	}}
	egress := &fakeEgressClient{status: livekit.EgressStatus_EGRESS_ACTIVE}
	svc := personalRecordingTestService(room, egress)

	egressID, participantIdentity, err := svc.StartPersonalAudioRecording(context.Background(), "call-id", "call_60_9_123", 9, "recordings/personal/call-id/9.ogg")
	if err != nil {
		t.Fatalf("StartPersonalAudioRecording() error = %v", err)
	}
	if egressID != "track-egress" {
		t.Fatalf("unexpected egress id: %s", egressID)
	}
	if participantIdentity != "9:session-a" {
		t.Fatalf("unexpected participant identity: %s", participantIdentity)
	}
	if egress.request == nil || egress.request.RoomName != "call_60_9_123" || egress.request.TrackId != "TR_AUDIO" {
		t.Fatalf("unexpected track egress request: %#v", egress.request)
	}
	if egress.request.GetFile() == nil || egress.request.GetFile().Filepath != "recordings/personal/call-id/9.ogg" {
		t.Fatalf("unexpected file output: %#v", egress.request.GetFile())
	}
	if egress.startRoomCalls != 0 {
		t.Fatalf("StartRoomCompositeEgress() calls = %d, want 0", egress.startRoomCalls)
	}
}

func TestStartPersonalAudioRecordingRetriesUntilParticipantAppears(t *testing.T) {
	withFastPersonalRecordingRetries(t)

	room := &fakeRoomClient{responses: []*livekit.ListParticipantsResponse{
		{Participants: []*livekit.ParticipantInfo{}},
		{Participants: []*livekit.ParticipantInfo{audioParticipant("9:session-a", audioTrack("TR_AUDIO", false))}},
	}}
	egress := &fakeEgressClient{status: livekit.EgressStatus_EGRESS_ACTIVE}
	svc := personalRecordingTestService(room, egress)

	_, participantIdentity, err := svc.StartPersonalAudioRecording(context.Background(), "call-id", "call_60_9_123", 9, "recordings/personal/call-id/9.ogg")
	if err != nil {
		t.Fatalf("StartPersonalAudioRecording() error = %v", err)
	}
	if participantIdentity != "9:session-a" {
		t.Fatalf("unexpected participant identity: %s", participantIdentity)
	}
	if room.calls < 2 {
		t.Fatalf("expected retry, got %d ListParticipants calls", room.calls)
	}
}

func TestStartPersonalAudioRecordingReturnsMutedErrorWhenAudioNeverReady(t *testing.T) {
	withFastPersonalRecordingRetries(t)

	room := &fakeRoomClient{responses: []*livekit.ListParticipantsResponse{
		{Participants: []*livekit.ParticipantInfo{audioParticipant("9:session-a", audioTrack("TR_AUDIO", true))}},
	}}
	egress := &fakeEgressClient{status: livekit.EgressStatus_EGRESS_ACTIVE}
	svc := personalRecordingTestService(room, egress)

	_, _, err := svc.StartPersonalAudioRecording(context.Background(), "call-id", "call_60_9_123", 9, "recordings/personal/call-id/9.ogg")
	if !errors.Is(err, ErrPersonalRecordingAudioTrackMuted) {
		t.Fatalf("expected ErrPersonalRecordingAudioTrackMuted, got %v", err)
	}
	if egress.startCalls != 0 {
		t.Fatalf("egress should not start without ready audio track")
	}
	if egress.startRoomCalls != 0 {
		t.Fatalf("StartRoomCompositeEgress() calls = %d, want 0", egress.startRoomCalls)
	}
}

func TestStartPersonalAudioRecordingReturnsTrackNotFoundWhenAudioMissing(t *testing.T) {
	withFastPersonalRecordingRetries(t)

	room := &fakeRoomClient{responses: []*livekit.ListParticipantsResponse{
		{Participants: []*livekit.ParticipantInfo{audioParticipant("9:session-a")}},
	}}
	egress := &fakeEgressClient{status: livekit.EgressStatus_EGRESS_ACTIVE}
	svc := personalRecordingTestService(room, egress)

	_, _, err := svc.StartPersonalAudioRecording(context.Background(), "call-id", "call_60_9_123", 9, "recordings/personal/call-id/9.ogg")
	if !errors.Is(err, ErrPersonalRecordingAudioTrackNotFound) {
		t.Fatalf("expected ErrPersonalRecordingAudioTrackNotFound, got %v", err)
	}
	if egress.startCalls != 0 {
		t.Fatalf("egress should not start without audio track")
	}
	if egress.startRoomCalls != 0 {
		t.Fatalf("StartRoomCompositeEgress() calls = %d, want 0", egress.startRoomCalls)
	}
}

func TestStartPersonalAudioRecordingNoParticipantReturnsConflictError(t *testing.T) {
	withFastPersonalRecordingRetries(t)

	room := &fakeRoomClient{responses: []*livekit.ListParticipantsResponse{
		{Participants: []*livekit.ParticipantInfo{audioParticipant("10:session-a")}},
	}}
	egress := &fakeEgressClient{status: livekit.EgressStatus_EGRESS_ACTIVE}
	svc := personalRecordingTestService(room, egress)

	_, _, err := svc.StartPersonalAudioRecording(context.Background(), "call-id", "call_60_9_123", 9, "recordings/personal/call-id/9.ogg")
	if !errors.Is(err, ErrPersonalRecordingParticipantNotFound) {
		t.Fatalf("expected ErrPersonalRecordingParticipantNotFound, got %v", err)
	}
	if egress.startCalls != 0 {
		t.Fatalf("egress should not start without participant")
	}
	if egress.startRoomCalls != 0 {
		t.Fatalf("StartRoomCompositeEgress() calls = %d, want 0", egress.startRoomCalls)
	}
}

func TestStartPersonalAudioRecordingUsesUnifiedIdentityMapping(t *testing.T) {
	withFastPersonalRecordingRetries(t)

	identity := BuildParticipantIdentity(9, "web-session")
	room := &fakeRoomClient{responses: []*livekit.ListParticipantsResponse{
		{Participants: []*livekit.ParticipantInfo{audioParticipant(identity, audioTrack("TR_AUDIO", false))}},
	}}
	egress := &fakeEgressClient{status: livekit.EgressStatus_EGRESS_ACTIVE}
	svc := personalRecordingTestService(room, egress)

	_, participantIdentity, err := svc.StartPersonalAudioRecording(context.Background(), "call-id", "call_60_9_123", 9, "recordings/personal/call-id/9.ogg")
	if err != nil {
		t.Fatalf("StartPersonalAudioRecording() error = %v", err)
	}
	if participantIdentity != identity {
		t.Fatalf("unexpected participant identity: %s", participantIdentity)
	}
}

func TestStartPersonalAudioRecordingImmediateEgressAbortReturnsError(t *testing.T) {
	withFastPersonalRecordingRetries(t)

	room := &fakeRoomClient{responses: []*livekit.ListParticipantsResponse{
		{Participants: []*livekit.ParticipantInfo{audioParticipant("9:session-a", audioTrack("TR_AUDIO", false))}},
	}}
	egress := &fakeEgressClient{
		status:      livekit.EgressStatus_EGRESS_ABORTED,
		statusError: "Start signal not received",
	}
	svc := personalRecordingTestService(room, egress)

	egressID, _, err := svc.StartPersonalAudioRecording(context.Background(), "call-id", "call_60_9_123", 9, "recordings/personal/call-id/9.ogg")
	if err != nil {
		t.Fatalf("unexpected start error: %v", err)
	}
	err = svc.WaitForPersonalRecordingActive(context.Background(), egressID)
	if err == nil {
		t.Fatalf("expected egress abort error")
	}
	if !strings.Contains(err.Error(), "Start signal not received") {
		t.Fatalf("expected start signal error, got %v", err)
	}
}

func TestStopPersonalRecordingEgressStopsActiveEgress(t *testing.T) {
	egress := &fakeEgressClient{status: livekit.EgressStatus_EGRESS_ACTIVE}
	svc := personalRecordingTestService(&fakeRoomClient{}, egress)

	result, err := svc.StopPersonalRecordingEgress(context.Background(), "room-egress")
	if err != nil {
		t.Fatalf("StopPersonalRecordingEgress() error = %v", err)
	}
	if result.Disposition != PersonalRecordingStopDispositionStopped {
		t.Fatalf("unexpected disposition: %s", result.Disposition)
	}
	if egress.stopCalls != 1 {
		t.Fatalf("StopEgress() calls = %d, want 1", egress.stopCalls)
	}
}

func TestStopPersonalRecordingEgressTreatsNotFoundAsAlreadyStopped(t *testing.T) {
	egress := &fakeEgressClient{listErr: errors.New("twirp error not_found: egress does not exist")}
	svc := personalRecordingTestService(&fakeRoomClient{}, egress)

	result, err := svc.StopPersonalRecordingEgress(context.Background(), "room-egress")
	if err != nil {
		t.Fatalf("StopPersonalRecordingEgress() error = %v", err)
	}
	if result.Disposition != PersonalRecordingStopDispositionAlreadyStopped {
		t.Fatalf("unexpected disposition: %s", result.Disposition)
	}
	if result.LiveKitErrorCode != 404 {
		t.Fatalf("unexpected livekit error code: %d", result.LiveKitErrorCode)
	}
}

func TestStopPersonalRecordingEgressTreatsStartSignalErrorAsAborted(t *testing.T) {
	egress := &fakeEgressClient{
		status:  livekit.EgressStatus_EGRESS_ACTIVE,
		stopErr: errors.New("egress_aborted error=\"Start signal not received\" code=412 details=\"End reason: StopEgress API\""),
	}
	svc := personalRecordingTestService(&fakeRoomClient{}, egress)

	result, err := svc.StopPersonalRecordingEgress(context.Background(), "room-egress")
	if err != nil {
		t.Fatalf("StopPersonalRecordingEgress() error = %v", err)
	}
	if result.Disposition != PersonalRecordingStopDispositionAborted {
		t.Fatalf("unexpected disposition: %s", result.Disposition)
	}
	if result.LiveKitErrorCode != 412 {
		t.Fatalf("unexpected livekit error code: %d", result.LiveKitErrorCode)
	}
}

func TestStopPersonalRecordingEgressTreatsStartingAsAbortedWithoutStopCall(t *testing.T) {
	egress := &fakeEgressClient{status: livekit.EgressStatus_EGRESS_STARTING, preserveStatus: true}
	svc := personalRecordingTestService(&fakeRoomClient{}, egress)

	result, err := svc.StopPersonalRecordingEgress(context.Background(), "room-egress")
	if err != nil {
		t.Fatalf("StopPersonalRecordingEgress() error = %v", err)
	}
	if result.Disposition != PersonalRecordingStopDispositionStopped {
		t.Fatalf("unexpected disposition: %s", result.Disposition)
	}
	if result.FinalStatus != domain.PersonalRecordingStatusStarting {
		t.Fatalf("unexpected final status: %s", result.FinalStatus)
	}
	if egress.stopCalls != 0 {
		t.Fatalf("StopEgress() calls = %d, want 0", egress.stopCalls)
	}
}

// ---------------------------------------------------------------------------
// Bare identity format: Flutter may join as "35" (no session suffix).
// StartPersonalAudioRecording must still find the participant and their track.
// ---------------------------------------------------------------------------

func TestStartPersonalAudioRecordingBareIdentityFormat(t *testing.T) {
	withFastPersonalRecordingRetries(t)

	// Participant joined with bare userID identity (no ":session" suffix).
	// This is a valid identity format that Flutter clients may produce.
	room := &fakeRoomClient{responses: []*livekit.ListParticipantsResponse{
		{Participants: []*livekit.ParticipantInfo{
			audioParticipant("35", audioTrack("TR_BARE", false)),
		}},
	}}
	egress := &fakeEgressClient{status: livekit.EgressStatus_EGRESS_ACTIVE}
	svc := personalRecordingTestService(room, egress)

	egressID, participantIdentity, err := svc.StartPersonalAudioRecording(
		context.Background(), "call-id", "call_35_38_123", 35, "recordings/personal/call-id/35.ogg",
	)
	if err != nil {
		t.Fatalf("StartPersonalAudioRecording() error = %v; bare identity '35' must be recognized", err)
	}
	if egressID != "track-egress" {
		t.Fatalf("unexpected egress id: %s", egressID)
	}
	if participantIdentity != "35" {
		t.Fatalf("unexpected participant identity: %s", participantIdentity)
	}
	if egress.request == nil || egress.request.TrackId != "TR_BARE" {
		t.Fatalf("unexpected track egress request: %#v", egress.request)
	}
	// GUARD: must never fall back to RoomCompositeEgress.
	if egress.startRoomCalls != 0 {
		t.Fatalf("StartRoomCompositeEgress() calls = %d, want 0; TrackEgress only", egress.startRoomCalls)
	}
}

// ---------------------------------------------------------------------------
// Invariant: StartPersonalAudioRecording must NEVER call StartRoomCompositeEgress.
// This is the core guard that prevents the Chrome/"start signal not received" failure.
// ---------------------------------------------------------------------------

func TestStartPersonalAudioRecordingNeverCallsRoomCompositeEgress(t *testing.T) {
	withFastPersonalRecordingRetries(t)

	scenarios := []struct {
		name       string
		identity   string
		trackMuted bool
		hasTrack   bool
	}{
		{"active_track", "9:session-a", false, true},
		{"muted_track", "9:session-a", true, true},
		{"no_track", "9:session-a", false, false},
		{"no_participant", "10:other", false, true},
		{"bare_identity", "9", false, true},
	}

	for _, sc := range scenarios {
		sc := sc
		t.Run(sc.name, func(t *testing.T) {
			t.Parallel()

			var tracks []*livekit.TrackInfo
			if sc.hasTrack {
				tracks = append(tracks, audioTrack("TR_X", sc.trackMuted))
			}
			room := &fakeRoomClient{responses: []*livekit.ListParticipantsResponse{
				{Participants: []*livekit.ParticipantInfo{audioParticipant(sc.identity, tracks...)}},
			}}
			egress := &fakeEgressClient{status: livekit.EgressStatus_EGRESS_ACTIVE}
			svc := personalRecordingTestService(room, egress)

			// We don't care whether this succeeds or fails — only that it never
			// calls StartRoomCompositeEgress (which would produce code=412).
			svc.StartPersonalAudioRecording( //nolint:errcheck
				context.Background(), "call-id", "call_60_9_123", 9, "recordings/personal/call-id/9.ogg",
			)

			if egress.startRoomCalls != 0 {
				t.Fatalf("scenario %q: StartRoomCompositeEgress() calls = %d, want 0",
					sc.name, egress.startRoomCalls)
			}
		})
	}
}
