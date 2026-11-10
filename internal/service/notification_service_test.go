package service

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	firebase "firebase.google.com/go/v4/messaging"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestNormalizeCallerDisplayName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		fallback string
		want     string
	}{
		{name: "trim explicit name", input: "  Alice  ", fallback: "Caller", want: "Alice"},
		{name: "use fallback", input: "   ", fallback: "Caller", want: "Caller"},
		{name: "default fallback", input: "", fallback: "", want: "Caller"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeCallerDisplayName(tt.input, tt.fallback); got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

func TestNormalizeDeviceType(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"iOS":      "ios",
		"IOS":      "ios",
		"iphone":   "ios",
		"ipad":     "ios",
		"apple":    "ios",
		"apns":     "ios",
		"android":  "android",
		"web":      "web",
		" custom ": "unknown",
	}

	for input, want := range cases {
		if got := normalizeDeviceType(input); got != want {
			t.Fatalf("normalizeDeviceType(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestBuildIncomingCallAPNSConfig(t *testing.T) {
	t.Parallel()

	expiresAt := time.Unix(1779274800, 0).UTC()
	cfg := buildIncomingCallAPNSConfig(
		expiresAt,
		"Incoming call",
		"Alice is calling",
		"INCOMING_CALL",
		map[string]interface{}{
			"call_id":     "call-1",
			"caller_name": "Alice",
		},
	)

	if cfg == nil || cfg.Payload == nil || cfg.Payload.Aps == nil {
		t.Fatal("expected APNS config with aps payload")
	}
	if cfg.Headers["apns-push-type"] != "alert" {
		t.Fatalf("apns-push-type = %q, want alert", cfg.Headers["apns-push-type"])
	}
	if cfg.Headers["apns-priority"] != "10" {
		t.Fatalf("apns-priority = %q, want 10", cfg.Headers["apns-priority"])
	}
	if cfg.Payload.Aps.Alert == nil || cfg.Payload.Aps.Alert.Title != "Incoming call" {
		t.Fatal("expected aps alert title to be set")
	}
	if !cfg.Payload.Aps.ContentAvailable {
		t.Fatal("expected content-available to be enabled")
	}
	if !cfg.Payload.Aps.MutableContent {
		t.Fatal("expected mutable-content to be enabled")
	}
	if cfg.Payload.Aps.Sound != "default" {
		t.Fatalf("sound = %q, want default", cfg.Payload.Aps.Sound)
	}
	if cfg.Payload.Aps.Category != "INCOMING_CALL" {
		t.Fatalf("category = %q, want INCOMING_CALL", cfg.Payload.Aps.Category)
	}
	if got := cfg.Payload.CustomData["call_id"]; got != "call-1" {
		t.Fatalf("custom call_id = %#v, want call-1", got)
	}
}

func TestBuildIncomingCallPayloadUsesUnifiedContract(t *testing.T) {
	t.Parallel()

	callID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	sentAt := time.Unix(1779444000, 0).UTC()

	payload := buildIncomingCallPayload(11111, "Alice", callID, "room-1", "chat-1", "wss://livekit", "preconnect-token", "high", true, sentAt, domain.CallTypePrivate)
	data := payload.toMap()
	pushData := payload.toStringMap()

	if got := data["type"]; got != "incoming_call" {
		t.Fatalf("type = %#v, want incoming_call", got)
	}
	if got := data["event"]; got != "call.incoming" {
		t.Fatalf("event = %#v, want call.incoming", got)
	}
	if got := data["notification_type"]; got != "incoming_call" {
		t.Fatalf("notification_type = %#v, want incoming_call", got)
	}
	if got := data["call_event"]; got != "incoming_call" {
		t.Fatalf("call_event = %#v, want incoming_call", got)
	}
	if got := data["is_video"]; got != true {
		t.Fatalf("is_video = %#v, want true", got)
	}
	if got := data["sent_at_unix"]; got != int64(1779444000) {
		t.Fatalf("sent_at_unix = %#v, want 1779444000", got)
	}
	if got := pushData["type"]; got != "incoming_call" {
		t.Fatalf("pushData[type] = %q, want incoming_call", got)
	}
	if got := pushData["livekit_token"]; got != "preconnect-token" {
		t.Fatalf("pushData[livekit_token] = %q, want preconnect-token", got)
	}
	if got := pushData["is_video"]; got != "true" {
		t.Fatalf("pushData[is_video] = %q, want true", got)
	}
}

func TestBuildCallSyncPayloadUsesReason(t *testing.T) {
	t.Parallel()

	callID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	sentAt := time.Unix(1779444000, 0).UTC()

	payload := buildCallSyncPayload("call_declined", callID, "room-1", "declined_on_another_device", 22345, false, sentAt, nil, "")
	data := payload.toMap()
	pushData := payload.toStringMap()

	if got := data["type"]; got != "call_sync" {
		t.Fatalf("type = %#v, want call_sync", got)
	}
	if got := data["event"]; got != "call_declined" {
		t.Fatalf("event = %#v, want call_declined", got)
	}
	if got := data["reason"]; got != "declined_on_another_device" {
		t.Fatalf("reason = %#v, want declined_on_another_device", got)
	}
	if got := data["receiver_id"]; got != int64(22345) {
		t.Fatalf("receiver_id = %#v, want 22345", got)
	}
	if got := data["is_video"]; got != false {
		t.Fatalf("is_video = %#v, want false", got)
	}
	if got := data["sent_at_unix"]; got != int64(1779444000) {
		t.Fatalf("sent_at_unix = %#v, want 1779444000", got)
	}
	if got := pushData["type"]; got != "call_sync" {
		t.Fatalf("pushData[type] = %q, want call_sync", got)
	}
	if got := pushData["receiver_id"]; got != "22345" {
		t.Fatalf("pushData[receiver_id] = %q, want 22345", got)
	}
}

func TestBuildCallSyncPayloadIncludesCallMetadata(t *testing.T) {
	t.Parallel()

	callID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	acceptedByUserID := int64(77)
	acceptedByDeviceID := "device-77"
	acceptedBySessionID := "session-77"
	createdAt := time.Unix(1779443900, 0).UTC()
	callInfo := &domain.CallHistory{
		CallID:              callID,
		FromUserID:          10,
		ToUserID:            0,
		RoomID:              "call_group_room",
		Status:              "accepted",
		CreatedAt:           createdAt,
		AcceptedByUserID:    &acceptedByUserID,
		AcceptedByDeviceID:  &acceptedByDeviceID,
		AcceptedBySessionID: &acceptedBySessionID,
	}

	payload := buildCallSyncPayload(
		"call_answered_elsewhere",
		callID,
		"call_group_room",
		"accepted_on_another_device",
		22345,
		true,
		createdAt.Add(5*time.Second),
		callInfo,
		acceptedByDeviceID,
	)
	data := payload.toMap()

	if got := data["call_type"]; got != "group" {
		t.Fatalf("call_type = %#v, want group", got)
	}
	if got := data["caller_id"]; got != int64(10) {
		t.Fatalf("caller_id = %#v, want 10", got)
	}
	if got := data["target_user_id"]; got != int64(0) {
		t.Fatalf("target_user_id = %#v, want 0", got)
	}
	if got := data["accepted_by_user_id"]; got != int64(77) {
		t.Fatalf("accepted_by_user_id = %#v, want 77", got)
	}
	if got := data["accepted_by_device_id"]; got != "device-77" {
		t.Fatalf("accepted_by_device_id = %#v, want device-77", got)
	}
	if got := data["accepted_by_session_id"]; got != "session-77" {
		t.Fatalf("accepted_by_session_id = %#v, want session-77", got)
	}
	if got := data["active_device_id"]; got != "device-77" {
		t.Fatalf("active_device_id = %#v, want device-77", got)
	}
	if got := data["status"]; got != "accepted" {
		t.Fatalf("status = %#v, want accepted", got)
	}
}

type fakeDeviceRepo struct {
	mu                sync.Mutex
	devicesByUser     map[int64][]domain.UserDevice
	savedDevices      []*domain.UserDevice
	clearVoIPCalls    []fakeClearVoIPCall
	getUserTokensHits int
}

type fakeClearVoIPCall struct {
	userID    int64
	deviceID  string
	voipToken string
}

func (f *fakeDeviceRepo) SaveDevice(_ context.Context, device *domain.UserDevice) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	copyValue := *device
	f.savedDevices = append(f.savedDevices, &copyValue)
	return nil
}

func (f *fakeDeviceRepo) GetUserDevices(_ context.Context, userID int64) ([]domain.UserDevice, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	devices := f.devicesByUser[userID]
	out := make([]domain.UserDevice, len(devices))
	copy(out, devices)
	return out, nil
}

func (f *fakeDeviceRepo) GetUserTokens(_ context.Context, userID int64) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getUserTokensHits++
	devices := f.devicesByUser[userID]
	tokens := make([]string, 0, len(devices))
	for _, device := range devices {
		if device.FCMToken != "" {
			tokens = append(tokens, device.FCMToken)
		}
	}
	return tokens, nil
}

func (f *fakeDeviceRepo) DeactivateToken(context.Context, int64, string) error  { return nil }
func (f *fakeDeviceRepo) DeactivateTokenByValue(context.Context, string) error  { return nil }
func (f *fakeDeviceRepo) DeactivateDevice(context.Context, int64, string) error { return nil }
func (f *fakeDeviceRepo) DeactivateAllUserDevices(context.Context, int64) error { return nil }

func (f *fakeDeviceRepo) ClearVoIPToken(_ context.Context, userID int64, deviceID string, voipToken string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clearVoIPCalls = append(f.clearVoIPCalls, fakeClearVoIPCall{
		userID:    userID,
		deviceID:  deviceID,
		voipToken: voipToken,
	})
	return nil
}

func (f *fakeDeviceRepo) ClearVoIPCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.clearVoIPCalls)
}

