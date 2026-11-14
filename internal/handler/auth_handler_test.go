package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/auth"
	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/keycloak"
	"go.uber.org/zap"
)

type memoryTokenStorage struct {
	tokens              map[string]int64
	linkedDeviceRevokes []string
	otherWebRevocations []string
}

func newMemoryTokenStorage() *memoryTokenStorage {
	return &memoryTokenStorage{
		tokens: make(map[string]int64),
	}
}

func (s *memoryTokenStorage) StoreToken(_ context.Context, tokenID string, userID int64, _ time.Time) error {
	s.tokens[tokenID] = userID
	return nil
}

func (s *memoryTokenStorage) IsTokenValid(_ context.Context, tokenID string) (bool, error) {
	_, exists := s.tokens[tokenID]
	return exists, nil
}

func (s *memoryTokenStorage) RevokeToken(_ context.Context, tokenID string) error {
	delete(s.tokens, tokenID)
	return nil
}

func (s *memoryTokenStorage) RevokeAllUserTokens(_ context.Context, userID int64) error {
	for tokenID, tokenUserID := range s.tokens {
		if tokenUserID == userID {
			delete(s.tokens, tokenID)
		}
	}
	return nil
}

func (s *memoryTokenStorage) StoreTokenContext(_ context.Context, _ string, _ int64, _ time.Time, _ *auth.Context) error {
	return nil
}

func (s *memoryTokenStorage) RevokeTokensByLinkedDevice(_ context.Context, userID int64, deviceID string) error {
	s.linkedDeviceRevokes = append(s.linkedDeviceRevokes, deviceID)
	for tokenID, tokenUserID := range s.tokens {
		if tokenUserID == userID {
			delete(s.tokens, tokenID)
		}
	}
	return nil
}

func (s *memoryTokenStorage) RevokeWebLinkedTokensExceptDevice(_ context.Context, _ int64, keepDeviceID string) error {
	s.otherWebRevocations = append(s.otherWebRevocations, keepDeviceID)
	return nil
}

type linkedDeviceRevokerMock struct {
	userID   int64
	deviceID string
	called   bool
}

func (m *linkedDeviceRevokerMock) RevokeLinkedDevice(_ context.Context, userID int64, deviceID string) error {
	m.called = true
	m.userID = userID
	m.deviceID = deviceID
	return nil
}

type pushDeviceManagerMock struct {
	deactivatedUserID int64
	deactivatedDevice string
	deactivateCalls   int
}

func (m *pushDeviceManagerMock) DeactivateDevice(_ context.Context, userID int64, deviceID string) error {
	m.deactivatedUserID = userID
	m.deactivatedDevice = deviceID
	m.deactivateCalls++
	return nil
}

func (m *pushDeviceManagerMock) DeactivateToken(_ context.Context, userID int64, token string) error {
	m.deactivatedUserID = userID
	m.deactivatedDevice = token
	m.deactivateCalls++
	return nil
}

func (m *pushDeviceManagerMock) DeactivateAllDevices(_ context.Context, _ int64) error {
	return nil
}

func TestAuthHandlerLogoutRevokesLinkedWebDevice(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)

	storage := newMemoryTokenStorage()
	tokenManager := auth.NewTokenManager(&config.JWTConfig{
		Secret:             "test-secret",
		Issuer:             "test-issuer",
		Audience:           "test-audience",
		AccessTokenExpiry:  time.Hour,
		RefreshTokenExpiry: 24 * time.Hour,
	}, storage)

	tokens, err := tokenManager.GenerateTokenPair(
		context.Background(),
		11,
		"123456789012",
		"user@example.com",
		"webuser",
		"Web",
		"User",
		false,
		map[string]bool{"web_linked_device": true},
		&auth.Context{LinkedDeviceID: "web-device-11"},
	)
	if err != nil {
		t.Fatalf("generate token pair: %v", err)
	}

	revoker := &linkedDeviceRevokerMock{}
	handler := NewAuthHandler(nil, nil, revoker, zap.NewNop(), tokenManager)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: tokens.AccessToken})
	req.AddCookie(&http.Cookie{Name: "refresh_token", Value: tokens.RefreshToken})
	ctx.Request = req

	handler.Logout(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}
	if !revoker.called {
		t.Fatalf("expected linked device revoker to be called")
	}
	if revoker.userID != 11 {
		t.Fatalf("unexpected revoked user id: %d", revoker.userID)
	}
	if revoker.deviceID != "web-device-11" {
		t.Fatalf("unexpected revoked device id: %s", revoker.deviceID)
	}
	if len(storage.linkedDeviceRevokes) != 1 || storage.linkedDeviceRevokes[0] != "web-device-11" {
		t.Fatalf("expected linked device token revoke for web-device-11, got %#v", storage.linkedDeviceRevokes)
	}
}

