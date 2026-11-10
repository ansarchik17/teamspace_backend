package handler

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/livekit/protocol/livekit"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

const (
	personalRecordingStartPollTimeout = 15 * time.Second

	personalRecordingPollInterval      = 750 * time.Millisecond
	personalRecordingCleanupTimeout    = 20 * time.Second
	personalRecordingReconcileAge      = 2 * time.Minute
	personalRecordingReconcileInterval = 1 * time.Minute
)

type personalRecordingEgressRuntime interface {
	GetEgressInfo(ctx context.Context, egressID string) (*livekit.EgressInfo, error)
}

type personalRecordingResultWriter interface {
	UpdateResult(
		ctx context.Context,
		id uuid.UUID,
		status domain.PersonalRecordingStatus,
		filePath *string,
		durationSec *int,
		failureReason *string,
		liveKitErrorCode *int,
		liveKitErrorMessage *string,
		endReason *string,
		minioObjectExists *bool,
	) error
}

type personalRecordingObjectStore interface {
	ObjectExists(ctx context.Context, objectName string) error
	BucketName() string
	Endpoint() string
}

type personalRecordingRuntimeState struct {
	status            domain.PersonalRecordingStatus
	liveKitStatus     string
	filePath          *string
	durationSec       *int
	failureReason     *string
	liveKitErrorCode  *int
	liveKitErrorMsg   *string
	endReason         *string
	minioObjectExists *bool
}

func personalRecordingIntPtr(v int) *int {
	return &v
}

func personalRecordingStringPtr(v string) *string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	value := v
	return &value
}

func personalRecordingBoolPtr(v bool) *bool {
	return &v
}

func personalRecordingErrorCodePtr(msg string) *int {
	msg = strings.ToLower(strings.TrimSpace(msg))
	if msg == "" {
		return nil
	}
	switch {
	case strings.Contains(msg, "code=412"), strings.Contains(msg, " 412 "), strings.Contains(msg, "status code 412"):
		code := 412
		return &code
	case strings.Contains(msg, "code=404"), strings.Contains(msg, "not_found"), strings.Contains(msg, "not found"):
		code := 404
		return &code
	default:
		return nil
	}
}

func personalRecordingEndReason(msg string) *string {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return nil
	}
	lower := strings.ToLower(msg)
	marker := "end reason:"
	idx := strings.Index(lower, marker)
	if idx >= 0 {
		reason := strings.TrimSpace(msg[idx+len(marker):])
		reason = strings.Trim(reason, "\"")
		if reason != "" {
			return &reason
		}
	}
	return nil
}

func personalRecordingFailureReason(state personalRecordingRuntimeState) *string {
	msg := ""
	if state.liveKitErrorMsg != nil {
		msg = strings.ToLower(strings.TrimSpace(*state.liveKitErrorMsg))
	}
	endReason := ""
	if state.endReason != nil {
		endReason = strings.ToLower(strings.TrimSpace(*state.endReason))
	}

	switch {
	case strings.Contains(msg, "start signal not received"):
		reason := "start_signal_not_received"
		return &reason
	case strings.Contains(msg, "source closed"), strings.Contains(endReason, "source closed"):
		reason := "source_closed"
		return &reason
	case strings.Contains(msg, "stop egress api"), strings.Contains(endReason, "stop egress api"):
		reason := "stop_egress_api"
		return &reason
	default:
		return state.failureReason
	}
}

func personalRecordingFileResult(info *livekit.EgressInfo) (*string, *int) {
	for _, fr := range info.GetFileResults() {
		if fr == nil || strings.TrimSpace(fr.Filename) == "" {
			continue
		}
		name := strings.TrimSpace(fr.Filename)
		var durationSec *int
		if fr.Duration > 0 {
			d := int(fr.Duration / int64(time.Second))
			durationSec = &d
		}
		return &name, durationSec
	}
	return nil, nil
}

