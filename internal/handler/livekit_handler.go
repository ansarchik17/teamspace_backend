package handler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
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
	"github.com/west6ide/workspace-backend/internal/telemetry"
	"github.com/west6ide/workspace-backend/internal/websocket"
	"go.uber.org/zap"
)

type LiveKitHandler struct {
	service                *service.LiveKitService
	notifService           *service.NotificationService
	groupService           *service.GroupService
	userRepo               *repository.UserRepository
	callRepo               *repository.CallHistoryRepository
	recordingRepo          *repository.CallRecordingRepository
	personalRecordingRepo  *repository.PersonalCallRecordingRepository
	fileService            *service.FileService
	telemetry              *telemetry.CallTelemetry
	logger                 *zap.Logger
	deletePrivateCallRoom  func(context.Context, string) (bool, error)
	listPrivateCallRoom    func(context.Context, string) ([]*livekit.ParticipantInfo, error)
	loadPrivateCallInfo    func(context.Context, string, string) *domain.CallHistory
	privateCallTimeoutQ    chan privateCallTimeoutJob
	now                    func() time.Time
	privateCallGrace       time.Duration
	callAccessBypass       bool
	callLifecycleBypass    bool
	resolveCanonicalUserID func(context.Context, int64) (int64, *domain.User, error)
}

const (
	privateCallRingingTimeout    = 45 * time.Second
	privateCallTimeoutJobTimeout = 10 * time.Second
	privateCallTimeoutQueueSize  = 256
	privateCallCleanupGrace      = 15 * time.Second
	privateCallSamePairBusyStale = 2 * time.Minute
	privateCallAnyPairBusyStale  = 10 * time.Minute
)

type privateCallTimeoutJob struct {
	CallID   uuid.UUID
	RoomName string
	CalleeID int64
	IsVideo  bool
	RunAt    time.Time
}

func NewLiveKitHandler(
	service *service.LiveKitService,
	notifService *service.NotificationService,
	groupService *service.GroupService,
	userRepo *repository.UserRepository,
	callRepo *repository.CallHistoryRepository,
	recordingRepo *repository.CallRecordingRepository,
	personalRecordingRepo *repository.PersonalCallRecordingRepository,
	fileService *service.FileService,
	callTelemetry *telemetry.CallTelemetry,
	logger *zap.Logger,
) *LiveKitHandler {
	handler := &LiveKitHandler{
		service:                service,
		notifService:           notifService,
		groupService:           groupService,
		userRepo:               userRepo,
		callRepo:               callRepo,
		recordingRepo:          recordingRepo,
		personalRecordingRepo:  personalRecordingRepo,
		fileService:            fileService,
		telemetry:              callTelemetry,
		logger:                 logger.With(zap.String("handler", "livekit")),
		deletePrivateCallRoom:  service.DeleteRoomIfExists,
		listPrivateCallRoom:    service.ListParticipants,
		loadPrivateCallInfo:    nil,
		privateCallTimeoutQ:    make(chan privateCallTimeoutJob, privateCallTimeoutQueueSize),
		now:                    time.Now,
		privateCallGrace:       privateCallCleanupGrace,
		callAccessBypass:       liveKitCallAccessBypassEnabled(),
		callLifecycleBypass:    liveKitCallLifecycleBypassEnabled(),
		resolveCanonicalUserID: nil,
	}
	if userRepo != nil {
		handler.resolveCanonicalUserID = userRepo.ResolveCanonicalUserID
	}
	if handler.callAccessBypass {
		handler.logger.Warn("call_access_bypass_enabled",
			zap.String("env", "LIVEKIT_CALL_ACCESS_BYPASS"),
			zap.Bool("access_bypassed", true),
		)
	}
	if handler.callLifecycleBypass {
		handler.logger.Warn("call_lifecycle_bypass_enabled",
			zap.String("env", "LIVEKIT_CALL_LIFECYCLE_BYPASS"),
			zap.Bool("lifecycle_bypassed", true),
		)
	}

	handler.startPrivateCallTimeoutWorker()
	return handler
}

func liveKitCallAccessBypassEnabled() bool {
	for _, key := range []string{"LIVEKIT_CALL_ACCESS_BYPASS", "CALL_ACCESS_BYPASS"} {
		value := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
		if value == "true" || value == "1" || value == "yes" {
			return true
		}
	}
	return false
}

func liveKitCallLifecycleBypassEnabled() bool {
	for _, key := range []string{"LIVEKIT_CALL_LIFECYCLE_BYPASS", "CALL_LIFECYCLE_BYPASS"} {
		value := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
		if value == "true" || value == "1" || value == "yes" {
			return true
		}
	}
	return false
}

func (h *LiveKitHandler) isCallAccessBypassEnabled() bool {
	return h != nil && h.callAccessBypass
}

func (h *LiveKitHandler) isCallLifecycleBypassEnabled() bool {
	return h != nil && h.callLifecycleBypass
}

func getUserDisplayName(c *gin.Context, userID int64) string {
	name := c.GetString("full_name")
	if name == "" {
		name = c.GetString("username")
	}
	if name == "" {
		name = fmt.Sprintf("User %d", userID)
	}
	return name
}

func derefString(v *string) string {
	if v == nil {
		return ""
	}
	return strings.TrimSpace(*v)
}

func (h *LiveKitHandler) resolveEffectiveCallUser(ctx context.Context, userID int64) (int64, *domain.User, error) {
	if userID <= 0 {
		return userID, nil, fmt.Errorf("invalid user id")
	}
	if h.resolveCanonicalUserID == nil {
		return userID, nil, nil
	}
	canonicalUserID, canonicalUser, err := h.resolveCanonicalUserID(ctx, userID)
	if err != nil {
		return userID, nil, err
	}
	if canonicalUserID <= 0 {
		return userID, canonicalUser, nil
	}
	return canonicalUserID, canonicalUser, nil
}

func acceptedByUserID(callInfo *domain.CallHistory) int64 {
	if callInfo == nil || callInfo.AcceptedByUserID == nil {
		return 0
	}
	return *callInfo.AcceptedByUserID
}

func acceptedByDeviceID(callInfo *domain.CallHistory) string {
	if callInfo == nil || callInfo.AcceptedByDeviceID == nil {
		return ""
	}
	return strings.TrimSpace(*callInfo.AcceptedByDeviceID)
}

func acceptedBySessionID(callInfo *domain.CallHistory) string {
	if callInfo == nil || callInfo.AcceptedBySessionID == nil {
		return ""
	}
	return strings.TrimSpace(*callInfo.AcceptedBySessionID)
}

func sameCallEndpoint(callInfo *domain.CallHistory, deviceID, sessionID string) bool {
	deviceID = strings.TrimSpace(deviceID)
	sessionID = strings.TrimSpace(sessionID)
	storedDeviceID := acceptedByDeviceID(callInfo)
	storedSessionID := acceptedBySessionID(callInfo)

	if deviceID != "" && storedDeviceID != "" && deviceID == storedDeviceID {
		return true
	}
	if sessionID != "" && storedSessionID != "" && sessionID == storedSessionID {
		return true
	}
	if deviceID == "" && sessionID == "" {
		return true
	}
	if storedDeviceID == "" && storedSessionID == "" {
		return true
	}
	return false
}

func isStaleAcceptedDevice(callInfo *domain.CallHistory, userID int64, deviceID, sessionID string) bool {
	if callInfo == nil || !domain.IsCallAcceptedStatus(callInfo.Status) {
		return false
	}
	if acceptedByUserID(callInfo) == 0 || acceptedByUserID(callInfo) != userID {
		return false
	}
	return !sameCallEndpoint(callInfo, deviceID, sessionID)
}

func acceptedSessionRealtimeKey(callInfo *domain.CallHistory) string {
	sessionID := acceptedBySessionID(callInfo)
	if sessionID == "" {
		return ""
	}
	return websocket.BuildSessionKey(sessionID)
}

func buildCallActionSuccessResponse(callID string, roomName string, status string, alreadyFinalized bool, message string, accessBypassed bool) gin.H {
	normalizedStatus := domain.NormalizeCallStatus(status)
	return gin.H{
		"success":                    true,
		"call_id":                    callID,
		"room_name":                  roomName,
		"status":                     normalizedStatus,
		"call_status":                normalizedStatus,
		"already_finalized":          alreadyFinalized,
		"message":                    message,
		"access_bypassed":            accessBypassed,
		"access_validation_disabled": true,
	}
}

func (h *LiveKitHandler) bypassAccessEnabled() bool {
	return h.isCallAccessBypassEnabled()
}

func (h *LiveKitHandler) bypassLifecycleEnabled() bool {
	return h.isCallLifecycleBypassEnabled()
}

func (h *LiveKitHandler) buildUserScopedTokenCompatFields(userID int64, username, email, token string) (string, string) {
	if h == nil || h.service == nil {
		return "", token
	}
	return h.service.ResolveClientURL(userID, username, email), token
}

func (h *LiveKitHandler) logCallAccessValidationDisabled(endpoint string, callInfo *domain.CallHistory, currentUserID int64, action string) {
	callID := ""
	roomName := ""
	status := ""
	if callInfo != nil {
		callID = callInfo.CallID.String()
		roomName = strings.TrimSpace(callInfo.RoomID)
		status = domain.NormalizeCallStatus(callInfo.Status)
	}
	h.logger.Warn("call_access_validation_disabled",
		zap.String("endpoint", endpoint),
		zap.String("call_id", callID),
		zap.String("room_name", roomName),
		zap.Int64("current_user_id", currentUserID),
		zap.String("previous_status", status),
		zap.String("action", action),
		zap.Bool("access_validation_disabled", true),
	)
}

func (h *LiveKitHandler) logCallForbiddenIgnored(endpoint string, callInfo *domain.CallHistory, currentUserID int64, originalReason string) {
	callID := ""
	roomName := ""
	if callInfo != nil {
		callID = callInfo.CallID.String()
		roomName = strings.TrimSpace(callInfo.RoomID)
	}
	h.logger.Warn("call_forbidden_ignored",
		zap.String("endpoint", endpoint),
		zap.String("call_id", callID),
		zap.String("room_name", roomName),
		zap.Int64("current_user_id", currentUserID),
		zap.String("original_reason", originalReason),
		zap.Bool("access_validation_disabled", true),
	)
}

type declineDecision struct {
	nextStatus         string
	message            string
	eventType          string
	notificationReason string
	effectiveAction    string
}

func (h *LiveKitHandler) decideDeclineAction(callInfo *domain.CallHistory, currentUserID int64, reason string) declineDecision {
	currentStatus := domain.NormalizeCallStatus("")
	if callInfo != nil {
		currentStatus = domain.NormalizeCallStatus(callInfo.Status)
	}

	switch currentStatus {
	case domain.CallStatusAccepted, domain.CallStatusOngoing, domain.CallStatusConnecting:
		return declineDecision{
			nextStatus:         domain.CallStatusEnded,
			message:            "Call ended",
			eventType:          "call_ended",
			notificationReason: "ended",
			effectiveAction:    "end",
		}
	default:
		if callInfo != nil && callInfo.FromUserID == currentUserID {
			return declineDecision{
				nextStatus:         domain.CallStatusCancelled,
				message:            "Call cancelled",
				eventType:          "call_cancelled",
				notificationReason: "canceled",
				effectiveAction:    "cancel",
			}
		}
		if strings.TrimSpace(reason) == "busy" {
			return declineDecision{
				nextStatus:         domain.CallStatusBusy,
				message:            "Call declined as busy",
				eventType:          "call_declined",
				notificationReason: "busy",
				effectiveAction:    "decline",
			}
		}
		return declineDecision{
			nextStatus:         domain.CallStatusDeclined,
			message:            "Call declined",
			eventType:          "call_declined",
			notificationReason: "declined",
			effectiveAction:    "decline",
		}
	}
}

func (h *LiveKitHandler) respondEndCallAlreadyFinalized(c *gin.Context, callID string, userID int64, currentStatus string, requestedAction string, deviceID string, sessionID string) {
	h.logger.Info("EndCall ignored because call is already finalized",
		zap.String("call_id", callID),
		zap.Int64("user_id", userID),
		zap.String("current_status", currentStatus),
		zap.String("requested_action", requestedAction),
		zap.String("device_id", deviceID),
		zap.String("session_id", sessionID),
		zap.Bool("already_finalized", true),
	)
	respondJSON(c, http.StatusOK, buildCallActionSuccessResponse(callID, "", currentStatus, true, "Call already finalized", h.bypassAccessEnabled()))
}

func decidePrivateCallTimeoutStatus(currentStatus string, activeUsers int) (string, bool) {
	if !domain.IsCallRingingStatus(currentStatus) {
		return "", false
	}
	switch {
	case activeUsers > 1:
		return domain.CallStatusAccepted, false
	case activeUsers > 0:
		return domain.CallStatusConnecting, false
	default:
		return domain.CallStatusNoAnswer, true
	}
}

func privateCallLastActivityAt(callInfo *domain.CallHistory) time.Time {
	if callInfo == nil {
		return time.Time{}
	}

	lastActivityAt := callInfo.CreatedAt
	if callInfo.UpdatedAt.After(lastActivityAt) {
		lastActivityAt = callInfo.UpdatedAt
	}
	if callInfo.AcceptedAt != nil && callInfo.AcceptedAt.After(lastActivityAt) {
		lastActivityAt = *callInfo.AcceptedAt
	}
	if callInfo.EndedAt != nil && callInfo.EndedAt.After(lastActivityAt) {
		lastActivityAt = *callInfo.EndedAt
	}

	return lastActivityAt
}

func activeCallBelongsToUsers(callInfo *domain.CallHistory, userA, userB int64) bool {
	if callInfo == nil {
		return false
	}
	return (callInfo.FromUserID == userA && callInfo.ToUserID == userB) ||
		(callInfo.FromUserID == userB && callInfo.ToUserID == userA)
}

func (h *LiveKitHandler) hasConfirmedActiveOtherPrivateCall(ctx context.Context, userID int64, excludeCallID uuid.UUID) (bool, string, error) {
	if h == nil || h.callRepo == nil {
		return false, "", nil
	}

	activeCall, err := h.callRepo.GetActiveCallByUserID(ctx, userID)
	if err != nil || activeCall == nil {
		return false, "", err
	}
	if activeCall.CallID == excludeCallID {
		return false, activeCall.CallID.String(), nil
	}
	if isGroupCallHistory(activeCall) || !domain.IsCallAcceptedStatus(activeCall.Status) {
		return false, activeCall.CallID.String(), nil
	}

	listParticipants := h.listPrivateCallRoom
	if listParticipants == nil && h.service != nil {
		listParticipants = h.service.ListParticipants
	}
	if listParticipants == nil {
		return false, activeCall.CallID.String(), nil
	}

	parts, err := listParticipants(ctx, activeCall.RoomID)
	if err != nil {
		h.logger.Warn("active_other_call_livekit_check_failed",
			zap.String("call_id", activeCall.CallID.String()),
			zap.String("room_name", activeCall.RoomID),
			zap.Int64("user_id", userID),
			zap.Error(err),
		)
		h.markDirectCallParticipantsLeft(ctx, activeCall.CallID)
		go h.forceClosePrivateCallRoom(context.Background(), activeCall.CallID.String(), activeCall.RoomID, "busy_decline_zombie_other_call")
		return false, activeCall.CallID.String(), nil
	}

	userInRoom := false
	liveKitUserIDs := make(map[int64]struct{}, len(parts))
	for _, part := range parts {
		if part == nil {
			continue
		}
		identity, parseErr := service.ParseParticipantIdentity(part.Identity)
		if parseErr != nil {
			continue
		}
		liveKitUserIDs[identity.UserID] = struct{}{}
		if identity.UserID == userID {
			userInRoom = true
		}
	}
	if userInRoom && len(liveKitUserIDs) >= 2 {
		return true, activeCall.CallID.String(), nil
	}

	h.logger.Warn("active_other_call_without_live_participants",
		zap.String("call_id", activeCall.CallID.String()),
		zap.String("room_name", activeCall.RoomID),
		zap.Int64("user_id", userID),
		zap.Int("livekit_user_count", len(liveKitUserIDs)),
		zap.Bool("user_in_room", userInRoom),
	)
	h.markDirectCallParticipantsLeft(ctx, activeCall.CallID)
	go h.forceClosePrivateCallRoom(context.Background(), activeCall.CallID.String(), activeCall.RoomID, "busy_decline_empty_other_call")
	return false, activeCall.CallID.String(), nil
}

func liveKitBypassWarning(callInfo *domain.CallHistory, bypass bool) string {
	if !bypass || callInfo == nil {
		return ""
	}
	if domain.IsCallFinalStatus(callInfo.Status) {
		return "call is terminal, joined by bypass mode"
	}
	return ""
}

func participantIdentities(participants []*livekit.ParticipantInfo) []string {
	identities := make([]string, 0, len(participants))
	for _, participant := range participants {
		if participant == nil {
			continue
		}
		identities = append(identities, strings.TrimSpace(participant.Identity))
	}
	return identities
}

func acceptCallUserFromCall(callInfo *domain.CallHistory, userID int64, fallbackName string) AcceptCallUser {
	user := AcceptCallUser{ID: userID, Name: fallbackName}
	if callInfo == nil {
		return user
	}
	if callInfo.FromUserID == userID {
		user.Name = derefString(callInfo.FromName)
		return user
	}
	if callInfo.ToUserID == userID && fallbackName != "" {
		user.Name = fallbackName
	}
	return user
}

func (h *LiveKitHandler) resolveDirectCallForAccess(ctx context.Context, callID string, roomName string) (*domain.CallHistory, error) {
	if strings.TrimSpace(callID) != "" {
		uid, err := uuid.Parse(strings.TrimSpace(callID))
		if err != nil {
			return nil, apperrors.Validation("invalid call_id")
		}
		callInfo, err := h.callRepo.GetByCallID(ctx, uid)
		if err != nil {
			return nil, apperrors.InternalWithErr("failed to resolve call", err)
		}
		if callInfo != nil {
			return callInfo, nil
		}
	}

	if strings.TrimSpace(roomName) == "" {
		return nil, apperrors.NotFound("call not found")
	}

	callInfo, err := h.callRepo.GetByRoomID(ctx, strings.TrimSpace(roomName))
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to resolve call", err)
	}
	if callInfo == nil {
		return nil, apperrors.NotFound("call not found")
	}
	return callInfo, nil
}

func (h *LiveKitHandler) ensureDirectCallAccess(c *gin.Context, endpoint string, callInfo *domain.CallHistory, userID int64) bool {
	ok, _ := h.ensureDirectCallAccessWithBypass(c, endpoint, callInfo, userID)
	return ok
}

func (h *LiveKitHandler) ensureDirectCallAccessWithBypass(c *gin.Context, endpoint string, callInfo *domain.CallHistory, userID int64) (bool, bool) {
	action := strings.TrimPrefix(endpoint, "calls.")
	if h.isCallAccessBypassEnabled() {
		h.logCallAccessValidationDisabled(endpoint, callInfo, userID, action)
		if callInfo == nil {
			h.logCallForbiddenIgnored(endpoint, callInfo, userID, "call_not_found")
		}
	} else {
		if callInfo == nil {
			h.respondError(c, apperrors.NotFound("call not found"))
			return false, false
		}
	}

	if callInfo == nil {
		return true, true
	}

	canonicalUserID := userID
	if resolvedUserID, _, err := h.resolveEffectiveCallUser(c.Request.Context(), userID); err == nil && resolvedUserID > 0 {
		canonicalUserID = resolvedUserID
	}
	if callInfo.FromUserID == userID || callInfo.ToUserID == userID || callInfo.FromUserID == canonicalUserID || callInfo.ToUserID == canonicalUserID {
		return true, h.isCallAccessBypassEnabled()
	}

	if h.callRepo != nil {
		isParticipant, err := h.callRepo.IsUserParticipant(c.Request.Context(), callInfo.CallID, userID)
		if err == nil && isParticipant {
			return true, h.isCallAccessBypassEnabled()
		}
		if err == nil && canonicalUserID != userID {
			if participantCanonical, canonicalErr := h.callRepo.IsUserParticipant(c.Request.Context(), callInfo.CallID, canonicalUserID); canonicalErr == nil && participantCanonical {
				return true, h.isCallAccessBypassEnabled()
			}
		}
	}

	if h.isCallAccessBypassEnabled() {
		h.logCallForbiddenIgnored(endpoint, callInfo, userID, "user_not_participant_or_caller_callee")
		return true, true
	}

	h.respondError(c, apperrors.Forbidden("access denied to call"))
	return false, false
}