func TestAuthHandlerLogoutDeactivatesCurrentPushDevice(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)

	storage := newMemoryTokenStorage()
	tokenManager := auth.NewTokenManager(&config.JWTConfig{
		Secret:             "test-secret",
		Issuer:             "test-issuer",
		Audience:           "test-audience",
		AccessTokenExpiry:  time.Hour,
		RefreshTokenExpiry: 24 * time.Hour,
	}, storage)

	tokens, err := tokenManager.GenerateTokenPair(
		context.Background(),
		42,
		"123456789012",
		"user@example.com",
		"mobileuser",
		"Mobile",
		"User",
		false,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("generate token pair: %v", err)
	}

	pushManager := &pushDeviceManagerMock{}
	handler := NewAuthHandler(nil, nil, nil, zap.NewNop(), tokenManager)
	handler.SetPushDeviceManager(pushManager)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: tokens.AccessToken})
	req.AddCookie(&http.Cookie{Name: "refresh_token", Value: tokens.RefreshToken})
	req.Header.Set("X-Device-ID", "mobile-device-42")
	ctx.Request = req

	handler.Logout(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}
	if pushManager.deactivateCalls != 1 {
		t.Fatalf("expected one device deactivation call, got %d", pushManager.deactivateCalls)
	}
	if pushManager.deactivatedUserID != 42 {
		t.Fatalf("unexpected user id: %d", pushManager.deactivatedUserID)
	}
	if pushManager.deactivatedDevice != "mobile-device-42" {
		t.Fatalf("unexpected deactivated device: %s", pushManager.deactivatedDevice)
	}
}

func TestAuthHandlerEgovCallbackWithoutQueryReturnsInvalidCallback(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	handler := NewAuthHandler(nil, nil, nil, zap.NewNop())

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/egov/callback", nil)
	req.Header.Set("Accept", "text/html")
	ctx.Request = req

	handler.CompleteEgovLogin(ctx)

	if recorder.Code != http.StatusFound {
		t.Fatalf("expected redirect, got %d", recorder.Code)
	}
	location := recorder.Header().Get("Location")
	if !strings.Contains(location, "error=EGOV_INVALID_CALLBACK") {
		t.Fatalf("expected invalid callback in redirect, got %s", location)
	}
	if !strings.Contains(location, "message=") {
		t.Fatalf("expected message in redirect, got %s", location)
	}
}

type authHandlerUserRepoStub struct {
	user                    *domain.User
	hasRegisteredMobileAuth bool
	paginatedUsers          []*domain.User
	paginatedTotal          int
	capturedQuery           string
	capturedLimit           int
	capturedOffset          int
	capturedFilters         domain.UserFilters
}

func (s *authHandlerUserRepoStub) GetByEmail(_ context.Context, _ string) (*domain.User, error) {
	return s.user, nil
}

func (s *authHandlerUserRepoStub) GetByID(_ context.Context, id int64) (*domain.User, error) {
	if s.user != nil && s.user.ID == id {
		return s.user, nil
	}
	return nil, nil
}

func (s *authHandlerUserRepoStub) GetByIIN(_ context.Context, _ string) (*domain.User, error) {
	return s.user, nil
}

func (s *authHandlerUserRepoStub) GetByKeycloakID(_ context.Context, _ string) (*domain.User, error) {
	return s.user, nil
}

func (s *authHandlerUserRepoStub) UpsertFromKeycloak(_ context.Context, _ *domain.User) error {
	return nil
}

func (s *authHandlerUserRepoStub) HasRegisteredMobileDevice(_ context.Context, _ int64) (bool, error) {
	return s.hasRegisteredMobileAuth, nil
}

func (s *authHandlerUserRepoStub) UpdateLastLogin(_ context.Context, _ int64, _ time.Time) error {
	return nil
}

func (s *authHandlerUserRepoStub) CompleteOnboarding(_ context.Context, _ int64) error {
	return nil
}

func (s *authHandlerUserRepoStub) SetIsFirstLogin(_ context.Context, _ int64, _ bool) error {
	return nil
}

func (s *authHandlerUserRepoStub) Update(_ context.Context, _ *domain.User) error {
	return nil
}

func (s *authHandlerUserRepoStub) GetAll(_ context.Context) ([]*domain.User, error) {
	return nil, nil
}

func (s *authHandlerUserRepoStub) GetPaginated(_ context.Context, q string, limit, offset int, filters ...domain.UserFilters) ([]*domain.User, int, error) {
	s.capturedQuery = q
	s.capturedLimit = limit
	s.capturedOffset = offset
	if len(filters) > 0 {
		s.capturedFilters = filters[0]
	}
	return s.paginatedUsers, s.paginatedTotal, nil
}