func (f *fakeDeviceRepo) FirstClearVoIPCall() *fakeClearVoIPCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.clearVoIPCalls) == 0 {
		return nil
	}
	call := f.clearVoIPCalls[0]
	return &call
}

func (f *fakeDeviceRepo) UserTokensHits() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.getUserTokensHits
}

func (f *fakeDeviceRepo) SavedDevices() []*domain.UserDevice {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*domain.UserDevice, len(f.savedDevices))
	copy(out, f.savedDevices)
	return out
}

type fakeFCMClient struct {
	mu       sync.Mutex
	messages []*firebase.MulticastMessage
	ch       chan struct{}
}

func (f *fakeFCMClient) SendEachForMulticast(_ context.Context, message *firebase.MulticastMessage) (*firebase.BatchResponse, error) {
	f.mu.Lock()
	f.messages = append(f.messages, message)
	f.mu.Unlock()
	if f.ch != nil {
		select {
		case f.ch <- struct{}{}:
		default:
		}
	}
	responses := make([]*firebase.SendResponse, len(message.Tokens))
	for i := range responses {
		responses[i] = &firebase.SendResponse{Success: true, MessageID: "msg"}
	}
	return &firebase.BatchResponse{
		SuccessCount: len(message.Tokens),
		Responses:    responses,
	}, nil
}

