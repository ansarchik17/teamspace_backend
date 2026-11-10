package service

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
	"github.com/west6ide/workspace-backend/internal/telemetry"
	"go.uber.org/zap"
	"google.golang.org/protobuf/encoding/protojson"
)

var (
	ErrPersonalRecordingParticipantNotFound = errors.New("personal recording participant not found in room")
	ErrPersonalRecordingAudioTrackNotFound  = errors.New("personal recording active audio track not found")
	ErrPersonalRecordingAudioTrackMuted     = errors.New("personal recording audio track is muted or not published")
)

type PersonalRecordingStopDisposition string

const (
	PersonalRecordingStopDispositionStopped        PersonalRecordingStopDisposition = "stopped"
	PersonalRecordingStopDispositionAlreadyStopped PersonalRecordingStopDisposition = "already_stopped"
	PersonalRecordingStopDispositionAborted        PersonalRecordingStopDisposition = "aborted"
)

type PersonalRecordingStopResult struct {
	Disposition         PersonalRecordingStopDisposition
	FinalStatus         domain.PersonalRecordingStatus
	LiveKitStatus       string
	Reason              string
	LiveKitErrorCode    int
	LiveKitErrorMessage string
	EndReason           string
	FilePath            *string
	DurationSec         *int
}

var (
	personalRecordingMaxRetries    = 10
	personalRecordingRetryInterval = 500 * time.Millisecond
	egressStartVerifyAttempts      = 3
	egressStartVerifyInterval      = 500 * time.Millisecond
)

type LiveKitService struct {
	apiKey       string
	apiSecret    string
	clientURL    string
	apiURL       string
	urlResolver  liveKitClientURLResolver
	roomClient   liveKitRoomClient
	egressClient liveKitEgressClient
	userRepo     *repository.UserRepository
	minioCfg     config.MinIOConfig
	telemetry    *telemetry.CallTelemetry
	logger       *zap.Logger
}

type liveKitRoomClient interface {
	RemoveParticipant(context.Context, *livekit.RoomParticipantIdentity) (*livekit.RemoveParticipantResponse, error)
	ListParticipants(context.Context, *livekit.ListParticipantsRequest) (*livekit.ListParticipantsResponse, error)
	ListRooms(context.Context, *livekit.ListRoomsRequest) (*livekit.ListRoomsResponse, error)
	UpdateParticipant(context.Context, *livekit.UpdateParticipantRequest) (*livekit.ParticipantInfo, error)
	MutePublishedTrack(context.Context, *livekit.MuteRoomTrackRequest) (*livekit.MuteRoomTrackResponse, error)
	DeleteRoom(context.Context, *livekit.DeleteRoomRequest) (*livekit.DeleteRoomResponse, error)
}

type liveKitEgressClient interface {
	StartRoomCompositeEgress(context.Context, *livekit.RoomCompositeEgressRequest) (*livekit.EgressInfo, error)
	StartTrackEgress(context.Context, *livekit.TrackEgressRequest) (*livekit.EgressInfo, error)
	ListEgress(context.Context, *livekit.ListEgressRequest) (*livekit.ListEgressResponse, error)
	StopEgress(context.Context, *livekit.StopEgressRequest) (*livekit.EgressInfo, error)
}

func resolveLiveKitAPIURL(cfg config.LiveKitConfig) string {
	apiURL := strings.TrimSpace(cfg.APIURL)
	if apiURL != "" {
		return apiURL
	}
	return strings.TrimSpace(cfg.URL)
}

func NewLiveKitService(cfg config.LiveKitConfig, minioCfg config.MinIOConfig, userRepo *repository.UserRepository, callTelemetry *telemetry.CallTelemetry, logger *zap.Logger) *LiveKitService {
	// apiURL — используется для серверных Twirp API-вызовов.
	// Если не задан — используем LIVEKIT_URL для dev/local fallback.
	// LiveKit SDK сам конвертирует ws/wss URL в http/https для серверного API.
	apiURL := resolveLiveKitAPIURL(cfg)
	clientURL := strings.TrimSpace(cfg.URL)
	urlResolver := newLiveKitClientURLResolver(cfg)

	roomClient := lksdk.NewRoomServiceClient(apiURL, cfg.APIKey, cfg.APISecret)
	egressClient := lksdk.NewEgressClient(apiURL, cfg.APIKey, cfg.APISecret)

	svc := &LiveKitService{
		apiKey:       cfg.APIKey,
		apiSecret:    cfg.APISecret,
		clientURL:    clientURL,
		apiURL:       apiURL,
		urlResolver:  urlResolver,
		roomClient:   roomClient,
		egressClient: egressClient,
		userRepo:     userRepo,
		minioCfg:     minioCfg,
		telemetry:    callTelemetry,
		logger:       logger.With(zap.String("service", "livekit")),
	}
	svc.logStartupConfig()
	return svc
}