func (s *authHandlerUserRepoStub) GetAllVisibleTo(_ context.Context, _ int64) ([]*domain.User, error) {
	return nil, nil
}

func (s *authHandlerUserRepoStub) GetByUsername(_ context.Context, _ string) (*domain.User, error) {
	return s.user, nil
}

func (s *authHandlerUserRepoStub) GetFullDetailsByID(_ context.Context, _ int64) (*domain.UserDetailResponse, error) {
	return nil, nil
}

func (s *authHandlerUserRepoStub) UpdateStatus(_ context.Context, _ int64, _ string) error {
	return nil
}

func (s *authHandlerUserRepoStub) GetByKeycloakIDs(_ context.Context, _ []string) ([]*domain.User, error) {
	return nil, nil
}

func (s *authHandlerUserRepoStub) GetNeverLoggedIn(_ context.Context) ([]*domain.User, error) {
	return nil, nil
}

func (s *authHandlerUserRepoStub) ExportMinistryUsers(_ context.Context, _ int64, _ []int64, _ []int64, _ []int64) ([]domain.MinistryUserExportRow, error) {
	return nil, nil
}
func (s *authHandlerUserRepoStub) GetMinistryName(_ context.Context, _ int64) (string, error) {
	return "", nil
}
func (s *authHandlerUserRepoStub) SoftDelete(_ context.Context, _ int64) error {
	return nil
}
func (s *authHandlerUserRepoStub) UpdateRole(_ context.Context, _ int64, _ domain.Role) error {
	return nil
}
func (s *authHandlerUserRepoStub) UpdateMinistryID(_ context.Context, _, _ int64) error {
	return nil
}

func newAuthHandlerLoginService(t *testing.T, hasRegisteredMobileAuth bool, opts ...auth.ServiceOption) (*auth.Service, *int32) {
	t.Helper()

	var logoutCalls int32
	kcServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/realms/test-realm/protocol/openid-connect/token":
			if err := r.ParseForm(); err != nil {
				t.Fatalf("parse token form: %v", err)
			}
			if r.Form.Get("grant_type") != "password" {
				t.Fatalf("unexpected grant_type: %s", r.Form.Get("grant_type"))
			}
			if r.Form.Get("username") != "user@example.com" || r.Form.Get("password") != "correct-password" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  "access-token",
				"refresh_token": "refresh-token",
				"token_type":    "Bearer",
				"expires_in":    300,
			})
		case "/realms/test-realm/protocol/openid-connect/logout":
			atomic.AddInt32(&logoutCalls, 1)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(kcServer.Close)

	userRepo := &authHandlerUserRepoStub{
		user: &domain.User{
			IsActive:  true,
			ID:        77,
			IIN:       stringPtr("123456789012"),
			Email:     "user@example.com",
			Username:  "user77",
			FirstName: "Иван",
			LastName:  "Петров",
		},
		hasRegisteredMobileAuth: hasRegisteredMobileAuth,
	}

	return auth.NewKeycloakService(
		userRepo,
		nil,
		nil,
		keycloak.New(kcServer.URL, "test-realm", "test-client", ""),
		nil,
		"test-realm",
		opts...,
	), &logoutCalls
}

func stringPtr(value string) *string {
	return &value
}

func testUnsignedJWT(t *testing.T, claims map[string]any) string {
	t.Helper()

	headerBytes, err := json.Marshal(map[string]any{
		"alg": "none",
		"typ": "JWT",
	})
	if err != nil {
		t.Fatalf("marshal jwt header: %v", err)
	}
	claimsBytes, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal jwt claims: %v", err)
	}

	return base64.RawURLEncoding.EncodeToString(headerBytes) + "." +
		base64.RawURLEncoding.EncodeToString(claimsBytes) + "."
}