func (h *LiveKitHandler) logCallLifecycleBypassed(callInfo *domain.CallHistory, currentUserID int64, previousStatus string, effectiveAction string, roomName string) {
	callID := ""
	if callInfo != nil {
		callID = callInfo.CallID.String()
		if roomName == "" {
			roomName = callInfo.RoomID
		}
	}
	h.logger.Warn("call_decline_status_bypassed",
		zap.String("call_id", callID),
		zap.String("room_name", roomName),
		zap.Int64("current_user_id", currentUserID),
		zap.String("previous_status", previousStatus),
		zap.String("action", "decline"),
		zap.String("effective_action", effectiveAction),
		zap.Bool("lifecycle_bypassed", true),
	)
}

func (h *LiveKitHandler) logCallLifecycleIdempotent(callInfo *domain.CallHistory, currentUserID int64, roomName string) {
	callID := ""
	status := ""
	if callInfo != nil {
		callID = callInfo.CallID.String()
		status = callInfo.Status
		if roomName == "" {
			roomName = callInfo.RoomID
		}
	}
	h.logger.Info("call_lifecycle_idempotent",
		zap.String("call_id", callID),
		zap.String("room_name", roomName),
		zap.Int64("current_user_id", currentUserID),
		zap.String("status", status),
		zap.Bool("already_finalized", true),
	)
}

func callActionSuccess(callID, roomName, status, message string, accessBypassed, alreadyFinalized bool) gin.H {
	return buildCallActionSuccessResponse(callID, roomName, status, alreadyFinalized, message, accessBypassed)
}

func decorateCallActionWithAcceptedEndpoint(resp gin.H, callInfo *domain.CallHistory) gin.H {
	if resp == nil || callInfo == nil {
		return resp
	}
	if value := acceptedByUserID(callInfo); value != 0 {
		resp["accepted_by_user_id"] = value
	}
	if value := acceptedByDeviceID(callInfo); value != "" {
		resp["accepted_device_id"] = value
		resp["accepted_by_device_id"] = value
	}
	if value := acceptedBySessionID(callInfo); value != "" {
		resp["accepted_session_id"] = value
		resp["accepted_by_session_id"] = value
	}
	return resp
}

func localDismissOnlyResponse(callID, roomName, status, message, reason string, accessBypassed bool, callInfo *domain.CallHistory) gin.H {
	resp := callActionSuccess(callID, roomName, status, message, accessBypassed, false)
	resp["local_dismiss_only"] = true
	if strings.TrimSpace(reason) != "" {
		resp["reason"] = strings.TrimSpace(reason)
	}
	return decorateCallActionWithAcceptedEndpoint(resp, callInfo)
}

func parseGroupIDFromRoomName(roomName string) (uuid.UUID, bool) {
	roomName = strings.TrimSpace(roomName)
	if !strings.HasPrefix(roomName, "call_group_") {
		return uuid.Nil, false
	}

	parts := strings.Split(roomName, "_")
	if len(parts) < 4 {
		return uuid.Nil, false
	}

	groupID, err := uuid.Parse(parts[2])
	if err != nil {
		return uuid.Nil, false
	}

	return groupID, true
}

func (h *LiveKitHandler) startPrivateCallTimeoutWorker() {
	go func() {
		for job := range h.privateCallTimeoutQ {
			go h.waitAndHandlePrivateCallTimeout(job)
		}
	}()
}

func (h *LiveKitHandler) enqueuePrivateCallTimeout(callID uuid.UUID, roomName string, calleeID int64, isVideo bool) {
	job := privateCallTimeoutJob{
		CallID:   callID,
		RoomName: strings.TrimSpace(roomName),
		CalleeID: calleeID,
		IsVideo:  isVideo,
		RunAt:    time.Now().UTC().Add(privateCallRingingTimeout),
	}

	select {
	case h.privateCallTimeoutQ <- job:
	default:
		h.logger.Warn("private call timeout queue is full, falling back to direct timer",
			zap.String("call_id", callID.String()),
			zap.String("room", roomName),
		)
		go h.waitAndHandlePrivateCallTimeout(job)
	}
}

func (h *LiveKitHandler) waitAndHandlePrivateCallTimeout(job privateCallTimeoutJob) {
	if delay := time.Until(job.RunAt); delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		<-timer.C
	}

	h.handlePrivateCallTimeout(job)
}

func (h *LiveKitHandler) handlePrivateCallTimeout(job privateCallTimeoutJob) {
	ctx, cancel := context.WithTimeout(context.Background(), privateCallTimeoutJobTimeout)
	defer cancel()

	callInfo, err := h.callRepo.GetByCallID(ctx, job.CallID)
	if err != nil || callInfo == nil {
		return
	}

	if deadline := callInfo.CreatedAt.Add(privateCallRingingTimeout); time.Now().UTC().Before(deadline) {
		h.logger.Debug("skipping private call timeout because ringing deadline has not passed yet",
			zap.String("call_id", job.CallID.String()),
			zap.String("room", job.RoomName),
			zap.Time("deadline", deadline),
		)
		return
	}

	if !(domain.IsCallRingingStatus(callInfo.Status) || callInfo.Status == domain.CallStatusCanceled || callInfo.Status == domain.CallStatusDeclined) {
		return
	}

	if domain.IsCallRingingStatus(callInfo.Status) {
		if activeUsers, err := h.countDistinctLiveKitUsers(ctx, job.RoomName); err == nil {
			nextStatus, shouldFinalize := decidePrivateCallTimeoutStatus(callInfo.Status, activeUsers)
			if !shouldFinalize && nextStatus != "" {
				h.logger.Info("Skipping call timeout cleanup because participants are already in room",
					zap.String("call_id", job.CallID.String()),
					zap.String("room", job.RoomName),
					zap.Int("active_users", activeUsers),
					zap.String("next_status", nextStatus),
				)
				_, _, _ = h.callRepo.TransitionCallStatus(ctx, job.CallID, nextStatus, domain.CallStatusInitiated, domain.CallStatusRinging)
				return
			}
		}
	}

	h.logger.Info("Call timeout or cleanup check triggered",
		zap.String("call_id", job.CallID.String()),
		zap.String("status", callInfo.Status),
		zap.Duration("ringing_timeout", privateCallRingingTimeout),
	)

	if domain.IsCallRingingStatus(callInfo.Status) {
		if _, updated, _ := h.callRepo.TransitionCallStatus(ctx, job.CallID, domain.CallStatusNoAnswer, domain.CallStatusInitiated, domain.CallStatusRinging); updated {
			if err := h.notifService.SendCallSyncEvent(
				ctx,
				callInfo.ToUserID,
				job.CallID,
				job.RoomName,
				"call_ended",
				"missed",
				job.IsVideo,
				"",
				"",
			); err != nil {
				h.logger.Warn("failed to fan out timeout call_ended to receiver devices",
					zap.Error(err),
					zap.String("call_id", job.CallID.String()),
					zap.Int64("receiver_id", callInfo.ToUserID),
				)
			}
		}
	}

	if callInfo.Status == domain.CallStatusCanceled || callInfo.Status == domain.CallStatusDeclined {
		if _, err := h.service.ListParticipants(ctx, job.RoomName); err != nil {
			if isLiveKitRoomNotFound(err) {
				h.logger.Info("Skipping timeout cleanup because room is already deleted",
					zap.String("call_id", job.CallID.String()),
					zap.String("room", job.RoomName),
					zap.String("status", callInfo.Status),
				)
				return
			}
		}
	}

	h.cleanupPrivateCallRoom(ctx, job.CallID.String(), job.RoomName, "start_call_timeout_cleanup")
}

type callStatusAccess struct {
	IsParticipant         bool
	CanAccept             bool
	CanDecline            bool
	CanJoin               bool
	IsExpired             bool
	CallType              string
	ActiveOnCurrentDevice bool
	ActiveDeviceID        string
	AcceptedByDeviceID    string
	AcceptedByUserID      int64
	ExpiresAt             time.Time
}

func isGroupCallHistory(callInfo *domain.CallHistory) bool {
	if callInfo == nil {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(callInfo.CallType), domain.CallTypeGroup) {
		return true
	}
	return callInfo.ToUserID == 0 || strings.HasPrefix(strings.TrimSpace(callInfo.RoomID), "call_group_")
}

func buildCallStatusAccess(callInfo *domain.CallHistory, userID int64, currentDeviceID string, isParticipant bool, now time.Time) callStatusAccess {
	isRinging := domain.IsCallRingingStatus(callInfo.Status)
	isAccepted := domain.IsCallAcceptedStatus(callInfo.Status)
	isFinal := domain.IsCallFinalStatus(callInfo.Status)
	isGroupCall := isGroupCallHistory(callInfo)
	callType := "private"
	if isGroupCall {
		callType = "group"
	}

	expiresAt := callInfo.CreatedAt.Add(privateCallRingingTimeout)
	acceptedByDeviceID := derefString(callInfo.AcceptedByDeviceID)
	acceptedByUserID := int64(0)
	if callInfo.AcceptedByUserID != nil {
		acceptedByUserID = *callInfo.AcceptedByUserID
	}

	activeOnCurrentDevice := true
	if !isGroupCall && acceptedByUserID == userID && acceptedByDeviceID != "" && strings.TrimSpace(currentDeviceID) != "" {
		activeOnCurrentDevice = acceptedByDeviceID == strings.TrimSpace(currentDeviceID)
	}

	return callStatusAccess{
		IsParticipant:         isParticipant,
		CanAccept:             isRinging && isParticipant && !isFinal,
		CanDecline:            isRinging && isParticipant && !isFinal,
		CanJoin:               isAccepted && isParticipant,
		IsExpired:             now.After(expiresAt) && isRinging,
		CallType:              callType,
		ActiveOnCurrentDevice: activeOnCurrentDevice,
		ActiveDeviceID:        acceptedByDeviceID,
		AcceptedByDeviceID:    acceptedByDeviceID,
		AcceptedByUserID:      acceptedByUserID,
		ExpiresAt:             expiresAt,
	}
}

func (h *LiveKitHandler) respondDirectCallConflict(c *gin.Context, callerID, requestedCalleeID int64, existingCall *domain.CallHistory) {
	if existingCall == nil {
		respondJSON(c, http.StatusConflict, gin.H{
			"error":   "call_busy",
			"message": apiMessage(c, "Call already exists"),
			"reason":  "busy",
		})
		return
	}

	message := "Call already exists for these users"
	isIncomingForCaller := existingCall.Status == "initiated" &&
		existingCall.FromUserID == requestedCalleeID &&
		existingCall.ToUserID == callerID

	if isIncomingForCaller {
		message = "You already have an incoming call from this user"
		h.notifService.SendIncomingCallWS(
			callerID,
			existingCall.FromUserID,
			derefString(existingCall.FromName),
			existingCall.CallID,
			existingCall.RoomID,
			derefString(existingCall.ChatID),
			existingCall.IsVideo,
		)
	}

	respondJSON(c, http.StatusConflict, gin.H{
		"error":         "call_busy",
		"message":       message,
		"reason":        "busy",
		"call_id":       existingCall.CallID.String(),
		"room_name":     existingCall.RoomID,
		"status":        existingCall.Status,
		"from_user_id":  existingCall.FromUserID,
		"to_user_id":    existingCall.ToUserID,
		"incoming_call": isIncomingForCaller,
	})
}

type StartCallRequest struct {
	CalleeID int64  `json:"callee_id" binding:"required"`
	ChatID   string `json:"chat_id"`
	Priority string `json:"priority"` // "normal" or "high"
	IsVideo  *bool  `json:"is_video,omitempty"`
}

type StartGroupCallRequest struct {
	GroupID string `json:"group_id" binding:"required"`
}

type StartCallResponse struct {
	Token    string `json:"token"`
	URL      string `json:"url"`
	RoomName string `json:"room_name"`
	CallID   string `json:"call_id"`
}

type AcceptCallRequest struct {
	RoomName  string `json:"room_name"`
	CallID    string `json:"call_id"`    // Optional: to mark call as accepted in history
	DeviceID  string `json:"device_id"`  // Optional legacy fallback when headers are unavailable
	SessionID string `json:"session_id"` // Optional legacy fallback when headers are unavailable
}

type AcceptCallUser struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Avatar string `json:"avatar"`
}

type AcceptCallResponse struct {
	Success                  bool           `json:"success"`
	CallID                   string         `json:"call_id"`
	Status                   string         `json:"status"`
	RoomID                   string         `json:"room_id"`
	RoomName                 string         `json:"room_name,omitempty"`
	LivekitURL               string         `json:"livekit_url"`
	LivekitToken             string         `json:"livekit_token"`
	Caller                   AcceptCallUser `json:"caller"`
	Callee                   AcceptCallUser `json:"callee"`
	IsVideo                  bool           `json:"is_video"`
	Token                    string         `json:"token,omitempty"`
	URL                      string         `json:"url,omitempty"`
	Message                  string         `json:"message,omitempty"`
	AlreadyFinalized         bool           `json:"already_finalized,omitempty"`
	LocalDismissOnly         bool           `json:"local_dismiss_only,omitempty"`
	AlreadyAcceptedElsewhere bool           `json:"already_accepted_elsewhere,omitempty"`
	AnsweredElsewhere        bool           `json:"answered_elsewhere,omitempty"`
	AcceptedByUserID         int64          `json:"accepted_by_user_id,omitempty"`
	AcceptedDeviceID         string         `json:"accepted_device_id,omitempty"`
	AcceptedSessionID        string         `json:"accepted_session_id,omitempty"`
	AccessBypassed           bool           `json:"access_bypassed,omitempty"`
	AccessValidationDisabled bool           `json:"access_validation_disabled,omitempty"`
	Warning                  string         `json:"warning,omitempty"`
}

type JoinCallRequest struct {
	CallID   string `json:"call_id"`
	RoomName string `json:"room_name"`
}

type JoinCallResponse struct {
	Success                  bool           `json:"success"`
	CallID                   string         `json:"call_id"`
	Status                   string         `json:"status"`
	RoomName                 string         `json:"room_name"`
	RoomID                   string         `json:"room_id,omitempty"`
	LivekitURL               string         `json:"livekit_url"`
	LivekitToken             string         `json:"livekit_token"`
	Token                    string         `json:"token,omitempty"`
	URL                      string         `json:"url,omitempty"`
	ParticipantsCount        int            `json:"participants_count"`
	Caller                   AcceptCallUser `json:"caller"`
	Callee                   AcceptCallUser `json:"callee"`
	IsVideo                  bool           `json:"is_video"`
	Message                  string         `json:"message,omitempty"`
	AlreadyFinalized         bool           `json:"already_finalized,omitempty"`
	LocalDismissOnly         bool           `json:"local_dismiss_only,omitempty"`
	AlreadyAcceptedElsewhere bool           `json:"already_accepted_elsewhere,omitempty"`
	AnsweredElsewhere        bool           `json:"answered_elsewhere,omitempty"`
	AcceptedByUserID         int64          `json:"accepted_by_user_id,omitempty"`
	AcceptedDeviceID         string         `json:"accepted_device_id,omitempty"`
	AcceptedSessionID        string         `json:"accepted_session_id,omitempty"`
	AccessBypassed           bool           `json:"access_bypassed,omitempty"`
	AccessValidationDisabled bool           `json:"access_validation_disabled,omitempty"`
	Warning                  string         `json:"warning,omitempty"`
}

type KickRequest struct {
	RoomName string `json:"room_name" binding:"required"`
	Identity string `json:"identity" binding:"required"`
}

type MuteAllRequest struct {
	RoomName string `json:"room_name" binding:"required"`
	Mute     bool   `json:"mute"`
}

type RecordRequest struct {
	RoomName string `json:"room_name" binding:"required"`
}

type InviteToCallRequest struct {
	CallID   string `json:"call_id"`
	CalleeID int64  `json:"callee_id" binding:"required"`
	RoomName string `json:"room_name" binding:"required"`
	ChatID   string `json:"chat_id"`
	IsVideo  *bool  `json:"is_video,omitempty"`
}

type DeclineCallRequest struct {
	CallID    string `json:"call_id"`
	Reason    string `json:"reason"`    // "busy", "declined"
	RoomName  string `json:"room_name"` // Optional fallback
	DeviceID  string `json:"device_id"`
	SessionID string `json:"session_id"`
}

type EndCallRequest struct {
	CallID    string `json:"call_id"`
	RoomName  string `json:"room_name"`
	ForceEnd  bool   `json:"force_end"`
	DeviceID  string `json:"device_id"`
	SessionID string `json:"session_id"`
}

// ─── Audio Recording ────────────────────────────────────────────────────────

// StartAudioRecordingRequest — реквест на начало аудио-записи.
type StartAudioRecordingRequest struct {
	// call_id или room_name — один из них обязателен.
	CallID   string `json:"call_id"   binding:"required"`
	RoomName string `json:"room_name" binding:"required"`
}

// StartAudioRecordingResponse — ответ после старта записи.
type StartAudioRecordingResponse struct {
	EgressID string `json:"egress_id"`
	FilePath string `json:"file_path"`
	Status   string `json:"status"`
}

// AudioRecordingStatusResponse — ответ поллинга статуса.
type AudioRecordingStatusResponse struct {
	EgressID  string `json:"egress_id"`
	Status    string `json:"status"`
	FilePath  string `json:"file_path,omitempty"`
	DurationS *int   `json:"duration_s,omitempty"`
}