func (f *fakeFCMClient) MessageCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.messages)
}

func (f *fakeFCMClient) FirstMessage() *firebase.MulticastMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.messages) == 0 {
		return nil
	}
	return f.messages[0]
}

type fakeVoIPSender struct {
	mu      sync.Mutex
	calls   []fakeVoIPCall
	sendErr error
	ch      chan struct{}
}

type fakeVoIPCall struct {
	token   string
	payload apnsVoIPPayload
}

func (f *fakeVoIPSender) sendVoIP(_ context.Context, deviceToken string, p apnsVoIPPayload, _ time.Time, _ *zap.Logger) error {
	f.mu.Lock()
	f.calls = append(f.calls, fakeVoIPCall{token: deviceToken, payload: p})
	f.mu.Unlock()
	if f.ch != nil {
		select {
		case f.ch <- struct{}{}:
		default:
		}
	}
	return f.sendErr
}

func (f *fakeVoIPSender) CallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeVoIPSender) FirstCall() *fakeVoIPCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		return nil
	}
	call := f.calls[0]
	return &call
}

type fakeLiveKitTokenIssuer struct{}

func (f *fakeLiveKitTokenIssuer) GenerateToken(roomName string, userID int64, username string, tokenCtx LiveKitTokenContext) (string, error) {
	return fmt.Sprintf("lk:%s:%d:%s", roomName, userID, tokenCtx.SessionID), nil
}

