package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/livekit/protocol/livekit"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

func TestCanonicalPersonalRecordingRoomUsesCallRecordRoom(t *testing.T) {
	room, mismatch := canonicalPersonalRecordingRoom(&domain.CallHistory{RoomID: "call_60_9_123"}, "call_9_60_123")
	if room != "call_60_9_123" {
		t.Fatalf("expected call record room, got %q", room)
	}
	if !mismatch {
		t.Fatalf("expected room mismatch to be reported")
	}
}

func TestCanonicalPersonalRecordingRoomKeepsMatchingRequest(t *testing.T) {
	room, mismatch := canonicalPersonalRecordingRoom(&domain.CallHistory{RoomID: "call_60_9_123"}, "call_60_9_123")
	if room != "call_60_9_123" {
		t.Fatalf("expected call record room, got %q", room)
	}
	if mismatch {
		t.Fatalf("did not expect room mismatch")
	}
}

func TestPersonalRecordingResponseAddsDownloadURLForCompletedRecording(t *testing.T) {
	id := uuid.New()
	callID := uuid.New()
	filePath := "recordings/personal/" + callID.String() + "/38-1778492205.ogg"
	objectExists := true
	rec := domain.PersonalCallRecording{
		ID:                id,
		CallID:            callID,
		RoomName:          "call_38_60_1778492172",
		OwnerUserID:       38,
		EgressID:          "EG_CikzPgXrFBVA",
		Status:            domain.PersonalRecordingStatusCompleted,
		StartedAt:         time.Now(),
		FilePath:          &filePath,
		MinIOObjectExists: &objectExists,
	}

	resp := toPersonalRecordingResponse(rec)
	if resp.DownloadURL == nil {
		t.Fatalf("expected download_url for completed recording")
	}
	expected := "/api/v1/calls/recordings/personal/" + id.String() + "/download"
	if *resp.DownloadURL != expected {
		t.Fatalf("expected download_url %q, got %q", expected, *resp.DownloadURL)
	}
}

func TestPersonalRecordingResponseOmitsDownloadURLForActiveRecording(t *testing.T) {
	filePath := "recordings/personal/call-id/38-1778492205.ogg"
	rec := domain.PersonalCallRecording{
		ID:        uuid.New(),
		CallID:    uuid.New(),
		Status:    domain.PersonalRecordingStatusActive,
		StartedAt: time.Now(),
		FilePath:  &filePath,
	}

	resp := toPersonalRecordingResponse(rec)
	if resp.DownloadURL != nil {
		t.Fatalf("did not expect download_url for active recording")
	}
	if resp.FilePath != nil {
		t.Fatalf("did not expect file_path for active recording")
	}
}

func TestPersonalRecordingResponseOmitsDownloadURLForProcessingRecording(t *testing.T) {
	filePath := "recordings/personal/call-id/38-1778492205.ogg"
	rec := domain.PersonalCallRecording{
		ID:        uuid.New(),
		CallID:    uuid.New(),
		Status:    domain.PersonalRecordingStatusStopping,
		StartedAt: time.Now(),
		FilePath:  &filePath,
	}

	resp := toPersonalRecordingResponse(rec)
	if resp.DownloadURL != nil {
		t.Fatalf("did not expect download_url while egress output is processing")
	}
	if resp.FilePath != nil {
		t.Fatalf("did not expect file_path while egress output is processing")
	}
}

type fakePersonalRecordingRepo struct {
	mu           sync.Mutex
	record       *domain.PersonalCallRecording
	markCalls    int
	markResult   bool
	updateCalls  []domain.PersonalRecordingStatus
	getByIDCalls int
}