// StartCall godoc
// @Summary Start a call
// @Description Initiates a call and sends a push notification to the recipient
// @Tags calls
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body StartCallRequest true "Start Call"
// @Success 200 {object} StartCallResponse
// @Failure 400 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /calls/start [post]
func (h *LiveKitHandler) StartCall(c *gin.Context) {
	callerID := middleware.MustGetUserID(c)
	callerName := getUserDisplayName(c, callerID)
	tokenCtx := h.tokenContextFromRequest(c)

	var req StartCallRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	if req.CalleeID == callerID {
		h.respondError(c, apperrors.BadRequest("cannot start a call with yourself"))
		return
	}

	if err := h.callRepo.EndStaleDirectCallsForUsers(c.Request.Context(), callerID, req.CalleeID); err != nil {
		h.logger.Warn("failed to cleanup stale direct calls before start",
			zap.Int64("caller_id", callerID),
			zap.Int64("callee_id", req.CalleeID),
			zap.Error(err),
		)
	}

	roomName := fmt.Sprintf("call_%d_%d_%d", callerID, req.CalleeID, time.Now().Unix())
	callID := uuid.New()
	h.logger.Info("create_call_started",
		zap.String("call_id", callID.String()),
		zap.Int64("caller_id", callerID),
		zap.Int64("callee_id", req.CalleeID),
		zap.String("room_id", roomName),
		zap.String("status", domain.CallStatusRinging),
	)

	// 0. Busy Check: if callee is already in an active call, return 409 Busy.
	if activeCall, err := h.callRepo.GetActiveCallByUserID(c.Request.Context(), req.CalleeID); err == nil && activeCall != nil {
		// Verify if the room is still active in LiveKit
		parts, errList := h.service.ListParticipants(c.Request.Context(), activeCall.RoomID)
		liveKitUserIDs := make(map[int64]struct{})

		isZombie := false
		isCalleeInRoom := false
		activeCallStale := false
		samePairStale := false

		if errList == nil {
			for _, p := range parts {
				if parsed, parseErr := service.ParseParticipantIdentity(p.Identity); parseErr == nil {
					liveKitUserIDs[parsed.UserID] = struct{}{}
				}
				if service.IdentityBelongsToUser(p.Identity, req.CalleeID) {
					isCalleeInRoom = true
				}
			}
		}
		now := time.Now()
		if h.now != nil {
			now = h.now()
		}
		lastActivityAt := privateCallLastActivityAt(activeCall)
		activeCallStale = now.Sub(lastActivityAt) >= privateCallAnyPairBusyStale
		if activeCallBelongsToUsers(activeCall, callerID, req.CalleeID) {
			samePairStale = now.Sub(lastActivityAt) >= privateCallSamePairBusyStale
		}

		h.logger.Warn("LiveKit busy check details",
			zap.Int64("callee_id", req.CalleeID),
			zap.String("room_id", activeCall.RoomID),
			zap.Int("parts_len", len(parts)),
			zap.Int("livekit_user_count", len(liveKitUserIDs)),
			zap.Bool("callee_in_room", isCalleeInRoom),
			zap.Bool("active_call_stale", activeCallStale),
			zap.Bool("same_pair_stale", samePairStale),
			zap.Time("last_activity_at", lastActivityAt),
			zap.Error(errList))

		if errList != nil {
			// Any error from LiveKit (TLS, timeout, not_found, etc.) means we can't
			// confirm the room is alive → treat as zombie to avoid blocking the callee
			// permanently due to infrastructure issues.
			isZombie = true
		} else if len(parts) == 0 {
			isZombie = true
		} else if len(liveKitUserIDs) < 2 {
			isZombie = true
		} else if !isCalleeInRoom {
			isZombie = true
		} else if samePairStale {
			isZombie = true
		} else if activeCallStale {
			isZombie = true
		}

		if isZombie {
			h.logger.Warn("Zombie active call found, cleaning up",
				zap.String("call_id", activeCall.CallID.String()),
				zap.String("room_id", activeCall.RoomID),
				zap.Int64("user_id", req.CalleeID),
				zap.Int("livekit_user_count", len(liveKitUserIDs)),
				zap.Bool("callee_in_room", isCalleeInRoom),
				zap.Error(errList),
			)
			_ = h.callRepo.UpdateCallStatus(c.Request.Context(), activeCall.CallID, domain.CallStatusEnded)
			h.markDirectCallParticipantsLeft(c.Request.Context(), activeCall.CallID)
			go h.forceClosePrivateCallRoom(context.Background(), activeCall.CallID.String(), activeCall.RoomID, "busy_check_zombie_call")
		} else {
			h.logger.Info("Callee is busy (in active call), returning busy status",
				zap.Int64("callee_id", req.CalleeID),
				zap.String("active_call_id", activeCall.CallID.String()),
			)
			// Notify the caller via WS that callee is busy
			h.notifService.SendCallEndedWS(callerID, callID, roomName, "busy")
			respondJSON(c, http.StatusConflict, gin.H{
				"error":   "call_busy",
				"message": apiMessage(c, "User is already in a call"),
				"reason":  "busy",
			})
			return
		}
	}

	newCall := &domain.CallHistory{
		CallID:     callID,
		FromUserID: callerID,
		ToUserID:   req.CalleeID,
		RoomID:     roomName,
		CallType:   domain.CallTypePrivate,
		ChatID:     &req.ChatID,
		FromName:   &callerName,
		IsVideo:    req.IsVideo == nil || *req.IsVideo,
		Status:     domain.CallStatusRinging,
	}

	callInfo, created, err := h.callRepo.CreateDirectCallOrGetExisting(c.Request.Context(), newCall)
	if err != nil {
		h.logger.Error("failed to create direct call", zap.Error(err))
		h.respondError(c, apperrors.InternalWithErr("failed to save call history", err))
		return
	}

	if !created {
		if domain.IsCallAcceptedStatus(callInfo.Status) {
			parts, errList := h.service.ListParticipants(c.Request.Context(), callInfo.RoomID)
			liveKitUserIDs := make(map[int64]struct{})
			isCalleeInRoom := false
			if errList == nil {
				for _, p := range parts {
					if parsed, parseErr := service.ParseParticipantIdentity(p.Identity); parseErr == nil {
						liveKitUserIDs[parsed.UserID] = struct{}{}
					}
					if service.IdentityBelongsToUser(p.Identity, req.CalleeID) {
						isCalleeInRoom = true
					}
				}
			}
			isZombie := errList != nil || len(parts) == 0 || len(liveKitUserIDs) < 2 || !isCalleeInRoom
			h.logger.Warn("Existing direct call conflict LiveKit check",
				zap.String("call_id", callInfo.CallID.String()),
				zap.String("room_id", callInfo.RoomID),
				zap.String("status", callInfo.Status),
				zap.Int("parts_len", len(parts)),
				zap.Int("livekit_user_count", len(liveKitUserIDs)),
				zap.Bool("callee_in_room", isCalleeInRoom),
				zap.Bool("is_zombie", isZombie),
				zap.Error(errList),
			)
			if isZombie {
				h.logger.Warn("Existing direct call is zombie, closing and retrying create",
					zap.String("call_id", callInfo.CallID.String()),
					zap.String("room_id", callInfo.RoomID),
				)
				_ = h.callRepo.UpdateCallStatus(c.Request.Context(), callInfo.CallID, domain.CallStatusEnded)
				if participants, participantsErr := h.callRepo.GetParticipants(c.Request.Context(), callInfo.CallID); participantsErr == nil {
					for _, participant := range participants {
						_ = h.callRepo.UpdateParticipantStatus(c.Request.Context(), callInfo.CallID, participant.UserID, "left")
					}
				}
				go h.cleanupPrivateCallRoom(context.Background(), callInfo.CallID.String(), callInfo.RoomID, "direct_conflict_zombie_call")

				callInfo, created, err = h.callRepo.CreateDirectCallOrGetExisting(c.Request.Context(), newCall)
				if err != nil {
					h.logger.Error("failed to create direct call after zombie cleanup", zap.Error(err))
					h.respondError(c, apperrors.InternalWithErr("failed to save call history", err))
					return
				}
			}
		}
	}

	if !created {
		h.logger.Info("Direct call start rejected because another call already exists",
			zap.String("event", "active_call_found"),
			zap.Int64("caller_id", callerID),
			zap.Int64("callee_id", req.CalleeID),
			zap.String("existing_call_id", callInfo.CallID.String()),
			zap.String("existing_status", callInfo.Status),
		)
		h.respondDirectCallConflict(c, callerID, req.CalleeID, callInfo)
		return
	}
	h.logger.Info("call_created",
		zap.String("call_id", callID.String()),
		zap.Int64("caller_id", callerID),
		zap.Int64("callee_id", req.CalleeID),
		zap.String("room_id", roomName),
		zap.String("status", domain.CallStatusRinging),
	)

	token, err := h.service.GenerateToken(roomName, callerID, callerName, tokenCtx)
	if err != nil {
		h.respondError(c, apperrors.InternalWithErr("failed to generate token", err))
		return
	}
	h.logLiveKitRouting("call_start", callID.String(), roomName)
	callIsVideo := req.IsVideo == nil || *req.IsVideo
	h.recordCallEvent(c, "call_start", callerID, callID.String(), roomName, service.BuildParticipantIdentityForToken(callerID, tokenCtx), "ok", map[string]any{
		"callee_id": req.CalleeID,
	})

	// Отправляем пуш уведомление в фоне
	go func(bgCtx context.Context, calleeID, fromID int64, fromName string, cID uuid.UUID, rName, chID string) {
		h.notifService.SendIncomingCallWS(
			calleeID,
			fromID,
			fromName,
			cID,
			rName,
			chID,
			callIsVideo,
		)
		err := h.notifService.SendIncomingCallPushWithType(
			bgCtx,
			calleeID,
			fromID,
			fromName,
			cID,
			rName,
			chID,
			req.Priority,
			callIsVideo,
			domain.CallTypePrivate,
		)
		if err != nil {
			h.logger.Error("failed to send incoming call push", zap.Error(err))
		}
	}(context.Background(), req.CalleeID, callerID, callerName, callID, roomName, req.ChatID)

	h.enqueuePrivateCallTimeout(callID, roomName, req.CalleeID, callIsVideo)

	respondJSON(c, http.StatusOK, StartCallResponse{
		Token:    token,
		URL:      h.service.ResolveClientURL(callerID, c.GetString("username"), c.GetString("email")),
		RoomName: roomName,
		CallID:   callID.String(),
	})
}

// StartAssistantCall godoc
// @Summary Start a video call for assistant flows
// @Tags calls
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body StartCallRequest true "Start assistant call"
// @Success 200 {object} StartCallResponse
// @Failure 400 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /calls/assistant/start [post]
func (h *LiveKitHandler) StartAssistantCall(c *gin.Context) {
	h.StartCall(c)
}

// StartGroupCall godoc
// @Summary Start a group call
// @Description Initiates a group call and sends push notifications to all group members
// @Tags calls
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body StartGroupCallRequest true "Start Group Call"
// @Success 200 {object} StartCallResponse
// @Failure 400 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /calls/group/start [post]
func (h *LiveKitHandler) StartGroupCall(c *gin.Context) {
	callerID := middleware.MustGetUserID(c)
	callerName := getUserDisplayName(c, callerID)
	tokenCtx := h.tokenContextFromRequest(c)

	var req StartGroupCallRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	groupID, err := uuid.Parse(req.GroupID)
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid group id"))
		return
	}

	// 1. Generate Room Name
	// We use groupID and timestamp to make it unique per session
	roomName := fmt.Sprintf("call_group_%s_%d", groupID.String(), time.Now().Unix())

	// 1.1 Create Call History record
	callID := uuid.New()
	err = h.callRepo.Create(c.Request.Context(), &domain.CallHistory{
		CallID:     callID,
		FromUserID: callerID,
		ToUserID:   0, // 0 implies group call
		RoomID:     roomName,
		CallType:   domain.CallTypeGroup,
		ChatID:     &req.GroupID,
		FromName:   &callerName,
		Status:     "initiated",
	})
	if err != nil {
		h.logger.Error("failed to create group call history", zap.Error(err))
	}

	// 1.2 Add caller as participant
	_ = h.callRepo.AddParticipant(c.Request.Context(), &domain.CallParticipant{
		CallID: callID,
		UserID: callerID,
		Status: "joined",
		Role:   "host",
	})

	// 2. Generate Token for caller
	token, err := h.service.GenerateToken(roomName, callerID, callerName, tokenCtx)
	if err != nil {
		h.respondError(c, apperrors.InternalWithErr("failed to generate token", err))
		return
	}
	h.logLiveKitRouting("group_call_start", callID.String(), roomName)
	h.recordCallEvent(c, "group_call_start", callerID, callID.String(), roomName, service.BuildParticipantIdentityForToken(callerID, tokenCtx), "ok", map[string]any{
		"group_id": req.GroupID,
	})

	// 3. Get Group Members to notify
	// We need to fetch group details to get members
	groupDetails, err := h.groupService.GetGroup(c.Request.Context(), groupID, callerID)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	participantIDs := make([]int64, 0, len(groupDetails.Members))
	for _, m := range groupDetails.Members {
		participantIDs = append(participantIDs, m.ID)
		if m.ID == callerID {
			continue
		}
		if err := h.callRepo.AddParticipant(c.Request.Context(), &domain.CallParticipant{
			CallID: callID,
			UserID: m.ID,
			Status: "invited",
			Role:   "participant",
		}); err != nil {
			h.logger.Warn("failed to add invited group participant",
				zap.String("call_id", callID.String()),
				zap.Int64("user_id", m.ID),
				zap.Error(err),
			)
		}
	}

	// 4. Send Push Asynchronously
	// 4. Send Push Asynchronously
	go func(bgCtx context.Context, fromID int64, fromName, gID, gName, rName string, parts []int64, cID uuid.UUID) {
		err := h.notifService.SendGroupCallPush(
			bgCtx,
			fromID,
			fromName,
			gID,
			gName,
			cID,
			rName,
			parts,
		)
		if err != nil {
			h.logger.Error("failed to send group call push", zap.Error(err))
		}
	}(context.Background(), callerID, callerName, req.GroupID, groupDetails.Name, roomName, participantIDs, callID)

	respondJSON(c, http.StatusOK, StartCallResponse{
		Token:    token,
		URL:      h.service.ResolveClientURL(callerID, c.GetString("username"), c.GetString("email")),
		RoomName: roomName,
		CallID:   callID.String(),
	})
}

// AcceptCall godoc
// @Summary Accept a call
// @Description Generates a token for the callee to join the room
// @Tags calls
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body AcceptCallRequest true "Accept Call"
// @Success 200 {object} AcceptCallResponse
// @Failure 400 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /calls/accept [post]
func (h *LiveKitHandler) AcceptCall(c *gin.Context) {
	authenticatedUserID := middleware.MustGetUserID(c)
	calleeID, _, resolveErr := h.resolveEffectiveCallUser(c.Request.Context(), authenticatedUserID)
	if resolveErr != nil {
		h.logger.Warn("failed to resolve canonical call user",
			zap.Int64("current_user_id", authenticatedUserID),
			zap.Error(resolveErr),
		)
		calleeID = authenticatedUserID
	}
	calleeName := getUserDisplayName(c, calleeID)
	tokenCtx := h.tokenContextFromRequest(c)
	excludeSessionKey := buildRealtimeSessionKey(c)
	excludeDeviceID := strings.TrimSpace(tokenCtx.DeviceID)

	var req AcceptCallRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}
	if strings.TrimSpace(req.CallID) == "" && strings.TrimSpace(req.RoomName) == "" {
		h.respondError(c, apperrors.Validation("call_id or room_name is required"))
		return
	}

	h.logger.Info("accept_call_started",
		zap.String("call_id", strings.TrimSpace(req.CallID)),
		zap.Int64("callee_id", calleeID),
		zap.String("room_id", strings.TrimSpace(req.RoomName)),
		zap.String("device_id", excludeDeviceID),
	)

	var acceptedCallInfo *domain.CallHistory
	if req.CallID != "" {
		if uid, err := uuid.Parse(strings.TrimSpace(req.CallID)); err == nil {
			acceptedCallInfo, _ = h.callRepo.GetByCallID(c.Request.Context(), uid)
		}
	}
	if acceptedCallInfo == nil && strings.TrimSpace(req.RoomName) != "" {
		acceptedCallInfo, _ = h.callRepo.GetByRoomID(c.Request.Context(), strings.TrimSpace(req.RoomName))
	}
	if acceptedCallInfo == nil {
		h.respondError(c, apperrors.NotFound("call not found"))
		return
	}

	deviceID := strings.TrimSpace(req.DeviceID)
	if deviceID == "" {
		deviceID = strings.TrimSpace(tokenCtx.DeviceID)
	}
	sessionID := strings.TrimSpace(req.SessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(tokenCtx.SessionID)
	}

	isGroupCall := isGroupCallHistory(acceptedCallInfo)

	acceptResult, acceptErr := h.callRepo.AcceptCall(c.Request.Context(), repository.AcceptCallParams{
		CallID:    acceptedCallInfo.CallID,
		UserID:    calleeID,
		DeviceID:  deviceID,
		SessionID: sessionID,
	})
	if acceptErr != nil {
		h.logger.Error("failed to accept call in repository",
			zap.Error(acceptErr),
			zap.String("call_id", acceptedCallInfo.CallID.String()),
			zap.String("room", acceptedCallInfo.RoomID),
			zap.String("device_id", deviceID),
			zap.String("session_id", sessionID),
		)
		h.respondError(c, apperrors.InternalWithErr("failed to accept call", acceptErr))
		return
	}
	if acceptResult == nil || acceptResult.Call == nil {
		h.respondError(c, apperrors.NotFound("call not found"))
		return
	}

	acceptedCallInfo = acceptResult.Call

	if acceptResult.Forbidden {
		h.respondError(c, apperrors.Forbidden("access denied to call"))
		return
	}

	if !isGroupCall && acceptResult.AcceptedOnOtherDevice {
		h.logger.Info("accept_call_rejected_other_device",
			zap.String("call_id", acceptedCallInfo.CallID.String()),
			zap.String("room_name", acceptedCallInfo.RoomID),
			zap.Int64("user_id", calleeID),
			zap.String("request_device_id", deviceID),
			zap.String("accepted_device_id", acceptedByDeviceID(acceptedCallInfo)),
			zap.String("accepted_session_id", acceptedBySessionID(acceptedCallInfo)),
		)
		h.respondError(c, apperrors.New("CALL_ALREADY_ACCEPTED", "call already accepted on another device", http.StatusConflict))
		return
	}

	currentStatus := domain.NormalizeCallStatus(acceptedCallInfo.Status)
	previousStatus := domain.NormalizeCallStatus(acceptResult.PreviousStatus)

	if domain.IsCallFinalStatus(currentStatus) || domain.IsCallFinalStatus(previousStatus) {
		h.logger.Info("accept_call_rejected_terminal",
			zap.String("call_id", acceptedCallInfo.CallID.String()),
			zap.String("room_name", acceptedCallInfo.RoomID),
			zap.Int64("user_id", calleeID),
			zap.String("previous_status", previousStatus),
			zap.String("current_status", currentStatus),
		)
		h.respondError(c, apperrors.New("CALL_ENDED", "call already ended", http.StatusConflict))
		return
	}

	allowToken := false
	switch {
	case acceptResult.Updated:
		allowToken = true
	case acceptResult.Idempotent && acceptResult.AcceptedBySameEndpoint:
		allowToken = true
	case currentStatus == domain.CallStatusAccepted:
		allowToken = true
	case currentStatus == domain.CallStatusConnecting:
		allowToken = true
	case currentStatus == domain.CallStatusOngoing:
		allowToken = true
	}

	if !allowToken {
		h.logger.Warn("accept_call_rejected_invalid_state",
			zap.String("call_id", acceptedCallInfo.CallID.String()),
			zap.String("room_name", acceptedCallInfo.RoomID),
			zap.Int64("user_id", calleeID),
			zap.String("previous_status", previousStatus),
			zap.String("current_status", currentStatus),
			zap.Bool("updated", acceptResult.Updated),
			zap.Bool("idempotent", acceptResult.Idempotent),
			zap.Bool("accepted_by_same_endpoint", acceptResult.AcceptedBySameEndpoint),
		)
		h.respondError(c, apperrors.New("CALL_NOT_AVAILABLE", "call is not available for accept", http.StatusConflict))
		return
	}

	if acceptResult.Updated {
		h.logger.Info("call_accepted_device_selected",
			zap.String("call_id", acceptedCallInfo.CallID.String()),
			zap.String("room_name", acceptedCallInfo.RoomID),
			zap.Int64("user_id", calleeID),
			zap.String("accepted_device_id", acceptedByDeviceID(acceptedCallInfo)),
			zap.String("accepted_session_id", acceptedBySessionID(acceptedCallInfo)),
			zap.String("previous_status", previousStatus),
			zap.String("new_status", currentStatus),
		)
	}

	roomName := strings.TrimSpace(req.RoomName)
	if roomName == "" {
		roomName = strings.TrimSpace(acceptedCallInfo.RoomID)
	}
	if roomName == "" {
		h.respondError(c, apperrors.Validation("room_name is required"))
		return
	}

	token, err := h.service.GenerateToken(roomName, calleeID, calleeName, tokenCtx)
	if err != nil {
		h.respondError(c, apperrors.InternalWithErr("failed to generate token", err))
		return
	}

	finalCallID := acceptedCallInfo.CallID.String()
	h.recordCallEvent(
		c,
		"call_accept",
		calleeID,
		finalCallID,
		roomName,
		service.BuildParticipantIdentityForToken(calleeID, tokenCtx),
		"ok",
		map[string]any{
			"idempotent":                acceptResult.Idempotent,
			"accepted_by_same_endpoint": acceptResult.AcceptedBySameEndpoint,
		},
	)

	if err := h.callRepo.AddParticipant(c.Request.Context(), &domain.CallParticipant{
		CallID: acceptedCallInfo.CallID,
		UserID: calleeID,
		Status: "joined",
		Role:   "participant",
	}); err != nil {
		h.logger.Error("failed to add callee participant", zap.Error(err))
	}

	if !isGroupCall &&
		acceptedCallInfo.FromUserID != 0 &&
		acceptedCallInfo.FromUserID != calleeID &&
		acceptResult.Updated {
		h.notifService.SendCallAcceptedWS(
			acceptedCallInfo.FromUserID,
			acceptedCallInfo.CallID,
			acceptedCallInfo.RoomID,
			calleeID,
			calleeName,
		)
	}

	if acceptResult.Updated {
		go func(callInfo *domain.CallHistory, userID int64, deviceID, sessionKey string) {
			bgCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			if err := h.notifService.SendCallSyncEvent(
				bgCtx,
				userID,
				callInfo.CallID,
				callInfo.RoomID,
				"call_answered_elsewhere",
				"answered_on_another_device",
				callInfo.IsVideo,
				deviceID,
				sessionKey,
				deviceID,
			); err != nil {
				h.logger.Warn("failed to fan out call_answered_elsewhere to receiver devices",
					zap.Error(err),
					zap.String("call_id", callInfo.CallID.String()),
					zap.Int64("receiver_id", userID),
				)
			}
		}(acceptedCallInfo, calleeID, excludeDeviceID, excludeSessionKey)
	}

	h.logger.Info("accept_call_success",
		zap.String("call_id", finalCallID),
		zap.Int64("caller_id", acceptedCallInfo.FromUserID),
		zap.Int64("callee_id", calleeID),
		zap.String("room_id", roomName),
		zap.String("status", domain.NormalizeCallStatus(acceptedCallInfo.Status)),
		zap.String("device_id", deviceID),
		zap.Bool("idempotent", acceptResult.Idempotent),
	)
	url, compatToken := h.buildUserScopedTokenCompatFields(calleeID, c.GetString("username"), c.GetString("email"), token)
	responseStatus := domain.NormalizeCallStatus(acceptedCallInfo.Status)
	if responseStatus == "" {
		responseStatus = domain.CallStatusAccepted
	}
	// When a token was successfully issued, never return a terminal status to Flutter.
	// Flutter sees "ended"/"cancelled"/"declined" and throws "Доступ к звонку запрещён".
	// Since we already generated a valid token, tell Flutter the call is "connecting"
	// so it proceeds to join the LiveKit room.
	if domain.IsCallFinalStatus(responseStatus) {
		responseStatus = domain.CallStatusConnecting
	}

	warning := liveKitBypassWarning(acceptedCallInfo, h.bypassAccessEnabled())
	respondJSON(c, http.StatusOK, AcceptCallResponse{
		Success:      true,
		CallID:       finalCallID,
		Status:       responseStatus,
		RoomID:       roomName,
		RoomName:     roomName,
		LivekitURL:   url,
		LivekitToken: compatToken,
		Caller: acceptCallUserFromCall(
			acceptedCallInfo,
			acceptedCallInfo.FromUserID,
			derefString(acceptedCallInfo.FromName),
		),
		Callee: AcceptCallUser{
			ID:     calleeID,
			Name:   calleeName,
			Avatar: "",
		},
		IsVideo:                  acceptedCallInfo.IsVideo,
		Token:                    compatToken,
		URL:                      url,
		Message:                  apiMessage(c, "Call accepted"),
		AlreadyAcceptedElsewhere: false,
		AnsweredElsewhere:        false,
		AcceptedByUserID:         acceptedByUserID(acceptedCallInfo),
		AcceptedDeviceID:         acceptedByDeviceID(acceptedCallInfo),
		AcceptedSessionID:        acceptedBySessionID(acceptedCallInfo),
		AccessBypassed:           h.bypassAccessEnabled(),
		AccessValidationDisabled: true,
		Warning:                  warning,
	})
}