func TestSendIncomingCallPushUsesVoIPWithoutFCMClient(t *testing.T) {
	t.Parallel()

	voipToken := "voip-token-1"
	repo := &fakeDeviceRepo{
		devicesByUser: map[int64][]domain.UserDevice{
			77: {
				{
					UserID:     77,
					Platform:   "ios",
					DeviceType: "ios",
					DeviceID:   "ios-device-1",
					VoIPToken:  &voipToken,
				},
			},
		},
	}
	voip := &fakeVoIPSender{}
	service := &NotificationService{
		deviceRepo:  repo,
		apnsVoip:    voip,
		tokenIssuer: &fakeLiveKitTokenIssuer{},
		logger:      zap.NewNop(),
		livekitURL:  "wss://livekit",
	}

	err := service.SendIncomingCallPush(context.Background(), 77, 11, "Alice", uuid.New(), "room-1", "chat-1", "high", true)
	if err != nil {
		t.Fatalf("SendIncomingCallPush returned error: %v", err)
	}

	if voip.CallCount() != 1 {
		t.Fatalf("voip send count = %d, want 1", voip.CallCount())
	}
	firstVoIP := voip.FirstCall()
	if firstVoIP == nil {
		t.Fatal("expected voip call")
	}
	if got := firstVoIP.payload.RoomID; got != "room-1" {
		t.Fatalf("payload.RoomID = %q, want room-1", got)
	}
	if got := firstVoIP.payload.LivekitToken; got == "" {
		t.Fatal("expected LivekitToken in VoIP payload")
	}
}

func TestSendIncomingCallPushClearsExpiredVoIPToken(t *testing.T) {
	t.Parallel()

	voipToken := "voip-token-expired"
	repo := &fakeDeviceRepo{
		devicesByUser: map[int64][]domain.UserDevice{
			88: {
				{
					UserID:     88,
					Platform:   "ios",
					DeviceType: "ios",
					DeviceID:   "ios-device-expired",
					VoIPToken:  &voipToken,
				},
			},
		},
	}
	voip := &fakeVoIPSender{sendErr: ErrAPNSVoIPTokenUnregistered}
	service := &NotificationService{
		deviceRepo: repo,
		apnsVoip:   voip,
		logger:     zap.NewNop(),
	}

	err := service.SendIncomingCallPush(context.Background(), 88, 11, "Alice", uuid.New(), "room-1", "chat-1", "high", false)
	if err != nil {
		t.Fatalf("SendIncomingCallPush returned error: %v", err)
	}
	if repo.ClearVoIPCallCount() != 1 {
		t.Fatalf("clearVoIPCalls = %d, want 1", repo.ClearVoIPCallCount())
	}
	clearCall := repo.FirstClearVoIPCall()
	if clearCall == nil {
		t.Fatal("expected clear voip cleanup")
	}
	if got := clearCall.deviceID; got != "ios-device-expired" {
		t.Fatalf("cleanup deviceID = %q, want ios-device-expired", got)
	}
}