func (s *LiveKitService) logStartupConfig() {
	egressEndpoint := s.MinIOEgressEndpoint()
	s.logger.Info("livekit_personal_recording_config",
		zap.String("LIVEKIT_API_URL", s.apiURL),
		zap.String("LIVEKIT_URL", s.clientURL),
		zap.String("LIVEKIT_PUBLIC_URL", s.urlResolver.publicURL),
		zap.Int("LIVEKIT_PUBLIC_USERS_COUNT", len(s.urlResolver.publicSelectors)),
		zap.String("MINIO_ENDPOINT", s.minioCfg.Endpoint),
		zap.String("MINIO_EGRESS_ENDPOINT", egressEndpoint),
	)
	if strings.Contains(egressEndpoint, "127.0.0.1") || strings.Contains(egressEndpoint, "172.17.0.1") {
		s.logger.Warn("minio_egress_endpoint_may_be_unreachable_for_remote_egress",
			zap.String("MINIO_EGRESS_ENDPOINT", egressEndpoint),
			zap.String("warning", "remote egress may not be able to write files to this endpoint"),
		)
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, err := s.roomClient.ListRooms(ctx, &livekit.ListRoomsRequest{})
		s.logger.Info("livekit_room_api_healthcheck",
			zap.String("LIVEKIT_API_URL", s.apiURL),
			zap.String("result", healthcheckResult(err)),
			zap.Error(err),
		)
	}()
}

func healthcheckResult(err error) string {
	if err == nil {
		return "ok"
	}
	return "fail"
}

func (s *LiveKitService) GenerateToken(roomName string, userID int64, username string, tokenCtx LiveKitTokenContext) (string, error) {
	at := auth.NewAccessToken(s.apiKey, s.apiSecret)

	grant := &auth.VideoGrant{
		RoomJoin: true,
		Room:     roomName,
	}
	grant.SetCanPublish(true)
	grant.SetCanSubscribe(true)
	grant.SetCanPublishData(true)

	identity := BuildParticipantIdentityForToken(userID, tokenCtx)
	at.SetVideoGrant(grant).
		SetIdentity(identity).
		SetName(username).
		SetValidFor(time.Hour * 6)

	if metadata := buildTokenMetadata(userID, username, tokenCtx, identity); metadata != "" {
		at.SetMetadata(metadata)
	}
	at.SetAttributes(buildTokenAttributes(userID, tokenCtx, identity))

	token, err := at.ToJWT()
	trace := telemetry.TraceContext{
		RoomName:  roomName,
		RequestID: strings.TrimSpace(tokenCtx.RequestID),
		UserID:    userID,
		Identity:  identity,
		SessionID: sanitizeParticipantSessionID(tokenCtx.SessionID),
		DeviceID:  sanitizeParticipantSessionID(tokenCtx.DeviceID),
	}
	if err != nil {
		s.observeCallEvent("livekit_token_issue", trace, "error", map[string]any{
			"error":         err.Error(),
			"identity_mode": IdentityMode(identity),
		})
		s.logger.Error("Failed to generate LiveKit token",
			zap.Error(err),
			zap.String("room", roomName),
			zap.Int64("user_id", userID),
			zap.String("identity", identity),
			zap.String("request_id", trace.RequestID),
			zap.String("device_id", trace.DeviceID),
			zap.String("call_session_id", trace.SessionID),
		)
		return "", fmt.Errorf("failed to generate token: %w", err)
	}
	s.incrementCounter("livekit_tokens_issued_total", map[string]string{
		"identity_mode": IdentityMode(identity),
	})
	s.observeCallEvent("livekit_token_issue", trace, "ok", map[string]any{
		"identity_mode": IdentityMode(identity),
	})
	s.logger.Info("Generated LiveKit token",
		zap.String("room", roomName),
		zap.Int64("user_id", userID),
		zap.String("identity", identity),
		zap.String("request_id", trace.RequestID),
		zap.String("device_id", trace.DeviceID),
		zap.String("call_session_id", trace.SessionID),
	)
	return token, nil
}

func (s *LiveKitService) GetClientURL() string {
	return s.clientURL
}

func (s *LiveKitService) ResolveClientURL(userID int64, username, email string) string {
	return s.urlResolver.Resolve(userID, username, email)
}

func (s *LiveKitService) ResolveClientURLForUser(user *domain.User) string {
	return s.urlResolver.ResolveUser(user)
}

func (s *LiveKitService) GetAPIURL() string {
	return s.apiURL
}

func (s *LiveKitService) GetServerURL() string {
	return s.GetClientURL()
}

func (s *LiveKitService) MinIOEgressEndpoint() string {
	if s.minioCfg.EgressEndpoint != "" {
		return s.minioCfg.EgressEndpoint
	}
	return s.minioCfg.Endpoint
}

func (s *LiveKitService) KickParticipant(ctx context.Context, roomName string, identity string) error {
	startedAt := time.Now()
	_, err := s.roomClient.RemoveParticipant(ctx, &livekit.RoomParticipantIdentity{
		Room:     roomName,
		Identity: identity,
	})
	if err != nil {
		if strings.Contains(err.Error(), "not_found") {
			s.observeLiveKitAPICall("remove_participant", roomName, startedAt, nil)
			s.logger.Info("Participant or room not found during kick, ignoring",
				zap.String("room", roomName), zap.String("identity", identity))
			return nil
		}
		s.observeLiveKitAPICall("remove_participant", roomName, startedAt, err)
		s.logger.Error("Failed to kick participant", zap.Error(err),
			zap.String("room", roomName), zap.String("identity", identity))
		return err
	}
	s.observeLiveKitAPICall("remove_participant", roomName, startedAt, nil)
	return nil
}

