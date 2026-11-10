package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/livekit/protocol/livekit"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"github.com/west6ide/workspace-backend/internal/repository"
	"github.com/west6ide/workspace-backend/internal/service"
	"github.com/west6ide/workspace-backend/internal/websocket"
	"go.uber.org/zap"
)

type personalRecordingService interface {
	StartPersonalAudioRecording(ctx context.Context, callID string, roomName string, ownerUserID int64, filePath string) (string, string, error)
	WaitForPersonalRecordingActive(ctx context.Context, egressID string) error
	StopPersonalRecordingEgress(ctx context.Context, egressID string) (service.PersonalRecordingStopResult, error)
	GetEgressInfo(ctx context.Context, egressID string) (*livekit.EgressInfo, error)
	ListParticipants(ctx context.Context, roomName string) ([]*livekit.ParticipantInfo, error)
	GetAPIURL() string
	GetClientURL() string
	MinIOEgressEndpoint() string
}

type personalRecordingRepository interface {
	Create(ctx context.Context, rec *domain.PersonalCallRecording) error
	GetLockedByCallIDAndOwner(ctx context.Context, callID uuid.UUID, ownerUserID int64) (*domain.PersonalCallRecording, error)
	GetByID(ctx context.Context, id uuid.UUID) (*domain.PersonalCallRecording, error)
	GetByEgressID(ctx context.Context, egressID string) (*domain.PersonalCallRecording, error)
	CountLiveByCallID(ctx context.Context, callID uuid.UUID) (int, []int64, error)
	ListForOwner(ctx context.Context, ownerUserID int64, limit, offset int) ([]domain.PersonalCallRecording, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status domain.PersonalRecordingStatus, filePath *string, durationSec *int, failureReason *string) error
	UpdateResult(ctx context.Context, id uuid.UUID, status domain.PersonalRecordingStatus, filePath *string, durationSec *int, failureReason *string, liveKitErrorCode *int, liveKitErrorMessage *string, endReason *string, minioObjectExists *bool) error
	MarkStoppingIfLive(ctx context.Context, id uuid.UUID) (bool, error)
	ListStaleLocked(ctx context.Context, updatedBefore time.Time, limit int) ([]domain.PersonalCallRecording, error)
}

// PersonalRecordingHandler обрабатывает REST-запросы персональных аудио-записей.
type PersonalRecordingHandler struct {
	service     personalRecordingService
	repo        personalRecordingRepository
	callRepo    *repository.CallHistoryRepository
	fileService *service.FileService
	wsHub       *websocket.Hub
	logger      *zap.Logger
}

func NewPersonalRecordingHandler(
	svc *service.LiveKitService,
	repo *repository.PersonalCallRecordingRepository,
	callRepo *repository.CallHistoryRepository,
	fileService *service.FileService,
	wsHub *websocket.Hub,
	logger *zap.Logger,
) *PersonalRecordingHandler {
	handler := &PersonalRecordingHandler{
		service:     svc,
		repo:        repo,
		callRepo:    callRepo,
		fileService: fileService,
		wsHub:       wsHub,
		logger:      logger.With(zap.String("handler", "personal_recording")),
	}
	go handler.startReconciler()
	return handler
}

// ─── Request / Response DTOs ─────────────────────────────────────────────────

type StartPersonalRecordingRequest struct {
	CallID   string `json:"call_id"   binding:"required"`
	RoomName string `json:"room_name" binding:"required"`
}

type StartPersonalRecordingResponse struct {
	RecordingID         string    `json:"recording_id"`
	EgressID            string    `json:"egress_id"`
	OwnerUserID         int64     `json:"owner_user_id"`
	ParticipantIdentity *string   `json:"participant_identity,omitempty"`
	Status              string    `json:"status"`
	StartedAt           time.Time `json:"started_at"`
	FailureReason       *string   `json:"failed_reason,omitempty"`
	LiveKitErrorMessage *string   `json:"livekit_error_message,omitempty"`
}

type StopPersonalRecordingResponse struct {
	RecordingID         string     `json:"recording_id"`
	EgressID            string     `json:"egress_id"`
	Success             bool       `json:"success"`
	Message             string     `json:"message,omitempty"`
	Status              string     `json:"status"`
	StoppedAt           *time.Time `json:"stopped_at"`
	DurationSec         *int       `json:"duration_sec,omitempty"`
	DownloadAvailable   bool       `json:"download_available"`
	FailureReason       *string    `json:"failed_reason,omitempty"`
	LiveKitErrorMessage *string    `json:"livekit_error_message,omitempty"`
}

