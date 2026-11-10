package handler

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/auth"
	ws "github.com/west6ide/workspace-backend/internal/websocket"
)

func TestBuildRealtimeSessionKeyPrefersStableHeaderOverToken(t *testing.T) {
	gin.SetMode(gin.TestMode)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest("GET", "/ws?token=token-from-query", nil)
	req.Header.Set("X-Device-ID", "mobile-device-1")
	ctx.Request = req
	ctx.Set("claims", &auth.Claims{
		Context: &auth.Context{
			LinkedDeviceID: "linked-web-device",
		},
	})

	got := buildRealtimeSessionKey(ctx)
	want := "X-Device-ID:mobile-device-1"

	if got != buildHashedSessionKey(want) {
		t.Fatalf("expected stable header to win over token/claims")
	}
}

func TestBuildRealtimeSessionKeyFallsBackToToken(t *testing.T) {
	gin.SetMode(gin.TestMode)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest("GET", "/ws?token=token-from-query", nil)

	got := buildRealtimeSessionKey(ctx)
	want := "token:token-from-query"

	if got != buildHashedSessionKey(want) {
		t.Fatalf("expected token fallback when stable identifiers are absent")
	}
}

func TestBuildRealtimeSessionKeyFallsBackToAccessTokenQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest("GET", "/ws?access_token=token-from-access-query", nil)

	got := buildRealtimeSessionKey(ctx)
	want := "token:token-from-access-query"

	if got != buildHashedSessionKey(want) {
		t.Fatalf("expected access_token query fallback when stable identifiers are absent")
	}
}

func TestCurrentLinkedDeviceIDPrefersClaimsContext(t *testing.T) {
	gin.SetMode(gin.TestMode)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest("GET", "/messages?web_device_id=web-from-query", nil)
	req.Header.Set("X-Web-Device-ID", "web-from-header")
	ctx.Request = req
	ctx.Set("claims", &auth.Claims{
		Context: &auth.Context{
			LinkedDeviceID: "linked-device-from-claims",
		},
	})

	got := currentLinkedDeviceID(ctx)
	if got != "linked-device-from-claims" {
		t.Fatalf("expected claims linked device id to win, got %q", got)
	}
}

func TestCurrentLinkedDeviceIDFallsBackToHeadersAndQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest("GET", "/messages?device_id=mobile-from-query", nil)
	ctx.Request = req

	got := currentLinkedDeviceID(ctx)
	if got != "mobile-from-query" {
		t.Fatalf("expected query device id fallback, got %q", got)
	}
}

func buildHashedSessionKey(source string) string {
	return ws.BuildSessionKey(source)
}