// DeclineCall godoc
// @Summary Decline or Cancel a call
// @Description Rejects an incoming call or cancels an outgoing one, sending a push notification to the other party
// @Tags calls
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body DeclineCallRequest true "Decline details"
// @Success 200 {object} map[string]string
// @Failure 400 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /calls/decline [post]
func (h *LiveKitHandler) DeclineCall(c *gin.Context) {
	startTime := time.Now()
	userID := middleware.MustGetUserID(c)
	tokenCtx := h.tokenContextFromRequest(c)
	excludeSessionKey := buildRealtimeSessionKey(c)
	excludeDeviceID := strings.TrimSpace(tokenCtx.DeviceID)
	requestDeviceID := strings.TrimSpace(tokenCtx.DeviceID)
	requestSessionID := strings.TrimSpace(tokenCtx.SessionID)

	var req DeclineCallRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}
	if strings.TrimSpace(req.DeviceID) != "" {
		requestDeviceID = strings.TrimSpace(req.DeviceID)
	}
	if strings.TrimSpace(req.SessionID) != "" {
		requestSessionID = strings.TrimSpace(req.SessionID)
	}
	if strings.TrimSpace(req.CallID) == "" && strings.TrimSpace(req.RoomName) == "" {
		h.respondError(c, apperrors.Validation("call_id or room_name is required"))
		return
	}

	callID := strings.TrimSpace(req.CallID)
	h.logger.Info("DeclineCall request received",
		zap.String("call_id", callID),
		zap.Int64("user_id", userID),
		zap.String("room_name_req", req.RoomName),
	)

	callInfo, resolveErr := h.resolveDirectCallForAccess(c.Request.Context(), callID, req.RoomName)
	if resolveErr != nil {
		if appErr, ok := resolveErr.(*apperrors.AppError); ok {
			if appErr.StatusCode != http.StatusNotFound {
				h.respondError(c, appErr)
				return
			}
		} else {
			h.respondError(c, apperrors.ToAppError(resolveErr))
			return
		}
	}

	roomName := strings.TrimSpace(req.RoomName)
	accessBypassed := false
	if callInfo != nil {
		var allowed bool
		allowed, accessBypassed = h.ensureDirectCallAccessWithBypass(c, "calls.decline", callInfo, userID)
		if !allowed {
			return
		}
		if roomName == "" {
			roomName = strings.TrimSpace(callInfo.RoomID)
		}
		callID = callInfo.CallID.String()
	} else if roomName != "" {
		h.logCallAccessValidationDisabled("calls.decline", nil, userID, "decline")
		h.logCallForbiddenIgnored("calls.decline", nil, userID, "call_not_found")
		h.forceClosePrivateCallRoom(c.Request.Context(), callID, roomName, "decline_missing_call")
		respondJSON(c, http.StatusOK, callActionSuccess(callID, roomName, domain.CallStatusEnded, "Call closed", true, false))
		return
	} else {
		h.respondError(c, apperrors.NotFound("call not found"))
		return
	}

	currentStatus := domain.NormalizeCallStatus(callInfo.Status)
	isGroupCall := isGroupCallHistory(callInfo) || strings.HasPrefix(strings.TrimSpace(roomName), "call_group_")
	if domain.IsCallFinalStatus(currentStatus) {
		h.logCallLifecycleIdempotent(callInfo, userID, roomName)
		respondJSON(c, http.StatusOK, decorateCallActionWithAcceptedEndpoint(callActionSuccess(callID, roomName, currentStatus, "Call already finalized", accessBypassed, true), callInfo))
		return
	}

	if !isGroupCall && isStaleAcceptedDevice(callInfo, userID, requestDeviceID, requestSessionID) {
		h.logger.Info("stale_decline_ignored",
			zap.String("call_id", callInfo.CallID.String()),
			zap.String("room_name", roomName),
			zap.Int64("current_user_id", userID),
			zap.String("request_device_id", requestDeviceID),
			zap.String("accepted_device_id", acceptedByDeviceID(callInfo)),
			zap.String("status", currentStatus),
			zap.Bool("local_dismiss_only", true),
		)
		acceptedSessionKey := acceptedSessionRealtimeKey(callInfo)
		_ = h.notifService.SendCallSyncEvent(
			c.Request.Context(),
			userID,
			callInfo.CallID,
			roomName,
			"dismiss_incoming_call",
			"answered_elsewhere",
			callInfo.IsVideo,
			acceptedByDeviceID(callInfo),
			acceptedSessionKey,
		)
		respondJSON(c, http.StatusOK, localDismissOnlyResponse(callID, roomName, currentStatus, "Call already accepted on another device", "answered_elsewhere", accessBypassed, callInfo))
		return
	}

	targetUserID := int64(0)
	if callInfo.FromUserID == userID {
		targetUserID = callInfo.ToUserID
	} else if callInfo.ToUserID == userID {
		targetUserID = callInfo.FromUserID
	}

	if isGroupCall && callInfo.FromUserID != userID {
		_ = h.callRepo.UpdateParticipantStatus(c.Request.Context(), callInfo.CallID, userID, "left")
		go func(callUUID uuid.UUID, rName string, receiverID int64, video bool) {
			bgCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = h.notifService.SendCallSyncEvent(bgCtx, receiverID, callUUID, rName, "call_declined", "declined_on_another_device", video, excludeDeviceID, excludeSessionKey)
		}(callInfo.CallID, roomName, userID, callInfo.IsVideo)
		respondJSON(c, http.StatusOK, callActionSuccess(callID, roomName, currentStatus, "Call closed", accessBypassed, false))
		return
	}

	declineReason := strings.TrimSpace(req.Reason)
	if !isGroupCall && declineReason == "busy" && domain.IsCallRingingStatus(currentStatus) && callInfo.ToUserID == userID {
		confirmedBusy, activeCallID, confirmErr := h.hasConfirmedActiveOtherPrivateCall(c.Request.Context(), userID, callInfo.CallID)
		if confirmErr != nil {
			h.logger.Warn("busy_decline_active_call_check_failed",
				zap.String("call_id", callInfo.CallID.String()),
				zap.String("room_name", roomName),
				zap.Int64("current_user_id", userID),
				zap.Error(confirmErr),
			)
		}
		if !confirmedBusy {
			h.logger.Warn("stale_busy_decline_ignored",
				zap.String("call_id", callInfo.CallID.String()),
				zap.String("room_name", roomName),
				zap.Int64("current_user_id", userID),
				zap.String("request_device_id", requestDeviceID),
				zap.String("request_session_id", requestSessionID),
				zap.String("active_call_id", activeCallID),
				zap.String("status", currentStatus),
				zap.Bool("local_dismiss_only", true),
			)
			respondJSON(c, http.StatusOK, localDismissOnlyResponse(callID, roomName, currentStatus, "Busy ignored for fresh ringing call", "stale_busy_ignored", accessBypassed, callInfo))
			return
		}
	}

	decision := h.decideDeclineAction(callInfo, userID, declineReason)
	nextStatus := decision.nextStatus
	message := decision.message
	eventType := decision.eventType
	notificationReason := decision.notificationReason
	if decision.effectiveAction == "end" && h.bypassLifecycleEnabled() {
		h.logCallLifecycleBypassed(callInfo, userID, currentStatus, decision.effectiveAction, roomName)
	}

	oldStatus, updated, transitionErr := h.callRepo.TransitionCallStatus(
		c.Request.Context(),
		callInfo.CallID,
		nextStatus,
		domain.CallStatusCreated,
		domain.CallStatusInitiated,
		domain.CallStatusRinging,
		domain.CallStatusConnecting,
		domain.CallStatusAccepted,
		domain.CallStatusOngoing,
	)
	if transitionErr != nil {
		h.respondError(c, apperrors.InternalWithErr("failed to decline call", transitionErr))
		return
	}
	if !updated {
		if domain.IsCallFinalStatus(oldStatus) {
			h.logCallLifecycleIdempotent(callInfo, userID, roomName)
			respondJSON(c, http.StatusOK, decorateCallActionWithAcceptedEndpoint(callActionSuccess(callID, roomName, oldStatus, "Call already finalized", accessBypassed, true), callInfo))
			return
		}
		// Permissive mode: always force-end the call for any non-final status.
		// No 409 / CALL_ALREADY_ACCEPTED is returned to authorized users.
		h.logCallForbiddenIgnored("calls.decline", callInfo, userID, fmt.Sprintf("call_not_declinable_status_%s_force_end", oldStatus))
		forceStatus := nextStatus
		if forceStatus == "" {
			forceStatus = domain.CallStatusEnded
		}
		if err := h.callRepo.UpdateCallStatus(c.Request.Context(), callInfo.CallID, forceStatus); err != nil {
			h.respondError(c, apperrors.InternalWithErr("failed to finalize call", err))
			return
		}
		nextStatus = forceStatus
		message = "Call closed"
		eventType = "call_ended"
		notificationReason = "ended"
	}
	callInfo.Status = nextStatus

	if targetUserID != 0 {
		if nextStatus == domain.CallStatusEnded {
			h.notifService.SendCallEndedWS(targetUserID, callInfo.CallID, roomName, notificationReason)
		} else if err := h.notifService.SendIncomingCallCancelPushEx(c.Request.Context(), targetUserID, callInfo.CallID, roomName, notificationReason, callInfo.IsVideo, "", ""); err != nil {
			h.logger.Warn("failed to send cancel/decline push to target", zap.Error(err))
		}
	}
	if err := h.notifService.SendCallSyncEvent(
		c.Request.Context(),
		userID,
		callInfo.CallID,
		roomName,
		eventType,
		notificationReason,
		callInfo.IsVideo,
		excludeDeviceID,
		excludeSessionKey,
	); err != nil {
		h.logger.Warn("failed to send call sync event to current user", zap.Error(err))
	}
	if err := h.notifService.SendCallSyncEvent(
		c.Request.Context(),
		userID,
		callInfo.CallID,
		roomName,
		"call_state_changed",
		nextStatus,
		callInfo.IsVideo,
		excludeDeviceID,
		excludeSessionKey,
	); err != nil {
		h.logger.Warn("failed to send call state changed event", zap.Error(err))
	}

	if isGroupCall {
		if targetUserID != 0 {
			if kickedCount, err := h.service.KickUser(c.Request.Context(), roomName, targetUserID); err != nil {
				h.logger.Warn("failed to kick participant on group decline", zap.String("room", roomName), zap.Int64("target_user_id", targetUserID), zap.Error(err))
			} else {
				h.logger.Info("kicked participant on group decline", zap.String("room", roomName), zap.Int64("target_user_id", targetUserID), zap.Int("kicked_count", kickedCount))
			}
		}
	} else if domain.IsCallFinalStatus(nextStatus) {
		h.markDirectCallParticipantsLeft(c.Request.Context(), callInfo.CallID)
		h.forceClosePrivateCallRoom(c.Request.Context(), callID, roomName, "decline_effective_end")
	} else {
		h.cleanupPrivateCallRoom(c.Request.Context(), callID, roomName, "decline_before_accept")
	}

	h.logger.Info("DeclineCall completed", zap.Duration("duration", time.Since(startTime)))
	respondJSON(c, http.StatusOK, decorateCallActionWithAcceptedEndpoint(callActionSuccess(callID, roomName, nextStatus, message, accessBypassed, false), callInfo))
}

// EndCall godoc
// @Summary End an active private call
// @Description Ends an accepted/ongoing private call for all participants.
// @Tags calls
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body EndCallRequest true "End call details"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Failure 409 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /calls/end [post]
func (h *LiveKitHandler) EndCall(c *gin.Context) {
	startTime := time.Now()
	userID := middleware.MustGetUserID(c)
	tokenCtx := h.tokenContextFromRequest(c)
	excludeSessionKey := buildRealtimeSessionKey(c)
	excludeDeviceID := strings.TrimSpace(tokenCtx.DeviceID)

	var req EndCallRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}
	if strings.TrimSpace(req.CallID) == "" && strings.TrimSpace(req.RoomName) == "" {
		h.respondError(c, apperrors.Validation("call_id or room_name is required"))
		return
	}

	callInfo, resolveErr := h.resolveDirectCallForAccess(c.Request.Context(), req.CallID, req.RoomName)
	if resolveErr != nil {
		if appErr, ok := resolveErr.(*apperrors.AppError); ok {
			if appErr.StatusCode != http.StatusNotFound {
				h.respondError(c, appErr)
				return
			}
		} else {
			h.respondError(c, apperrors.ToAppError(resolveErr))
			return
		}
	}

	roomName := strings.TrimSpace(req.RoomName)
	callID := strings.TrimSpace(req.CallID)
	accessBypassed := false
	if callInfo != nil {
		var allowed bool
		allowed, accessBypassed = h.ensureDirectCallAccessWithBypass(c, "calls.end", callInfo, userID)
		if !allowed {
			return
		}
		roomName = strings.TrimSpace(callInfo.RoomID)
		callID = callInfo.CallID.String()
	} else if roomName != "" {
		h.logCallAccessValidationDisabled("calls.end", nil, userID, "end")
		h.logCallForbiddenIgnored("calls.end", nil, userID, "call_not_found")
		h.forceClosePrivateCallRoom(c.Request.Context(), callID, roomName, "end_missing_call")
		respondJSON(c, http.StatusOK, callActionSuccess(callID, roomName, domain.CallStatusEnded, "Call ended", true, false))
		return
	} else {
		h.respondError(c, apperrors.NotFound("call not found"))
		return
	}

	h.logger.Info("EndCall request received",
		zap.String("call_id", callID),
		zap.Int64("user_id", userID),
		zap.String("requested_action", "end"),
		zap.String("current_status", callInfo.Status),
		zap.String("device_id", excludeDeviceID),
		zap.String("session_id", excludeSessionKey),
	)
	requestDeviceID := strings.TrimSpace(tokenCtx.DeviceID)
	if strings.TrimSpace(req.DeviceID) != "" {
		requestDeviceID = strings.TrimSpace(req.DeviceID)
	}
	requestSessionID := strings.TrimSpace(tokenCtx.SessionID)
	if strings.TrimSpace(req.SessionID) != "" {
		requestSessionID = strings.TrimSpace(req.SessionID)
	}

	if domain.IsCallFinalStatus(callInfo.Status) {
		h.logCallLifecycleIdempotent(callInfo, userID, roomName)
		if domain.NormalizeCallStatus(callInfo.Status) == domain.CallStatusBusy {
			h.markDirectCallParticipantsLeft(c.Request.Context(), callInfo.CallID)
			h.forceClosePrivateCallRoom(c.Request.Context(), callID, roomName, "end_busy_finalized")
		}
		h.respondEndCallAlreadyFinalized(c, callID, userID, callInfo.Status, "end", excludeDeviceID, excludeSessionKey)
		return
	}

	if !req.ForceEnd && callInfo.FromUserID != userID && isStaleAcceptedDevice(callInfo, userID, requestDeviceID, requestSessionID) {
		h.logger.Info("call_end_ignored_stale_device",
			zap.String("call_id", callInfo.CallID.String()),
			zap.String("request_device_id", requestDeviceID),
			zap.String("accepted_device_id", acceptedByDeviceID(callInfo)),
			zap.Bool("local_dismiss_only", true),
		)
		acceptedSessionKey := acceptedSessionRealtimeKey(callInfo)
		_ = h.notifService.SendCallSyncEvent(
			c.Request.Context(),
			userID,
			callInfo.CallID,
			roomName,
			"dismiss_incoming_call",
			"stale_device",
			callInfo.IsVideo,
			acceptedByDeviceID(callInfo),
			acceptedSessionKey,
		)
		respondJSON(c, http.StatusOK, localDismissOnlyResponse(callID, roomName, callInfo.Status, "Call already active on another device", "stale_device", accessBypassed, callInfo))
		return
	}

	if isGroupCallHistory(callInfo) && callInfo.FromUserID != userID && !req.ForceEnd {
		if err := h.callRepo.UpdateParticipantStatus(c.Request.Context(), callInfo.CallID, userID, "left"); err != nil {
			h.respondError(c, apperrors.InternalWithErr("failed to leave group call", err))
			return
		}
		if _, _, lifecycleErr := h.syncGroupCallLifecycle(c.Request.Context(), callInfo, "participant_left_via_end"); lifecycleErr != nil {
			h.logger.Warn("failed to sync group call lifecycle after leave",
				zap.String("call_id", callInfo.CallID.String()),
				zap.String("room_name", roomName),
				zap.Int64("user_id", userID),
				zap.Error(lifecycleErr),
			)
		}
		respondJSON(c, http.StatusOK, callActionSuccess(callID, roomName, callInfo.Status, "Left call", accessBypassed, false))
		return
	}

	oldStatus, updated, transitionErr := h.callRepo.TransitionCallStatus(
		c.Request.Context(),
		callInfo.CallID,
		domain.CallStatusEnded,
		domain.CallStatusCreated,
		domain.CallStatusInitiated,
		domain.CallStatusRinging,
		domain.CallStatusConnecting,
		domain.CallStatusAccepted,
		domain.CallStatusOngoing,
	)
	if transitionErr != nil {
		h.respondError(c, apperrors.InternalWithErr("failed to end call", transitionErr))
		return
	}
	if !updated {
		if domain.IsCallFinalStatus(oldStatus) {
			h.logCallLifecycleIdempotent(callInfo, userID, roomName)
			h.respondEndCallAlreadyFinalized(c, callID, userID, oldStatus, "end", excludeDeviceID, excludeSessionKey)
			return
		}
		h.logCallAccessValidationDisabled("calls.end", callInfo, userID, "end")
		h.logCallForbiddenIgnored("calls.end", callInfo, userID, fmt.Sprintf("call_not_endable_status_%s", oldStatus))
		if err := h.callRepo.UpdateCallStatus(c.Request.Context(), callInfo.CallID, domain.CallStatusEnded); err != nil {
			h.respondError(c, apperrors.InternalWithErr("failed to end call", err))
			return
		}
	}

	if isGroupCallHistory(callInfo) {
		go h.broadcastGroupCallEnded(callInfo, "ended")
		h.forceClosePrivateCallRoom(c.Request.Context(), callID, roomName, "end_group_call")
		h.logger.Info("EndCall completed",
			zap.Duration("duration", time.Since(startTime)),
			zap.String("call_id", callID),
			zap.String("previous_status", oldStatus),
			zap.String("next_status", domain.CallStatusEnded),
			zap.String("decision", "ended_group_call"),
		)
		respondJSON(c, http.StatusOK, callActionSuccess(callID, roomName, domain.CallStatusEnded, "Call ended", accessBypassed, false))
		return
	}

	targetUserID := callInfo.FromUserID
	if targetUserID == userID {
		targetUserID = callInfo.ToUserID
	}
	h.logger.Info("call_end_allowed",
		zap.String("call_id", callInfo.CallID.String()),
		zap.String("request_device_id", requestDeviceID),
		zap.String("accepted_device_id", acceptedByDeviceID(callInfo)),
		zap.String("reason", "end"),
	)

	h.notifService.SendCallEndedWS(targetUserID, callInfo.CallID, callInfo.RoomID, "ended")
	if err := h.notifService.SendCallSyncEvent(
		c.Request.Context(),
		userID,
		callInfo.CallID,
		callInfo.RoomID,
		"call_ended",
		"ended_on_another_device",
		callInfo.IsVideo,
		excludeDeviceID,
		excludeSessionKey,
	); err != nil {
		h.logger.Warn("failed to fan out call_ended to user devices",
			zap.Error(err),
			zap.String("call_id", callInfo.CallID.String()),
			zap.Int64("user_id", userID),
		)
	}

	if err := h.notifService.SendCallSyncEvent(
		c.Request.Context(),
		userID,
		callInfo.CallID,
		callInfo.RoomID,
		"call_state_changed",
		domain.CallStatusEnded,
		callInfo.IsVideo,
		excludeDeviceID,
		excludeSessionKey,
	); err != nil {
		h.logger.Warn("failed to fan out call_state_changed to user devices",
			zap.Error(err),
			zap.String("call_id", callInfo.CallID.String()),
			zap.Int64("user_id", userID),
		)
	}

	h.forceClosePrivateCallRoom(c.Request.Context(), callID, roomName, "end_after_accept")

	h.logger.Info("EndCall completed",
		zap.Duration("duration", time.Since(startTime)),
		zap.String("call_id", callID),
		zap.String("previous_status", oldStatus),
		zap.String("next_status", domain.CallStatusEnded),
		zap.String("decision", "ended_active_call"),
	)
	respondJSON(c, http.StatusOK, callActionSuccess(callID, roomName, domain.CallStatusEnded, "Call ended", accessBypassed, false))
}