func TestAuthHandlerCompleteKeycloakLoginRedirectsWithoutTokensInURL(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)

	accessToken := testUnsignedJWT(t, map[string]any{
		"sub":                "kc-user-77",
		"email":              "user@example.com",
		"preferred_username": "user77",
		"given_name":         "Иван",
		"family_name":        "Петров",
		"iin":                "123456789012",
		"large_claim":        strings.Repeat("x", 12_000),
	})
	refreshToken := "refresh-" + strings.Repeat("y", 12_000)

	kcServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/realms/test-realm/protocol/openid-connect/token" {
			http.NotFound(w, r)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse token form: %v", err)
		}
		if r.Form.Get("grant_type") != "authorization_code" {
			t.Fatalf("unexpected grant_type: %s", r.Form.Get("grant_type"))
		}
		if r.Form.Get("code") != "auth-code" {
			t.Fatalf("unexpected code: %s", r.Form.Get("code"))
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  accessToken,
			"refresh_token": refreshToken,
			"token_type":    "Bearer",
			"expires_in":    300,
		})
	}))
	t.Cleanup(kcServer.Close)

	userRepo := &authHandlerUserRepoStub{
		user: &domain.User{
			IsActive:  true,
			ID:        77,
			IIN:       stringPtr("123456789012"),
			Email:     "user@example.com",
			Username:  "user77",
			FirstName: "Иван",
			LastName:  "Петров",
		},
	}
	authService := auth.NewKeycloakService(
		userRepo,
		nil,
		nil,
		keycloak.New(kcServer.URL, "test-realm", "test-client", ""),
		nil,
		"test-realm",
	)
	handler := NewAuthHandler(authService, nil, nil, zap.NewNop())

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/keycloak/callback?state=state-123&code=auth-code", nil)
	req.Host = "alem-workspace.gov.kz"
	req.Header.Set("X-Forwarded-Proto", "https")
	req.AddCookie(&http.Cookie{Name: keycloakOAuthStateCookie, Value: "state-123"})
	req.AddCookie(&http.Cookie{Name: keycloakOAuthRedirectCookie, Value: "/web/chat?tab=1"})
	ctx.Request = req

	handler.CompleteKeycloakLogin(ctx)

	if recorder.Code != http.StatusFound {
		t.Fatalf("expected status 302, got %d, body=%s", recorder.Code, recorder.Body.String())
	}
	location := recorder.Header().Get("Location")
	parsedLocation, err := url.Parse(location)
	if err != nil {
		t.Fatalf("parse redirect location: %v", err)
	}
	if parsedLocation.Path != "/web/auth/callback" {
		t.Fatalf("expected frontend auth callback, got %q", location)
	}
	if parsedLocation.Query().Get("login") != "success" {
		t.Fatalf("expected login=success, got %q", location)
	}
	if parsedLocation.Query().Get("redirect") != "/web/chat?tab=1" {
		t.Fatalf("expected clean redirect target, got %q", location)
	}
	if strings.Contains(location, "token=") || strings.Contains(location, "refreshToken=") {
		t.Fatalf("redirect location should not include tokens: %q", location)
	}
	setCookieHeaders := recorder.Result().Header.Values("Set-Cookie")
	if len(setCookieHeaders) < 4 {
		t.Fatalf("expected multiple Set-Cookie headers on callback, got %d: %#v", len(setCookieHeaders), setCookieHeaders)
	}
	assertAuthCookie(t, recorder.Result(), "access_token", accessToken)
	assertAuthCookie(t, recorder.Result(), "refresh_token", refreshToken)
	assertCookieFlags(t, recorder.Result(), "access_token", true, true, http.SameSiteNoneMode)
	assertCookieFlags(t, recorder.Result(), "refresh_token", true, true, http.SameSiteNoneMode)
}

func TestAuthHandlerCompleteKeycloakLoginInvalidStateRedirectsToLogin(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)

	handler := NewAuthHandler(&auth.Service{}, nil, nil, zap.NewNop())

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/keycloak/callback?state=returned-state&code=auth-code", nil)
	req.Host = "alem-workspace.gov.kz"
	req.Header.Set("X-Forwarded-Proto", "https")
	ctx.Request = req

	handler.CompleteKeycloakLogin(ctx)

	if recorder.Code != http.StatusFound {
		t.Fatalf("expected status 302, got %d, body=%s", recorder.Code, recorder.Body.String())
	}
	if location := recorder.Header().Get("Location"); location != "/web/login" {
		t.Fatalf("expected redirect to login, got %q", location)
	}
	if strings.Contains(recorder.Body.String(), "invalid keycloak login state") {
		t.Fatalf("callback should not render API error JSON to the browser")
	}
}