func TestSendIncomingCallPushKeepsAndroidFlow(t *testing.T) {
	t.Parallel()

	repo := &fakeDeviceRepo{
		devicesByUser: map[int64][]domain.UserDevice{
			99: {
				{
					UserID:     99,
					Platform:   "android",
					DeviceType: "android",
					DeviceID:   "android-1",
					FCMToken:   "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				},
			},
		},
	}
	fcm := &fakeFCMClient{}
	service := &NotificationService{
		deviceRepo:  repo,
		fcmClient:   fcm,
		tokenIssuer: &fakeLiveKitTokenIssuer{},
		logger:      zap.NewNop(),
		livekitURL:  "wss://livekit",
	}

	err := service.SendIncomingCallPush(context.Background(), 99, 11, "Alice", uuid.New(), "room-1", "chat-1", "high", true)
	if err != nil {
		t.Fatalf("SendIncomingCallPush returned error: %v", err)
	}
	if fcm.MessageCount() != 1 {
		t.Fatalf("fcm messages = %d, want 1", fcm.MessageCount())
	}
	firstFCM := fcm.FirstMessage()
	if firstFCM == nil {
		t.Fatal("expected fcm message")
	}
	if len(firstFCM.Tokens) != 1 || firstFCM.Tokens[0] == "" {
		t.Fatal("expected Android FCM token to be used")
	}
	if got := firstFCM.Data["livekit_token"]; got == "" {
		t.Fatal("expected livekit_token in Android FCM data")
	}
}

func TestSendGroupCallPushUsesStableCallIDAndVoIPTargets(t *testing.T) {
	t.Parallel()

	voipToken := "voip-group-1"
	repo := &fakeDeviceRepo{
		devicesByUser: map[int64][]domain.UserDevice{
			201: {
				{
					UserID:     201,
					Platform:   "ios",
					DeviceType: "ios",
					DeviceID:   "ios-group-1",
					VoIPToken:  &voipToken,
				},
			},
			202: {
				{
					UserID:     202,
					Platform:   "android",
					DeviceType: "android",
					DeviceID:   "android-group-1",
					FCMToken:   "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
				},
			},
		},
	}
	fcm := &fakeFCMClient{ch: make(chan struct{}, 1)}
	voip := &fakeVoIPSender{ch: make(chan struct{}, 1)}
	service := &NotificationService{
		deviceRepo: repo,
		fcmClient:  fcm,
		apnsVoip:   voip,
		logger:     zap.NewNop(),
		livekitURL: "wss://livekit",
	}

	stableCallID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440111")
	err := service.SendGroupCallPush(context.Background(), 200, "Alice", "group-1", "Group", stableCallID, "room-1", []int64{201, 202})
	if err != nil {
		t.Fatalf("SendGroupCallPush returned error: %v", err)
	}

	if repo.UserTokensHits() != 0 {
		t.Fatalf("GetUserTokens was used, want 0 calls")
	}
	select {
	case <-voip.ch:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected voip call")
	}
	select {
	case <-fcm.ch:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected FCM group call message")
	}
	if voip.CallCount() != 1 {
		t.Fatalf("voip calls = %d, want 1", voip.CallCount())
	}
	firstVoIP := voip.FirstCall()
	firstFCM := fcm.FirstMessage()
	if firstVoIP == nil || firstFCM == nil {
		t.Fatal("expected group call fanout")
	}
	voipCallID := firstVoIP.payload.CallID
	fcmCallID := firstFCM.Data["call_id"]
	if voipCallID == "" || fcmCallID == "" || voipCallID != fcmCallID || voipCallID != stableCallID.String() {
		t.Fatalf("group callID mismatch: voip=%q fcm=%q", voipCallID, fcmCallID)
	}
}

func TestRegisterDeviceWarnsOnUnknownDeviceType(t *testing.T) {
	t.Parallel()

	core, logs := observer.New(zap.WarnLevel)
	repo := &fakeDeviceRepo{}
	service := &NotificationService{
		deviceRepo: repo,
		logger:     zap.New(core),
	}

	err := service.RegisterDevice(context.Background(), 1, "cccccccccccccccccccccccccccccccccccccccccccccccccc", "flutter", "dev-1", "")
	if err != nil {
		t.Fatalf("RegisterDevice returned error: %v", err)
	}
	savedDevices := repo.SavedDevices()
	if len(savedDevices) != 1 {
		t.Fatalf("saved devices = %d, want 1", len(savedDevices))
	}
	if got := savedDevices[0].Platform; got != devicePlatformUnknown {
		t.Fatalf("saved platform = %q, want %q", got, devicePlatformUnknown)
	}
	if logs.Len() == 0 {
		t.Fatal("expected warning log for unknown device_type")
	}
}

func TestRegisterDeviceAllowsIOSVoIPOnlyDevice(t *testing.T) {
	t.Parallel()

	repo := &fakeDeviceRepo{}
	service := &NotificationService{
		deviceRepo: repo,
		logger:     zap.NewNop(),
	}

	err := service.RegisterDevice(context.Background(), 5, "", "ios", "ios-voip-only-1", "voip-only-token")
	if err != nil {
		t.Fatalf("RegisterDevice returned error: %v", err)
	}
	savedDevices := repo.SavedDevices()
	if len(savedDevices) != 1 {
		t.Fatalf("saved devices = %d, want 1", len(savedDevices))
	}
	if savedDevices[0].FCMToken != "" {
		t.Fatalf("expected empty FCM token, got %q", savedDevices[0].FCMToken)
	}
	if savedDevices[0].VoIPToken == nil || *savedDevices[0].VoIPToken != "voip-only-token" {
		t.Fatal("expected voip-only token to be saved")
	}
}