// JoinRoom godoc
// @Summary Join a LiveKit room (Direct)
// @Description Generates an access token for a video call room directly by room name
// @Tags calls
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body JoinCallRequest true "Room details"
// @Success 200 {object} JoinCallResponse
// @Failure 400 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /calls/join [post]
func (h *LiveKitHandler) JoinRoom(c *gin.Context) {
	authenticatedUserID := middleware.MustGetUserID(c)
	userID, _, resolveErr := h.resolveEffectiveCallUser(c.Request.Context(), authenticatedUserID)
	if resolveErr != nil {
		h.logger.Warn("failed to resolve canonical call user",
			zap.Int64("current_user_id", authenticatedUserID),
			zap.Error(resolveErr),
		)
		userID = authenticatedUserID
	}
	username := getUserDisplayName(c, userID)
	tokenCtx := h.tokenContextFromRequest(c)

	var req JoinCallRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid body"))
		return
	}
	if strings.TrimSpace(req.CallID) == "" && strings.TrimSpace(req.RoomName) == "" {
		h.respondError(c, apperrors.Validation("call_id or room_name is required"))
		return
	}

	callInfo, resolveErr := h.resolveDirectCallForAccess(c.Request.Context(), req.CallID, req.RoomName)
	var appErr *apperrors.AppError
	if resolveErr != nil {
		if typedErr, ok := resolveErr.(*apperrors.AppError); ok {
			appErr = typedErr
		} else {
			appErr = apperrors.ToAppError(resolveErr)
		}
	}
	if appErr != nil && appErr.StatusCode != http.StatusNotFound {
		h.respondError(c, appErr)
		return
	}
	if appErr == nil && callInfo != nil {
		if !h.ensureDirectCallAccess(c, "calls.join", callInfo, userID) {
			return
		}
		if domain.IsCallFinalStatus(callInfo.Status) {
			h.respondError(c, apperrors.New("CALL_ENDED", "call already ended", http.StatusConflict))
			return
		}
	}

	roomName := strings.TrimSpace(req.RoomName)
	if callInfo != nil {
		roomName = strings.TrimSpace(callInfo.RoomID)
	}
	if roomName == "" {
		h.respondError(c, apperrors.NotFound("call not found"))
		return
	}

	token, genErr := h.service.GenerateToken(roomName, userID, username, tokenCtx)
	if genErr != nil {
		h.respondError(c, apperrors.InternalWithErr("failed", genErr))
		return
	}
	if callInfo != nil && !isGroupCallHistory(callInfo) {
		h.markPrivateCallConnecting(c.Request.Context(), callInfo, "join_room_token_issued")
	}

	callID := ""
	participantCount := 0
	caller := AcceptCallUser{}
	callee := AcceptCallUser{}
	isVideo := true
	status := domain.CallStatusConnecting
	if callInfo != nil {
		callID = callInfo.CallID.String()
		caller = acceptCallUserFromCall(callInfo, callInfo.FromUserID, derefString(callInfo.FromName))
		callee = acceptCallUserFromCall(callInfo, callInfo.ToUserID, "")
		isVideo = callInfo.IsVideo
		status = domain.NormalizeCallStatus(callInfo.Status)
		if parts, partsErr := h.service.ListParticipants(c.Request.Context(), roomName); partsErr == nil {
			participantCount = len(parts)
		}
	}
	if domain.IsCallFinalStatus(status) {
		h.respondError(c, apperrors.New("CALL_ENDED", "call already ended", http.StatusConflict))
		return
	}

	h.recordCallEvent(c, "call_join", userID, callID, roomName, service.BuildParticipantIdentityForToken(userID, tokenCtx), "ok", nil)
	if callInfo != nil {
		_ = h.callRepo.AddParticipant(c.Request.Context(), &domain.CallParticipant{
			CallID: callInfo.CallID,
			UserID: userID,
			Status: "joined",
			Role:   "participant",
		})
	}

	url, compatToken := h.buildUserScopedTokenCompatFields(userID, c.GetString("username"), c.GetString("email"), token)
	respondJSON(c, http.StatusOK, JoinCallResponse{
		Success:                  true,
		CallID:                   callID,
		Status:                   status,
		RoomName:                 roomName,
		RoomID:                   roomName,
		LivekitURL:               url,
		LivekitToken:             compatToken,
		Token:                    compatToken,
		URL:                      url,
		ParticipantsCount:        participantCount,
		Caller:                   caller,
		Callee:                   callee,
		IsVideo:                  isVideo,
		Message:                  apiMessage(c, "Call join token issued"),
		AlreadyAcceptedElsewhere: callInfo != nil && acceptedByUserID(callInfo) == userID && !sameCallEndpoint(callInfo, tokenCtx.DeviceID, tokenCtx.SessionID),
		AnsweredElsewhere:        callInfo != nil && acceptedByUserID(callInfo) == userID && !sameCallEndpoint(callInfo, tokenCtx.DeviceID, tokenCtx.SessionID),
		AcceptedByUserID:         acceptedByUserID(callInfo),
		AcceptedDeviceID:         acceptedByDeviceID(callInfo),
		AcceptedSessionID:        acceptedBySessionID(callInfo),
		AccessBypassed:           h.bypassAccessEnabled(),
		AccessValidationDisabled: true,
		Warning:                  liveKitBypassWarning(callInfo, h.bypassAccessEnabled()),
	})
}

// KickParticipant godoc
// @Summary Kick a participant
// @Description Disconnect a participant from a room
// @Tags calls
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body KickRequest true "Kick details"
// @Success 200 {object} map[string]string
// @Failure 400 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /calls/kick [post]
func (h *LiveKitHandler) KickParticipant(c *gin.Context) {
	var req KickRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	if err := h.service.KickParticipant(c.Request.Context(), req.RoomName, req.Identity); err != nil {
		h.respondError(c, apperrors.InternalWithErr("failed to kick participant", err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "participant kicked")})
}

// MuteAll godoc
// @Summary Mute/Unmute room
// @Description Mute or unmute all participants in a room (useful for lectures)
// @Tags calls
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body MuteAllRequest true "Mute details"
// @Success 200 {object} map[string]string
// @Failure 400 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /calls/mute-all [post]
func (h *LiveKitHandler) MuteAll(c *gin.Context) {
	var req MuteAllRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	if err := h.service.MuteRoom(c.Request.Context(), req.RoomName, req.Mute); err != nil {
		h.respondError(c, apperrors.InternalWithErr("failed to mute room", err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "room mute status updated")})
}

// StartRecording godoc
// @Summary Start recording
// @Description Start recording a room via Egress
// @Tags calls
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body RecordRequest true "Recording details"
// @Success 200 {object} map[string]string "Returns egress_id"
// @Failure 400 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /calls/record [post]
func (h *LiveKitHandler) StartRecording(c *gin.Context) {
	var req RecordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	egressID, err := h.service.StartRecording(c.Request.Context(), req.RoomName)
	if err != nil {
		h.respondError(c, apperrors.InternalWithErr("failed to start recording", err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"egress_id": egressID})
}

// StopRecording godoc
// @Summary Stop recording
// @Description Stop an active recording by Egress ID
// @Tags calls
// @Security BearerAuth
// @Produce json
// @Param id path string true "Egress ID"
// @Success 200 {object} map[string]string
// @Failure 400 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /calls/record/{id} [delete]
// GetCallStatus godoc
// @Summary Get call status
// @Description Check the current status of a call (useful for polling)
// @Tags calls
// @Security BearerAuth
// @Produce json
// @Param id path string true "Call ID"
// @Success 200 {object} map[string]string
// @Failure 404 {object} map[string]interface{}
// @Router /calls/{id} [get]
func (h *LiveKitHandler) GetCallStatus(c *gin.Context) {
	callIDStr := strings.TrimSpace(c.Param("id"))
	var (
		callInfo *domain.CallHistory
		err      error
	)
	if callUUID, parseErr := uuid.Parse(callIDStr); parseErr == nil {
		callInfo, err = h.callRepo.GetByCallID(c.Request.Context(), callUUID)
	} else {
		callInfo, err = h.callRepo.GetByRoomID(c.Request.Context(), callIDStr)
	}
	if err != nil {
		h.respondError(c, apperrors.InternalWithErr("failed to get call info", err))
		return
	}
	if callInfo == nil {
		h.respondError(c, apperrors.NotFound("call not found"))
		return
	}

	authenticatedUserID := middleware.MustGetUserID(c)
	userID, _, resolveErr := h.resolveEffectiveCallUser(c.Request.Context(), authenticatedUserID)
	if resolveErr != nil {
		h.logger.Warn("failed to resolve canonical call user",
			zap.Int64("current_user_id", authenticatedUserID),
			zap.Error(resolveErr),
		)
		userID = authenticatedUserID
	}
	allowed, accessBypassed := h.ensureDirectCallAccessWithBypass(c, "calls.status", callInfo, userID)
	if !allowed {
		return
	}
	isParticipant := true

	currentDeviceID := currentLinkedDeviceID(c)
	access := buildCallStatusAccess(callInfo, userID, currentDeviceID, isParticipant, time.Now())
	status := callInfo.Status
	canJoin := access.CanJoin
	participantsCount := 0
	if isGroupCallHistory(callInfo) && strings.TrimSpace(callInfo.RoomID) != "" {
		if activeUsers, activeUsersErr := h.countDistinctLiveKitUsers(c.Request.Context(), callInfo.RoomID); activeUsersErr != nil {
			h.logger.Warn("failed to resolve group call participant count for call status",
				zap.String("call_id", callInfo.CallID.String()),
				zap.String("room_name", callInfo.RoomID),
				zap.Error(activeUsersErr),
			)
		} else {
			participantsCount = activeUsers
			if activeUsers >= 2 && !domain.IsCallFinalStatus(status) {
				status = domain.CallStatusOngoing
			} else if activeUsers == 0 || domain.IsCallAcceptedStatus(status) {
				status = domain.CallStatusEnded
				canJoin = false
			}
		}
	}

	respondJSON(c, http.StatusOK, gin.H{
		"call_id":                    callInfo.CallID.String(),
		"room_id":                    callInfo.RoomID,
		"room_name":                  callInfo.RoomID,
		"livekit_room":               callInfo.RoomID,
		"status":                     status,
		"call_type":                  access.CallType,
		"can_accept":                 access.CanAccept,
		"can_decline":                access.CanDecline,
		"can_join":                   canJoin,
		"is_expired":                 access.IsExpired,
		"caller_id":                  callInfo.FromUserID,
		"callee_id":                  callInfo.ToUserID,
		"caller_name":                derefString(callInfo.FromName),
		"is_video":                   callInfo.IsVideo,
		"created_at":                 callInfo.CreatedAt,
		"expires_at":                 access.ExpiresAt,
		"is_participant":             access.IsParticipant,
		"accepted_by_user_id":        access.AcceptedByUserID,
		"accepted_by_device_id":      access.AcceptedByDeviceID,
		"accepted_by_session_id":     acceptedBySessionID(callInfo),
		"active_device_id":           access.ActiveDeviceID,
		"active_on_current_device":   access.ActiveOnCurrentDevice,
		"participants_count":         participantsCount,
		"access_bypassed":            accessBypassed,
		"access_validation_disabled": true,
	})
}

func (h *LiveKitHandler) StopRecording(c *gin.Context) {
	egressID := c.Param("id")
	if egressID == "" {
		h.respondError(c, apperrors.Validation("egress id required"))
		return
	}

	if err := h.service.StopRecording(c.Request.Context(), egressID); err != nil {
		if strings.Contains(err.Error(), "EGRESS_ABORTED") || strings.Contains(err.Error(), "not found") {
			h.logger.Warn("recording already aborted or not found, treating as stopped",
				zap.String("egress_id", egressID), zap.Error(err))
		} else {
			h.logger.Error("failed to stop recording", zap.String("egress_id", egressID), zap.Error(err))
			h.respondError(c, apperrors.InternalWithErr("failed to stop recording", err))
			return
		}
	}

	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "recording stopped")})
}

// ─── Audio Recording Handlers ────────────────────────────────────────────────

// StartAudioRecording godoc
// @Summary Start audio recording of a call
// @Description Starts an audio-only recording (MP4/AAC) of the specified call room.
// @Description Uses RoomCompositeEgress with AudioOnly=true — LiveKit best practice for audio calls.
// @Description Only one active recording per call is allowed (idempotency guard).
// @Tags calls
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body StartAudioRecordingRequest true "Call and Room details"
// @Success 200 {object} StartAudioRecordingResponse
// @Failure 400 {object} map[string]interface{}
// @Failure 409 {object} map[string]interface{} "Recording already running"
// @Failure 500 {object} map[string]interface{}
// @Router /calls/record/audio [post]
func (h *LiveKitHandler) StartAudioRecording(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	// Diagnostic trace: emitted before any processing so it always reaches logs
	// even when the request fails early.
	h.logger.Info("personal_recording_start_runtime_flow",
		zap.String("endpoint", "/api/v1/calls/record/audio"),
		zap.String("egress_type", "track_audio"),
		zap.String("livekit_method", "StartTrackEgress"),
		zap.Int64("current_user_id", userID),
		zap.String("livekit_api_url", h.service.GetAPIURL()),
		zap.String("livekit_url", h.service.GetClientURL()),
		zap.String("minio_endpoint", h.service.MinIOEgressEndpoint()),
	)

	var req StartAudioRecordingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body: call_id and room_name are required"))
		return
	}

	callUUID, err := uuid.Parse(req.CallID)
	if err != nil {
		h.respondError(c, apperrors.Validation("invalid call_id format"))
		return
	}

	// 1. Idempotency Guard: не запускать если запись уже идёт.
	existing, err := h.recordingRepo.GetActiveByCallID(c.Request.Context(), callUUID)
	if err != nil {
		h.logger.Error("failed to check active recording", zap.Error(err))
		h.respondError(c, apperrors.InternalWithErr("failed to check existing recording", err))
		return
	}
	if existing != nil {
		h.logger.Warn("Recording already active",
			zap.String("egress_id", existing.EgressID),
			zap.String("call_id", req.CallID),
		)
		respondJSON(c, http.StatusConflict, gin.H{
			"error":     "recording_already_active",
			"message":   apiMessage(c, "A recording is already running for this call"),
			"egress_id": existing.EgressID,
			"status":    existing.Status,
		})
		return
	}

	// 2. Запускаем TrackEgress (audio-only, прямая RTP-запись трека участника).
	//    Используем StartPersonalAudioRecording, а НЕ StartRoomCompositeEgress —
	//    RoomComposite запускает Chrome headless и требует "start signal",
	//    что приводит к ошибке 412 "Start signal not received" для аудио-only вызовов.
	filePath := fmt.Sprintf("recordings/audio/%s/%d-%d.ogg", req.CallID, userID, time.Now().Unix())
	egressID, participantIdentity, err := h.service.StartPersonalAudioRecording(
		c.Request.Context(),
		req.CallID,
		req.RoomName,
		userID,
		filePath,
	)
	if err != nil {
		h.logger.Error("failed to start audio recording",
			zap.Error(err),
			zap.String("room", req.RoomName),
			zap.String("call_id", req.CallID),
			zap.Int64("started_by", userID),
			zap.String("egress_type", "track_audio"),
			zap.String("livekit_method", "StartTrackEgress"),
		)
		switch {
		case errors.Is(err, service.ErrPersonalRecordingParticipantNotFound):
			h.respondError(c, apperrors.Conflict("participant is not connected to the call"))
		case errors.Is(err, service.ErrPersonalRecordingAudioTrackNotFound):
			h.respondError(c, apperrors.Conflict("audio track is not ready for recording"))
		case errors.Is(err, service.ErrPersonalRecordingAudioTrackMuted):
			h.respondError(c, apperrors.Conflict("audio track is muted and cannot be recorded"))
		default:
			h.respondError(c, apperrors.InternalWithErr("failed to start audio recording", err))
		}
		return
	}

	// 3. Персистируем метаданные.
	rec := &domain.CallRecording{
		CallID:    callUUID,
		RoomID:    req.RoomName,
		EgressID:  egressID,
		Status:    domain.RecordingStatusRecording,
		FilePath:  &filePath,
		StartedBy: userID,
	}
	if saveErr := h.recordingRepo.Create(c.Request.Context(), rec); saveErr != nil {
		// Критически важно залогировать — егресс уже запущен, но мы не можем его отследить!
		h.logger.Error("CRITICAL: egress started but failed to save recording metadata",
			zap.Error(saveErr),
			zap.String("egress_id", egressID),
			zap.String("call_id", req.CallID),
		)
		// Не возвращаем ошибку клиенту — запись всё равно началась.
	}

	h.logger.Info("Audio recording started successfully",
		zap.String("egress_id", egressID),
		zap.String("call_id", req.CallID),
		zap.String("room", req.RoomName),
		zap.String("participant_identity", participantIdentity),
		zap.Int64("started_by", userID),
		zap.String("egress_type", "track_audio"),
	)

	respondJSON(c, http.StatusOK, StartAudioRecordingResponse{
		EgressID: egressID,
		FilePath: filePath,
		Status:   string(domain.RecordingStatusRecording),
	})
}

// StopAudioRecording godoc
// @Summary Stop audio recording
// @Description Stops an active audio recording by its Egress ID and updates DB status.
// @Tags calls
// @Security BearerAuth
// @Produce json
// @Param id path string true "Egress ID"
// @Success 200 {object} map[string]string
// @Failure 400 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /calls/record/audio/{id} [delete]
func (h *LiveKitHandler) StopAudioRecording(c *gin.Context) {
	egressID := c.Param("id")
	if egressID == "" {
		h.respondError(c, apperrors.Validation("egress_id is required"))
		return
	}

	// 1. Остановить Egress в LiveKit.
	if err := h.service.StopRecording(c.Request.Context(), egressID); err != nil {
		if strings.Contains(err.Error(), "EGRESS_ABORTED") || strings.Contains(err.Error(), "not found") {
			h.logger.Warn("audio recording already aborted or not found, treating as stopped",
				zap.String("egress_id", egressID), zap.Error(err))
		} else {
			h.logger.Error("failed to stop audio recording",
				zap.String("egress_id", egressID),
				zap.Error(err),
			)
			h.respondError(c, apperrors.InternalWithErr("failed to stop recording", err))
			return
		}
	}

	// 2. Обновить статус в DB.
	status := domain.RecordingStatusStopped
	if updateErr := h.recordingRepo.UpdateStatus(c.Request.Context(), egressID, status, nil, nil, nil); updateErr != nil {
		h.logger.Error("failed to update recording status after stop",
			zap.String("egress_id", egressID),
			zap.Error(updateErr),
		)
		// Не фейлим ответ — запись остановлена на уровне LiveKit.
	}

	h.logger.Info("Audio recording stopped", zap.String("egress_id", egressID))
	respondJSON(c, http.StatusOK, gin.H{
		"message":   apiMessage(c, "recording stopped"),
		"egress_id": egressID,
		"status":    string(status),
	})
}