func TestAuthHandlerStartKeycloakLoginRedirectsToPublicKeycloakURL(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Setenv("APP_PUBLIC_URL", "https://alem-workspace.gov.kz")
	authService := auth.NewKeycloakService(
		&authHandlerUserRepoStub{},
		nil,
		nil,
		keycloak.NewWithPublicURL(
			"http://keycloak:8080/keycloak",
			"https://alem-workspace.gov.kz/keycloak",
			"GovApps",
			"alem-workspace",
			"secret",
		),
		nil,
		"GovApps",
	)
	handler := NewAuthHandler(authService, nil, nil, zap.NewNop())

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/keycloak/login?platform=web&redirect=/web/workspace", nil)
	req.Host = "alem-workspace.gov.kz"
	req.Header.Set("X-Forwarded-Proto", "https")
	ctx.Request = req

	handler.StartKeycloakLogin(ctx)

	if recorder.Code != http.StatusFound {
		t.Fatalf("expected status 302, got %d", recorder.Code)
	}

	location := recorder.Header().Get("Location")
	parsed, err := url.Parse(location)
	if err != nil {
		t.Fatalf("parse location: %v", err)
	}
	if parsed.Scheme != "https" || parsed.Host != "alem-workspace.gov.kz" {
		t.Fatalf("expected public keycloak host, got %q", location)
	}
	if parsed.Path != "/keycloak/realms/GovApps/protocol/openid-connect/auth" {
		t.Fatalf("unexpected auth path: %q", parsed.Path)
	}
	if parsed.Query().Get("client_id") != "alem-workspace" {
		t.Fatalf("unexpected client_id: %q", parsed.Query().Get("client_id"))
	}
	if parsed.Query().Get("response_type") != "code" {
		t.Fatalf("unexpected response_type: %q", parsed.Query().Get("response_type"))
	}
	if parsed.Query().Get("scope") != "openid email profile" {
		t.Fatalf("unexpected scope: %q", parsed.Query().Get("scope"))
	}
	if parsed.Query().Get("redirect_uri") != "https://alem-workspace.gov.kz/api/v1/auth/keycloak/callback" {
		t.Fatalf("unexpected redirect_uri: %q", parsed.Query().Get("redirect_uri"))
	}
	if parsed.Query().Get("state") == "" {
		t.Fatal("expected state query param")
	}
}

func TestAuthHandlerStartMobileKeycloakLoginReturnsPKCEAuthURL(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Setenv("KEYCLOAK_MOBILE_REDIRECT_URIS", "kz.alemSuperapp.app://auth/callback,kz.superapp.app://auth/callback")
	defaultClient := keycloak.NewWithPublicURL(
		"http://keycloak:8080/keycloak",
		"https://alem-workspace.gov.kz/keycloak",
		"GovApps",
		"alem-workspace",
		"secret",
	)
	mobileClient := keycloak.NewWithPublicURL(
		"http://keycloak:8080/keycloak",
		"https://alem-workspace.gov.kz/keycloak",
		"GovApps",
		"alem-mobile",
		"",
	)
	authService := auth.NewKeycloakService(
		&authHandlerUserRepoStub{},
		nil,
		nil,
		defaultClient,
		nil,
		"GovApps",
		auth.WithMobileClientID("alem-mobile"),
		auth.WithKeycloakClients(map[string]*keycloak.Client{
			"alem-workspace": defaultClient,
			"alem-mobile":    mobileClient,
		}),
	)
	handler := NewAuthHandler(authService, nil, nil, zap.NewNop())

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/keycloak/mobile/login?redirect_uri=kz.alemSuperapp.app%3A%2F%2Fauth%2Fcallback&state=mobile-state&code_challenge=challenge-123&code_challenge_method=S256&platform=mobile", nil)
	ctx.Request = req

	handler.StartMobileKeycloakLogin(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", recorder.Code, recorder.Body.String())
	}

	var payload mobileKeycloakLoginPayload
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if payload.State == "" {
		t.Fatal("expected signed state in response")
	}

	parsed, err := url.Parse(payload.AuthURL)
	if err != nil {
		t.Fatalf("parse auth url: %v", err)
	}
	if parsed.Query().Get("client_id") != "alem-mobile" {
		t.Fatalf("unexpected client_id: %q", parsed.Query().Get("client_id"))
	}
	if parsed.Query().Get("scope") != "openid email profile offline_access" {
		t.Fatalf("unexpected scope: %q", parsed.Query().Get("scope"))
	}
	if parsed.Query().Get("redirect_uri") != "kz.alemSuperapp.app://auth/callback" {
		t.Fatalf("unexpected redirect_uri: %q", parsed.Query().Get("redirect_uri"))
	}
	if parsed.Query().Get("code_challenge") != "challenge-123" {
		t.Fatalf("unexpected code_challenge: %q", parsed.Query().Get("code_challenge"))
	}
	if parsed.Query().Get("code_challenge_method") != "S256" {
		t.Fatalf("unexpected code_challenge_method: %q", parsed.Query().Get("code_challenge_method"))
	}
	if parsed.Query().Get("state") != payload.State {
		t.Fatalf("expected auth url state to equal response state")
	}
}