func (s *LiveKitService) MuteRoom(ctx context.Context, roomName string, mute bool) error {
	res, err := s.roomClient.ListParticipants(ctx, &livekit.ListParticipantsRequest{Room: roomName})
	if err != nil {
		return err
	}

	for _, p := range res.Participants {
		permission := &livekit.ParticipantPermission{
			CanSubscribe: true,
			CanPublish:   !mute,
		}
		_, err := s.roomClient.UpdateParticipant(ctx, &livekit.UpdateParticipantRequest{
			Room:       roomName,
			Identity:   p.Identity,
			Permission: permission,
		})
		if err != nil {
			s.logger.Warn("Failed to update participant permission", zap.String("identity", p.Identity), zap.Error(err))
		}
		if mute {
			for _, track := range p.Tracks {
				if track.Source == livekit.TrackSource_MICROPHONE {
					_, _ = s.roomClient.MutePublishedTrack(ctx, &livekit.MuteRoomTrackRequest{
						Room:     roomName,
						Identity: p.Identity,
						TrackSid: track.Sid,
						Muted:    true,
					})
				}
			}
		}
	}
	return nil
}

// StartRecording запускает video+audio запись через RoomCompositeEgress (MP4).
func (s *LiveKitService) StartRecording(ctx context.Context, roomName string) (string, error) {
	egressEndpoint := s.minioCfg.EgressEndpoint
	if egressEndpoint == "" {
		egressEndpoint = s.minioCfg.Endpoint
	}

	req := &livekit.RoomCompositeEgressRequest{
		RoomName:  roomName,
		Layout:    "grid",
		AudioOnly: false,
		FileOutputs: []*livekit.EncodedFileOutput{
			{
				FileType: livekit.EncodedFileType_MP4,
				Filepath: fmt.Sprintf("recordings/video/%s-%d.mp4", roomName, time.Now().Unix()),
				Output: &livekit.EncodedFileOutput_S3{
					S3: &livekit.S3Upload{
						AccessKey:      s.minioCfg.AccessKey,
						Secret:         s.minioCfg.SecretKey,
						Endpoint:       egressEndpoint,
						Bucket:         s.minioCfg.Bucket,
						ForcePathStyle: true,
						Region:         "us-east-1",
					},
				},
			},
		},
	}
	info, err := s.egressClient.StartRoomCompositeEgress(ctx, req)
	if err != nil {
		s.logger.Error("Failed to start video recording", zap.Error(err), zap.String("room", roomName))
		return "", err
	}
	return info.EgressId, nil
}

// StartAudioRecording запускает аудио-only запись звонка через RoomCompositeEgress (MP4/AAC).
func (s *LiveKitService) StartAudioRecording(ctx context.Context, roomName string) (string, string, error) {
	// Сохраняем как .mp4 (кодек AAC внутри), так как iOS/AVPlayer не поддерживает OGG/Opus из коробки
	filePath := fmt.Sprintf("recordings/audio/%s-%d.mp4", roomName, time.Now().Unix())
	egressEndpoint := s.minioCfg.EgressEndpoint
	if egressEndpoint == "" {
		egressEndpoint = s.minioCfg.Endpoint
	}
	req := &livekit.RoomCompositeEgressRequest{
		RoomName:  roomName,
		AudioOnly: true,
		FileOutputs: []*livekit.EncodedFileOutput{
			{
				FileType: livekit.EncodedFileType_MP4,
				Filepath: filePath,
				Output: &livekit.EncodedFileOutput_S3{
					S3: &livekit.S3Upload{
						AccessKey:      s.minioCfg.AccessKey,
						Secret:         s.minioCfg.SecretKey,
						Endpoint:       egressEndpoint,
						Bucket:         s.minioCfg.Bucket,
						ForcePathStyle: true,
						Region:         "us-east-1",
					},
				},
			},
		},
	}
	info, err := s.egressClient.StartRoomCompositeEgress(ctx, req)
	if err != nil {
		s.logger.Error("Failed to start audio recording", zap.Error(err), zap.String("room", roomName))
		return "", "", fmt.Errorf("start audio egress: %w", err)
	}
	s.logger.Info("Audio recording started",
		zap.String("egress_id", info.EgressId),
		zap.String("room", roomName),
		zap.String("file_path", filePath),
	)
	return info.EgressId, filePath, nil
}

func (s *LiveKitService) isParticipantOwnedByUser(p *livekit.ParticipantInfo, userID int64) bool {
	if IdentityBelongsToUser(p.Identity, userID) {
		return true
	}
	// Fallback check for raw userID string or userID:session
	userIDStr := strconv.FormatInt(userID, 10)
	if p.Identity == userIDStr || strings.HasPrefix(p.Identity, userIDStr+":") {
		return true
	}
	// Fallback check metadata
	if p.Metadata != "" {
		var meta struct {
			UserID string `json:"user_id"`
		}
		if err := json.Unmarshal([]byte(p.Metadata), &meta); err == nil && meta.UserID == userIDStr {
			return true
		}
	}
	return false
}