type PersonalRecordingResponse struct {
	ID                  uuid.UUID  `json:"id"`
	RecordingID         uuid.UUID  `json:"recording_id"`
	CallID              uuid.UUID  `json:"call_id"`
	RoomName            string     `json:"room_name"`
	OwnerUserID         int64      `json:"owner_user_id"`
	ParticipantIdentity *string    `json:"participant_identity,omitempty"`
	EgressID            string     `json:"egress_id"`
	Status              string     `json:"status"`
	StartedAt           time.Time  `json:"started_at"`
	StoppedAt           *time.Time `json:"stopped_at"`
	FilePath            *string    `json:"file_path"`
	DownloadURL         *string    `json:"download_url,omitempty"`
	DownloadAvailable   bool       `json:"download_available"`
	DurationSec         *int       `json:"duration_sec"`
	FailureReason       *string    `json:"failure_reason"`
	FailedReason        *string    `json:"failed_reason"`
	LiveKitErrorMessage *string    `json:"livekit_error_message,omitempty"`
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func (h *PersonalRecordingHandler) respondErr(c *gin.Context, err *apperrors.AppError) {
	respondJSON(c, err.StatusCode, gin.H{
		"error": gin.H{
			"code":    err.Code,
			"message": err.Message,
			"details": err.Details,
		},
	})
}

func isLiveKitEgressStartFailure(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "start signal not received") ||
		strings.Contains(msg, "source closed") ||
		strings.Contains(msg, "failed to start") ||
		strings.Contains(msg, "egress_aborted") ||
		strings.Contains(msg, "egress aborted") ||
		strings.Contains(msg, "egress_failed") ||
		strings.Contains(msg, "egress failed")
}

func isLiveKitUnavailable(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "deadline exceeded") ||
		strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "unavailable") ||
		strings.Contains(msg, "transport is closing")
}

func canonicalPersonalRecordingRoom(callInfo *domain.CallHistory, requestRoom string) (string, bool) {
	if callInfo == nil {
		return strings.TrimSpace(requestRoom), false
	}
	roomName := strings.TrimSpace(callInfo.RoomID)
	if roomName == "" {
		return strings.TrimSpace(requestRoom), false
	}
	return roomName, strings.TrimSpace(requestRoom) != "" && strings.TrimSpace(requestRoom) != roomName
}

func personalRecordingDownloadURL(recordingID uuid.UUID) string {
	return fmt.Sprintf("/api/v1/calls/recordings/personal/%s/download", recordingID.String())
}

func apiPersonalRecordingStatus(status domain.PersonalRecordingStatus) string {
	switch status {
	case domain.PersonalRecordingStatusActive:
		return "recording"
	case domain.PersonalRecordingStatusStopped:
		return "failed"
	default:
		return string(status)
	}
}

func isCallAllowedForPersonalRecording(status string) bool {
	switch normalized := domain.NormalizeCallStatus(status); normalized {
	case domain.CallStatusAccepted, domain.CallStatusOngoing, "active", "in_progress":
		return true
	default:
		return false
	}
}

func toPersonalRecordingResponse(rec domain.PersonalCallRecording) PersonalRecordingResponse {
	var downloadURL *string
	var filePath *string
	downloadAvailable := rec.IsDownloadReady()
	if downloadAvailable {
		filePath = rec.FilePath
		url := personalRecordingDownloadURL(rec.ID)
		downloadURL = &url
	}

	return PersonalRecordingResponse{
		ID:                  rec.ID,
		RecordingID:         rec.ID,
		CallID:              rec.CallID,
		RoomName:            rec.RoomName,
		OwnerUserID:         rec.OwnerUserID,
		ParticipantIdentity: rec.ParticipantIdentity,
		EgressID:            rec.EgressID,
		Status:              apiPersonalRecordingStatus(rec.Status),
		StartedAt:           rec.StartedAt,
		StoppedAt:           rec.StoppedAt,
		FilePath:            filePath,
		DownloadURL:         downloadURL,
		DownloadAvailable:   downloadAvailable,
		DurationSec:         rec.DurationSec,
		FailureReason:       rec.FailureReason,
		FailedReason:        rec.FailureReason,
		LiveKitErrorMessage: rec.LiveKitErrorMessage,
	}
}

// broadcastRecordingStatus отправляет WS-событие call_recording_status_changed
// всем активным участникам звонка.
func (h *PersonalRecordingHandler) broadcastRecordingStatus(
	ctx context.Context,
	callID uuid.UUID,
	action string, // "started" | "stopped"
	ownerUserID int64,
) {
	if h.wsHub == nil {
		return
	}

	// 1. Получаем актуальное количество активных записей и список recorder-ов.
	activeCount, _, err := h.repo.CountLiveByCallID(ctx, callID)
	if err != nil {
		h.logger.Warn("broadcastRecordingStatus: failed to count active recordings", zap.Error(err))
	}

	// 2. Получаем список участников звонка из БД.
	participants, err := h.callRepo.GetParticipants(ctx, callID)
	if err != nil {
		h.logger.Warn("broadcastRecordingStatus: failed to get call participants", zap.Error(err))
		return
	}
	participantIDs := make([]int64, 0, len(participants))
	for _, p := range participants {
		participantIDs = append(participantIDs, p.UserID)
	}
	if len(participantIDs) == 0 {
		return
	}

	payload := domain.RecordingStatusChangedEvent{
		CallID:               callID.String(),
		Action:               action,
		OwnerUserID:          ownerUserID,
		ActiveCount:          activeCount,
		IsAnyRecordingActive: activeCount > 0,
		Ts:                   time.Now().Unix(),
	}

	msg := &websocket.Message{
		Type:      "call_recording_status_changed",
		From:      ownerUserID,
		Content:   payload,
		Timestamp: time.Now().Unix(),
	}

	h.wsHub.SendToUsers(participantIDs, msg)
	h.logger.Info("Broadcast call_recording_status_changed",
		zap.String("call_id", callID.String()),
		zap.String("action", action),
		zap.Int64("owner", ownerUserID),
		zap.Int("active_count", activeCount),
		zap.Int("recipients", len(participantIDs)),
	)
}

