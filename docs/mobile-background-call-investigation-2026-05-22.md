# Mobile Background Incoming Call Investigation

Date: 2026-05-22

## Scope

Investigated incoming call delivery for:
- Flutter mobile app
- Go backend
- Firebase Cloud Messaging
- LiveKit calls
- Android background call UI
- iOS CallKit / APNs path

## What Was Checked

### Flutter

- `FirebaseMessaging.onBackgroundMessage(...)` registration in `lib/main.dart`
- background handler implementation and payload parsing
- lifecycle handling via `WidgetsBindingObserver`
- logout and token deactivation paths
- CallKit / incoming call UI invocation through `flutter_callkit_incoming`
- FCM token registration and refresh flow
- call close events on multi-device flows

### Backend

- incoming call push generation
- token selection logic
- device token active filtering
- logout deactivation logic
- call state transitions for accept/decline/timeout
- WebSocket vs push split

## Findings

### 1. The issue is not logout when simply switching apps

Code inspection did not find any explicit logout on:
- `AppLifecycleState.paused`
- `AppLifecycleState.inactive`
- `AppLifecycleState.hidden`
- `AppLifecycleState.detached`

Current background lifecycle behavior in `MainNavigationPage` only schedules WebSocket disconnect after a delay. It does not intentionally clear auth state or deactivate the token.

### 2. Backend is sending the incoming call push successfully

Observed backend logs show:
- incoming call push batch completed for Android
- incoming call push batch completed for iOS
- push is selected by active device tokens, not by online status

Push selection is filtered by:
- `active = TRUE`
- `logged_out_at IS NULL`

There is no evidence that backend filters incoming call push by `online = true`.

### 3. Most likely mobile-side failure point was the Flutter background isolate path

Incoming call UI in background was being shown from the Firebase background handler in Dart.

That path depended on plugins such as:
- `flutter_callkit_incoming`
- `shared_preferences`

The background handler did not call:
- `WidgetsFlutterBinding.ensureInitialized()`
- `DartPluginRegistrant.ensureInitialized()`

Without plugin registration in the background isolate, incoming call UI can fail silently or throw plugin registration errors when the app is backgrounded.

This is the strongest common explanation for both Android and iOS failing after the app is simply switched to background.

### 4. WebSocket is not enough for background incoming calls

Foreground delivery can still work via WebSocket.

Background delivery must rely on push, because:
- WebSocket is disconnected after a delay in background
- even before disconnect, WebSocket delivery alone does not guarantee system call UI

### 5. iOS still has a structural limitation for killed-mode reliability

The app stores `voip_token`, but backend currently logs that direct APNs VoIP delivery is not configured and still uses FCM/APNS alert/background path.

That means:
- background may work better after the Flutter isolate fix
- killed-mode on iOS is still not guaranteed for real call UX without PushKit + APNs VoIP push

## Changes Applied

### Flutter

#### `Alem_SuperApp/lib/main.dart`

- strengthened Firebase background handler
- added:
  - `WidgetsFlutterBinding.ensureInitialized()`
  - `DartPluginRegistrant.ensureInitialized()`
  - explicit debug logs for:
    - background handler invocation
    - payload contents
    - parsed event
    - parsed call type
    - call UI invocation
    - terminal event cleanup
    - handler failures

#### `Alem_SuperApp/lib/features/navigation/presentation/pages/main_navigation_page.dart`

- added lifecycle debug logs
- explicitly documents in logs that background transition only schedules WebSocket disconnect and must not be treated as logout

### Backend

#### `alem-super-app-backend/internal/service/notification_service.go`

- added incoming-call push resolution logs:
  - device count
  - Android token count
  - iOS token count
  - fallback token count
  - VoIP token count
- added warning when no active tokens are resolved for an incoming call

## Files Previously Fixed In This Flow

These earlier changes remain part of the full working call flow:

### Flutter

- `lib/core/services/call_payload_utils.dart`
- `lib/core/services/push_notification_service.dart`
- `lib/features/auth/presentation/logic/auth_controller.dart`
- `lib/features/auth/data/datasources/auth_remote_datasource.dart`
- `lib/features/chat/presentation/pages/chat_detail_page_ws.part.dart`
- `lib/features/chat/presentation/pages/livekit_call_page.dart`
- `lib/features/chat/presentation/pages/video_call_page.dart`
- `.gitignore`

### Backend

- `internal/handler/auth_handler.go`
- `internal/handler/auth_handler_test.go`
- `internal/service/notification_service.go`

## Why The Previous Symptom Happened

When the app stayed open, the call could still appear because:
- WebSocket path was alive
- foreground FCM listeners were active

When the app moved to background, the system depended on the Firebase background isolate.

That isolate was not explicitly initializing Flutter/plugin infrastructure before trying to show the incoming call UI.

As a result, backend push could be accepted by Firebase, while the app still failed to show the call screen.

## What Still Needs Separate Verification

### Android

Verify on a real device:
- app in foreground
- app in background
- app swiped away / killed
- `POST_NOTIFICATIONS` granted
- full-screen call notification actually appears

### iOS

Verify on a real device:
- app in foreground
- app in background
- app killed

Important:
- background may improve from the Dart background-isolate fix
- killed-mode incoming calls on iOS still need proper PushKit + APNs VoIP implementation for production-grade reliability

## Graphify Note

Graphify CLI was not available in this environment.

Used:
- existing backend `graphify-out/graph.json`
- direct code inspection for Flutter and native mobile files

## Tests Performed

### Performed

- backend log analysis for real incoming call attempts
- code-path verification for:
  - lifecycle
  - logout
  - background messaging
  - device token filtering
  - incoming call push construction
  - multi-device call sync events

### Not Performed In This Environment

- `flutter analyze`
- `dart format`
- Android runtime validation
- iOS runtime validation
- Go tests

Go tests are currently blocked by local toolchain mismatch.

## Current Conclusion

Primary confirmed backend behavior:
- push is sent
- token filtering is correct
- logout is not the trigger in this scenario

Primary mobile-side cause:
- fragile or incomplete Flutter background-isolate setup for incoming call UI

Remaining platform-specific gap:
- iOS killed-mode still needs native PushKit / APNs VoIP support if full reliability is required