func TestFrontendAuthCallbackTargetCarriesSafeRedirectOnly(t *testing.T) {
	t.Parallel()

	location := frontendAuthCallbackTarget("https://evil.example/path")
	parsedLocation, err := url.Parse(location)
	if err != nil {
		t.Fatalf("parse callback location: %v", err)
	}
	if parsedLocation.Path != "/web/auth/callback" {
		t.Fatalf("expected callback path, got %q", parsedLocation.Path)
	}
	if parsedLocation.Query().Get("redirect") != "/web/" {
		t.Fatalf("expected unsafe redirect to fall back to /web/, got %q", parsedLocation.Query().Get("redirect"))
	}
}

func TestAuthHandlerCompleteKeycloakSilentLoginRequiredReturnsToLocalLogin(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)

	handler := NewAuthHandler(&auth.Service{}, nil, nil, zap.NewNop())

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/keycloak/sso-check/callback?error=login_required&state=silent-state", nil)
	req.AddCookie(&http.Cookie{Name: keycloakSilentStateCookie, Value: "silent-state"})
	req.AddCookie(&http.Cookie{Name: keycloakSilentRedirectCookie, Value: "/web/dashboard"})
	ctx.Request = req

	handler.CompleteKeycloakSilentCheck(ctx)

	if recorder.Code != http.StatusFound {
		t.Fatalf("expected status 302, got %d", recorder.Code)
	}
	if location := recorder.Header().Get("Location"); location != "/web/login?redirect=%2Fweb%2Fdashboard" {
		t.Fatalf("expected redirect to local login with redirect target, got %q", location)
	}
}

func TestFrontendLoginTargetCarriesSafeRedirectOnly(t *testing.T) {
	t.Parallel()

	if location := frontendLoginTarget("https://evil.example/path"); location != "/web/login" {
		t.Fatalf("expected unsafe redirect to fall back to /web/login, got %q", location)
	}

	if location := frontendLoginTarget("/web/dashboard"); location != "/web/login?redirect=%2Fweb%2Fdashboard" {
		t.Fatalf("expected safe redirect to be preserved, got %q", location)
	}
}

func TestSanitizeRedirectTargetNormalizesLegacyFrontendRoutes(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"":                          "/web/",
		"/":                         "/web/",
		"/web":                      "/web/",
		"/web/dashboard":            "/web/dashboard",
		"/dashboard":                "/web/dashboard",
		"/teamai?prompt=hello#chat": "/web/teamai?prompt=hello#chat",
		"/team-rag/tablet":          "/web/team-rag/tablet",
		"/team-contact/org-map":     "/web/team-contact/org-map",
		"/teamstore":                "/web/teamstore",
		"/dashboard?tab=tasks":      "/web/dashboard?tab=tasks",
		"/main/dashboard":           "/web/main/dashboard",
		"/messenger/chat/42":        "/web/workspace",
		"/web/messenger/chat/42":    "/web/workspace",
		"/drive/files?folder=docs":  "/web/drive/files?folder=docs",
		"https://evil.example":      "/web/",
		"dashboard":                 "/web/",
		"//evil.example/path":       "/web/",
	}

	for input, expected := range tests {
		input, expected := input, expected
		t.Run(input, func(t *testing.T) {
			t.Parallel()

			if actual := sanitizeRedirectTarget(input); actual != expected {
				t.Fatalf("expected %q, got %q", expected, actual)
			}
		})
	}
}

func TestAuthHandlerLoginRejectsWebPasswordLoginWhenMobileDeviceExists(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)

	authService, logoutCalls := newAuthHandlerLoginService(
		t,
		true,
		auth.WithRequireQRForWebLoginWhenMobileRegistered(true),
	)
	handler := NewAuthHandler(authService, nil, nil, zap.NewNop())

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"email":"user@example.com","password":"correct-password"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://web.example.com")
	ctx.Request = req

	handler.Login(ctx)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("expected status 409, got %d", recorder.Code)
	}
	if atomic.LoadInt32(logoutCalls) != 1 {
		t.Fatalf("expected keycloak logout to be called once, got %d", atomic.LoadInt32(logoutCalls))
	}

	var response struct {
		Error struct {
			Code    string         `json:"code"`
			Message string         `json:"message"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if response.Error.Code != "QR_LOGIN_REQUIRED" {
		t.Fatalf("unexpected error code: %s", response.Error.Code)
	}
	if !strings.Contains(response.Error.Message, "QR-код") {
		t.Fatalf("expected QR message, got %q", response.Error.Message)
	}
	if response.Error.Details["title"] != "Войдите через QR-код" {
		t.Fatalf("unexpected title: %#v", response.Error.Details["title"])
	}
	if response.Error.Details["auth_method"] != "qr" {
		t.Fatalf("unexpected auth_method: %#v", response.Error.Details["auth_method"])
	}
}

func TestAuthHandlerLoginAllowsPasswordLoginWhenRequestIsNotWeb(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)

	authService, logoutCalls := newAuthHandlerLoginService(
		t,
		true,
		auth.WithRequireQRForWebLoginWhenMobileRegistered(true),
	)
	handler := NewAuthHandler(authService, nil, nil, zap.NewNop())

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"email":"user@example.com","password":"correct-password"}`))
	req.Header.Set("Content-Type", "application/json")
	ctx.Request = req

	handler.Login(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", recorder.Code, recorder.Body.String())
	}
	if atomic.LoadInt32(logoutCalls) != 0 {
		t.Fatalf("expected keycloak logout not to be called, got %d", atomic.LoadInt32(logoutCalls))
	}
	if !strings.Contains(recorder.Body.String(), "access-token") {
		t.Fatalf("expected access token in response, got %s", recorder.Body.String())
	}
	assertAuthCookie(t, recorder.Result(), "access_token", "access-token")
	assertAuthCookie(t, recorder.Result(), "refresh_token", "refresh-token")
}

