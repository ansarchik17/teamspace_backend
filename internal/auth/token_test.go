package auth

import (
	"context"
	"testing"
	"time"

	"github.com/west6ide/workspace-backend/internal/config"
)

type linkedDeviceTokenStorageStub struct {
	tokens              map[string]int64
	linkedDeviceRevokes []string
	otherWebRevocations []string
}

func newLinkedDeviceTokenStorageStub() *linkedDeviceTokenStorageStub {
	return &linkedDeviceTokenStorageStub{tokens: make(map[string]int64)}
}

func (s *linkedDeviceTokenStorageStub) StoreToken(_ context.Context, tokenID string, userID int64, _ time.Time) error {
	s.tokens[tokenID] = userID
	return nil
}

func (s *linkedDeviceTokenStorageStub) IsTokenValid(_ context.Context, tokenID string) (bool, error) {
	_, ok := s.tokens[tokenID]
	return ok, nil
}

func (s *linkedDeviceTokenStorageStub) RevokeToken(_ context.Context, tokenID string) error {
	delete(s.tokens, tokenID)
	return nil
}

func (s *linkedDeviceTokenStorageStub) RevokeAllUserTokens(_ context.Context, userID int64) error {
	for tokenID, tokenUserID := range s.tokens {
		if tokenUserID == userID {
			delete(s.tokens, tokenID)
		}
	}
	return nil
}

func (s *linkedDeviceTokenStorageStub) StoreTokenContext(_ context.Context, _ string, _ int64, _ time.Time, _ *Context) error {
	return nil
}

func (s *linkedDeviceTokenStorageStub) RevokeTokensByLinkedDevice(_ context.Context, _ int64, deviceID string) error {
	s.linkedDeviceRevokes = append(s.linkedDeviceRevokes, deviceID)
	return nil
}

func (s *linkedDeviceTokenStorageStub) RevokeWebLinkedTokensExceptDevice(_ context.Context, _ int64, keepDeviceID string) error {
	s.otherWebRevocations = append(s.otherWebRevocations, keepDeviceID)
	return nil
}

func TestGenerateTokenPairPreservesLinkedDeviceContextAcrossRefresh(t *testing.T) {
	t.Parallel()

	tm := NewTokenManager(&config.JWTConfig{
		Secret:             "test-secret",
		Issuer:             "test-issuer",
		Audience:           "test-audience",
		AccessTokenExpiry:  time.Hour,
		RefreshTokenExpiry: 24 * time.Hour,
	}, nil)

	pair, err := tm.GenerateTokenPair(
		context.Background(),
		77,
		"123456789012",
		"user@example.com",
		"webuser",
		"Web",
		"User",
		false,
		map[string]bool{"web_linked_device": true},
		&Context{LinkedDeviceID: "web-device-77"},
	)
	if err != nil {
		t.Fatalf("generate token pair: %v", err)
	}

	accessClaims, err := tm.ExtractClaims(pair.AccessToken)
	if err != nil {
		t.Fatalf("extract access claims: %v", err)
	}
	if accessClaims.Context == nil || accessClaims.Context.LinkedDeviceID != "web-device-77" {
		t.Fatalf("access token lost linked device id: %#v", accessClaims.Context)
	}
	if accessClaims.IIN != "123456789012" {
		t.Fatalf("access token lost iin: %#v", accessClaims.IIN)
	}
	if !accessClaims.Context.Features["web_linked_device"] {
		t.Fatalf("access token lost web_linked_device feature")
	}

	refreshClaims, err := tm.ExtractClaims(pair.RefreshToken)
	if err != nil {
		t.Fatalf("extract refresh claims: %v", err)
	}
	if refreshClaims.Context == nil || refreshClaims.Context.LinkedDeviceID != "web-device-77" {
		t.Fatalf("refresh token lost linked device id: %#v", refreshClaims.Context)
	}
	if refreshClaims.IIN != "123456789012" {
		t.Fatalf("refresh token lost iin: %#v", refreshClaims.IIN)
	}
	if !refreshClaims.Context.Features["web_linked_device"] {
		t.Fatalf("refresh token lost web_linked_device feature")
	}

	refreshed, err := tm.RefreshAccessToken(context.Background(), pair.RefreshToken)
	if err != nil {
		t.Fatalf("refresh access token: %v", err)
	}

	refreshedClaims, err := tm.ExtractClaims(refreshed.AccessToken)
	if err != nil {
		t.Fatalf("extract refreshed access claims: %v", err)
	}
	if refreshedClaims.Context == nil || refreshedClaims.Context.LinkedDeviceID != "web-device-77" {
		t.Fatalf("refreshed access token lost linked device id: %#v", refreshedClaims.Context)
	}
	if refreshedClaims.IIN != "123456789012" {
		t.Fatalf("refreshed access token lost iin: %#v", refreshedClaims.IIN)
	}
	if !refreshedClaims.Context.Features["web_linked_device"] {
		t.Fatalf("refreshed access token lost web_linked_device feature")
	}
}

func TestTokenManagerCanRevokeSpecificLinkedDeviceScopes(t *testing.T) {
	t.Parallel()

	storage := newLinkedDeviceTokenStorageStub()
	tm := NewTokenManager(&config.JWTConfig{
		Secret:             "test-secret",
		Issuer:             "test-issuer",
		Audience:           "test-audience",
		AccessTokenExpiry:  time.Hour,
		RefreshTokenExpiry: 24 * time.Hour,
	}, storage)

	if err := tm.RevokeLinkedDeviceTokens(context.Background(), 7, "web-device-7"); err != nil {
		t.Fatalf("revoke linked device tokens: %v", err)
	}
	if err := tm.RevokeOtherWebLinkedTokens(context.Background(), 7, "web-device-7"); err != nil {
		t.Fatalf("revoke other web linked tokens: %v", err)
	}

	if len(storage.linkedDeviceRevokes) != 1 || storage.linkedDeviceRevokes[0] != "web-device-7" {
		t.Fatalf("unexpected linked device revoke calls: %#v", storage.linkedDeviceRevokes)
	}
	if len(storage.otherWebRevocations) != 1 || storage.otherWebRevocations[0] != "web-device-7" {
		t.Fatalf("unexpected other web revoke calls: %#v", storage.otherWebRevocations)
	}
}
