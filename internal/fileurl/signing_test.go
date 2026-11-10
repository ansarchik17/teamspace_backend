package fileurl

import (
	"net/url"
	"testing"
)

func TestSignLegacyDownloadURLRefreshesExistingSignature(t *testing.T) {
	raw := "https://api.example.com/api/v1/files/download?object_name=voice%2F42%2Fclip.ogg&expires=1&signature=expired"

	got := SignLegacyDownloadURL(&raw)
	if got == nil {
		t.Fatal("expected signed URL")
	}
	parsed, err := url.Parse(*got)
	if err != nil {
		t.Fatalf("parse refreshed URL: %v", err)
	}
	query := parsed.Query()
	if query.Get("object_name") != "voice/42/clip.ogg" {
		t.Fatalf("unexpected object_name: %q", query.Get("object_name"))
	}
	if query.Get("expires") == "" || query.Get("expires") == "1" {
		t.Fatalf("expected refreshed expiry, got %q", query.Get("expires"))
	}
	if query.Get("signature") == "" || query.Get("signature") == "expired" {
		t.Fatalf("expected refreshed signature, got %q", query.Get("signature"))
	}
}

func TestSignLegacyDownloadURLLeavesExternalURLUntouched(t *testing.T) {
	raw := "https://cdn.example.com/files/clip.ogg?object_name=voice/42/clip.ogg"

	got := SignLegacyDownloadURL(&raw)
	if got == nil || *got != raw {
		t.Fatalf("expected external URL to remain unchanged, got %v", got)
	}
}