// ─── Handlers ────────────────────────────────────────────────────────────────

// StartPersonalRecording godoc
// @Summary Start my personal call recording
// @Description Any call participant can start their own personal audio recording.
// @Description Idempotent: if an active recording already exists for this user in this call, it is returned as-is (no new egress).
// @Tags calls
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body StartPersonalRecordingRequest true "call_id and room_name"
// @Success 200 {object} StartPersonalRecordingResponse
// @Failure 400 {object} domain.ErrorResponse
// @Failure 500 {object} domain.ErrorResponse
// @Router /calls/recordings/personal/start [post]
func (h *PersonalRecordingHandler) StartPersonalRecording(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	// Diagnostic trace: emitted immediately so it reaches logs regardless of
	// downstream failure. Confirms that THIS endpoint (TrackEgress, not
	// RoomComposite) is being invoked for personal recording.
	h.logger.Info("personal_recording_start_runtime_flow",
		zap.String("endpoint", "/api/v1/calls/recordings/personal/start"),
		zap.String("egress_type", "track_audio"),
		zap.String("livekit_method", "StartTrackEgress"),
		zap.Int64("current_user_id", userID),
		zap.String("livekit_api_url", h.service.GetAPIURL()),
		zap.String("livekit_url", h.service.GetClientURL()),
		zap.String("minio_endpoint", h.service.MinIOEgressEndpoint()),
	)

	var req StartPersonalRecordingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondErr(c, apperrors.Validation("invalid request body: call_id and room_name are required"))
		return
	}

	callUUID, err := uuid.Parse(req.CallID)
	if err != nil {
		h.respondErr(c, apperrors.Validation("invalid call_id format"))
		return
	}

	callInfo, err := h.callRepo.GetByCallID(c.Request.Context(), callUUID)
	if err != nil {
		h.logger.Error("failed to load call for personal recording",
			zap.Error(err),
			zap.String("call_id", req.CallID),
			zap.Int64("user_id", userID),
		)
		h.respondErr(c, apperrors.InternalWithErr("failed to load call", err))
		return
	}
	if callInfo == nil {
		h.respondErr(c, apperrors.NotFound("call"))
		return
	}
	if !isCallAllowedForPersonalRecording(callInfo.Status) {
		h.logger.Warn("recording_start_rejected_invalid_call_status",
			zap.String("call_id", req.CallID),
			zap.String("room_name", callInfo.RoomID),
			zap.Int64("owner_user_id", userID),
			zap.String("call_status", domain.NormalizeCallStatus(callInfo.Status)),
			zap.Bool("call_finalized", domain.IsCallFinalStatus(callInfo.Status)),
		)
		h.respondErr(c, apperrors.Conflict("call is not active for recording"))
		return
	}
	if callInfo.FromUserID != userID && callInfo.ToUserID != userID {
		isParticipant := false
		participants, participantsErr := h.callRepo.GetParticipants(c.Request.Context(), callUUID)
		if participantsErr != nil {
			h.logger.Warn("failed to verify call participants for personal recording",
				zap.Error(participantsErr),
				zap.String("call_id", req.CallID),
				zap.Int64("user_id", userID),
			)
		}
		for _, participant := range participants {
			if participant.UserID == userID {
				isParticipant = true
				break
			}
		}
		if !isParticipant {
			h.respondErr(c, apperrors.Forbidden("you are not a participant of this call"))
			return
		}
	}

	roomName, roomMismatch := canonicalPersonalRecordingRoom(callInfo, req.RoomName)
	if roomName == "" {
		h.respondErr(c, apperrors.Conflict("call room is not available"))
		return
	}
	if roomMismatch {
		h.logger.Warn("personal recording request room differs from call record; using call record room",
			zap.String("call_id", req.CallID),
			zap.String("request_room", strings.TrimSpace(req.RoomName)),
			zap.String("call_record_room", roomName),
			zap.Int64("user_id", userID),
		)
	}

	// 1. Idempotency Guard — если у этого пользователя уже есть активная запись в этом звонке,
	//    возвращаем её (без нового egress).
	existing, err := h.repo.GetLockedByCallIDAndOwner(c.Request.Context(), callUUID, userID)
	if err != nil {
		h.logger.Error("failed to check active personal recording", zap.Error(err))
		h.respondErr(c, apperrors.InternalWithErr("failed to check existing recording", err))
		return
	}
	if existing != nil {
		if strings.TrimSpace(existing.EgressID) != "" {
			state, pollErr := pollPersonalRecordingState(
				c.Request.Context(),
				h.service,
				existing.EgressID,
				2*time.Second,
				func(state personalRecordingRuntimeState) bool {
					return state.status != domain.PersonalRecordingStatusStarting
				},
				nil,
			)
			if pollErr == nil {
				switch state.status {
				case domain.PersonalRecordingStatusActive, domain.PersonalRecordingStatusStarting:
					if updateErr := h.repo.UpdateResult(c.Request.Context(), existing.ID, state.status, nil, nil, nil, state.liveKitErrorCode, state.liveKitErrorMsg, state.endReason, nil); updateErr != nil {
						h.logger.Warn("failed to refresh personal recording state", zap.Error(updateErr), zap.String("recording_id", existing.ID.String()))
					}
					existing.Status = state.status
					respondJSON(c, http.StatusOK, StartPersonalRecordingResponse{
						RecordingID:         existing.ID.String(),
						EgressID:            existing.EgressID,
						OwnerUserID:         existing.OwnerUserID,
						ParticipantIdentity: existing.ParticipantIdentity,
						Status:              apiPersonalRecordingStatus(state.status),
						StartedAt:           existing.StartedAt,
						FailureReason:       existing.FailureReason,
						LiveKitErrorMessage: existing.LiveKitErrorMessage,
					})
					return
				default:
					if updateErr := finalizePersonalRecordingState(c.Request.Context(), existing, state, h.repo, h.fileService, h.logger); updateErr != nil {
						h.logger.Warn("failed to finalize stale personal recording before restart", zap.Error(updateErr), zap.String("recording_id", existing.ID.String()))
					}
				}
			}
		}
	}

	// 2. Запускаем новый audio-only Egress для персональной копии звонка.
	//    file_path содержит уникальный путь с owner_user_id и timestamp.
	filePath := fmt.Sprintf("recordings/personal/%s/%d-%d.ogg", req.CallID, userID, time.Now().Unix())
	egressID, participantIdentity, err := h.service.StartPersonalAudioRecording(
		c.Request.Context(),
		req.CallID,
		roomName,
		userID,
		filePath,
	)
	if err != nil {
		h.logger.Error("egress_start_failed",
			zap.Error(err),
			zap.String("call_id", req.CallID),
			zap.String("room", roomName),
			zap.String("expected_livekit_identity", service.BuildParticipantIdentity(userID, "")),
			zap.Int64("user_id", userID),
		)
		switch {
		case errors.Is(err, service.ErrPersonalRecordingParticipantNotFound):
			h.respondErr(c, apperrors.Conflict("participant is not connected to the call"))
		case errors.Is(err, service.ErrPersonalRecordingAudioTrackNotFound):
			h.respondErr(c, apperrors.Conflict("audio track is not ready for recording"))
		case errors.Is(err, service.ErrPersonalRecordingAudioTrackMuted):
			h.respondErr(c, apperrors.Conflict("audio track is not ready for recording"))
		case isLiveKitEgressStartFailure(err):
			h.respondErr(c, apperrors.New(apperrors.ErrCodeInternal, "LiveKit egress failed to start", http.StatusBadGateway).WithError(err))
		case isLiveKitUnavailable(err):
			h.respondErr(c, apperrors.New(apperrors.ErrCodeInternal, "LiveKit API unavailable", http.StatusBadGateway).WithError(err))
		default:
			h.respondErr(c, apperrors.InternalWithErr("failed to start recording", err))
		}
		return
	}

	// 3. Сохраняем метаданные в БД.
	rec := &domain.PersonalCallRecording{
		CallID:              callUUID,
		RoomName:            roomName,
		OwnerUserID:         userID,
		ParticipantIdentity: personalRecordingStringPtr(participantIdentity),
		EgressID:            egressID,
		Status:              domain.PersonalRecordingStatusStarting,
		FilePath:            &filePath,
	}
	saveErr := h.repo.Create(c.Request.Context(), rec)
	if saveErr != nil {
		h.logger.Error("CRITICAL: personal egress started but failed to persist metadata",
			zap.Error(saveErr),
			zap.String("egress_id", egressID),
			zap.String("call_id", req.CallID),
		)
		// Не фейлим ответ — egress уже запущен.
	}
	startState, pollErr := pollPersonalRecordingState(
		c.Request.Context(),
		h.service,
		egressID,
		personalRecordingStartPollTimeout,
		func(state personalRecordingRuntimeState) bool {
			return state.status == domain.PersonalRecordingStatusActive || state.status.IsTerminal()
		},
		func(info *livekit.EgressInfo, err error) {
			fields := []zap.Field{
				zap.String("recording_id", rec.ID.String()),
				zap.String("call_id", req.CallID),
				zap.String("room_name", roomName),
				zap.Int64("owner_user_id", userID),
				zap.String("participant_identity", participantIdentity),
				zap.String("egress_id", egressID),
			}
			if info != nil {
				fields = append(fields,
					zap.String("egress_status", info.Status.String()),
					zap.String("livekit_error", strings.TrimSpace(info.Error)),
					zap.Int64("started_at", info.GetStartedAt()),
					zap.Int64("ended_at", info.GetEndedAt()),
				)
			}
			if err != nil {
				fields = append(fields, zap.Error(err))
			}
			h.logger.Info("egress_poll_status", fields...)
		},
	)
	if pollErr != nil {
		reason := pollErr.Error()
		startState = personalRecordingRuntimeState{
			status:           domain.PersonalRecordingStatusFailed,
			failureReason:    &reason,
			liveKitErrorCode: personalRecordingErrorCodePtr(reason),
			liveKitErrorMsg:  &reason,
			endReason:        personalRecordingEndReason(reason),
		}
	}

	if saveErr == nil {
		if startState.status == domain.PersonalRecordingStatusActive || startState.status == domain.PersonalRecordingStatusStarting {
			if err := h.repo.UpdateResult(c.Request.Context(), rec.ID, startState.status, nil, nil, nil, startState.liveKitErrorCode, startState.liveKitErrorMsg, startState.endReason, nil); err != nil {
				h.logger.Error("failed to persist personal recording start state",
					zap.String("call_id", req.CallID),
					zap.String("egress_id", egressID),
					zap.Error(err),
				)
			}
		} else if err := finalizePersonalRecordingState(c.Request.Context(), rec, startState, h.repo, h.fileService, h.logger); err != nil {
			h.logger.Error("failed to finalize personal recording start state",
				zap.String("call_id", req.CallID),
				zap.String("egress_id", egressID),
				zap.Error(err),
			)
		}
	}

	// 4. Перечитываем запись для получения ID и StartedAt.
	saved, _ := h.repo.GetLockedByCallIDAndOwner(c.Request.Context(), callUUID, userID)

	var recID string
	var startedAt time.Time
	if saved != nil {
		recID = saved.ID.String()
		startedAt = saved.StartedAt
	} else {
		recID = uuid.New().String() // fallback если сохранение не удалось
		startedAt = time.Now()
	}

	h.logger.Info("recording_start_requested",
		zap.String("recording_id", recID),
		zap.String("egress_id", egressID),
		zap.String("call_id", req.CallID),
		zap.Int64("owner", userID),
		zap.String("room", roomName),
		zap.String("egress_type", "track_audio"),
		zap.String("file_path", filePath),
		zap.String("minio_endpoint", h.service.MinIOEgressEndpoint()),
		zap.String("status", apiPersonalRecordingStatus(startState.status)),
		zap.String("participant_identity", participantIdentity),
	)

	if startState.status == domain.PersonalRecordingStatusActive {
		h.logger.Info("egress_start_confirmed",
			zap.String("recording_id", recID),
			zap.String("call_id", req.CallID),
			zap.String("egress_id", egressID),
			zap.String("participant_identity", participantIdentity),
		)
		go h.broadcastRecordingStatus(context.Background(), callUUID, "started", userID)
	} else if startState.status.IsTerminal() {
		h.logger.Warn("egress_start_failed",
			zap.String("recording_id", recID),
			zap.String("call_id", req.CallID),
			zap.String("egress_id", egressID),
			zap.String("status", apiPersonalRecordingStatus(startState.status)),
			zap.Any("livekit_error_message", startState.liveKitErrorMsg),
		)
	}

	respondJSON(c, http.StatusOK, StartPersonalRecordingResponse{
		RecordingID:         recID,
		EgressID:            egressID,
		OwnerUserID:         userID,
		ParticipantIdentity: rec.ParticipantIdentity,
		Status:              apiPersonalRecordingStatus(startState.status),
		StartedAt:           startedAt,
		FailureReason:       startState.failureReason,
		LiveKitErrorMessage: startState.liveKitErrorMsg,
	})
}