type PersonalRecordingTrackLookupDebug struct {
	RoomName         string
	RequestedUserID  int64
	ExpectedIdentity string
	Attempts         int
	Participants     []PersonalRecordingParticipantDebug
	SelectedTrackSID string
	SelectedIdentity string
}

type PersonalRecordingParticipantDebug struct {
	Identity string                        `json:"identity"`
	Name     string                        `json:"name"`
	Metadata string                        `json:"metadata,omitempty"`
	Tracks   []PersonalRecordingTrackDebug `json:"tracks"`
}

type PersonalRecordingTrackDebug struct {
	SID    string `json:"sid"`
	Name   string `json:"name"`
	Type   string `json:"type"`
	Source string `json:"source"`
	Muted  bool   `json:"muted"`
	Width  uint32 `json:"width,omitempty"`
	Height uint32 `json:"height,omitempty"`
}

func newPersonalRecordingTrackLookupDebug(roomName string, ownerUserID int64) PersonalRecordingTrackLookupDebug {
	return PersonalRecordingTrackLookupDebug{
		RoomName:         roomName,
		RequestedUserID:  ownerUserID,
		ExpectedIdentity: BuildParticipantIdentity(ownerUserID, ""),
	}
}

func participantDebugSnapshot(participants *livekit.ListParticipantsResponse) []PersonalRecordingParticipantDebug {
	if participants == nil {
		return nil
	}

	result := make([]PersonalRecordingParticipantDebug, 0, len(participants.Participants))
	for _, p := range participants.Participants {
		item := PersonalRecordingParticipantDebug{
			Identity: p.Identity,
			Name:     p.Name,
			Metadata: p.Metadata,
			Tracks:   make([]PersonalRecordingTrackDebug, 0, len(p.Tracks)),
		}
		for _, t := range p.Tracks {
			item.Tracks = append(item.Tracks, PersonalRecordingTrackDebug{
				SID:    t.Sid,
				Name:   t.Name,
				Type:   t.Type.String(),
				Source: t.Source.String(),
				Muted:  t.Muted,
				Width:  t.Width,
				Height: t.Height,
			})
		}
		result = append(result, item)
	}
	return result
}

func (s *LiveKitService) logRoomState(roomName string, participants *livekit.ListParticipantsResponse) {
	data, _ := protojson.Marshal(participants)
	s.logger.Info("Detailed room state for recording debug",
		zap.String("room", roomName),
		zap.String("participants_json", string(data)),
	)
}

func (s *LiveKitService) logPersonalRecordingTrackLookup(debug PersonalRecordingTrackLookupDebug) {
	s.logger.Warn("Personal recording audio track lookup failed",
		zap.String("room", debug.RoomName),
		zap.Int64("requested_user_id", debug.RequestedUserID),
		zap.String("expected_livekit_identity", debug.ExpectedIdentity),
		zap.Int("retry_attempts", debug.Attempts),
		zap.String("selected_track_sid", debug.SelectedTrackSID),
		zap.String("selected_participant_identity", debug.SelectedIdentity),
		zap.Any("participants", debug.Participants),
	)
}

func (s *LiveKitService) verifyEgressDidNotFailImmediately(ctx context.Context, egressID string) error {
	if strings.TrimSpace(egressID) == "" {
		return fmt.Errorf("egress id is empty")
	}

	var lastErr error
	for attempt := 0; attempt < egressStartVerifyAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(egressStartVerifyInterval):
			}
		}

		info, err := s.GetEgressInfo(ctx, egressID)
		if err != nil {
			lastErr = err
			continue
		}
		switch info.Status {
		case livekit.EgressStatus_EGRESS_FAILED, livekit.EgressStatus_EGRESS_ABORTED, livekit.EgressStatus_EGRESS_LIMIT_REACHED:
			return fmt.Errorf("egress %s ended immediately with status %s: %s", egressID, info.Status.String(), strings.TrimSpace(info.Error))
		case livekit.EgressStatus_EGRESS_ACTIVE, livekit.EgressStatus_EGRESS_COMPLETE:
			return nil
		}
	}

	return lastErr
}

func waitForRetry(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func liveKitErrorCode(err error) int {
	if err == nil {
		return 0
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "code=412"), strings.Contains(msg, " 412 "), strings.Contains(msg, "status code 412"):
		return 412
	case strings.Contains(msg, "code=404"), strings.Contains(msg, "not_found"), strings.Contains(msg, "not found"):
		return 404
	default:
		return 0
	}
}

func isLiveKitAlreadyStoppedError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "not_found") ||
		strings.Contains(msg, "not found") ||
		strings.Contains(msg, "already stopped") ||
		strings.Contains(msg, "source closed") ||
		strings.Contains(msg, "start signal not received") ||
		strings.Contains(msg, "stop egress api") ||
		strings.Contains(msg, "code=412")
}

func liveKitEndReason(msg string) string {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return ""
	}
	lower := strings.ToLower(msg)
	marker := "end reason:"
	idx := strings.Index(lower, marker)
	if idx < 0 {
		return ""
	}
	return strings.Trim(strings.TrimSpace(msg[idx+len(marker):]), "\"")
}

