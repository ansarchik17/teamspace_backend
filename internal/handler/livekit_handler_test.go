package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

func TestParseGroupIDFromRoomName(t *testing.T) {
	groupID := uuid.New()
	roomName := fmt.Sprintf("call_group_%s_%d", groupID.String(), time.Now().Unix())

	parsedID, ok := parseGroupIDFromRoomName(roomName)
	if !ok {
		t.Fatal("expected room name to be parsed as group call")
	}
	if parsedID != groupID {
		t.Fatalf("expected group id %s, got %s", groupID, parsedID)
	}
}

func TestParseGroupIDFromRoomNameRejectsInvalidValues(t *testing.T) {
	if _, ok := parseGroupIDFromRoomName("call_1_2_3"); ok {
		t.Fatal("direct call room must not parse as group room")
	}
	if _, ok := parseGroupIDFromRoomName("call_group_invalid"); ok {
		t.Fatal("invalid group room must not parse")
	}
}

func TestBuildCallActionSuccessResponseEnded(t *testing.T) {
	resp := buildCallActionSuccessResponse("call-1", "room-1", domain.CallStatusEnded, false, "Call ended", true)

	if got := resp["success"]; got != true {
		t.Fatalf("success = %#v, want true", got)
	}
	if got := resp["call_id"]; got != "call-1" {
		t.Fatalf("call_id = %#v, want call-1", got)
	}
	if got := resp["room_name"]; got != "room-1" {
		t.Fatalf("room_name = %#v, want room-1", got)
	}
	if got := resp["status"]; got != domain.CallStatusEnded {
		t.Fatalf("status = %#v, want %q", got, domain.CallStatusEnded)
	}
	if got := resp["call_status"]; got != domain.CallStatusEnded {
		t.Fatalf("call_status = %#v, want %q", got, domain.CallStatusEnded)
	}
	if got := resp["already_finalized"]; got != false {
		t.Fatalf("already_finalized = %#v, want false", got)
	}
	if got := resp["access_bypassed"]; got != true {
		t.Fatalf("access_bypassed = %#v, want true", got)
	}
	if got := resp["access_validation_disabled"]; got != true {
		t.Fatalf("access_validation_disabled = %#v, want true", got)
	}
}

func TestBuildCallActionSuccessResponseAlreadyFinalized(t *testing.T) {
	resp := buildCallActionSuccessResponse("call-1", "", domain.CallStatusCanceled, true, "Call already finalized", false)

	if got := resp["status"]; got != domain.CallStatusCancelled {
		t.Fatalf("status = %#v, want %q", got, domain.CallStatusCancelled)
	}
	if got := resp["call_status"]; got != domain.CallStatusCancelled {
		t.Fatalf("call_status = %#v, want %q", got, domain.CallStatusCancelled)
	}
	if got := resp["already_finalized"]; got != true {
		t.Fatalf("already_finalized = %#v, want true", got)
	}
	if got := resp["message"]; got != "Call already finalized" {
		t.Fatalf("message = %#v, want %q", got, "Call already finalized")
	}
	if got := resp["access_validation_disabled"]; got != true {
		t.Fatalf("access_validation_disabled = %#v, want true", got)
	}
}

func TestDecidePrivateCallTimeoutStatus(t *testing.T) {
	tests := []struct {
		name          string
		currentStatus string
		activeUsers   int
		wantStatus    string
		wantFinalize  bool
	}{
		{
			name:          "ringing without participants becomes no_answer",
			currentStatus: domain.CallStatusInitiated,
			activeUsers:   0,
			wantStatus:    domain.CallStatusNoAnswer,
			wantFinalize:  true,
		},
		{
			name:          "ringing with one participant becomes connecting",
			currentStatus: domain.CallStatusInitiated,
			activeUsers:   1,
			wantStatus:    domain.CallStatusConnecting,
			wantFinalize:  false,
		},
		{
			name:          "ringing with two participants becomes accepted",
			currentStatus: domain.CallStatusInitiated,
			activeUsers:   2,
			wantStatus:    domain.CallStatusAccepted,
			wantFinalize:  false,
		},
		{
			name:          "accepted call is ignored by timeout decision",
			currentStatus: domain.CallStatusAccepted,
			activeUsers:   1,
			wantStatus:    "",
			wantFinalize:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotStatus, gotFinalize := decidePrivateCallTimeoutStatus(tt.currentStatus, tt.activeUsers)
			if gotStatus != tt.wantStatus || gotFinalize != tt.wantFinalize {
				t.Fatalf("decidePrivateCallTimeoutStatus(%q, %d) = (%q, %v), want (%q, %v)",
					tt.currentStatus, tt.activeUsers, gotStatus, gotFinalize, tt.wantStatus, tt.wantFinalize)
			}
		})
	}
}