// StopPersonalRecording godoc
// @Summary Stop my personal call recording
// @Description Stops an active personal recording by its UUID. Only the recording owner can stop it.
// @Description If the recording is already completed/failed/aborted, returns current state idempotently.
// @Tags calls
// @Security BearerAuth
// @Produce json
// @Param recording_id path string true "Recording UUID"
// @Success 200 {object} StopPersonalRecordingResponse
// @Failure 400 {object} domain.ErrorResponse
// @Failure 403 {object} domain.ErrorResponse "Only the owner can stop their recording"
// @Failure 404 {object} domain.ErrorResponse
// @Failure 500 {object} domain.ErrorResponse
// @Router /calls/recordings/personal/{recording_id}/stop [post]
func (h *PersonalRecordingHandler) StopPersonalRecording(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	recIDStr := c.Param("recording_id")

	recID, err := uuid.Parse(recIDStr)
	if err != nil {
		h.respondErr(c, apperrors.Validation("invalid recording_id"))
		return
	}

	// 1. Получаем запись и проверяем ownership.
	rec, err := h.repo.GetByID(c.Request.Context(), recID)
	if err != nil {
		h.respondErr(c, apperrors.InternalWithErr("failed to get recording", err))
		return
	}
	if rec == nil {
		h.respondErr(c, apperrors.NotFound("recording not found"))
		return
	}
	if rec.OwnerUserID != userID {
		h.respondErr(c, apperrors.Forbidden("you can only stop your own recording"))
		return
	}
	if rec.Status.IsTerminal() || rec.Status == domain.PersonalRecordingStatusStopping {
		h.logger.Info("personal recording stop is idempotent",
			zap.String("call_id", rec.CallID.String()),
			zap.String("room_name", rec.RoomName),
			zap.Int64("user_id", userID),
			zap.String("recording_id", rec.ID.String()),
			zap.String("egress_id", rec.EgressID),
			zap.String("previous_status", string(rec.Status)),
			zap.String("new_status", string(rec.Status)),
			zap.String("reason", "already_stopped"),
		)
		// Already finalized — return current state idempotently.
		respondJSON(c, http.StatusOK, StopPersonalRecordingResponse{
			RecordingID:         rec.ID.String(),
			EgressID:            rec.EgressID,
			Success:             true,
			Message:             apiMessage(c, "recording already stopped"),
			Status:              apiPersonalRecordingStatus(rec.Status),
			StoppedAt:           rec.StoppedAt,
			DurationSec:         rec.DurationSec,
			DownloadAvailable:   rec.IsDownloadReady(),
			FailureReason:       rec.FailureReason,
			LiveKitErrorMessage: rec.LiveKitErrorMessage,
		})
		return
	}

	if strings.TrimSpace(rec.EgressID) == "" {
		if err := h.repo.UpdateStatus(c.Request.Context(), rec.ID, domain.PersonalRecordingStatusStopped, nil, nil, nil); err != nil {
			h.logger.Warn("failed to finalize personal recording without egress id",
				zap.String("recording_id", rec.ID.String()),
				zap.Error(err),
			)
		}
		updated, _ := h.repo.GetByID(c.Request.Context(), rec.ID)
		if updated == nil {
			updated = rec
			updated.Status = domain.PersonalRecordingStatusStopped
		}
		h.logger.Info("personal recording stopped without egress id",
			zap.String("call_id", rec.CallID.String()),
			zap.String("room_name", rec.RoomName),
			zap.Int64("user_id", userID),
			zap.String("recording_id", rec.ID.String()),
			zap.String("previous_status", string(rec.Status)),
			zap.String("new_status", string(domain.PersonalRecordingStatusStopped)),
			zap.String("reason", "not_started"),
		)
		respondJSON(c, http.StatusOK, StopPersonalRecordingResponse{
			RecordingID:         rec.ID.String(),
			EgressID:            rec.EgressID,
			Success:             true,
			Message:             apiMessage(c, "recording already stopped"),
			Status:              apiPersonalRecordingStatus(updated.Status),
			StoppedAt:           updated.StoppedAt,
			DurationSec:         updated.DurationSec,
			DownloadAvailable:   updated.IsDownloadReady(),
			FailureReason:       updated.FailureReason,
			LiveKitErrorMessage: updated.LiveKitErrorMessage,
		})
		return
	}

	markedStopping, err := h.repo.MarkStoppingIfLive(c.Request.Context(), rec.ID)
	if err != nil {
		h.respondErr(c, apperrors.InternalWithErr("failed to update recording status", err))
		return
	}
	if !markedStopping {
		current, getErr := h.repo.GetByID(c.Request.Context(), rec.ID)
		if getErr != nil {
			h.respondErr(c, apperrors.InternalWithErr("failed to get recording", getErr))
			return
		}
		if current == nil {
			h.respondErr(c, apperrors.NotFound("recording not found"))
			return
		}
		respondJSON(c, http.StatusOK, StopPersonalRecordingResponse{
			RecordingID:         current.ID.String(),
			EgressID:            current.EgressID,
			Success:             true,
			Message:             apiMessage(c, "recording already stopped"),
			Status:              apiPersonalRecordingStatus(current.Status),
			StoppedAt:           current.StoppedAt,
			DurationSec:         current.DurationSec,
			DownloadAvailable:   current.IsDownloadReady(),
			FailureReason:       current.FailureReason,
			LiveKitErrorMessage: current.LiveKitErrorMessage,
		})
		return
	}

	stopResult, err := h.service.StopPersonalRecordingEgress(c.Request.Context(), rec.EgressID)
	if err != nil {
		errMsg := err.Error()
		if updateErr := h.repo.UpdateResult(
			c.Request.Context(),
			rec.ID,
			domain.PersonalRecordingStatusFailed,
			nil,
			nil,
			&errMsg,
			personalRecordingErrorCodePtr(errMsg),
			&errMsg,
			personalRecordingEndReason(errMsg),
			nil,
		); updateErr != nil {
			h.logger.Error("failed to mark personal recording as failed after stop error",
				zap.String("recording_id", rec.ID.String()),
				zap.String("egress_id", rec.EgressID),
				zap.Error(updateErr),
			)
		}
		h.logger.Error("failed to stop personal recording egress",
			zap.String("recording_id", rec.ID.String()),
			zap.String("egress_id", rec.EgressID),
			zap.Error(err),
		)
		h.respondErr(c, apperrors.InternalWithErr("failed to stop recording", err))
		return
	}

	responseMessage := apiMessage(c, "recording stop accepted")
	responseStatus := apiPersonalRecordingStatus(stopResult.FinalStatus)
	switch stopResult.FinalStatus {
	case domain.PersonalRecordingStatusCompleted:
		objectExists := true
		if h.fileService != nil && stopResult.FilePath != nil {
			if err := h.fileService.ObjectExists(c.Request.Context(), *stopResult.FilePath); err != nil {
				objectExists = false
				reason := "minio_object_missing"
				stopResult.FinalStatus = domain.PersonalRecordingStatusFailed
				responseStatus = apiPersonalRecordingStatus(stopResult.FinalStatus)
				stopResult.Reason = reason
				if stopResult.LiveKitErrorMessage == "" {
					stopResult.LiveKitErrorMessage = err.Error()
				}
			}
		} else {
			objectExists = false
			stopResult.FinalStatus = domain.PersonalRecordingStatusFailed
			responseStatus = apiPersonalRecordingStatus(stopResult.FinalStatus)
			stopResult.Reason = "minio_object_missing"
		}
		if updateErr := h.repo.UpdateResult(c.Request.Context(), rec.ID, stopResult.FinalStatus, stopResult.FilePath, stopResult.DurationSec, nil, personalRecordingIntPtr(stopResult.LiveKitErrorCode), personalRecordingStringPtr(stopResult.LiveKitErrorMessage), personalRecordingStringPtr(stopResult.EndReason), &objectExists); updateErr != nil {
			h.logger.Warn("failed to finalize completed recording",
				zap.String("recording_id", rec.ID.String()),
				zap.String("egress_id", rec.EgressID),
				zap.Error(updateErr),
			)
		}
		responseMessage = apiMessage(c, "recording completed")
	case domain.PersonalRecordingStatusFailed, domain.PersonalRecordingStatusAborted:
		reason := stopResult.LiveKitErrorMessage
		if reason == "" {
			reason = stopResult.Reason
		}
		if strings.Contains(strings.ToLower(reason), "start signal not received") {
			reason = "start_signal_not_received"
		}
		if updateErr := h.repo.UpdateResult(c.Request.Context(), rec.ID, stopResult.FinalStatus, stopResult.FilePath, stopResult.DurationSec, &reason, personalRecordingIntPtr(stopResult.LiveKitErrorCode), personalRecordingStringPtr(stopResult.LiveKitErrorMessage), personalRecordingStringPtr(stopResult.EndReason), personalRecordingBoolPtr(false)); updateErr != nil {
			h.logger.Warn("failed to finalize failed recording",
				zap.String("recording_id", rec.ID.String()),
				zap.String("egress_id", rec.EgressID),
				zap.Error(updateErr),
			)
		}
		responseMessage = apiMessage(c, "recording failed")
	case domain.PersonalRecordingStatusStarting, domain.PersonalRecordingStatusStopping, domain.PersonalRecordingStatusActive:
		if updateErr := h.repo.UpdateResult(c.Request.Context(), rec.ID, domain.PersonalRecordingStatusStopping, nil, nil, nil, personalRecordingIntPtr(stopResult.LiveKitErrorCode), personalRecordingStringPtr(stopResult.LiveKitErrorMessage), personalRecordingStringPtr(stopResult.EndReason), nil); updateErr != nil {
			h.logger.Warn("failed to persist stopping state",
				zap.String("recording_id", rec.ID.String()),
				zap.String("egress_id", rec.EgressID),
				zap.Error(updateErr),
			)
		}
		responseStatus = apiPersonalRecordingStatus(domain.PersonalRecordingStatusStopping)
	}

	updated, err := h.repo.GetByID(c.Request.Context(), rec.ID)
	if err != nil {
		h.logger.Warn("failed to reload stopped personal recording",
			zap.String("recording_id", recIDStr),
			zap.Error(err),
		)
		updated = rec
		updated.Status = domain.PersonalRecordingStatusStopping
		stoppedAt := time.Now()
		updated.StoppedAt = &stoppedAt
		duration := int(stoppedAt.Sub(rec.StartedAt).Seconds())
		if duration < 0 {
			duration = 0
		}
		updated.DurationSec = &duration
	}

	h.logger.Info("recording_stop_requested",
		zap.String("recording_id", recIDStr),
		zap.String("egress_id", rec.EgressID),
		zap.Int64("owner", userID),
		zap.String("call_id", rec.CallID.String()),
		zap.String("room_name", rec.RoomName),
		zap.String("previous_status", string(rec.Status)),
		zap.String("new_status", responseStatus),
		zap.String("egress_status", stopResult.LiveKitStatus),
		zap.Int("livekit_error_code", stopResult.LiveKitErrorCode),
		zap.String("livekit_error_message", stopResult.LiveKitErrorMessage),
		zap.String("end_reason", stopResult.EndReason),
		zap.String("reason", stopResult.Reason),
	)

	go h.broadcastRecordingStatus(context.Background(), rec.CallID, "stopped", userID)

	respondJSON(c, http.StatusOK, StopPersonalRecordingResponse{
		RecordingID:         recIDStr,
		EgressID:            rec.EgressID,
		Success:             true,
		Message:             responseMessage,
		Status:              responseStatus,
		StoppedAt:           updated.StoppedAt,
		DurationSec:         updated.DurationSec,
		DownloadAvailable:   updated.IsDownloadReady(),
		FailureReason:       updated.FailureReason,
		LiveKitErrorMessage: updated.LiveKitErrorMessage,
	})
}