func egressFileResult(info *livekit.EgressInfo) (*string, *int) {
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

func personalRecordingStopResultFromInfo(info *livekit.EgressInfo) PersonalRecordingStopResult {
	result := PersonalRecordingStopResult{
		LiveKitStatus:       info.Status.String(),
		LiveKitErrorMessage: strings.TrimSpace(info.Error),
		LiveKitErrorCode:    liveKitErrorCode(fmt.Errorf("%s", info.Error)),
		EndReason:           liveKitEndReason(info.Error),
	}
	result.FilePath, result.DurationSec = egressFileResult(info)

	switch info.Status {
	case livekit.EgressStatus_EGRESS_COMPLETE:
		result.Disposition = PersonalRecordingStopDispositionAlreadyStopped
		result.FinalStatus = domain.PersonalRecordingStatusCompleted
		result.Reason = "completed"
	case livekit.EgressStatus_EGRESS_ABORTED:
		result.Disposition = PersonalRecordingStopDispositionAborted
		result.FinalStatus = domain.PersonalRecordingStatusAborted
		result.Reason = "aborted"
	case livekit.EgressStatus_EGRESS_FAILED, livekit.EgressStatus_EGRESS_LIMIT_REACHED:
		result.Disposition = PersonalRecordingStopDispositionAborted
		result.FinalStatus = domain.PersonalRecordingStatusFailed
		result.Reason = "failed"
	case livekit.EgressStatus_EGRESS_ACTIVE:
		result.Disposition = PersonalRecordingStopDispositionStopped
		result.FinalStatus = domain.PersonalRecordingStatusStopping
		result.Reason = "user_stop"
	case livekit.EgressStatus_EGRESS_STARTING:
		result.Disposition = PersonalRecordingStopDispositionStopped
		result.FinalStatus = domain.PersonalRecordingStatusStarting
		result.Reason = "starting"
	default:
		result.Disposition = PersonalRecordingStopDispositionAborted
		result.FinalStatus = domain.PersonalRecordingStatusFailed
		if result.LiveKitErrorMessage == "" {
			result.LiveKitErrorMessage = fmt.Sprintf("unexpected egress status: %s", info.Status.String())
		}
		result.Reason = "failed"
	}

	return result
}

// StartPersonalAudioRecording запускает персональную аудио-запись звонка для конкретного пользователя.
// filePath приходит снаружи (содержит owner_user_id), чтобы все записи хранились раздельно.
func (s *LiveKitService) StartPersonalAudioRecording(
	ctx context.Context,
	callID string,
	roomName string,
	ownerUserID int64,
	filePath string,
) (string, string, error) {
	egressEndpoint := s.MinIOEgressEndpoint()

	var targetIdentity string
	var selectedTrackSID string
	var selectedTrackMuted bool
	var selectedTrackSource livekit.TrackSource
	var foundParticipant bool
	var trackReady bool
	var mutedAudioSeen bool
	var fallbackIdentity string
	var fallbackTrackSID string
	var fallbackTrackMuted bool
	var fallbackTrackSource livekit.TrackSource
	debug := newPersonalRecordingTrackLookupDebug(roomName, ownerUserID)

	// Retry loop to handle race conditions where the participant has joined but
	// LiveKit room state is not visible to the API yet.
	for attempt := 0; attempt < personalRecordingMaxRetries; attempt++ {
		debug.Attempts = attempt + 1
		participants, err := s.roomClient.ListParticipants(ctx, &livekit.ListParticipantsRequest{Room: roomName})
		if err != nil {
			s.logger.Warn("Failed to list participants", zap.Error(err), zap.String("room", roomName), zap.Int("attempt", attempt+1))
			if attempt < personalRecordingMaxRetries-1 {
				time.Sleep(personalRecordingRetryInterval)
			}
			continue
		}
		debug.Participants = participantDebugSnapshot(participants)

		foundParticipant = false
		targetIdentity = ""
		selectedTrackSID = ""
		trackReady = false
		mutedAudioSeen = false
		fallbackIdentity = ""
		fallbackTrackSID = ""
		fallbackTrackMuted = false
		fallbackTrackSource = livekit.TrackSource_UNKNOWN

		for _, p := range participants.Participants {
			if s.isParticipantOwnedByUser(p, ownerUserID) {
				foundParticipant = true
				targetIdentity = p.Identity
				debug.SelectedIdentity = targetIdentity
			}
			for _, track := range p.Tracks {
				if track == nil || track.Source != livekit.TrackSource_MICROPHONE || track.Type != livekit.TrackType_AUDIO {
					continue
				}
				if fallbackTrackSID == "" && !track.Muted {
					fallbackIdentity = p.Identity
					fallbackTrackSID = track.Sid
					fallbackTrackMuted = track.Muted
					fallbackTrackSource = track.Source
				}
				if !s.isParticipantOwnedByUser(p, ownerUserID) {
					continue
				}
				if track.Muted {
					mutedAudioSeen = true
					continue
				}
				trackReady = true
				selectedTrackSID = track.Sid
				selectedTrackMuted = track.Muted
				selectedTrackSource = track.Source
				debug.SelectedTrackSID = track.Sid
				break
			}
			if trackReady {
				break
			}
		}

		if foundParticipant && trackReady {
			break
		}

		if attempt < personalRecordingMaxRetries-1 {
			if err := waitForRetry(ctx, personalRecordingRetryInterval); err != nil {
				return "", "", err
			}
		} else {
			s.logRoomState(roomName, participants)
		}
	}

	if !foundParticipant {
		if fallbackTrackSID == "" {
			s.logPersonalRecordingTrackLookup(debug)
			return "", "", fmt.Errorf("%w for user %d in room %s", ErrPersonalRecordingParticipantNotFound, ownerUserID, roomName)
		}
		targetIdentity = fallbackIdentity
		selectedTrackSID = fallbackTrackSID
		selectedTrackMuted = fallbackTrackMuted
		selectedTrackSource = fallbackTrackSource
		trackReady = true
		s.logger.Warn("personal recording owner track not found, using fallback audio track",
			zap.String("call_id", callID),
			zap.String("room", roomName),
			zap.Int64("owner_id", ownerUserID),
			zap.String("selected_audio_track_sid", selectedTrackSID),
			zap.String("selected_audio_track_participant_identity", targetIdentity),
		)
	}
	if !trackReady {
		s.logPersonalRecordingTrackLookup(debug)
		if mutedAudioSeen {
			return "", "", fmt.Errorf("%w for user %d in room %s", ErrPersonalRecordingAudioTrackMuted, ownerUserID, roomName)
		}
		return "", "", fmt.Errorf("%w for user %d in room %s", ErrPersonalRecordingAudioTrackNotFound, ownerUserID, roomName)
	}

	s.logger.Info("egress_start_requested",
		zap.String("call_id", callID),
		zap.String("room", roomName),
		zap.Int64("owner_id", ownerUserID),
		zap.String("participant_identity", targetIdentity),
		zap.String("egress_type", "track_audio"),
		zap.String("selected_audio_track_sid", selectedTrackSID),
		zap.String("selected_audio_track_participant_identity", targetIdentity),
		zap.String("selected_audio_track_source", selectedTrackSource.String()),
		zap.Bool("selected_audio_track_muted", selectedTrackMuted),
		zap.String("file_path", filePath),
		zap.String("minio_endpoint", egressEndpoint),
	)

	req := &livekit.TrackEgressRequest{
		RoomName: roomName,
		TrackId:  selectedTrackSID,
		Output: &livekit.TrackEgressRequest_File{
			File: &livekit.DirectFileOutput{
				Filepath: filePath,
				Output: &livekit.DirectFileOutput_S3{
					S3: &livekit.S3Upload{
						AccessKey:      s.minioCfg.AccessKey,
						Secret:         s.minioCfg.SecretKey,
						Endpoint:       egressEndpoint,
						Bucket:         s.minioCfg.Bucket,
						ForcePathStyle: true,
						Region:         "us-east-1",
					},
				},
			},
		},
	}
	info, err := s.egressClient.StartTrackEgress(ctx, req)
	if err != nil {
		s.logger.Error("Failed to start personal audio recording", zap.Error(err),
			zap.String("room", roomName),
			zap.Int64("owner_id", ownerUserID),
			zap.String("selected_audio_track_sid", selectedTrackSID),
			zap.String("file_path", filePath))
		return "", "", fmt.Errorf("start personal audio egress: %w", err)
	}
	s.logger.Info("egress_start_requested_done",
		zap.String("egress_id", info.EgressId),
		zap.String("room", roomName),
		zap.Int64("owner_id", ownerUserID),
		zap.String("participant_identity", targetIdentity),
		zap.String("egress_type", "track_audio"),
		zap.String("selected_audio_track_sid", selectedTrackSID),
		zap.String("file_path", filePath),
	)
	return info.EgressId, targetIdentity, nil
}

func (s *LiveKitService) WaitForPersonalRecordingActive(ctx context.Context, egressID string) error {
	if err := s.verifyEgressDidNotFailImmediately(ctx, egressID); err != nil {
		return fmt.Errorf("personal audio egress failed to start: %w", err)
	}
	return nil
}

// GetEgressInfo возвращает текущее состояние Egress задачи.
func (s *LiveKitService) GetEgressInfo(ctx context.Context, egressID string) (*livekit.EgressInfo, error) {
	list, err := s.egressClient.ListEgress(ctx, &livekit.ListEgressRequest{EgressId: egressID})
	if err != nil {
		return nil, fmt.Errorf("list egress: %w", err)
	}
	if len(list.Items) == 0 {
		return nil, fmt.Errorf("egress not found: %s", egressID)
	}
	return list.Items[0], nil
}

func (s *LiveKitService) StopRecording(ctx context.Context, egressID string) error {
	_, err := s.egressClient.StopEgress(ctx, &livekit.StopEgressRequest{EgressId: egressID})
	return err
}

func (s *LiveKitService) StopPersonalRecordingEgress(ctx context.Context, egressID string) (PersonalRecordingStopResult, error) {
	if strings.TrimSpace(egressID) == "" {
		return PersonalRecordingStopResult{
			Disposition: PersonalRecordingStopDispositionAlreadyStopped,
			FinalStatus: domain.PersonalRecordingStatusAborted,
			Reason:      "not_started",
		}, nil
	}

	info, err := s.GetEgressInfo(ctx, egressID)
	if err != nil {
		if isLiveKitAlreadyStoppedError(err) {
			return PersonalRecordingStopResult{
				Disposition:         PersonalRecordingStopDispositionAlreadyStopped,
				FinalStatus:         domain.PersonalRecordingStatusFailed,
				Reason:              "not_found",
				LiveKitErrorCode:    liveKitErrorCode(err),
				LiveKitErrorMessage: err.Error(),
				EndReason:           liveKitEndReason(err.Error()),
			}, nil
		}
		return PersonalRecordingStopResult{}, err
	}

	switch info.Status {
	case livekit.EgressStatus_EGRESS_COMPLETE:
		return personalRecordingStopResultFromInfo(info), nil
	case livekit.EgressStatus_EGRESS_FAILED:
		return personalRecordingStopResultFromInfo(info), nil
	case livekit.EgressStatus_EGRESS_ABORTED:
		return personalRecordingStopResultFromInfo(info), nil
	}

	if info.Status == livekit.EgressStatus_EGRESS_ACTIVE {
		_, err = s.egressClient.StopEgress(ctx, &livekit.StopEgressRequest{EgressId: egressID})
		if err != nil {
			if isLiveKitAlreadyStoppedError(err) {
				return PersonalRecordingStopResult{
					Disposition:         PersonalRecordingStopDispositionAborted,
					FinalStatus:         domain.PersonalRecordingStatusFailed,
					Reason:              "stop_failed",
					LiveKitErrorCode:    liveKitErrorCode(err),
					LiveKitErrorMessage: err.Error(),
					EndReason:           liveKitEndReason(err.Error()),
				}, nil
			}
			return PersonalRecordingStopResult{}, err
		}
	}

	deadline := time.Now().Add(time.Duration(egressStartVerifyAttempts) * egressStartVerifyInterval)
	if info.Status == livekit.EgressStatus_EGRESS_ACTIVE {
		deadline = time.Now().Add(20 * time.Second)
	} else if info.Status == livekit.EgressStatus_EGRESS_STARTING {
		deadline = time.Now().Add(10 * time.Second)
	}
	for {
		current, currentErr := s.GetEgressInfo(ctx, egressID)
		if currentErr == nil {
			switch current.Status {
			case livekit.EgressStatus_EGRESS_COMPLETE, livekit.EgressStatus_EGRESS_FAILED, livekit.EgressStatus_EGRESS_ABORTED, livekit.EgressStatus_EGRESS_LIMIT_REACHED:
				return personalRecordingStopResultFromInfo(current), nil
			}
		} else if isLiveKitAlreadyStoppedError(currentErr) {
			return PersonalRecordingStopResult{
				Disposition:         PersonalRecordingStopDispositionAborted,
				FinalStatus:         domain.PersonalRecordingStatusFailed,
				Reason:              "not_found",
				LiveKitErrorCode:    liveKitErrorCode(currentErr),
				LiveKitErrorMessage: currentErr.Error(),
				EndReason:           liveKitEndReason(currentErr.Error()),
			}, nil
		} else {
			return PersonalRecordingStopResult{}, currentErr
		}
		if time.Now().After(deadline) {
			if info.Status == livekit.EgressStatus_EGRESS_ACTIVE {
				return PersonalRecordingStopResult{
					Disposition:   PersonalRecordingStopDispositionStopped,
					FinalStatus:   domain.PersonalRecordingStatusStopping,
					LiveKitStatus: livekit.EgressStatus_EGRESS_ACTIVE.String(),
					Reason:        "stopping_timeout",
				}, nil
			}
			return PersonalRecordingStopResult{
				Disposition:   PersonalRecordingStopDispositionStopped,
				FinalStatus:   domain.PersonalRecordingStatusStarting,
				LiveKitStatus: livekit.EgressStatus_EGRESS_STARTING.String(),
				Reason:        "starting_timeout",
			}, nil
		}
		if err := waitForRetry(ctx, 500*time.Millisecond); err != nil {
			return PersonalRecordingStopResult{}, err
		}
	}
}

func (s *LiveKitService) KickAllParticipants(ctx context.Context, roomName string) (int, error) {
	startedAt := time.Now()
	res, err := s.roomClient.ListParticipants(ctx, &livekit.ListParticipantsRequest{Room: roomName})
	if err != nil {
		s.observeLiveKitAPICall("kick_all_participants", roomName, startedAt, err)
		s.logger.Warn("Failed to list participants for kick all", zap.Error(err))
		return 0, err
	}
	count := 0
	for _, p := range res.Participants {
		s.logger.Info("Kicking participant", zap.String("room", roomName), zap.String("identity", p.Identity))
		_, _ = s.roomClient.RemoveParticipant(ctx, &livekit.RoomParticipantIdentity{
			Room:     roomName,
			Identity: p.Identity,
		})
		count++
	}
	s.observeLiveKitAPICall("kick_all_participants", roomName, startedAt, nil)
	s.observeCallEvent("kick_all_participants", telemetry.TraceContext{RoomName: roomName}, "ok", map[string]any{
		"kicked_count": count,
	})
	return count, nil
}

func (s *LiveKitService) DeleteRoomIfExists(ctx context.Context, roomName string) (bool, error) {
	startedAt := time.Now()
	_, err := s.roomClient.DeleteRoom(ctx, &livekit.DeleteRoomRequest{Room: roomName})
	if err != nil {
		if strings.Contains(err.Error(), "not_found") || strings.Contains(err.Error(), "not found") {
			s.observeLiveKitAPICall("delete_room", roomName, startedAt, nil)
			return false, nil
		}
		s.observeLiveKitAPICall("delete_room", roomName, startedAt, err)
		s.logger.Warn("Failed to delete room", zap.String("room", roomName), zap.Error(err))
		return false, err
	}
	s.observeLiveKitAPICall("delete_room", roomName, startedAt, nil)
	return true, nil
}

func (s *LiveKitService) DeleteRoom(ctx context.Context, roomName string) error {
	_, err := s.DeleteRoomIfExists(ctx, roomName)
	return err
}

func (s *LiveKitService) ValidateWebhook(body string, authHeader string) (*livekit.WebhookEvent, error) {
	startedAt := time.Now()
	if authHeader == "" {
		s.observeLiveKitAPICall("validate_webhook", "", startedAt, fmt.Errorf("missing auth header"))
		return nil, fmt.Errorf("missing auth header")
	}
	v, err := auth.ParseAPIToken(authHeader)
	if err != nil {
		s.observeLiveKitAPICall("validate_webhook", "", startedAt, err)
		return nil, err
	}
	_, claims, err := v.Verify(s.apiSecret)
	if err != nil {
		s.observeLiveKitAPICall("validate_webhook", "", startedAt, err)
		return nil, err
	}
	sha := sha256.Sum256([]byte(body))
	hash := base64.StdEncoding.EncodeToString(sha[:])
	if subtle.ConstantTimeCompare([]byte(claims.Sha256), []byte(hash)) != 1 {
		err := fmt.Errorf("invalid checksum")
		s.observeLiveKitAPICall("validate_webhook", "", startedAt, err)
		return nil, fmt.Errorf("invalid checksum")
	}
	event := &livekit.WebhookEvent{}
	if err := protojson.Unmarshal([]byte(body), event); err != nil {
		s.observeLiveKitAPICall("validate_webhook", "", startedAt, err)
		return nil, err
	}
	s.observeLiveKitAPICall("validate_webhook", "", startedAt, nil)
	return event, nil
}

func (s *LiveKitService) ListParticipants(ctx context.Context, roomName string) ([]*livekit.ParticipantInfo, error) {
	startedAt := time.Now()
	res, err := s.roomClient.ListParticipants(ctx, &livekit.ListParticipantsRequest{Room: roomName})
	if err != nil {
		s.observeLiveKitAPICall("list_participants", roomName, startedAt, err)
		return nil, err
	}
	s.observeLiveKitAPICall("list_participants", roomName, startedAt, nil)
	return res.Participants, nil
}

func (s *LiveKitService) DescribeRoomParticipants(ctx context.Context, roomName string) ([]PersonalRecordingParticipantDebug, error) {
	startedAt := time.Now()
	res, err := s.roomClient.ListParticipants(ctx, &livekit.ListParticipantsRequest{Room: roomName})
	if err != nil {
		s.observeLiveKitAPICall("describe_room_participants", roomName, startedAt, err)
		return nil, err
	}
	s.observeLiveKitAPICall("describe_room_participants", roomName, startedAt, nil)
	return participantDebugSnapshot(res), nil
}

func (s *LiveKitService) KickUser(ctx context.Context, roomName string, userID int64) (int, error) {
	participants, err := s.ListParticipants(ctx, roomName)
	if err != nil {
		return 0, err
	}

	kickedCount := 0
	for _, participant := range participants {
		if !IdentityBelongsToUser(participant.Identity, userID) {
			continue
		}
		if err := s.KickParticipant(ctx, roomName, participant.Identity); err != nil {
			return kickedCount, err
		}
		kickedCount++
	}

	s.observeCallEvent("kick_user_participants", telemetry.TraceContext{
		RoomName: roomName,
		UserID:   userID,
	}, "ok", map[string]any{
		"kicked_count": kickedCount,
	})
	return kickedCount, nil
}

func (s *LiveKitService) observeLiveKitAPICall(operation, roomName string, startedAt time.Time, err error) {
	if s.telemetry == nil {
		return
	}

	status := "ok"
	if err != nil {
		status = "error"
	}
	s.telemetry.IncrementCounter("livekit_api_calls_total", map[string]string{
		"operation": operation,
		"status":    status,
	})
	s.telemetry.ObserveDuration("livekit_api_latency_ms", operation, time.Since(startedAt), err)
	if operation == "list_participants" && err != nil {
		s.telemetry.IncrementCounter("livekit_list_participants_errors_total", nil)
	}
}

func (s *LiveKitService) observeCallEvent(kind string, trace telemetry.TraceContext, status string, details map[string]any) {
	if s.telemetry == nil {
		return
	}
	s.telemetry.RecordEvent(kind, trace, status, details)
}

func (s *LiveKitService) incrementCounter(name string, labels map[string]string) {
	if s.telemetry == nil {
		return
	}
	s.telemetry.IncrementCounter(name, labels)
}