// GetAudioRecordingStatus godoc
// @Summary Get audio recording status
// @Description Polls the current status of an audio recording from both LiveKit and DB.
// @Tags calls
// @Security BearerAuth
// @Produce json
// @Param id path string true "Egress ID"
// @Success 200 {object} AudioRecordingStatusResponse
// @Failure 404 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /calls/record/audio/{id}/status [get]
func (h *LiveKitHandler) GetAudioRecordingStatus(c *gin.Context) {
	egressID := c.Param("id")
	if egressID == "" {
		h.respondError(c, apperrors.Validation("egress_id is required"))
		return
	}

	// 1. DB — наши метаданные.
	rec, err := h.recordingRepo.GetByEgressID(c.Request.Context(), egressID)
	if err != nil {
		h.respondError(c, apperrors.InternalWithErr("failed to get recording", err))
		return
	}
	if rec == nil {
		h.respondError(c, apperrors.NotFound("recording not found"))
		return
	}

	resp := AudioRecordingStatusResponse{
		EgressID:  egressID,
		Status:    string(rec.Status),
		DurationS: rec.DurationS,
	}
	if rec.FilePath != nil {
		resp.FilePath = *rec.FilePath
	}

	// 2. Опционально: live-статус из LiveKit (если запись ещё идёт).
	if rec.Status == domain.RecordingStatusRecording {
		info, lkErr := h.service.GetEgressInfo(c.Request.Context(), egressID)
		if lkErr == nil && info != nil {
			// Маппинг LiveKit Egress Status → наш статус.
			switch info.Status {
			case 3: // EGRESS_COMPLETE
				newStatus := domain.RecordingStatusCompleted
				_ = h.recordingRepo.UpdateStatus(c.Request.Context(), egressID, newStatus, nil, nil, nil)
				resp.Status = string(newStatus)
			case 4: // EGRESS_FAILED
				newStatus := domain.RecordingStatusFailed
				_ = h.recordingRepo.UpdateStatus(c.Request.Context(), egressID, newStatus, nil, nil, nil)
				resp.Status = string(newStatus)
			}
		}
	}

	respondJSON(c, http.StatusOK, resp)
}

// ListCallRecordings godoc
// @Summary List recordings for a call
// @Description Returns all audio recordings associated with a call_id.
// @Tags calls
// @Security BearerAuth
// @Produce json
// @Param id path string true "Call ID (UUID)"
// @Success 200 {array} domain.CallRecording
// @Failure 400 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /calls/{id}/recordings [get]
func (h *LiveKitHandler) ListCallRecordings(c *gin.Context) {
	callIDStr := c.Param("id")
	callUUID, err := uuid.Parse(callIDStr)
	if err != nil {
		h.respondError(c, apperrors.Validation("invalid call_id"))
		return
	}

	recordings, err := h.recordingRepo.ListByCallID(c.Request.Context(), callUUID)
	if err != nil {
		h.respondError(c, apperrors.InternalWithErr("failed to list recordings", err))
		return
	}

	if recordings == nil {
		recordings = []domain.CallRecording{}
	}

	respondJSON(c, http.StatusOK, gin.H{"recordings": recordings})
}

// ListUserRecordings godoc
// @Summary List all user recordings
// @Description Returns all audio recordings for calls that the current user participated in.
// @Tags calls
// @Security BearerAuth
// @Produce json
// @Param limit query int false "Limit"
// @Param offset query int false "Offset"
// @Success 200 {array} domain.CallRecording
// @Failure 500 {object} map[string]interface{}
// @Router /calls/recordings/audio [get]
func (h *LiveKitHandler) ListUserRecordings(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	limitStr := c.DefaultQuery("limit", "20")
	offsetStr := c.DefaultQuery("offset", "0")

	limit, _ := strconv.Atoi(limitStr)
	offset, _ := strconv.Atoi(offsetStr)

	recordings, err := h.recordingRepo.ListForUser(c.Request.Context(), userID, limit, offset)
	if err != nil {
		h.logger.Error("failed to list user recordings", zap.Error(err))
		h.respondError(c, apperrors.InternalWithErr("failed to list user recordings", err))
		return
	}

	if recordings == nil {
		recordings = []domain.CallRecording{}
	}

	respondJSON(c, http.StatusOK, gin.H{"recordings": recordings})
}

// DownloadAudioRecording godoc
// @Summary Download audio recording
// @Description Streams the actual audio file (.mp4) for a specified call Egress
// @Tags calls
// @Security BearerAuth
// @Produce application/octet-stream
// @Param id path string true "Egress ID"
// @Success 200 {file} file
// @Failure 404 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /calls/record/audio/{id}/download [get]
func (h *LiveKitHandler) DownloadAudioRecording(c *gin.Context) {
	egressID := c.Param("id")
	if egressID == "" {
		h.respondError(c, apperrors.Validation("egress_id is required"))
		return
	}

	// 1. Получаем метаданные
	rec, err := h.recordingRepo.GetByEgressID(c.Request.Context(), egressID)
	if err != nil {
		h.respondError(c, apperrors.InternalWithErr("failed to get recording by egressID", err))
		return
	}
	if rec == nil || rec.FilePath == nil || *rec.FilePath == "" {
		h.respondError(c, apperrors.NotFound("recording file not found or still recording"))
		return
	}

	if h.fileService == nil {
		h.respondError(c, apperrors.Internal("file service is not initialized"))
		return
	}

	// 2. Делаем стриминг через FileService из S3 (MinIO)
	reader, contentType, size, err := h.fileService.DownloadFile(c.Request.Context(), *rec.FilePath)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "not found") {
			h.logger.Warn("recording not found in storage", zap.String("file_path", *rec.FilePath))
			h.respondError(c, apperrors.NotFound("recording file not found in storage"))
			return
		}
		h.logger.Error("failed to download recording from storage", zap.Error(err), zap.String("file_path", *rec.FilePath))
		h.respondError(c, apperrors.InternalWithErr("failed to read recording from storage", err))
		return
	}
	defer reader.Close()

	if contentType == "" || contentType == "application/octet-stream" {
		contentType = "audio/mp4" // Fallback to MP4 (AAC) based on our logic
	}

	// 3. Отдаём клиенту как файл
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", *rec.FilePath))
	c.DataFromReader(http.StatusOK, size, contentType, reader, map[string]string{})
}

// ─────────────────────────────────────────────────────────────────────────────

func (h *LiveKitHandler) respondError(c *gin.Context, err *apperrors.AppError) {
	respondAppError(c, err)
}

// InviteToCall godoc
// @Summary Invite a user to an existing call
// @Description Sends a call invitation (ringing) to a user for an existing room
// @Tags calls
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body InviteToCallRequest true "Invite details"
// @Success 200 {object} map[string]string
// @Failure 400 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /calls/invite [post]
func (h *LiveKitHandler) InviteToCall(c *gin.Context) {
	callerID := middleware.MustGetUserID(c)
	callerName := getUserDisplayName(c, callerID)

	var req InviteToCallRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	existingCall, resolveErr := h.resolveDirectCallForAccess(c.Request.Context(), req.CallID, req.RoomName)
	if resolveErr != nil {
		h.respondError(c, apperrors.ToAppError(resolveErr))
		return
	}
	if existingCall == nil {
		h.respondError(c, apperrors.NotFound("call not found"))
		return
	}
	if req.CalleeID == callerID {
		h.respondError(c, apperrors.BadRequest("cannot invite yourself to the call"))
		return
	}
	if !h.ensureDirectCallAccess(c, "calls.invite", existingCall, callerID) {
		return
	}

	normalizedStatus := domain.NormalizeCallStatus(existingCall.Status)
	switch normalizedStatus {
	case domain.CallStatusRinging, domain.CallStatusConnecting, domain.CallStatusAccepted, domain.CallStatusOngoing:
	default:
		h.respondError(c, apperrors.New("CALL_NOT_ACTIVE", "call is not active for invite", http.StatusConflict))
		return
	}

	if existingCall.CallID == uuid.Nil {
		h.respondError(c, apperrors.NotFound("call not found"))
		return
	}

	callRoomName := strings.TrimSpace(existingCall.RoomID)
	if callRoomName == "" {
		callRoomName = strings.TrimSpace(req.RoomName)
	}
	if callRoomName == "" {
		h.respondError(c, apperrors.Validation("room_name is required"))
		return
	}

	if !isGroupCallHistory(existingCall) {
		if err := h.callRepo.UpdateCallType(c.Request.Context(), existingCall.CallID, domain.CallTypeGroup); err != nil {
			h.logger.Error("failed to upgrade private call to group", zap.Error(err), zap.String("call_id", existingCall.CallID.String()))
			h.respondError(c, apperrors.InternalWithErr("failed to upgrade call", err))
			return
		}
		existingCall.CallType = domain.CallTypeGroup
	}

	err := h.callRepo.AddParticipant(c.Request.Context(), &domain.CallParticipant{
		CallID: existingCall.CallID,
		UserID: req.CalleeID,
		Status: "invited",
		Role:   "participant",
	})
	if err != nil {
		h.logger.Error("failed to add participant", zap.Error(err), zap.String("call_id", existingCall.CallID.String()), zap.Int64("callee_id", req.CalleeID))
		h.respondError(c, apperrors.InternalWithErr("failed to invite participant", err))
		return
	}

	groupIDForWS := strings.TrimSpace(req.ChatID)
	if groupIDForWS == "" && existingCall.ChatID != nil {
		groupIDForWS = strings.TrimSpace(*existingCall.ChatID)
	}
	isGroupInvite := true
	groupNameForWS := ""
	if groupIDForWS == "" {
		if parsedGroupID, ok := parseGroupIDFromRoomName(callRoomName); ok {
			groupIDForWS = parsedGroupID.String()
		}
	}
	if isGroupInvite && groupIDForWS != "" && h.groupService != nil {
		if parsedGroupID, err := uuid.Parse(groupIDForWS); err == nil {
			if groupDetails, groupErr := h.groupService.GetGroup(c.Request.Context(), parsedGroupID, callerID); groupErr == nil && groupDetails != nil {
				groupNameForWS = groupDetails.Name
			}
		}
	}

	// 4. Send Push Notification with the CORRECT CallID
	go func(bgCtx context.Context, calleeID, fromID int64, fromName string, cID uuid.UUID, rName, chID, groupID, groupName string, groupInvite bool) {
		if groupInvite {
			h.notifService.SendGroupCallInviteWS(
				calleeID,
				fromID,
				fromName,
				groupID,
				groupName,
				rName,
				cID,
			)
		} else {
			h.notifService.SendIncomingCallWS(
				calleeID,
				fromID,
				fromName,
				cID,
				rName,
				chID,
				req.IsVideo == nil || *req.IsVideo,
			)
		}
		err := h.notifService.SendIncomingCallPushWithType(
			bgCtx,
			calleeID,
			fromID,
			fromName,
			cID,
			rName,
			chID,
			"high",
			req.IsVideo == nil || *req.IsVideo,
			domain.CallTypeGroup,
		)
		if err != nil {
			h.logger.Error("failed to send invite call push", zap.Error(err))
		}
	}(context.Background(), req.CalleeID, callerID, callerName, existingCall.CallID, callRoomName, strings.TrimSpace(req.ChatID), groupIDForWS, groupNameForWS, isGroupInvite)

	respondJSON(c, http.StatusOK, gin.H{
		"message":   apiMessage(c, "invitation sent"),
		"call_id":   existingCall.CallID.String(),
		"room_name": callRoomName,
		"call_type": domain.CallTypeGroup,
	})
}

type RejoinCallRequest struct {
	CallID   string `json:"call_id"`
	RoomName string `json:"room_name"`
}

type RejoinCallResponse struct {
	Success                  bool           `json:"success"`
	Token                    string         `json:"token"`
	URL                      string         `json:"url"`
	RoomName                 string         `json:"room_name"`
	RoomID                   string         `json:"room_id,omitempty"`
	CallID                   string         `json:"call_id"`
	Status                   string         `json:"status"` // "ongoing", "ended"
	ChatID                   string         `json:"chat_id,omitempty"`
	ParticipantsCount        int            `json:"participants_count"`
	LivekitURL               string         `json:"livekit_url,omitempty"`
	LivekitToken             string         `json:"livekit_token,omitempty"`
	Caller                   AcceptCallUser `json:"caller"`
	Callee                   AcceptCallUser `json:"callee"`
	IsVideo                  bool           `json:"is_video"`
	Message                  string         `json:"message,omitempty"`
	AlreadyFinalized         bool           `json:"already_finalized,omitempty"`
	LocalDismissOnly         bool           `json:"local_dismiss_only,omitempty"`
	AlreadyAcceptedElsewhere bool           `json:"already_accepted_elsewhere,omitempty"`
	AnsweredElsewhere        bool           `json:"answered_elsewhere,omitempty"`
	AcceptedByUserID         int64          `json:"accepted_by_user_id,omitempty"`
	AcceptedDeviceID         string         `json:"accepted_device_id,omitempty"`
	AcceptedSessionID        string         `json:"accepted_session_id,omitempty"`
	AccessBypassed           bool           `json:"access_bypassed,omitempty"`
	AccessValidationDisabled bool           `json:"access_validation_disabled,omitempty"`
	Warning                  string         `json:"warning,omitempty"`
}

func (h *LiveKitHandler) logLiveKitRouting(event, callID, roomName string) {
	if h.service == nil || h.logger == nil {
		return
	}
	h.logger.Info("LiveKit routing resolved",
		zap.String("event", event),
		zap.String("call_id", callID),
		zap.String("room_name", roomName),
		zap.String("client_livekit_url", h.service.GetClientURL()),
		zap.String("server_livekit_api_url", h.service.GetAPIURL()),
	)
}

// RejoinCall godoc
// @Summary Rejoin an active call
// @Description Allows a user to rejoin an active call (1:1 or group) without creating a new one
// @Tags calls
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body RejoinCallRequest true "Rejoin details"
// @Success 200 {object} RejoinCallResponse
// @Failure 404 {object} map[string]interface{} "Call not found"
// @Failure 409 {object} map[string]interface{} "Call ended"
// @Router /calls/rejoin [post]
func (h *LiveKitHandler) RejoinCall(c *gin.Context) {
	authenticatedUserID := middleware.MustGetUserID(c)
	userID, _, resolveEffectiveErr := h.resolveEffectiveCallUser(c.Request.Context(), authenticatedUserID)
	if resolveEffectiveErr != nil {
		h.logger.Warn("failed to resolve canonical call user",
			zap.Int64("current_user_id", authenticatedUserID),
			zap.Error(resolveEffectiveErr),
		)
		userID = authenticatedUserID
	}
	userName := getUserDisplayName(c, userID)
	h.incrementTelemetry("call_rejoin_total", nil)

	var req RejoinCallRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}
	if req.CallID == "" && req.RoomName == "" {
		h.respondError(c, apperrors.Validation("either call_id or room_name is required"))
		return
	}

	var roomName string
	var callIDStr string
	var chatID string
	var callInfo *domain.CallHistory

	resolveErr := error(nil)
	callInfo, resolveErr = h.resolveDirectCallForAccess(c.Request.Context(), req.CallID, req.RoomName)
	if resolveErr != nil {
		if appErr, ok := resolveErr.(*apperrors.AppError); ok {
			if appErr.StatusCode != http.StatusNotFound {
				h.respondError(c, appErr)
				return
			}
			if strings.TrimSpace(req.RoomName) == "" {
				h.respondError(c, appErr)
				return
			}
		}
	}

	if callInfo != nil {
		if !h.ensureDirectCallAccess(c, "calls.rejoin", callInfo, userID) {
			return
		}
		if domain.IsCallFinalStatus(callInfo.Status) {
			h.respondError(c, apperrors.New("CALL_ENDED", "call already ended", http.StatusConflict))
			return
		}
		roomName = callInfo.RoomID
		callIDStr = callInfo.CallID.String()
		if callInfo.ChatID != nil {
			chatID = *callInfo.ChatID
		}
	} else {
		roomName = req.RoomName
		if roomName == "" {
			roomName = req.CallID
		}
		if strings.HasPrefix(roomName, "call_group_") {
			parts := strings.Split(roomName, "_")
			if len(parts) >= 3 {
				groupIDStr := parts[2]
				groupID, err := uuid.Parse(groupIDStr)
				if err != nil {
					h.respondError(c, apperrors.BadRequest("invalid group call room name"))
					return
				}
				if _, err = h.groupService.GetGroup(c.Request.Context(), groupID, userID); err != nil {
					h.respondError(c, apperrors.Forbidden("access denied to group call"))
					return
				}
				callIDStr = roomName
			} else {
				h.respondError(c, apperrors.BadRequest("invalid group call room format"))
				return
			}
		} else {
			callIDStr = strings.TrimSpace(req.CallID)
			if callIDStr == "" {
				callIDStr = roomName
			}
		}
	}

	participants, err := h.service.ListParticipants(c.Request.Context(), roomName)
	if err != nil {
		h.logger.Error("failed to list participants",
			zap.Error(err),
			zap.String("room_name", roomName),
			zap.String("call_id", callIDStr),
			zap.Int64("user_id", userID),
			zap.String("request_id", h.requestIDFromContext(c)),
		)
		if strings.Contains(err.Error(), "not_found") || strings.Contains(err.Error(), "not found") {
			h.respondError(c, apperrors.New("CALL_ENDED", "call already ended", http.StatusConflict))
			return
		}
		h.incrementTelemetry("call_rejoin_livekit_unreachable_total", nil)
		h.recordCallEvent(c, "call_rejoin", userID, callIDStr, roomName, "", "livekit_unreachable", map[string]any{
			"error": err.Error(),
		})
	}

	participantCount := len(participants)
	isGroupRejoin := isGroupCallHistory(callInfo) || strings.HasPrefix(strings.TrimSpace(roomName), "call_group_")
	if err == nil && participantCount == 0 {
		h.respondError(c, apperrors.New("CALL_ENDED", "call already ended", http.StatusConflict))
		return
	}
	if err == nil && isGroupRejoin && participantCount < 2 {
		if callInfo != nil {
			if _, _, finalizeErr := h.syncGroupCallLifecycle(c.Request.Context(), callInfo, "rejoin_below_two_participants"); finalizeErr != nil {
				h.logger.Warn("failed to finalize stale group call before rejecting rejoin",
					zap.String("call_id", callIDStr),
					zap.String("room_name", roomName),
					zap.Error(finalizeErr),
				)
			}
		}
		respondJSON(c, http.StatusConflict, gin.H{
			"success":            false,
			"call_id":            callIDStr,
			"room_name":          roomName,
			"room_id":            roomName,
			"status":             domain.CallStatusEnded,
			"participants_count": participantCount,
			"message":            apiMessage(c, "Group call already ended"),
			"reason":             "group_call_below_two_participants",
		})
		return
	}

	tokenCtx := h.tokenContextFromRequest(c)
	token, err := h.service.GenerateToken(roomName, userID, userName, tokenCtx)
	if err != nil {
		h.respondError(c, apperrors.InternalWithErr("failed to generate token", err))
		return
	}
	if callInfo != nil && !isGroupCallHistory(callInfo) {
		h.markPrivateCallConnecting(c.Request.Context(), callInfo, "rejoin_token_issued")
	}
	h.recordCallEvent(c, "call_rejoin", userID, callIDStr, roomName, service.BuildParticipantIdentityForToken(userID, tokenCtx), "ok", map[string]any{
		"participants_count": participantCount,
	})

	if uid, err := uuid.Parse(callIDStr); err == nil {
		_ = h.callRepo.AddParticipant(c.Request.Context(), &domain.CallParticipant{
			CallID: uid,
			UserID: userID,
			Status: "joined",
			Role:   "participant",
		})
	}

	caller := AcceptCallUser{}
	callee := AcceptCallUser{}
	isVideo := true
	status := domain.CallStatusConnecting
	if callInfo != nil {
		caller = acceptCallUserFromCall(callInfo, callInfo.FromUserID, derefString(callInfo.FromName))
		callee = acceptCallUserFromCall(callInfo, callInfo.ToUserID, "")
		isVideo = callInfo.IsVideo
		status = domain.NormalizeCallStatus(callInfo.Status)
	}
	if domain.IsCallFinalStatus(status) {
		h.respondError(c, apperrors.New("CALL_ENDED", "call already ended", http.StatusConflict))
		return
	}
	url, compatToken := h.buildUserScopedTokenCompatFields(userID, c.GetString("username"), c.GetString("email"), token)
	respondJSON(c, http.StatusOK, RejoinCallResponse{
		Success:                  true,
		CallID:                   callIDStr,
		RoomName:                 roomName,
		RoomID:                   roomName,
		Token:                    compatToken,
		URL:                      url,
		Status:                   status,
		ChatID:                   chatID,
		ParticipantsCount:        participantCount,
		LivekitURL:               url,
		LivekitToken:             compatToken,
		Caller:                   caller,
		Callee:                   callee,
		IsVideo:                  isVideo,
		Message:                  apiMessage(c, "Call rejoin token issued"),
		AlreadyAcceptedElsewhere: callInfo != nil && acceptedByUserID(callInfo) == userID && !sameCallEndpoint(callInfo, tokenCtx.DeviceID, tokenCtx.SessionID),
		AnsweredElsewhere:        callInfo != nil && acceptedByUserID(callInfo) == userID && !sameCallEndpoint(callInfo, tokenCtx.DeviceID, tokenCtx.SessionID),
		AcceptedByUserID:         acceptedByUserID(callInfo),
		AcceptedDeviceID:         acceptedByDeviceID(callInfo),
		AcceptedSessionID:        acceptedBySessionID(callInfo),
		AccessBypassed:           h.bypassAccessEnabled(),
		AccessValidationDisabled: true,
		Warning:                  liveKitBypassWarning(callInfo, h.bypassAccessEnabled()),
	})
}

