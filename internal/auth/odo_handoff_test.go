package auth

import (
	"context"
	"errors"
	"testing"
)

func TestODOHandoffIsOpaqueAndSingleUse(t *testing.T) {
	store := NewODOHandoffStore(nil)
	code, err := store.Create(context.Background(), "sensitive-workspace-jwt")
	if err != nil {
		t.Fatal(err)
	}
	if code == "" || code == "sensitive-workspace-jwt" {
		t.Fatalf("handoff code is not opaque: %q", code)
	}

	token, err := store.Redeem(context.Background(), code)
	if err != nil {
		t.Fatal(err)
	}
	if token != "sensitive-workspace-jwt" {
		t.Fatalf("redeemed token = %q", token)
	}
	if _, err := store.Redeem(context.Background(), code); !errors.Is(err, ErrODOHandoffInvalid) {
		t.Fatalf("second redemption error = %v", err)
	}
}

func TestODOHandoffRejectsEmptyValues(t *testing.T) {
	store := NewODOHandoffStore(nil)
	if _, err := store.Create(context.Background(), " "); !errors.Is(err, ErrODOHandoffToken) {
		t.Fatalf("empty token error = %v", err)
	}
	if _, err := store.Redeem(context.Background(), " "); !errors.Is(err, ErrODOHandoffInvalid) {
		t.Fatalf("empty code error = %v", err)
	}
}