// GetActiveRecordingsForCall godoc
// @Summary Get active recording status for a call
// @Description Returns the count of active personal recordings and list of recorder user IDs.
// @Description Use this on join/reconnect to determine whether to show the "Recording in progress" banner.
// @Tags calls
// @Security BearerAuth
// @Produce json
// @Param call_id query string true "Call UUID"
// @Success 200 {object} domain.ActiveRecordingsStatus
// @Failure 400 {object} domain.ErrorResponse
// @Failure 500 {object} domain.ErrorResponse
// @Router /calls/recordings/personal/active [get]
func (h *PersonalRecordingHandler) GetActiveRecordingsForCall(c *gin.Context) {
	callIDStr := c.Query("call_id")
	callID, err := uuid.Parse(callIDStr)
	if err != nil {
		h.respondErr(c, apperrors.Validation("invalid call_id"))
		return
	}

	count, owners, err := h.repo.CountLiveByCallID(c.Request.Context(), callID)
	if err != nil {
		h.respondErr(c, apperrors.InternalWithErr("failed to get active recordings", err))
		return
	}

	if owners == nil {
		owners = []int64{}
	}

	respondJSON(c, http.StatusOK, domain.ActiveRecordingsStatus{
		ActiveCount:          count,
		ActiveRecorders:      owners,
		IsAnyRecordingActive: count > 0,
	})
}

