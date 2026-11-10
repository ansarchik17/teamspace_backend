package service

import "testing"

func TestBuildAndParseParticipantIdentity(t *testing.T) {
	t.Parallel()

	identity := BuildParticipantIdentity(42, "web-session_01")
	if identity != "42:web-session_01" {
		t.Fatalf("unexpected identity: %s", identity)
	}

	parsed, err := ParseParticipantIdentity(identity)
	if err != nil {
		t.Fatalf("parse identity: %v", err)
	}

	if parsed.UserID != 42 {
		t.Fatalf("unexpected user id: %d", parsed.UserID)
	}
	if parsed.SessionID != "web-session_01" {
		t.Fatalf("unexpected session id: %s", parsed.SessionID)
	}
	if !IdentityBelongsToUser(identity, 42) {
		t.Fatalf("identity should belong to user 42")
	}
}

func TestBuildParticipantIdentityWithoutSession(t *testing.T) {
	t.Parallel()

	identity := BuildParticipantIdentity(7, "")
	if identity != "7" {
		t.Fatalf("unexpected identity without session: %s", identity)
	}

	parsed, err := ParseParticipantIdentity(identity)
	if err != nil {
		t.Fatalf("parse identity without session: %v", err)
	}

	if parsed.UserID != 7 {
		t.Fatalf("unexpected user id: %d", parsed.UserID)
	}
	if parsed.SessionID != "" {
		t.Fatalf("expected empty session id, got %q", parsed.SessionID)
	}
}

func TestBuildParticipantIdentitySanitizesSession(t *testing.T) {
	t.Parallel()

	identity := BuildParticipantIdentity(99, " mobile /tab#1 ")
	if identity != "99:mobiletab1" {
		t.Fatalf("unexpected sanitized identity: %s", identity)
	}
}

func TestParseParticipantIdentityWithGeneratedSuffix(t *testing.T) {
	t.Parallel()

	parsed, err := ParseParticipantIdentity("9:z5fd12d")
	if err != nil {
		t.Fatalf("parse identity with suffix: %v", err)
	}
	if parsed.UserID != 9 {
		t.Fatalf("unexpected user id: %d", parsed.UserID)
	}
	if parsed.SessionID != "z5fd12d" {
		t.Fatalf("unexpected session id: %s", parsed.SessionID)
	}
	if !IdentityBelongsToUser("9:z5fd12d", 9) {
		t.Fatal("identity should belong to user 9")
	}
}

func TestResolveParticipantIdentityScopePrefersDeviceID(t *testing.T) {
	t.Parallel()

	scope := ResolveParticipantIdentityScope(LiveKitTokenContext{
		DeviceID:  "0bf367ec-a5d4-4f3e-af01-a84c18ac6c2d",
		SessionID: "volatile-session",
	})
	if scope != "dev.0bf367ec-a5d4-4f3e-af01-a84c18ac6c2d" {
		t.Fatalf("unexpected scope: %s", scope)
	}

	identity := BuildParticipantIdentityForToken(86, LiveKitTokenContext{
		DeviceID:  "0bf367ec-a5d4-4f3e-af01-a84c18ac6c2d",
		SessionID: "volatile-session",
	})
	if identity != "86:dev.0bf367ec-a5d4-4f3e-af01-a84c18ac6c2d" {
		t.Fatalf("unexpected token identity: %s", identity)
	}
}

func TestResolveParticipantIdentityScopeFallsBackToSession(t *testing.T) {
	t.Parallel()

	scope := ResolveParticipantIdentityScope(LiveKitTokenContext{
		SessionID: " mobile /tab#1 ",
	})
	if scope != "mobiletab1" {
		t.Fatalf("unexpected scope: %s", scope)
	}
}