func personalRecordingStateFromEgressInfo(info *livekit.EgressInfo) personalRecordingRuntimeState {
	state := personalRecordingRuntimeState{liveKitStatus: info.Status.String()}
	if strings.TrimSpace(info.Error) != "" {
		msg := strings.TrimSpace(info.Error)
		state.liveKitErrorMsg = &msg
		state.liveKitErrorCode = personalRecordingErrorCodePtr(msg)
		state.endReason = personalRecordingEndReason(msg)
	}
	state.filePath, state.durationSec = personalRecordingFileResult(info)

	switch info.Status {
	case livekit.EgressStatus_EGRESS_ACTIVE:
		state.status = domain.PersonalRecordingStatusActive
	case livekit.EgressStatus_EGRESS_STARTING:
		state.status = domain.PersonalRecordingStatusStarting
	case livekit.EgressStatus_EGRESS_COMPLETE:
		state.status = domain.PersonalRecordingStatusCompleted
	case livekit.EgressStatus_EGRESS_ABORTED:
		state.status = domain.PersonalRecordingStatusAborted
		state.failureReason = personalRecordingFailureReason(state)
	case livekit.EgressStatus_EGRESS_FAILED, livekit.EgressStatus_EGRESS_LIMIT_REACHED:
		state.status = domain.PersonalRecordingStatusFailed
		state.failureReason = personalRecordingFailureReason(state)
	default:
		state.status = domain.PersonalRecordingStatusFailed
		reason := fmt.Sprintf("unexpected egress status: %s", info.Status.String())
		state.failureReason = &reason
	}

	if state.status == domain.PersonalRecordingStatusCompleted && state.filePath == nil {
		state.status = domain.PersonalRecordingStatusFailed
		reason := "egress completed without file result"
		state.failureReason = &reason
	}

	return state
}

func finalizePersonalRecordingState(
	ctx context.Context,
	rec *domain.PersonalCallRecording,
	state personalRecordingRuntimeState,
	repo personalRecordingResultWriter,
	fileService personalRecordingObjectStore,
	logger *zap.Logger,
) error {
	if rec == nil {
		return nil
	}

	if state.status == domain.PersonalRecordingStatusCompleted {
		objectExists := false
		if fileService != nil && state.filePath != nil && *state.filePath != "" {
			if err := fileService.ObjectExists(ctx, *state.filePath); err == nil {
				objectExists = true
			} else {
				if logger != nil {
					logger.Warn("minio_file_missing",
						zap.String("recording_id", rec.ID.String()),
						zap.String("call_id", rec.CallID.String()),
						zap.String("room_name", rec.RoomName),
						zap.String("egress_id", rec.EgressID),
						zap.Any("file_path", state.filePath),
						zap.String("minio_bucket", fileService.BucketName()),
						zap.String("minio_endpoint", fileService.Endpoint()),
					)
				}
				reason := "minio_object_missing"
				state.status = domain.PersonalRecordingStatusFailed
				state.failureReason = &reason
				if state.liveKitErrorMsg == nil {
					msg := err.Error()
					state.liveKitErrorMsg = &msg
				}
			}
		} else {
			reason := "minio_object_missing"
			state.status = domain.PersonalRecordingStatusFailed
			state.failureReason = &reason
		}
		state.minioObjectExists = &objectExists
	}

	if err := repo.UpdateResult(
		ctx,
		rec.ID,
		state.status,
		state.filePath,
		state.durationSec,
		state.failureReason,
		state.liveKitErrorCode,
		state.liveKitErrorMsg,
		state.endReason,
		state.minioObjectExists,
	); err != nil {
		return err
	}

	if logger != nil {
		logName := "recording_failed"
		switch state.status {
		case domain.PersonalRecordingStatusCompleted:
			logName = "recording_completed"
		case domain.PersonalRecordingStatusAborted:
			logName = "egress_aborted"
		}
		logger.Info(logName,
			zap.String("recording_id", rec.ID.String()),
			zap.String("call_id", rec.CallID.String()),
			zap.String("room_name", rec.RoomName),
			zap.Int64("owner_id", rec.OwnerUserID),
			zap.String("egress_id", rec.EgressID),
			zap.String("egress_status", state.liveKitStatus),
			zap.String("status", string(state.status)),
			zap.Any("file_path", state.filePath),
			zap.Any("duration_sec", state.durationSec),
			zap.Any("livekit_error_code", state.liveKitErrorCode),
			zap.Any("livekit_error_message", state.liveKitErrorMsg),
			zap.Any("end_reason", state.endReason),
			zap.Any("minio_object_exists", state.minioObjectExists),
		)
	}

	return nil
}