// HandleWebhook godoc
// @Summary Handle LiveKit Webhook
// @Description Receives and processes webhook events from LiveKit server (e.g. participant_left)
// @Tags calls
// @Accept json
// @Produce json
// @Success 200 {object} map[string]string
// @Failure 401 {object} map[string]interface{} "Invalid signature"
// @Failure 500 {object} map[string]interface{}
// @Router /calls/webhook [post]
func (h *LiveKitHandler) HandleWebhook(c *gin.Context) {
	authProvider := c.GetHeader("Authorization")
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		h.logger.Error("Failed to read webhook body", zap.Error(err))
		respondJSON(c, http.StatusInternalServerError, gin.H{"error": "failed to read body"})
		return
	}
	body := string(bodyBytes)

	event, err := h.service.ValidateWebhook(body, authProvider)
	if err != nil {
		h.incrementTelemetry("livekit_webhook_validation_failed_total", nil)
		h.logger.Warn("Webhook validation failed", zap.Error(err))
		respondJSON(c, http.StatusUnauthorized, gin.H{"error": "invalid signature"})
		return
	}

	var roomName string
	if event.Room != nil {
		roomName = event.Room.Name
	} else if event.EgressInfo != nil && event.EgressInfo.RoomName != "" {
		roomName = event.EgressInfo.RoomName
	}

	identity := ""
	if event.Participant != nil {
		identity = strings.TrimSpace(event.Participant.Identity)
	}

	var parsedIdentity service.ParticipantIdentity
	parseIdentityErr := fmt.Errorf("empty identity")
	if identity != "" {
		parsedIdentity, parseIdentityErr = service.ParseParticipantIdentity(identity)
	}

	var callInfo *domain.CallHistory
	if roomName != "" {
		callInfo, _ = h.callRepo.GetByRoomID(c.Request.Context(), roomName)
	}

	callIDStr := ""
	webhookTrace := telemetry.TraceContext{
		RoomName:  roomName,
		RequestID: h.requestIDFromContext(c),
		Identity:  identity,
	}
	if callInfo != nil {
		callIDStr = callInfo.CallID.String()
		webhookTrace.CallID = callIDStr
	}
	if parseIdentityErr == nil {
		webhookTrace.UserID = parsedIdentity.UserID
		webhookTrace.SessionID = parsedIdentity.SessionID
	}

	h.incrementTelemetry("livekit_webhook_total", map[string]string{"event": event.Event})
	if h.telemetry != nil {
		h.telemetry.RecordEvent("livekit_webhook", webhookTrace, event.Event, nil)
	}

	h.logger.Info("Received LiveKit webhook",
		zap.String("event", event.Event),
		zap.String("room", roomName),
		zap.String("call_id", callIDStr),
		zap.String("identity", identity),
		zap.Int64("user_id", webhookTrace.UserID),
		zap.String("request_id", webhookTrace.RequestID),
	)

	switch event.Event {
	case "participant_joined":
		if h.telemetry != nil {
			h.telemetry.RecordEvent("participant_joined", webhookTrace, "ok", nil)
		}
		// Add/Update participant status
		if parseIdentityErr == nil {
			if callInfo != nil {
				if err := h.callRepo.AddParticipant(c.Request.Context(), &domain.CallParticipant{
					CallID: callInfo.CallID,
					UserID: parsedIdentity.UserID,
					Status: "joined",
					Role:   "participant",
				}); err != nil {
					h.logger.Error("Failed to add participant (webhook)", zap.Error(err), zap.String("room", roomName), zap.Int64("user_id", parsedIdentity.UserID), zap.String("call_id", callIDStr))
				} else {
					h.logger.Info("Added participant (webhook)", zap.String("room", roomName), zap.Int64("user_id", parsedIdentity.UserID), zap.String("call_id", callIDStr))
				}
			}
		} else if identity != "" {
			h.logger.Warn("Failed to parse identity (participant_joined)", zap.String("identity", identity), zap.Error(parseIdentityErr))
		}

		h.logger.Info("Participant joined room", zap.String("room", roomName), zap.String("identity", identity), zap.String("call_id", callIDStr))

		// Check call status in DB
		if callInfo != nil {
			if isGroupCallHistory(callInfo) {
				if activeUsers, finalized, lifecycleErr := h.syncGroupCallLifecycle(c.Request.Context(), callInfo, "participant_joined"); lifecycleErr != nil {
					h.logger.Warn("failed to sync group call lifecycle after participant joined",
						zap.String("room", roomName),
						zap.String("call_id", callIDStr),
						zap.Error(lifecycleErr),
					)
				} else {
					h.logger.Info("group call lifecycle synced after participant joined",
						zap.String("room", roomName),
						zap.String("call_id", callIDStr),
						zap.Int("active_users", activeUsers),
						zap.Bool("finalized", finalized),
						zap.String("status", callInfo.Status),
					)
				}
			} else if domain.IsCallRingingStatus(callInfo.Status) {
				if activeUsers, err := h.countDistinctLiveKitUsers(c.Request.Context(), roomName); err == nil {
					if activeUsers > 1 {
						if _, updated, err := h.callRepo.TransitionCallStatus(c.Request.Context(), callInfo.CallID, domain.CallStatusAccepted, domain.CallStatusInitiated, domain.CallStatusRinging, domain.CallStatusConnecting); err != nil {
							h.logger.Error("Failed to update call status to accepted on participant_joined",
								zap.Error(err),
								zap.String("room", roomName),
								zap.String("call_id", callIDStr),
							)
						} else if updated {
							callInfo.Status = domain.CallStatusAccepted
							h.logger.Info("Call status updated to accepted after participant_joined",
								zap.String("room", roomName),
								zap.String("call_id", callIDStr),
								zap.Int("active_users", activeUsers),
							)
						}
					} else if activeUsers > 0 {
						h.markPrivateCallConnecting(c.Request.Context(), callInfo, "participant_joined")
					}
				}
			}

			// If call is already terminated, kick the participant immediately
			if domain.IsCallFinalStatus(callInfo.Status) {
				h.logger.Warn("Participant joined terminated call, kicking immediately",
					zap.String("room", roomName),
					zap.String("identity", identity),
					zap.String("call_status", callInfo.Status),
					zap.String("call_id", callIDStr))

				// Kick this participant
				go func(ctx context.Context, room, ident string) {
					kickCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
					defer cancel()
					if err := h.service.KickParticipant(kickCtx, room, ident); err != nil {
						h.logger.Error("Failed to kick participant who joined terminated call", zap.Error(err))
					} else {
						h.logger.Info("Successfully kicked participant from terminated call", zap.String("identity", ident))
					}
				}(c.Request.Context(), roomName, identity)
			}
		}

	case "room_finished":
		if h.telemetry != nil {
			h.telemetry.RecordEvent("room_finished", webhookTrace, "ok", nil)
		}
		if callInfo != nil {
			if err := h.callRepo.UpdateCallStatus(c.Request.Context(), callInfo.CallID, "ended"); err != nil {
				h.logger.Error("Failed to update call status on room_finished", zap.Error(err), zap.String("room", roomName))
			} else {
				h.logger.Info("Call status updated to ended (room_finished)", zap.String("room", roomName))
			}

			go func(cID uuid.UUID) {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if parts, err := h.callRepo.GetParticipants(ctx, cID); err == nil {
					for _, p := range parts {
						if p.Status == "joined" {
							_ = h.callRepo.UpdateParticipantStatus(ctx, cID, p.UserID, "left")
						}
					}
				}
			}(callInfo.CallID)

			// Finalize all personal recordings with active locks for this call.
			if h.personalRecordingRepo != nil {
				go func(cID uuid.UUID) {
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					if termErr := h.personalRecordingRepo.FinalizeCallRecordings(ctx, cID, domain.PersonalRecordingStatusAborted, "call finished before egress completion"); termErr != nil {
						h.logger.Error("Failed to terminate personal recordings on room_finished",
							zap.Error(termErr), zap.String("call_id", cID.String()))
					} else {
						h.logger.Info("Terminated active personal recordings (room_finished)",
							zap.String("call_id", cID.String()))
					}
				}(callInfo.CallID)
			}
		}

	case "participant_left":
		if h.telemetry != nil {
			h.telemetry.RecordEvent("participant_left", webhookTrace, "ok", nil)
		}
		// 1. Update participant status to left
		if parseIdentityErr == nil {
			if callInfo != nil {
				if err := h.callRepo.UpdateParticipantStatus(c.Request.Context(), callInfo.CallID, parsedIdentity.UserID, "left"); err != nil {
					h.logger.Error("Failed to update participant status to left", zap.Error(err), zap.String("room", roomName), zap.Int64("user_id", parsedIdentity.UserID), zap.String("call_id", callIDStr))
				} else {
					h.logger.Info("Updated participant status to left", zap.String("room", roomName), zap.Int64("user_id", parsedIdentity.UserID), zap.String("call_id", callIDStr))
				}
			} else {
				h.logger.Warn("Failed to find call info for room (participant_left)", zap.String("room", roomName), zap.String("call_id", callIDStr))
			}
		} else {
			h.logger.Warn("Failed to parse identity (participant_left)", zap.String("identity", identity), zap.Error(parseIdentityErr))
		}

		// 2. Logic to handle unexpected disconnect during ringing
		if callInfo != nil {
			if isGroupCallHistory(callInfo) {
				if activeUsers, finalized, lifecycleErr := h.syncGroupCallLifecycle(c.Request.Context(), callInfo, "participant_left"); lifecycleErr != nil {
					h.logger.Warn("failed to sync group call lifecycle after participant left",
						zap.String("room", roomName),
						zap.String("call_id", callIDStr),
						zap.Error(lifecycleErr),
					)
				} else {
					h.logger.Info("group call lifecycle synced after participant left",
						zap.String("room", roomName),
						zap.String("call_id", callIDStr),
						zap.Int("active_users", activeUsers),
						zap.Bool("finalized", finalized),
						zap.String("status", callInfo.Status),
					)
				}
			}
			if callInfo.Status == "initiated" {
				var targetUserID int64
				var reason string

				switch {
				case parseIdentityErr == nil && parsedIdentity.UserID == callInfo.FromUserID:
					targetUserID = callInfo.ToUserID
					reason = "canceled"
					h.logger.Info("Caller disconnected while ringing, cancelling call", zap.String("room", roomName))
				case parseIdentityErr == nil && parsedIdentity.UserID == callInfo.ToUserID:
					// For group calls, if an invited participant leaves/declines, we SHOULD NOT cancel the whole call
					if isGroupCallHistory(callInfo) {
						h.logger.Info("Group participant left during initiated state, ignoring auto-cancel", zap.String("room", roomName))
						break
					}
					targetUserID = callInfo.FromUserID
					reason = "declined"
					h.logger.Info("Callee disconnected while ringing, declining call", zap.String("room", roomName))
				}

				if targetUserID != 0 {
					if err := h.notifService.SendIncomingCallCancelPush(c.Request.Context(), targetUserID, callInfo.CallID, roomName, reason); err != nil {
						h.logger.Error("Failed to send cancel push from webhook", zap.Error(err))
					}
					h.cleanupPrivateCallRoom(c.Request.Context(), callInfo.CallID.String(), roomName, "participant_left_during_ringing")
				}
			}
		}
	case "egress_ended":
		// Финализируем статус персональной записи по egress_id.
		if event.EgressInfo == nil {
			break
		}
		egressID := event.EgressInfo.EgressId
		if egressID == "" {
			break
		}

		if h.personalRecordingRepo != nil {
			go func(eid string, info *livekit.EgressInfo) {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				rec, recErr := h.personalRecordingRepo.GetByEgressID(ctx, eid)
				if recErr != nil || rec == nil {
					return
				}
				state := personalRecordingStateFromEgressInfo(info)
				if stopErr := finalizePersonalRecordingState(ctx, rec, state, h.personalRecordingRepo, h.fileService, h.logger); stopErr != nil {
					h.logger.Error("Failed to finalize personal recording on egress_ended",
						zap.String("egress_id", eid), zap.Error(stopErr))
				}
			}(egressID, event.EgressInfo)

		}
	}

	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "ok")})
}

func (h *LiveKitHandler) countDistinctLiveKitUsers(ctx context.Context, roomName string) (int, error) {
	participants, err := h.service.ListParticipants(ctx, roomName)
	if err != nil {
		return 0, err
	}

	userIDs := make(map[int64]struct{}, len(participants))
	for _, participant := range participants {
		parsedIdentity, parseErr := service.ParseParticipantIdentity(participant.Identity)
		if parseErr != nil {
			continue
		}
		userIDs[parsedIdentity.UserID] = struct{}{}
	}
	return len(userIDs), nil
}

func (h *LiveKitHandler) syncGroupCallLifecycle(ctx context.Context, callInfo *domain.CallHistory, reason string) (int, bool, error) {
	if callInfo == nil || !isGroupCallHistory(callInfo) || strings.TrimSpace(callInfo.RoomID) == "" {
		return 0, false, nil
	}

	activeUsers, err := h.countDistinctLiveKitUsers(ctx, callInfo.RoomID)
	if err != nil {
		return 0, false, err
	}

	normalizedStatus := domain.NormalizeCallStatus(callInfo.Status)
	if activeUsers >= 2 {
		if normalizedStatus == domain.CallStatusInitiated ||
			normalizedStatus == domain.CallStatusRinging ||
			normalizedStatus == domain.CallStatusConnecting ||
			normalizedStatus == domain.CallStatusAccepted {
			if _, updated, transitionErr := h.callRepo.TransitionCallStatus(
				ctx,
				callInfo.CallID,
				domain.CallStatusOngoing,
				domain.CallStatusInitiated,
				domain.CallStatusRinging,
				domain.CallStatusConnecting,
				domain.CallStatusAccepted,
			); transitionErr != nil {
				return activeUsers, false, transitionErr
			} else if updated {
				callInfo.Status = domain.CallStatusOngoing
			}
		}
		return activeUsers, false, nil
	}

	shouldFinalize := activeUsers == 0 || normalizedStatus == domain.CallStatusAccepted || normalizedStatus == domain.CallStatusOngoing
	if !shouldFinalize || domain.IsCallFinalStatus(normalizedStatus) {
		return activeUsers, false, nil
	}

	now := time.Now()
	if h.now != nil {
		now = h.now()
	}
	gracePeriod := h.privateCallGrace
	if gracePeriod <= 0 {
		gracePeriod = privateCallCleanupGrace
	}
	lastActivityAt := privateCallLastActivityAt(callInfo)
	if !lastActivityAt.IsZero() {
		if elapsed := now.Sub(lastActivityAt); elapsed < gracePeriod {
			retryAfter := gracePeriod - elapsed
			h.logger.Info("Skip group call finalize because grace period has not passed",
				zap.String("call_id", callInfo.CallID.String()),
				zap.String("room_name", callInfo.RoomID),
				zap.String("status", normalizedStatus),
				zap.String("reason", reason),
				zap.Int("active_users", activeUsers),
				zap.Time("last_activity_at", lastActivityAt),
				zap.Duration("retry_after", retryAfter),
			)
			h.scheduleGroupCallLifecycleRetry(callInfo.CallID, callInfo.RoomID, reason, retryAfter)
			return activeUsers, false, nil
		}
	}

	_, updated, transitionErr := h.callRepo.TransitionCallStatus(
		ctx,
		callInfo.CallID,
		domain.CallStatusEnded,
		domain.CallStatusCreated,
		domain.CallStatusInitiated,
		domain.CallStatusRinging,
		domain.CallStatusConnecting,
		domain.CallStatusAccepted,
		domain.CallStatusOngoing,
	)
	if transitionErr != nil {
		return activeUsers, false, transitionErr
	}
	if !updated {
		if err := h.callRepo.UpdateCallStatus(ctx, callInfo.CallID, domain.CallStatusEnded); err != nil {
			return activeUsers, false, err
		}
	}
	callInfo.Status = domain.CallStatusEnded

	go h.broadcastGroupCallEnded(callInfo, reason)
	h.forceClosePrivateCallRoom(ctx, callInfo.CallID.String(), callInfo.RoomID, reason+"_group_below_two_participants")

	return activeUsers, true, nil
}

func (h *LiveKitHandler) scheduleGroupCallLifecycleRetry(callID uuid.UUID, roomName string, reason string, after time.Duration) {
	if h == nil || h.callRepo == nil {
		return
	}
	if after <= 0 {
		after = time.Second
	}

	go func() {
		timer := time.NewTimer(after)
		defer timer.Stop()
		<-timer.C

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		callInfo, err := h.callRepo.GetByCallID(ctx, callID)
		if err != nil {
			h.logger.Warn("failed to reload group call for lifecycle retry",
				zap.String("call_id", callID.String()),
				zap.String("room_name", roomName),
				zap.String("reason", reason),
				zap.Error(err),
			)
			return
		}
		if callInfo == nil || domain.IsCallFinalStatus(callInfo.Status) {
			return
		}

		if _, _, lifecycleErr := h.syncGroupCallLifecycle(ctx, callInfo, reason+"_grace_retry"); lifecycleErr != nil {
			h.logger.Warn("failed to sync group call lifecycle on retry",
				zap.String("call_id", callID.String()),
				zap.String("room_name", roomName),
				zap.String("reason", reason),
				zap.Error(lifecycleErr),
			)
		}
	}()
}

func (h *LiveKitHandler) broadcastGroupCallEnded(callInfo *domain.CallHistory, reason string) {
	if callInfo == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	participants, err := h.callRepo.GetParticipants(ctx, callInfo.CallID)
	if err != nil {
		h.logger.Warn("failed to load group call participants for end broadcast",
			zap.String("call_id", callInfo.CallID.String()),
			zap.String("room_name", callInfo.RoomID),
			zap.Error(err),
		)
		return
	}

	sentUsers := make(map[int64]struct{}, len(participants))
	for _, participant := range participants {
		if _, exists := sentUsers[participant.UserID]; exists {
			continue
		}
		sentUsers[participant.UserID] = struct{}{}

		h.notifService.SendCallEndedWS(participant.UserID, callInfo.CallID, callInfo.RoomID, reason)
		if err := h.notifService.SendCallSyncEvent(
			ctx,
			participant.UserID,
			callInfo.CallID,
			callInfo.RoomID,
			"call_ended",
			reason,
			callInfo.IsVideo,
			"",
			"",
		); err != nil {
			h.logger.Warn("failed to send group call_ended sync event",
				zap.String("call_id", callInfo.CallID.String()),
				zap.Int64("user_id", participant.UserID),
				zap.Error(err),
			)
		}
		if err := h.notifService.SendCallSyncEvent(
			ctx,
			participant.UserID,
			callInfo.CallID,
			callInfo.RoomID,
			"call_state_changed",
			domain.CallStatusEnded,
			callInfo.IsVideo,
			"",
			"",
		); err != nil {
			h.logger.Warn("failed to send group call_state_changed sync event",
				zap.String("call_id", callInfo.CallID.String()),
				zap.Int64("user_id", participant.UserID),
				zap.Error(err),
			)
		}
		if participant.Status == "joined" {
			if err := h.callRepo.UpdateParticipantStatus(ctx, callInfo.CallID, participant.UserID, "left"); err != nil {
				h.logger.Warn("failed to mark participant left while finalizing group call",
					zap.String("call_id", callInfo.CallID.String()),
					zap.Int64("user_id", participant.UserID),
					zap.Error(err),
				)
			}
		}
	}
}