// ListMyPersonalRecordings godoc
// @Summary List my personal recordings
// @Description Returns paginated list of all personal recordings belonging to the current user.
// @Tags calls
// @Security BearerAuth
// @Produce json
// @Param limit  query int false "Number of records to return (default: 20, max: 100)"
// @Param offset query int false "Number of records to skip (default: 0)"
// @Success 200 {object} map[string][]domain.PersonalCallRecording
// @Failure 500 {object} domain.ErrorResponse
// @Router /calls/recordings/personal [get]
func (h *PersonalRecordingHandler) ListMyPersonalRecordings(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	recordings, err := h.repo.ListForOwner(c.Request.Context(), userID, limit, offset)
	if err != nil {
		h.logger.Error("failed to list personal recordings", zap.Error(err))
		h.respondErr(c, apperrors.InternalWithErr("failed to list recordings", err))
		return
	}
	if recordings == nil {
		recordings = []domain.PersonalCallRecording{}
	}

	response := make([]PersonalRecordingResponse, 0, len(recordings))
	for _, rec := range recordings {
		response = append(response, toPersonalRecordingResponse(rec))
	}

	respondJSON(c, http.StatusOK, gin.H{"recordings": response})
}

// DownloadPersonalRecording godoc
// @Summary Download personal recording file
// @Description Streams the audio file for a personal recording. Only the recording owner can download it.
// @Tags calls
// @Security BearerAuth
// @Produce application/octet-stream
// @Param recording_id path string true "Recording UUID"
// @Success 200 {file} binary
// @Failure 400 {object} domain.ErrorResponse
// @Failure 403 {object} domain.ErrorResponse "Only the owner can download their recording"
// @Failure 404 {object} domain.ErrorResponse
// @Failure 500 {object} domain.ErrorResponse
// @Router /calls/recordings/personal/{recording_id}/download [get]
func (h *PersonalRecordingHandler) DownloadPersonalRecording(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	recIDStr := c.Param("recording_id")

	recID, err := uuid.Parse(recIDStr)
	if err != nil {
		h.respondErr(c, apperrors.Validation("invalid recording_id"))
		return
	}

	rec, err := h.repo.GetByID(c.Request.Context(), recID)
	if err != nil {
		h.respondErr(c, apperrors.InternalWithErr("failed to get recording", err))
		return
	}
	if rec == nil {
		h.respondErr(c, apperrors.NotFound("recording not found"))
		return
	}
	if rec.OwnerUserID != userID {
		h.respondErr(c, apperrors.Forbidden("access denied: you are not the owner of this recording"))
		return
	}
	if rec.Status != domain.PersonalRecordingStatusCompleted {
		respondJSON(c, http.StatusConflict, gin.H{
			"error":   "recording_not_ready",
			"status":  apiPersonalRecordingStatus(rec.Status),
			"message": apiMessage(c, "Recording is not completed and cannot be downloaded"),
		})
		return
	}
	if !rec.IsDownloadReady() {
		respondJSON(c, http.StatusConflict, gin.H{
			"error":   "recording_not_ready",
			"status":  apiPersonalRecordingStatus(rec.Status),
			"message": apiMessage(c, "Recording is not completed and cannot be downloaded"),
		})
		return
	}

	if h.fileService == nil {
		h.respondErr(c, apperrors.Internal("file service is not initialized"))
		return
	}

	if err := h.fileService.ObjectExists(c.Request.Context(), *rec.FilePath); err != nil {
		reason := "minio_object_missing"
		_ = h.repo.UpdateResult(c.Request.Context(), rec.ID, domain.PersonalRecordingStatusFailed, rec.FilePath, rec.DurationSec, &reason, rec.LiveKitErrorCode, rec.LiveKitErrorMessage, rec.EndReason, personalRecordingBoolPtr(false))
		respondJSON(c, http.StatusNotFound, gin.H{
			"error":   "recording_file_not_found",
			"message": apiMessage(c, "Recording file was not created in storage"),
		})
		return
	}

	reader, contentType, size, err := h.fileService.DownloadFile(c.Request.Context(), *rec.FilePath)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "not found") {
			h.respondErr(c, apperrors.NotFound("recording file not found in storage"))
			return
		}
		h.logger.Error("failed to download personal recording", zap.Error(err), zap.String("path", *rec.FilePath))
		h.respondErr(c, apperrors.InternalWithErr("failed to read recording from storage", err))
		return
	}
	defer reader.Close()

	if contentType == "" || contentType == "application/octet-stream" {
		switch {
		case strings.HasSuffix(strings.ToLower(*rec.FilePath), ".ogg"):
			contentType = "audio/ogg"
		case strings.HasSuffix(strings.ToLower(*rec.FilePath), ".mp3"):
			contentType = "audio/mpeg"
		default:
			contentType = "audio/mp4"
		}
	}

	// filename из последней части пути
	parts := strings.Split(*rec.FilePath, "/")
	filename := parts[len(parts)-1]

	c.Header("Content-Disposition", fmt.Sprintf(`inline; filename="%s"`, filename))
	c.DataFromReader(http.StatusOK, size, contentType, reader, map[string]string{})
}