func pollPersonalRecordingState(
	ctx context.Context,
	egress personalRecordingEgressRuntime,
	egressID string,
	timeout time.Duration,
	stopWhen func(personalRecordingRuntimeState) bool,
	observe func(*livekit.EgressInfo, error),
) (personalRecordingRuntimeState, error) {
	deadline := time.Now().Add(timeout)
	for {
		info, err := egress.GetEgressInfo(ctx, egressID)
		if observe != nil {
			observe(info, err)
		}
		if err == nil {
			state := personalRecordingStateFromEgressInfo(info)
			if stopWhen == nil || stopWhen(state) {
				return state, nil
			}
		} else if time.Now().After(deadline) {
			reason := err.Error()
			state := personalRecordingRuntimeState{
				status:           domain.PersonalRecordingStatusFailed,
				failureReason:    &reason,
				liveKitErrorCode: personalRecordingErrorCodePtr(reason),
				liveKitErrorMsg:  &reason,
				endReason:        personalRecordingEndReason(reason),
			}
			return state, nil
		}

		if time.Now().After(deadline) {
			return personalRecordingRuntimeState{
				status:        domain.PersonalRecordingStatusStarting,
				liveKitStatus: livekit.EgressStatus_EGRESS_STARTING.String(),
			}, nil
		}

		timer := time.NewTimer(personalRecordingPollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return personalRecordingRuntimeState{}, ctx.Err()
		case <-timer.C:
		}
	}
}

func (h *PersonalRecordingHandler) startReconciler() {
	if h == nil || h.repo == nil || h.service == nil {
		return
	}

	h.runStartupSelfCheck()

	ticker := time.NewTicker(personalRecordingReconcileInterval)
	defer ticker.Stop()
	for range ticker.C {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		h.reconcileStaleRecordings(ctx)
		cancel()
	}
}

func (h *PersonalRecordingHandler) reconcileStaleRecordings(ctx context.Context) {
	stale, err := h.repo.ListStaleLocked(ctx, time.Now().Add(-personalRecordingReconcileAge), 100)
	if err != nil {
		h.logger.Warn("failed to list stale personal recordings", zap.Error(err))
		return
	}

	for i := range stale {
		rec := stale[i]
		state, stateErr := pollPersonalRecordingState(ctx, h.service, rec.EgressID, 2*time.Second, func(state personalRecordingRuntimeState) bool {
			return state.status != domain.PersonalRecordingStatusStarting
		}, nil)
		if stateErr != nil {
			h.logger.Warn("failed to reconcile stale personal recording",
				zap.String("recording_id", rec.ID.String()),
				zap.String("egress_id", rec.EgressID),
				zap.Error(stateErr),
			)
			continue
		}
		if state.status == domain.PersonalRecordingStatusStarting || state.status == domain.PersonalRecordingStatusActive {
			continue
		}
		_ = finalizePersonalRecordingState(ctx, &rec, state, h.repo, h.fileService, h.logger)
	}
}

func (h *PersonalRecordingHandler) runStartupSelfCheck() {
	if h.logger == nil {
		return
	}

	// Confirm at startup that personal recording uses TrackEgress, never RoomComposite.
	// Search for "personal_recording_egress_mode" in logs to verify configuration.
	h.logger.Info("personal_recording_egress_mode",
		zap.String("personal_recording_egress_mode", "track_audio"),
		zap.Bool("room_composite_personal_recording_enabled", false),
		zap.String("personal_recording_livekit_method", "StartTrackEgress"),
		zap.String("note", "personal audio recording uses direct RTP track capture, not Chrome/RoomComposite"),
	)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if h.service != nil {
			_, err := h.service.ListParticipants(ctx, "__personal_recording_healthcheck__")
			h.logger.Info("personal recording livekit self-check",
				zap.String("livekit_api_url", h.service.GetAPIURL()),
				zap.String("livekit_client_url", h.service.GetClientURL()),
				zap.String("result", startupProbeResult(err)),
				zap.Error(err),
			)

			req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, h.service.MinIOEgressEndpoint(), nil)
			if reqErr == nil {
				resp, probeErr := http.DefaultClient.Do(req)
				if resp != nil {
					resp.Body.Close()
				}
				h.logger.Info("personal recording egress endpoint self-check",
					zap.String("minio_egress_endpoint", h.service.MinIOEgressEndpoint()),
					zap.String("result", startupProbeResult(probeErr)),
					zap.Error(probeErr),
				)
			}
		}

		if h.fileService != nil {
			err := h.fileService.CheckBucketAccess(ctx)
			h.logger.Info("personal recording storage self-check",
				zap.String("minio_bucket", h.fileService.BucketName()),
				zap.String("minio_endpoint", h.fileService.Endpoint()),
				zap.String("result", startupProbeResult(err)),
				zap.Error(err),
			)
		}
	}()
}

func startupProbeResult(err error) string {
	if err == nil {
		return "ok"
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "not_found") || strings.Contains(msg, "not found") {
		return "reachable"
	}
	return "error"
}