func (f *fakePersonalRecordingRepo) Create(context.Context, *domain.PersonalCallRecording) error {
	return nil
}
func (f *fakePersonalRecordingRepo) GetLockedByCallIDAndOwner(context.Context, uuid.UUID, int64) (*domain.PersonalCallRecording, error) {
	return nil, nil
}
func (f *fakePersonalRecordingRepo) GetByEgressID(context.Context, string) (*domain.PersonalCallRecording, error) {
	return nil, nil
}
func (f *fakePersonalRecordingRepo) CountLiveByCallID(context.Context, uuid.UUID) (int, []int64, error) {
	return 0, nil, nil
}
func (f *fakePersonalRecordingRepo) ListForOwner(context.Context, int64, int, int) ([]domain.PersonalCallRecording, error) {
	return nil, nil
}
func (f *fakePersonalRecordingRepo) ListStaleLocked(context.Context, time.Time, int) ([]domain.PersonalCallRecording, error) {
	return nil, nil
}
func (f *fakePersonalRecordingRepo) GetByID(_ context.Context, id uuid.UUID) (*domain.PersonalCallRecording, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getByIDCalls++
	if f.record == nil || f.record.ID != id {
		return nil, nil
	}
	copy := *f.record
	return &copy, nil
}
func (f *fakePersonalRecordingRepo) UpdateStatus(_ context.Context, id uuid.UUID, status domain.PersonalRecordingStatus, _ *string, _ *int, _ *string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updateCalls = append(f.updateCalls, status)
	if f.record != nil && f.record.ID == id {
		f.record.Status = status
		now := time.Now()
		f.record.StoppedAt = &now
	}
	return nil
}
func (f *fakePersonalRecordingRepo) UpdateResult(_ context.Context, id uuid.UUID, status domain.PersonalRecordingStatus, _ *string, _ *int, _ *string, _ *int, _ *string, _ *string, _ *bool) error {
	return f.UpdateStatus(context.Background(), id, status, nil, nil, nil)
}
func (f *fakePersonalRecordingRepo) MarkStoppingIfLive(_ context.Context, id uuid.UUID) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.markCalls++
	if !f.markResult || f.record == nil || f.record.ID != id {
		return false, nil
	}
	f.record.Status = domain.PersonalRecordingStatusStopping
	now := time.Now()
	f.record.StoppedAt = &now
	return true, nil
}

type fakePersonalRecordingService struct {
	stopResult service.PersonalRecordingStopResult
	stopErr    error
	stopCalls  int
}

func (f *fakePersonalRecordingService) StartPersonalAudioRecording(context.Context, string, string, int64, string) (string, string, error) {
	return "", "", nil
}
func (f *fakePersonalRecordingService) WaitForPersonalRecordingActive(context.Context, string) error {
	return nil
}
func (f *fakePersonalRecordingService) GetEgressInfo(context.Context, string) (*livekit.EgressInfo, error) {
	return &livekit.EgressInfo{Status: livekit.EgressStatus_EGRESS_ACTIVE}, nil
}
func (f *fakePersonalRecordingService) ListParticipants(context.Context, string) ([]*livekit.ParticipantInfo, error) {
	return nil, nil
}
func (f *fakePersonalRecordingService) GetAPIURL() string { return "http://100.72.193.179:7880" }
func (f *fakePersonalRecordingService) GetClientURL() string {
	return "wss://alem-workspace.gov.kz/livekit"
}
func (f *fakePersonalRecordingService) StopPersonalRecordingEgress(context.Context, string) (service.PersonalRecordingStopResult, error) {
	f.stopCalls++
	return f.stopResult, f.stopErr
}
func (f *fakePersonalRecordingService) MinIOEgressEndpoint() string { return "" }

func executeStopPersonalRecording(t *testing.T, h *PersonalRecordingHandler, recordingID uuid.UUID, userID int64) StopPersonalRecordingResponse {
	t.Helper()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/calls/recordings/personal/"+recordingID.String()+"/stop", nil)
	ctx.Params = gin.Params{{Key: "recording_id", Value: recordingID.String()}}
	ctx.Set("user_id", userID)

	h.StopPersonalRecording(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d body=%s", recorder.Code, recorder.Body.String())
	}

	var resp StopPersonalRecordingResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	return resp
}