func (h *LiveKitHandler) markPrivateCallConnecting(ctx context.Context, callInfo *domain.CallHistory, reason string) {
	if callInfo == nil || !domain.IsCallRingingStatus(callInfo.Status) {
		return
	}

	previousStatus, updated, err := h.callRepo.TransitionCallStatus(
		ctx,
		callInfo.CallID,
		domain.CallStatusConnecting,
		domain.CallStatusInitiated,
		domain.CallStatusRinging,
	)
	if err != nil {
		h.logger.Warn("Failed to update private call status to connecting",
			zap.Error(err),
			zap.String("call_id", callInfo.CallID.String()),
			zap.String("room", callInfo.RoomID),
			zap.String("reason", reason),
		)
		return
	}
	if updated {
		callInfo.Status = domain.CallStatusConnecting
		h.logger.Info("Private call status updated to connecting",
			zap.String("call_id", callInfo.CallID.String()),
			zap.String("room", callInfo.RoomID),
			zap.String("reason", reason),
			zap.String("previous_status", previousStatus),
		)
	}
}

func isLiveKitRoomNotFound(err error) bool {
	if err == nil {
		return false
	}

	msg := err.Error()
	return strings.Contains(msg, "not_found") || strings.Contains(msg, "not found")
}

func (h *LiveKitHandler) loadCallInfoForCleanup(ctx context.Context, callID string, roomName string) *domain.CallHistory {
	if h.loadPrivateCallInfo != nil {
		return h.loadPrivateCallInfo(ctx, callID, roomName)
	}
	if h.callRepo == nil {
		return nil
	}

	if uid, err := uuid.Parse(strings.TrimSpace(callID)); err == nil {
		callInfo, getErr := h.callRepo.GetByCallID(ctx, uid)
		if getErr == nil && callInfo != nil {
			return callInfo
		}
	}

	if strings.TrimSpace(roomName) == "" {
		return nil
	}
	callInfo, err := h.callRepo.GetByRoomID(ctx, roomName)
	if err != nil {
		return nil
	}
	return callInfo
}

func (h *LiveKitHandler) schedulePrivateCallCleanupRetry(callID string, roomName string, reason string, after time.Duration) {
	if after <= 0 {
		return
	}

	go func() {
		timer := time.NewTimer(after)
		defer timer.Stop()
		<-timer.C

		ctx, cancel := context.WithTimeout(context.Background(), privateCallTimeoutJobTimeout)
		defer cancel()
		h.cleanupPrivateCallRoom(ctx, callID, roomName, reason+"_grace_retry")
	}()
}

func (h *LiveKitHandler) stopPersonalRecordingsForCall(ctx context.Context, callInfo *domain.CallHistory, reason string) bool {
	if h.personalRecordingRepo == nil || h.service == nil || callInfo == nil {
		return true
	}

	recordings, err := h.personalRecordingRepo.ListLockedByCallID(ctx, callInfo.CallID)
	if err != nil {
		h.logger.Warn("failed to list personal recordings before cleanup",
			zap.String("call_id", callInfo.CallID.String()),
			zap.String("room_name", callInfo.RoomID),
			zap.String("reason", reason),
			zap.Error(err),
		)
		return false
	}
	if len(recordings) > 0 {
		h.logger.Info("call_end_waiting_for_recordings",
			zap.String("call_id", callInfo.CallID.String()),
			zap.String("room_name", callInfo.RoomID),
			zap.Int("recordings_count", len(recordings)),
		)
	}

	for _, rec := range recordings {
		markedStopping, markErr := h.personalRecordingRepo.MarkStoppingIfLive(ctx, rec.ID)
		if markErr != nil {
			h.logger.Warn("failed to mark personal recording stopping during cleanup",
				zap.String("call_id", rec.CallID.String()),
				zap.String("room_name", rec.RoomName),
				zap.Int64("user_id", rec.OwnerUserID),
				zap.String("recording_id", rec.ID.String()),
				zap.String("egress_id", rec.EgressID),
				zap.String("previous_status", string(rec.Status)),
				zap.String("reason", reason),
				zap.Error(markErr),
			)
			return false
		}
		if !markedStopping {
			continue
		}

		stopResult, stopErr := h.service.StopPersonalRecordingEgress(ctx, rec.EgressID)
		if stopErr != nil {
			errMsg := stopErr.Error()
			_ = h.personalRecordingRepo.UpdateResult(ctx, rec.ID, domain.PersonalRecordingStatusFailed, nil, nil, &errMsg, personalRecordingErrorCodePtr(errMsg), &errMsg, personalRecordingEndReason(errMsg), personalRecordingBoolPtr(false))
			h.logger.Warn("failed to stop personal recording during cleanup",
				zap.String("call_id", rec.CallID.String()),
				zap.String("room_name", rec.RoomName),
				zap.Int64("user_id", rec.OwnerUserID),
				zap.String("recording_id", rec.ID.String()),
				zap.String("egress_id", rec.EgressID),
				zap.String("previous_status", string(rec.Status)),
				zap.String("new_status", string(domain.PersonalRecordingStatusFailed)),
				zap.String("reason", reason),
				zap.Error(stopErr),
			)
			return false
		}

		newStatus := stopResult.FinalStatus
		if newStatus == "" {
			newStatus = domain.PersonalRecordingStatusStopping
		}
		failureReason := stopResult.LiveKitErrorMessage
		if failureReason == "" && newStatus != domain.PersonalRecordingStatusCompleted {
			failureReason = stopResult.Reason
		}
		minioObjectExists := (*bool)(nil)
		if newStatus == domain.PersonalRecordingStatusCompleted {
			minioObjectExists = personalRecordingBoolPtr(true)
		} else if newStatus.IsTerminal() {
			minioObjectExists = personalRecordingBoolPtr(false)
		}
		_ = h.personalRecordingRepo.UpdateResult(ctx, rec.ID, newStatus, stopResult.FilePath, stopResult.DurationSec, personalRecordingStringPtr(failureReason), personalRecordingIntPtr(stopResult.LiveKitErrorCode), personalRecordingStringPtr(stopResult.LiveKitErrorMessage), personalRecordingStringPtr(stopResult.EndReason), minioObjectExists)

		h.logger.Info("recording_finalized_before_room_delete",
			zap.String("call_id", rec.CallID.String()),
			zap.String("room_name", rec.RoomName),
			zap.Int64("user_id", rec.OwnerUserID),
			zap.String("recording_id", rec.ID.String()),
			zap.String("egress_id", rec.EgressID),
			zap.String("previous_status", string(rec.Status)),
			zap.String("new_status", string(newStatus)),
			zap.String("egress_status", stopResult.LiveKitStatus),
			zap.Int("livekit_error_code", stopResult.LiveKitErrorCode),
			zap.String("livekit_error_message", stopResult.LiveKitErrorMessage),
			zap.String("end_reason", stopResult.EndReason),
			zap.String("reason", reason),
		)
	}
	return true
}

func (h *LiveKitHandler) markDirectCallParticipantsLeft(ctx context.Context, callID uuid.UUID) {
	participants, err := h.callRepo.GetParticipants(ctx, callID)
	if err != nil {
		h.logger.Warn("failed to load call participants for cleanup",
			zap.String("call_id", callID.String()),
			zap.Error(err),
		)
		return
	}
	for _, participant := range participants {
		if err := h.callRepo.UpdateParticipantStatus(ctx, callID, participant.UserID, "left"); err != nil {
			h.logger.Warn("failed to mark call participant left",
				zap.String("call_id", callID.String()),
				zap.Int64("user_id", participant.UserID),
				zap.Error(err),
			)
		}
	}
}

func (h *LiveKitHandler) cleanupPrivateCallRoom(ctx context.Context, callID string, roomName string, reason string) {
	if strings.TrimSpace(roomName) == "" {
		h.logger.Warn("private call cleanup skipped: empty room name",
			zap.String("call_id", callID),
			zap.String("reason", reason),
		)
		return
	}
	if h.deletePrivateCallRoom == nil {
		h.logger.Warn("private call cleanup skipped: delete room handler is not configured",
			zap.String("call_id", callID),
			zap.String("room", roomName),
			zap.String("reason", reason),
		)
		return
	}

	callInfo := h.loadCallInfoForCleanup(ctx, callID, roomName)
	participantsCount := -1
	participantIDs := []string{}
	lastActivityAt := time.Time{}
	callStatus := ""
	callerID := int64(0)
	calleeID := int64(0)
	if callInfo != nil {
		callStatus = callInfo.Status
		callerID = callInfo.FromUserID
		calleeID = callInfo.ToUserID
		lastActivityAt = privateCallLastActivityAt(callInfo)
	}

	h.logger.Info("Attempting to delete LiveKit room",
		zap.String("call_id", callID),
		zap.String("room_name", roomName),
		zap.String("call_status", callStatus),
		zap.String("reason", reason),
		zap.Int64("caller_id", callerID),
		zap.Int64("callee_id", calleeID),
		zap.Int("participants_count", participantsCount),
		zap.Strings("participant_identities", participantIDs),
		zap.Bool("timeout_job", strings.Contains(reason, "timeout")),
		zap.Time("last_activity_at", lastActivityAt),
	)

	if h.listPrivateCallRoom != nil {
		participants, err := h.listPrivateCallRoom(ctx, roomName)
		if err != nil {
			if isLiveKitRoomNotFound(err) {
				h.logger.Info("private call room already deleted, cleanup skipped",
					zap.String("call_id", callID),
					zap.String("room_name", roomName),
					zap.String("reason", reason),
					zap.String("call_status", callStatus),
				)
				return
			}
			h.logger.Warn("private call cleanup skipped because participant listing failed",
				zap.String("call_id", callID),
				zap.String("room_name", roomName),
				zap.String("reason", reason),
				zap.String("call_status", callStatus),
				zap.Error(err),
			)
			return
		}

		participantsCount = len(participants)
		participantIDs = participantIdentities(participants)
		if participantsCount > 0 {
			h.logger.Info("Skip DeleteRoom because room has active participants",
				zap.String("call_id", callID),
				zap.String("room_name", roomName),
				zap.String("call_status", callStatus),
				zap.String("reason", reason),
				zap.Int("participants_count", participantsCount),
				zap.Strings("participant_identities", participantIDs),
				zap.Int64("caller_id", callerID),
				zap.Int64("callee_id", calleeID),
				zap.Time("last_activity_at", lastActivityAt),
			)
			if callInfo != nil {
				h.markPrivateCallConnecting(ctx, callInfo, "cleanup_skip_active_participants")
			}
			return
		}
	}

	now := time.Now()
	if h.now != nil {
		now = h.now()
	}
	gracePeriod := h.privateCallGrace
	if gracePeriod <= 0 {
		gracePeriod = privateCallCleanupGrace
	}
	if !lastActivityAt.IsZero() {
		if elapsed := now.Sub(lastActivityAt); elapsed < gracePeriod {
			retryAfter := gracePeriod - elapsed
			h.logger.Info("Skip DeleteRoom because grace period has not passed",
				zap.String("call_id", callID),
				zap.String("room_name", roomName),
				zap.String("call_status", callStatus),
				zap.String("reason", reason),
				zap.Int("participants_count", participantsCount),
				zap.Strings("participant_identities", participantIDs),
				zap.Int64("caller_id", callerID),
				zap.Int64("callee_id", calleeID),
				zap.Time("last_activity_at", lastActivityAt),
				zap.Duration("retry_after", retryAfter),
			)
			h.schedulePrivateCallCleanupRetry(callID, roomName, reason, retryAfter)
			return
		}
	}

	roomDeletedAfterRecording := true
	if callInfo != nil {
		recordingCtx, cancel := context.WithTimeout(ctx, personalRecordingCleanupTimeout)
		roomDeletedAfterRecording = h.stopPersonalRecordingsForCall(recordingCtx, callInfo, reason)
		cancel()
	}

	deleted, err := h.deletePrivateCallRoom(ctx, roomName)
	if err != nil {
		h.logger.Warn("private call cleanup failed",
			zap.String("call_id", callID),
			zap.String("room_name", roomName),
			zap.String("reason", reason),
			zap.String("call_status", callStatus),
			zap.Int("participants_count", participantsCount),
			zap.Strings("participant_identities", participantIDs),
			zap.Error(err),
		)
		return
	}
	if !deleted {
		h.logger.Info("private call room already deleted, cleanup skipped",
			zap.String("call_id", callID),
			zap.String("room_name", roomName),
			zap.String("reason", reason),
			zap.String("call_status", callStatus),
		)
		return
	}

	h.logger.Info("room_deleted_after_recordings_finalized",
		zap.String("call_id", callID),
		zap.String("room_name", roomName),
		zap.String("call_status", callStatus),
		zap.String("reason", reason),
		zap.Int("participants_count", participantsCount),
		zap.Strings("participant_identities", participantIDs),
		zap.Int64("caller_id", callerID),
		zap.Int64("callee_id", calleeID),
		zap.Time("last_activity_at", lastActivityAt),
		zap.Bool("room_deleted_after_recording", roomDeletedAfterRecording),
	)
}

func (h *LiveKitHandler) forceClosePrivateCallRoom(ctx context.Context, callID string, roomName string, reason string) {
	if strings.TrimSpace(roomName) == "" {
		h.logger.Warn("private call force cleanup skipped: empty room name",
			zap.String("call_id", callID),
			zap.String("reason", reason),
		)
		return
	}
	if h.deletePrivateCallRoom == nil {
		h.logger.Warn("private call force cleanup skipped: delete room handler is not configured",
			zap.String("call_id", callID),
			zap.String("room_name", roomName),
			zap.String("reason", reason),
		)
		return
	}
	if h.listPrivateCallRoom != nil {
		if participants, err := h.listPrivateCallRoom(ctx, roomName); err != nil {
			h.logger.Warn("private call force cleanup list participants failed",
				zap.String("call_id", callID),
				zap.String("room_name", roomName),
				zap.String("reason", reason),
				zap.Error(err),
			)
		} else {
			h.logger.Info("private call force cleanup participants snapshot",
				zap.String("call_id", callID),
				zap.String("room_name", roomName),
				zap.String("reason", reason),
				zap.Int("participants_count", len(participants)),
				zap.Strings("participant_identities", participantIdentities(participants)),
			)
		}
	}
	if deleted, err := h.deletePrivateCallRoom(ctx, roomName); err != nil {
		h.logger.Warn("private call force cleanup delete failed",
			zap.String("call_id", callID),
			zap.String("room_name", roomName),
			zap.String("reason", reason),
			zap.Error(err),
		)
	} else {
		h.logger.Info("private call force cleanup delete finished",
			zap.String("call_id", callID),
			zap.String("room_name", roomName),
			zap.String("reason", reason),
			zap.Bool("deleted", deleted),
		)
	}
}

// GetCallParticipants godoc
// @Summary Get call participants
// @Description returns list of participants for a given call
// @Tags calls
// @Security BearerAuth
// @Produce json
// @Param id path string true "Call ID"
// @Success 200 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /calls/{id}/participants [get]
func (h *LiveKitHandler) GetCallParticipants(c *gin.Context) {
	callIDStr := c.Param("id")

	var callUUID uuid.UUID
	var callInfo *domain.CallHistory

	// 1. Resolve Call Info (Support UUID or RoomName)
	parsedUUID, err := uuid.Parse(callIDStr)
	if err == nil {
		// Valid UUID
		callUUID = parsedUUID
		callInfo, err = h.callRepo.GetByCallID(c.Request.Context(), callUUID)
	} else {
		// Not a UUID -> Try as RoomName
		h.logger.Info("GetCallParticipants treating id as room_name", zap.String("id", callIDStr))
		callInfo, err = h.callRepo.GetByRoomID(c.Request.Context(), callIDStr)
		if callInfo != nil {
			callUUID = callInfo.CallID
		}
	}

	if err != nil {
		h.respondError(c, apperrors.InternalWithErr("failed to get call info", err))
		return
	}
	if callInfo == nil {
		h.respondError(c, apperrors.NotFound("call not found"))
		return
	}

	participants, err := h.callRepo.GetParticipants(c.Request.Context(), callUUID)
	if err != nil {
		h.respondError(c, apperrors.InternalWithErr("failed to get participants", err))
		return
	}

	if len(participants) < 2 && callInfo.RoomID != "" {
		if mainCall, err := h.callRepo.GetByRoomID(c.Request.Context(), callInfo.RoomID); err == nil && mainCall != nil {
			if mainCall.CallID != callUUID {
				h.logger.Info("Redirecting GetParticipants to main call ID", zap.String("original", callUUID.String()), zap.String("main", mainCall.CallID.String()))
				parts, err := h.callRepo.GetParticipants(c.Request.Context(), mainCall.CallID)
				if err == nil && len(parts) > len(participants) {
					participants = parts
					callInfo = mainCall
				}
			}
		}
	}

	authenticatedUserID := middleware.MustGetUserID(c)
	userID, _, resolveErr := h.resolveEffectiveCallUser(c.Request.Context(), authenticatedUserID)
	if resolveErr != nil {
		h.logger.Warn("failed to resolve canonical call user",
			zap.Int64("current_user_id", authenticatedUserID),
			zap.Error(resolveErr),
		)
		userID = authenticatedUserID
	}
	allowed, accessBypassed := h.ensureDirectCallAccessWithBypass(c, "calls.participants", callInfo, userID)
	if !allowed {
		return
	}

	isActive := false
	if callInfo.Status == "ongoing" || callInfo.Status == "initiated" {
		isActive = true
	}

	// SYNC: If call is active, verify with LiveKit real-time state
	// Webhooks might be delayed, so we trust LiveKit as source of truth for "who is currently online"
	if isActive && callInfo.RoomID != "" {
		liveParticipants, err := h.service.ListParticipants(c.Request.Context(), callInfo.RoomID)
		// If room is not found (err), it usually means everyone left or call ended.
		// If err is specific "room not found", we could assume everyone left, but let's be careful.

		if err == nil {
			// Map of currently connected user IDs according to LiveKit identities.
			onlineMap := make(map[int64]bool)
			for _, lp := range liveParticipants {
				if parsedIdentity, parseErr := service.ParseParticipantIdentity(lp.Identity); parseErr == nil {
					onlineMap[parsedIdentity.UserID] = true
				}
			}

			// Iterate over DB participants and fix status if they are missing from LiveKit
			for i := range participants {
				p := &participants[i]
				// If DB says joined, but LiveKit says NOT here -> They Left
				if p.Status == "joined" && !onlineMap[p.UserID] {
					h.logger.Info("Participant found in DB as joined but missing in LiveKit, marking as left",
						zap.Int64("user_id", p.UserID), zap.String("room", callInfo.RoomID))
					h.recordCallEvent(c, "call_participant_sync", p.UserID, callInfo.CallID.String(), callInfo.RoomID, "", "marked_left", nil)

					p.Status = "left"
					now := time.Now()
					p.LeftAt = &now

					// Lazy update DB to keep it consistent
					go func(cID uuid.UUID, uID int64) {
						// Create a detached context
						ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
						defer cancel()
						_ = h.callRepo.UpdateParticipantStatus(ctx, cID, uID, "left")
					}(callInfo.CallID, p.UserID)
				}
			}
		} else {
			// If ListParticipants failed (e.g. room empty/closed), but DB says "ongoing",
			// it might be that the call just ended.
			// We won't force-update everyone to 'left' here to avoid flickering if it's just a temporary network error,
			// but usually "Room not found" means it's over.
			h.logger.Warn("Failed to list real-time participants in GetCallParticipants", zap.Error(err), zap.String("call_id", callInfo.CallID.String()), zap.String("room", callInfo.RoomID))
		}
	}

	respondJSON(c, http.StatusOK, gin.H{
		"call_id":                    callInfo.CallID,
		"room_name":                  callInfo.RoomID,
		"status":                     callInfo.Status,
		"is_active":                  isActive,
		"started_at":                 callInfo.CreatedAt,
		"ended_at":                   callInfo.EndedAt,
		"participants":               participants,
		"access_bypassed":            accessBypassed,
		"access_validation_disabled": true,
	})
}