func TestLiveKitCallAccessBypassEnabled(t *testing.T) {
	t.Setenv("LIVEKIT_CALL_ACCESS_BYPASS", "")
	t.Setenv("CALL_ACCESS_BYPASS", "")
	if liveKitCallAccessBypassEnabled() {
		t.Fatal("expected bypass disabled by default")
	}

	t.Setenv("LIVEKIT_CALL_ACCESS_BYPASS", "true")
	if !liveKitCallAccessBypassEnabled() {
		t.Fatal("expected bypass enabled from LIVEKIT_CALL_ACCESS_BYPASS")
	}

	t.Setenv("LIVEKIT_CALL_ACCESS_BYPASS", "")
	t.Setenv("CALL_ACCESS_BYPASS", "1")
	if !liveKitCallAccessBypassEnabled() {
		t.Fatal("expected bypass enabled from CALL_ACCESS_BYPASS")
	}
}

func TestLiveKitCallLifecycleBypassEnabled(t *testing.T) {
	t.Setenv("LIVEKIT_CALL_LIFECYCLE_BYPASS", "")
	t.Setenv("CALL_LIFECYCLE_BYPASS", "")
	if liveKitCallLifecycleBypassEnabled() {
		t.Fatal("expected lifecycle bypass disabled by default")
	}

	t.Setenv("LIVEKIT_CALL_LIFECYCLE_BYPASS", "true")
	if !liveKitCallLifecycleBypassEnabled() {
		t.Fatal("expected lifecycle bypass enabled from LIVEKIT_CALL_LIFECYCLE_BYPASS")
	}

	t.Setenv("LIVEKIT_CALL_LIFECYCLE_BYPASS", "")
	t.Setenv("CALL_LIFECYCLE_BYPASS", "1")
	if !liveKitCallLifecycleBypassEnabled() {
		t.Fatal("expected lifecycle bypass enabled from CALL_LIFECYCLE_BYPASS")
	}
}

func TestEnsureDirectCallAccessBypassesMissingCall(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest("GET", "/api/v1/calls/join", nil)

	handler := &LiveKitHandler{
		logger:           zap.NewNop(),
		callAccessBypass: true,
	}

	ok, bypassed := handler.ensureDirectCallAccessWithBypass(ctx, "calls.join", nil, 60)
	if !ok {
		t.Fatal("expected access helper to bypass missing call")
	}
	if !bypassed {
		t.Fatal("expected bypass flag")
	}
}

func TestEnsureDirectCallAccessAllowsCanonicalMatch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest("GET", "/api/v1/calls/join", nil)

	handler := &LiveKitHandler{
		logger: zap.NewNop(),
		resolveCanonicalUserID: func(_ context.Context, userID int64) (int64, *domain.User, error) {
			if userID != 501 {
				t.Fatalf("unexpected user id %d", userID)
			}
			return 47, &domain.User{ID: 47}, nil
		},
	}

	callInfo := &domain.CallHistory{
		FromUserID: 9,
		ToUserID:   47,
	}

	if ok := handler.ensureDirectCallAccess(ctx, "calls.join", callInfo, 501); !ok {
		t.Fatal("expected access helper to allow canonical user match")
	}
}

func TestEnsureDirectCallAccessDeniesUnauthorizedUserWhenBypassDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest("GET", "/api/v1/calls/status", nil)

	handler := &LiveKitHandler{logger: zap.NewNop()}
	callInfo := &domain.CallHistory{
		FromUserID: 10,
		ToUserID:   11,
		RoomID:     "call_10_11_1",
		Status:     domain.CallStatusAccepted,
	}

	ok, disabled := handler.ensureDirectCallAccessWithBypass(ctx, "calls.status", callInfo, 999)
	if ok {
		t.Fatal("expected access helper to deny unauthorized user")
	}
	if disabled {
		t.Fatal("did not expect bypass flag when bypass disabled")
	}
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusForbidden)
	}
}

func TestCallActionSuccessNormalizesCancelled(t *testing.T) {
	resp := callActionSuccess("call-1", "room-1", domain.CallStatusCanceled, "Call closed", true, true)
	if got := resp["status"]; got != domain.CallStatusCancelled {
		t.Fatalf("status = %#v, want %q", got, domain.CallStatusCancelled)
	}
}

func TestLiveKitBypassWarning(t *testing.T) {
	callInfo := &domain.CallHistory{Status: domain.CallStatusEnded}
	if got := liveKitBypassWarning(callInfo, true); got == "" {
		t.Fatal("expected warning for terminal call in bypass mode")
	}
	if got := liveKitBypassWarning(callInfo, false); got != "" {
		t.Fatalf("expected empty warning when bypass disabled, got %q", got)
	}
}