func TestDownloadPersonalRecordingRejectsNonCompletedRecording(t *testing.T) {
	recordingID := uuid.New()
	repo := &fakePersonalRecordingRepo{
		record: &domain.PersonalCallRecording{
			ID:          recordingID,
			OwnerUserID: 42,
			Status:      domain.PersonalRecordingStatusFailed,
		},
	}

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/calls/recordings/personal/"+recordingID.String()+"/download", nil)
	ctx.Params = gin.Params{{Key: "recording_id", Value: recordingID.String()}}
	ctx.Set("user_id", int64(42))

	h := &PersonalRecordingHandler{repo: repo, logger: zap.NewNop()}
	h.DownloadPersonalRecording(ctx)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestStopPersonalRecordingReturnsStoppingForActiveEgress(t *testing.T) {
	recordingID := uuid.New()
	repo := &fakePersonalRecordingRepo{
		record: &domain.PersonalCallRecording{
			ID:          recordingID,
			OwnerUserID: 42,
			EgressID:    "EG_1",
			Status:      domain.PersonalRecordingStatusActive,
			StartedAt:   time.Now().Add(-2 * time.Minute),
		},
		markResult: true,
	}
	svc := &fakePersonalRecordingService{
		stopResult: service.PersonalRecordingStopResult{Disposition: service.PersonalRecordingStopDispositionStopped, FinalStatus: domain.PersonalRecordingStatusStopping},
	}
	h := &PersonalRecordingHandler{service: svc, repo: repo, logger: zap.NewNop()}

	resp := executeStopPersonalRecording(t, h, recordingID, 42)
	if !resp.Success || resp.Status != string(domain.PersonalRecordingStatusStopping) {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if svc.stopCalls != 1 {
		t.Fatalf("StopPersonalRecordingEgress() calls = %d, want 1", svc.stopCalls)
	}
}

func TestStopPersonalRecordingReturnsAlreadyStoppedForTerminalRecord(t *testing.T) {
	recordingID := uuid.New()
	repo := &fakePersonalRecordingRepo{
		record: &domain.PersonalCallRecording{
			ID:          recordingID,
			OwnerUserID: 42,
			EgressID:    "EG_1",
			Status:      domain.PersonalRecordingStatusStopped,
		},
	}
	h := &PersonalRecordingHandler{service: &fakePersonalRecordingService{}, repo: repo, logger: zap.NewNop()}

	resp := executeStopPersonalRecording(t, h, recordingID, 42)
	if !resp.Success || resp.Status != "failed" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestStopPersonalRecordingTreatsAlreadyStoppedLiveKitAsSuccess(t *testing.T) {
	recordingID := uuid.New()
	repo := &fakePersonalRecordingRepo{
		record: &domain.PersonalCallRecording{
			ID:          recordingID,
			OwnerUserID: 42,
			EgressID:    "EG_1",
			Status:      domain.PersonalRecordingStatusActive,
			StartedAt:   time.Now().Add(-time.Minute),
		},
		markResult: true,
	}
	svc := &fakePersonalRecordingService{
		stopResult: service.PersonalRecordingStopResult{
			Disposition:         service.PersonalRecordingStopDispositionAlreadyStopped,
			FinalStatus:         domain.PersonalRecordingStatusFailed,
			Reason:              "not_found",
			LiveKitErrorCode:    404,
			LiveKitErrorMessage: "twirp error not_found",
		},
	}
	h := &PersonalRecordingHandler{service: svc, repo: repo, logger: zap.NewNop()}

	resp := executeStopPersonalRecording(t, h, recordingID, 42)
	if !resp.Success || resp.Status != string(domain.PersonalRecordingStatusFailed) {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if len(repo.updateCalls) == 0 || repo.updateCalls[len(repo.updateCalls)-1] != domain.PersonalRecordingStatusFailed {
		t.Fatalf("expected failed status update, got %+v", repo.updateCalls)
	}
}

func TestStopPersonalRecordingTreatsStartSignalErrorAsSuccess(t *testing.T) {
	recordingID := uuid.New()
	repo := &fakePersonalRecordingRepo{
		record: &domain.PersonalCallRecording{
			ID:          recordingID,
			OwnerUserID: 42,
			EgressID:    "EG_1",
			Status:      domain.PersonalRecordingStatusActive,
			StartedAt:   time.Now().Add(-time.Minute),
		},
		markResult: true,
	}
	svc := &fakePersonalRecordingService{
		stopResult: service.PersonalRecordingStopResult{
			Disposition:         service.PersonalRecordingStopDispositionAborted,
			FinalStatus:         domain.PersonalRecordingStatusAborted,
			Reason:              "not_started",
			LiveKitErrorCode:    412,
			LiveKitErrorMessage: "Start signal not received",
		},
	}
	h := &PersonalRecordingHandler{service: svc, repo: repo, logger: zap.NewNop()}

	resp := executeStopPersonalRecording(t, h, recordingID, 42)
	if !resp.Success || resp.Status != string(domain.PersonalRecordingStatusAborted) {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if len(repo.updateCalls) == 0 || repo.updateCalls[len(repo.updateCalls)-1] != domain.PersonalRecordingStatusAborted {
		t.Fatalf("expected aborted status update, got %+v", repo.updateCalls)
	}
}

func TestStopPersonalRecordingDoubleStopDoesNotRepeatStopEgress(t *testing.T) {
	recordingID := uuid.New()
	repo := &fakePersonalRecordingRepo{
		record: &domain.PersonalCallRecording{
			ID:          recordingID,
			OwnerUserID: 42,
			EgressID:    "EG_1",
			Status:      domain.PersonalRecordingStatusStopping,
		},
	}
	svc := &fakePersonalRecordingService{}
	h := &PersonalRecordingHandler{service: svc, repo: repo, logger: zap.NewNop()}

	resp := executeStopPersonalRecording(t, h, recordingID, 42)
	if !resp.Success || resp.Status != "stopping" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if svc.stopCalls != 0 {
		t.Fatalf("StopPersonalRecordingEgress() calls = %d, want 0", svc.stopCalls)
	}
}