func assertAuthCookie(t *testing.T, response *http.Response, name, value string) {
	t.Helper()

	for _, cookie := range response.Cookies() {
		if cookie.Name == name {
			if cookie.Value != value {
				t.Fatalf("expected %s cookie value %q, got %q", name, value, cookie.Value)
			}
			if !cookie.HttpOnly {
				t.Fatalf("expected %s cookie to be HttpOnly", name)
			}
			if cookie.Path != "/" {
				t.Fatalf("expected %s cookie path /, got %q", name, cookie.Path)
			}
			return
		}
	}

	t.Fatalf("expected %s cookie in response", name)
}

func assertCookieFlags(t *testing.T, response *http.Response, name string, secure bool, httpOnly bool, sameSite http.SameSite) {
	t.Helper()

	for _, cookie := range response.Cookies() {
		if cookie.Name != name {
			continue
		}
		if cookie.Secure != secure {
			t.Fatalf("expected %s secure=%v, got %v", name, secure, cookie.Secure)
		}
		if cookie.HttpOnly != httpOnly {
			t.Fatalf("expected %s httpOnly=%v, got %v", name, httpOnly, cookie.HttpOnly)
		}
		if cookie.SameSite != sameSite {
			t.Fatalf("expected %s sameSite=%v, got %v", name, sameSite, cookie.SameSite)
		}
		return
	}

	t.Fatalf("expected %s cookie in response", name)
}

func TestAuthHandlerLoginWrongPasswordReturnsReadableError(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)

	authService, _ := newAuthHandlerLoginService(t, false)
	handler := NewAuthHandler(authService, nil, nil, zap.NewNop())

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"email":"user@example.com","password":"wrong-password"}`))
	req.Header.Set("Content-Type", "application/json")
	ctx.Request = req

	handler.Login(ctx)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d, body=%s", recorder.Code, recorder.Body.String())
	}

	var response apperrors.ErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if response.Error.Code != apperrors.ErrCodeInvalidCredentials {
		t.Fatalf("unexpected error code: %s", response.Error.Code)
	}
	if response.Error.Message != "Неверный логин или пароль" {
		t.Fatalf("unexpected message: %q", response.Error.Message)
	}
	if !strings.Contains(response.Error.UserMessage, "Проверьте данные") {
		t.Fatalf("expected readable user message, got %q", response.Error.UserMessage)
	}
}

func TestAuthHandlerLoginAllowsUsernameIdentifier(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)

	authService, logoutCalls := newAuthHandlerLoginService(t, false)
	handler := NewAuthHandler(authService, nil, nil, zap.NewNop())

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"user77","password":"correct-password"}`))
	req.Header.Set("Content-Type", "application/json")
	ctx.Request = req

	handler.Login(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", recorder.Code, recorder.Body.String())
	}
	if atomic.LoadInt32(logoutCalls) != 0 {
		t.Fatalf("expected keycloak logout not to be called, got %d", atomic.LoadInt32(logoutCalls))
	}
	if !strings.Contains(recorder.Body.String(), "access-token") {
		t.Fatalf("expected access token in response, got %s", recorder.Body.String())
	}
}