func TestDecideDeclineAction(t *testing.T) {
	t.Parallel()

	handler := &LiveKitHandler{
		logger:              zap.NewNop(),
		callLifecycleBypass: true,
	}

	tests := []struct {
		name          string
		call          *domain.CallHistory
		currentUserID int64
		reason        string
		wantStatus    string
		wantMessage   string
		wantEventType string
		wantReason    string
		wantEffective string
	}{
		{
			name:          "callee decline on ringing becomes declined",
			call:          &domain.CallHistory{Status: domain.CallStatusRinging, FromUserID: 9, ToUserID: 10},
			currentUserID: 10,
			wantStatus:    domain.CallStatusDeclined,
			wantMessage:   "Call declined",
			wantEventType: "call_declined",
			wantReason:    "declined",
			wantEffective: "decline",
		},
		{
			name:          "caller decline on ringing becomes cancelled",
			call:          &domain.CallHistory{Status: domain.CallStatusRinging, FromUserID: 9, ToUserID: 10},
			currentUserID: 9,
			wantStatus:    domain.CallStatusCancelled,
			wantMessage:   "Call cancelled",
			wantEventType: "call_cancelled",
			wantReason:    "canceled",
			wantEffective: "cancel",
		},
		{
			name:          "accepted decline becomes ended",
			call:          &domain.CallHistory{Status: domain.CallStatusAccepted, FromUserID: 9, ToUserID: 10},
			currentUserID: 10,
			wantStatus:    domain.CallStatusEnded,
			wantMessage:   "Call ended",
			wantEventType: "call_ended",
			wantReason:    "ended",
			wantEffective: "end",
		},
		{
			name:          "ongoing decline becomes ended",
			call:          &domain.CallHistory{Status: domain.CallStatusOngoing, FromUserID: 9, ToUserID: 10},
			currentUserID: 10,
			wantStatus:    domain.CallStatusEnded,
			wantMessage:   "Call ended",
			wantEventType: "call_ended",
			wantReason:    "ended",
			wantEffective: "end",
		},
		{
			name:          "connecting decline becomes ended",
			call:          &domain.CallHistory{Status: domain.CallStatusConnecting, FromUserID: 9, ToUserID: 10},
			currentUserID: 10,
			wantStatus:    domain.CallStatusEnded,
			wantMessage:   "Call ended",
			wantEventType: "call_ended",
			wantReason:    "ended",
			wantEffective: "end",
		},
		{
			name:          "busy reason on ringing is preserved",
			call:          &domain.CallHistory{Status: domain.CallStatusRinging, FromUserID: 9, ToUserID: 10},
			currentUserID: 10,
			reason:        "busy",
			wantStatus:    domain.CallStatusBusy,
			wantMessage:   "Call declined as busy",
			wantEventType: "call_declined",
			wantReason:    "busy",
			wantEffective: "decline",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := handler.decideDeclineAction(tt.call, tt.currentUserID, tt.reason)
			if decision.nextStatus != tt.wantStatus {
				t.Fatalf("nextStatus = %q, want %q", decision.nextStatus, tt.wantStatus)
			}
			if decision.message != tt.wantMessage {
				t.Fatalf("message = %q, want %q", decision.message, tt.wantMessage)
			}
			if decision.eventType != tt.wantEventType {
				t.Fatalf("eventType = %q, want %q", decision.eventType, tt.wantEventType)
			}
			if decision.notificationReason != tt.wantReason {
				t.Fatalf("notificationReason = %q, want %q", decision.notificationReason, tt.wantReason)
			}
			if decision.effectiveAction != tt.wantEffective {
				t.Fatalf("effectiveAction = %q, want %q", decision.effectiveAction, tt.wantEffective)
			}
		})
	}
}

func TestIsStaleAcceptedDevice(t *testing.T) {
	t.Parallel()

	acceptedByUserID := int64(38)
	acceptedByDeviceID := "device-a"
	acceptedBySessionID := "session-a"
	callInfo := &domain.CallHistory{
		Status:              domain.CallStatusAccepted,
		AcceptedByUserID:    &acceptedByUserID,
		AcceptedByDeviceID:  &acceptedByDeviceID,
		AcceptedBySessionID: &acceptedBySessionID,
	}

	if !isStaleAcceptedDevice(callInfo, 38, "device-b", "session-b") {
		t.Fatal("expected another device to be treated as stale")
	}
	if isStaleAcceptedDevice(callInfo, 38, "device-a", "session-b") {
		t.Fatal("same device must not be treated as stale")
	}
	if isStaleAcceptedDevice(callInfo, 35, "device-b", "session-b") {
		t.Fatal("different user must not be treated as stale accepted device")
	}
}

func TestLocalDismissOnlyResponseIncludesAcceptedEndpoint(t *testing.T) {
	t.Parallel()

	acceptedByUserID := int64(38)
	acceptedByDeviceID := "device-a"
	acceptedBySessionID := "session-a"
	callInfo := &domain.CallHistory{
		AcceptedByUserID:    &acceptedByUserID,
		AcceptedByDeviceID:  &acceptedByDeviceID,
		AcceptedBySessionID: &acceptedBySessionID,
	}

	resp := localDismissOnlyResponse(
		"call-1",
		"room-1",
		domain.CallStatusAccepted,
		"Call already active on another device",
		"stale_device",
		true,
		callInfo,
	)

	if got := resp["local_dismiss_only"]; got != true {
		t.Fatalf("local_dismiss_only = %#v, want true", got)
	}
	if got := resp["accepted_device_id"]; got != "device-a" {
		t.Fatalf("accepted_device_id = %#v, want device-a", got)
	}
	if got := resp["accepted_session_id"]; got != "session-a" {
		t.Fatalf("accepted_session_id = %#v, want session-a", got)
	}
	if got := resp["reason"]; got != "stale_device" {
		t.Fatalf("reason = %#v, want stale_device", got)
	}
}
