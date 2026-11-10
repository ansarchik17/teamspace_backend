package service

import (
	"testing"

	"github.com/west6ide/workspace-backend/internal/config"
	"go.uber.org/zap"
)

func TestResolveLiveKitAPIURLUsesOverride(t *testing.T) {
	cfg := config.LiveKitConfig{
		URL:    "ws://10.61.83.126:7880",
		APIURL: "http://100.72.193.179:7880",
	}

	if got := resolveLiveKitAPIURL(cfg); got != "http://100.72.193.179:7880" {
		t.Fatalf("resolveLiveKitAPIURL() = %q, want %q", got, "http://100.72.193.179:7880")
	}
}

func TestResolveLiveKitAPIURLFallsBackToClientURL(t *testing.T) {
	cfg := config.LiveKitConfig{
		URL: "ws://10.61.83.126:7880",
	}

	if got := resolveLiveKitAPIURL(cfg); got != "ws://10.61.83.126:7880" {
		t.Fatalf("resolveLiveKitAPIURL() = %q, want %q", got, "ws://10.61.83.126:7880")
	}
}

func TestNewLiveKitServiceSeparatesClientAndAPIURLs(t *testing.T) {
	cfg := config.LiveKitConfig{
		APIKey:    "test-key",
		APISecret: "test-secret",
		URL:       "ws://10.61.83.126:7880",
		APIURL:    "http://100.72.193.179:7880",
	}

	svc := NewLiveKitService(cfg, config.MinIOConfig{}, nil, nil, zap.NewNop())

	if got := svc.GetClientURL(); got != "ws://10.61.83.126:7880" {
		t.Fatalf("GetClientURL() = %q, want %q", got, "ws://10.61.83.126:7880")
	}
	if got := svc.GetAPIURL(); got != "http://100.72.193.179:7880" {
		t.Fatalf("GetAPIURL() = %q, want %q", got, "http://100.72.193.179:7880")
	}
}

func TestLiveKitServiceResolveClientURLFallsBackToDefault(t *testing.T) {
	cfg := config.LiveKitConfig{
		URL: "wss://etc.example/livekit",
	}

	svc := NewLiveKitService(cfg, config.MinIOConfig{}, nil, nil, zap.NewNop())

	if got := svc.ResolveClientURL(35, "askar", "askar@example.com"); got != "wss://etc.example/livekit" {
		t.Fatalf("ResolveClientURL() = %q, want %q", got, "wss://etc.example/livekit")
	}
}

func TestLiveKitServiceResolveClientURLUsesPublicRouteSelectors(t *testing.T) {
	cfg := config.LiveKitConfig{
		URL:         "wss://etc.example/livekit",
		PublicURL:   "wss://public.example/livekit",
		PublicUsers: []string{"35", "sultan.sultan", "tamerlan.kartayev@nitec.kz"},
	}

	svc := NewLiveKitService(cfg, config.MinIOConfig{}, nil, nil, zap.NewNop())

	if got := svc.ResolveClientURL(35, "", ""); got != "wss://public.example/livekit" {
		t.Fatalf("ResolveClientURL() by id = %q, want %q", got, "wss://public.example/livekit")
	}
	if got := svc.ResolveClientURL(1, "SULTAN.SULTAN", ""); got != "wss://public.example/livekit" {
		t.Fatalf("ResolveClientURL() by username = %q, want %q", got, "wss://public.example/livekit")
	}
	if got := svc.ResolveClientURL(2, "", "Tamerlan.Kartayev@nitec.kz"); got != "wss://public.example/livekit" {
		t.Fatalf("ResolveClientURL() by email = %q, want %q", got, "wss://public.example/livekit")
	}
	if got := svc.ResolveClientURL(99, "other", "other@example.com"); got != "wss://etc.example/livekit" {
		t.Fatalf("ResolveClientURL() non-match = %q, want %q", got, "wss://etc.example/livekit")
	}
}

func TestLiveKitServiceResolveClientURLFallsBackWhenPublicRouteIsInsecure(t *testing.T) {
	cfg := config.LiveKitConfig{
		URL:         "wss://etc.example/livekit",
		PublicURL:   "ws://92.38.49.156:7881",
		PublicUsers: []string{"35"},
	}

	svc := NewLiveKitService(cfg, config.MinIOConfig{}, nil, nil, zap.NewNop())

	if got := svc.ResolveClientURL(35, "", ""); got != "wss://etc.example/livekit" {
		t.Fatalf("ResolveClientURL() with insecure public URL = %q, want %q", got, "wss://etc.example/livekit")
	}
}