func TestAuthHandlerLoginAllowsIINIdentifier(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)

	authService, logoutCalls := newAuthHandlerLoginService(t, false)
	handler := NewAuthHandler(authService, nil, nil, zap.NewNop())

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"iin":"123456789012","password":"correct-password"}`))
	req.Header.Set("Content-Type", "application/json")
	ctx.Request = req

	handler.Login(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", recorder.Code, recorder.Body.String())
	}
	if atomic.LoadInt32(logoutCalls) != 0 {
		t.Fatalf("expected keycloak logout not to be called, got %d", atomic.LoadInt32(logoutCalls))
	}
	if !strings.Contains(recorder.Body.String(), "access-token") {
		t.Fatalf("expected access token in response, got %s", recorder.Body.String())
	}
}

func TestAuthHandlerLoginAllowsWebPasswordLoginWhenQRPolicyDisabled(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)

	authService, logoutCalls := newAuthHandlerLoginService(
		t,
		true,
		auth.WithRequireQRForWebLoginWhenMobileRegistered(false),
	)
	handler := NewAuthHandler(authService, nil, nil, zap.NewNop())

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"email":"user@example.com","password":"correct-password"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://web.example.com")
	ctx.Request = req

	handler.Login(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", recorder.Code, recorder.Body.String())
	}
	if atomic.LoadInt32(logoutCalls) != 0 {
		t.Fatalf("expected keycloak logout not to be called, got %d", atomic.LoadInt32(logoutCalls))
	}
	if !strings.Contains(recorder.Body.String(), "access-token") {
		t.Fatalf("expected access token in response, got %s", recorder.Body.String())
	}
}

func TestAuthHandlerGetAllUsersPaginated(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)

	userRepo := &authHandlerUserRepoStub{
		paginatedUsers: []*domain.User{
			{
				ID:        1,
				Email:     "user1@example.com",
				Username:  "user1",
				FirstName: "First",
				LastName:  "One",
				IsActive:  true,
			},
			{
				ID:        2,
				Email:     "user2@example.com",
				Username:  "user2",
				FirstName: "Second",
				LastName:  "Two",
				IsActive:  true,
			},
		},
		paginatedTotal: 2,
	}

	authService := auth.NewKeycloakService(
		userRepo,
		nil,
		nil,
		nil,
		nil,
		"test-realm",
	)

	handler := NewAuthHandler(authService, nil, nil, zap.NewNop())

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/users?limit=10&offset=5&q=testquery&position_name=Dev&department_name=IT&organization_name=Alem", nil)
	ctx.Request = req

	handler.GetAllUsers(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", recorder.Code, recorder.Body.String())
	}

	if userRepo.capturedLimit != 10 {
		t.Fatalf("expected limit 10, got %d", userRepo.capturedLimit)
	}
	if userRepo.capturedOffset != 5 {
		t.Fatalf("expected offset 5, got %d", userRepo.capturedOffset)
	}
	if userRepo.capturedQuery != "testquery" {
		t.Fatalf("expected query 'testquery', got %s", userRepo.capturedQuery)
	}
	if userRepo.capturedFilters.PositionName != "Dev" {
		t.Fatalf("expected position_name 'Dev', got %s", userRepo.capturedFilters.PositionName)
	}
	if userRepo.capturedFilters.DepartmentName != "IT" {
		t.Fatalf("expected department_name 'IT', got %s", userRepo.capturedFilters.DepartmentName)
	}
	if userRepo.capturedFilters.OrganizationName != "Alem" {
		t.Fatalf("expected organization_name 'Alem', got %s", userRepo.capturedFilters.OrganizationName)
	}

	var resp struct {
		Items  []domain.UserResponse `json:"items"`
		Total  int                   `json:"total"`
		Limit  int                   `json:"limit"`
		Offset int                   `json:"offset"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if resp.Total != 2 {
		t.Fatalf("expected total 2, got %d", resp.Total)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(resp.Items))
	}
	if resp.Items[0].Email != "user1@example.com" {
		t.Fatalf("expected first item email 'user1@example.com', got %s", resp.Items[0].Email)
	}

	// Test default parameters (should return a backward-compatible flat array)
	recorderDefault := httptest.NewRecorder()
	ctxDefault, _ := gin.CreateTestContext(recorderDefault)
	reqDefault := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
	ctxDefault.Request = reqDefault

	handler.GetAllUsers(ctxDefault)

	if recorderDefault.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", recorderDefault.Code, recorderDefault.Body.String())
	}
	if userRepo.capturedLimit != 100 {
		t.Fatalf("expected default limit 100, got %d", userRepo.capturedLimit)
	}
	if userRepo.capturedOffset != 0 {
		t.Fatalf("expected default offset 0, got %d", userRepo.capturedOffset)
	}
	if userRepo.capturedQuery != "" {
		t.Fatalf("expected empty default query, got %s", userRepo.capturedQuery)
	}

	var respDefault []domain.UserResponse
	if err := json.Unmarshal(recorderDefault.Body.Bytes(), &respDefault); err != nil {
		t.Fatalf("unmarshal flat list response: %v, body: %s", err, recorderDefault.Body.String())
	}
	if len(respDefault) != 2 {
		t.Fatalf("expected 2 items, got %d", len(respDefault))
	}
}
