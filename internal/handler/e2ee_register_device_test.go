package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/auth"
	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

const testValidHTTPX25519PublicKeyBase64 = "MTIzNDU2Nzg5MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTI="

// ─── минимальный repo-мок для handler-тестов E2EE ─────────────────────────────
// Реализует весь e2eeRepository интерфейс (unexported в package service),
// поэтому определён здесь через полную перечень методов.

type e2eeHandlerMockRepo struct {
	upsertBackupFn func(ctx context.Context, backup *domain.UserKeyBackup) error
	registerFn     func(ctx context.Context, userID int64, deviceID, publicKey string, deviceName, platform *string, meta *domain.ClientMetadata) error
	backupFn       func(ctx context.Context, userID int64) (*domain.UserKeyBackup, error)
	userFn         func(ctx context.Context, userID int64) (*domain.User, error)
	revokeFn       func(ctx context.Context, userID int64, keepDeviceID string) ([]string, error)
	notifFn        func(ctx context.Context, userID int64, deviceID string, deviceName, platform *string, meta *domain.ClientMetadata, revokedDeviceIDs []string) error
}

// ── все методы интерфейса (no-op по умолчанию) ────────────────────────────────

func (r *e2eeHandlerMockRepo) UpsertPublicKey(_ context.Context, _ *domain.UserPublicKey) error {
	return nil
}
func (r *e2eeHandlerMockRepo) GetKeysByUser(_ context.Context, _ int64) (map[string]string, error) {
	return nil, nil
}
func (r *e2eeHandlerMockRepo) UpsertKeyBackup(ctx context.Context, backup *domain.UserKeyBackup) error {
	if r.upsertBackupFn != nil {
		return r.upsertBackupFn(ctx, backup)
	}
	return nil
}
func (r *e2eeHandlerMockRepo) GetKeyBackup(ctx context.Context, userID int64) (*domain.UserKeyBackup, error) {
	if r.backupFn != nil {
		return r.backupFn(ctx, userID)
	}
	return nil, nil
}
func (r *e2eeHandlerMockRepo) DeleteKeyBackup(_ context.Context, _ int64) error { return nil }
func (r *e2eeHandlerMockRepo) CreateWebPairingSession(_ context.Context, _ *domain.WebPairingSession) error {
	return nil
}
func (r *e2eeHandlerMockRepo) GetWebPairingSessionBySessionToken(_ context.Context, _ uuid.UUID, _ string) (*domain.WebPairingSession, error) {
	return nil, nil
}
func (r *e2eeHandlerMockRepo) GetWebPairingSessionByQRToken(_ context.Context, _ uuid.UUID, _ string) (*domain.WebPairingSession, error) {
	return nil, nil
}
func (r *e2eeHandlerMockRepo) ApproveWebPairingSession(_ context.Context, _ uuid.UUID, _ int64, _ *string, _, _ *string) (*domain.WebPairingSession, error) {
	return nil, nil
}
func (r *e2eeHandlerMockRepo) RejectWebPairingSession(_ context.Context, _ uuid.UUID, _ int64) (*domain.WebPairingSession, error) {
	return nil, nil
}
func (r *e2eeHandlerMockRepo) RedeemWebPairingSession(_ context.Context, _ uuid.UUID, _ string) (*domain.WebPairingSession, error) {
	return nil, nil
}
func (r *e2eeHandlerMockRepo) UpdateLinkedDeviceAccessMetadata(_ context.Context, _ int64, _ string, _ *domain.ClientMetadata) error {
	return nil
}
func (r *e2eeHandlerMockRepo) ListLinkedDevices(_ context.Context, _ int64) ([]*domain.LinkedDevice, error) {
	return nil, nil
}
func (r *e2eeHandlerMockRepo) RevokeLinkedDevice(_ context.Context, _ int64, _ string) error {
	return nil
}
func (r *e2eeHandlerMockRepo) GetUserBasicProfile(ctx context.Context, userID int64) (*domain.User, error) {
	if r.userFn != nil {
		return r.userFn(ctx, userID)
	}
	return nil, nil
}
func (r *e2eeHandlerMockRepo) RegisterWebDeviceFromPasswordLogin(ctx context.Context, userID int64, deviceID, publicKey string, deviceName, platform *string, meta *domain.ClientMetadata) error {
	if r.registerFn != nil {
		return r.registerFn(ctx, userID, deviceID, publicKey, deviceName, platform, meta)
	}
	return nil
}
func (r *e2eeHandlerMockRepo) RevokeOtherWebDevices(ctx context.Context, userID int64, keepDeviceID string) ([]string, error) {
	if r.revokeFn != nil {
		return r.revokeFn(ctx, userID, keepDeviceID)
	}
	return nil, nil
}
func (r *e2eeHandlerMockRepo) CreateWebLoginNotification(ctx context.Context, userID int64, deviceID string, deviceName, platform *string, meta *domain.ClientMetadata, revokedDeviceIDs []string) error {
	if r.notifFn != nil {
		return r.notifFn(ctx, userID, deviceID, deviceName, platform, meta, revokedDeviceIDs)
	}
	return nil
}

// ─── helper: строит handler + gin-роутер ──────────────────────────────────────

func buildE2EETestRouter(repo *e2eeHandlerMockRepo) *gin.Engine {
	gin.SetMode(gin.TestMode)
	svc := service.NewE2EEService(repo, zap.NewNop())
	h := NewE2EEHandler(svc, zap.NewNop())

	r := gin.New()
	// Inject authenticated user_id без реального JWT
	r.Use(func(c *gin.Context) {
		c.Set("user_id", int64(99))
		c.Next()
	})
	r.POST("/e2ee/device/register", h.RegisterWebDevice)
	return r
}

func buildE2EEBackupTestRouter(repo *e2eeHandlerMockRepo) *gin.Engine {
	gin.SetMode(gin.TestMode)
	svc := service.NewE2EEService(repo, zap.NewNop())
	h := NewE2EEHandler(svc, zap.NewNop())

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("user_id", int64(99))
		c.Next()
	})
	r.POST("/e2ee/backup", h.SaveKeyBackup)
	return r
}

func buildE2EETestRouterWithTokenManager(repo *e2eeHandlerMockRepo, tokenManager *auth.TokenManager) *gin.Engine {
	gin.SetMode(gin.TestMode)
	svc := service.NewE2EEService(repo, zap.NewNop())
	h := NewE2EEHandler(svc, zap.NewNop(), tokenManager)

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("user_id", int64(99))
		c.Set("claims", &auth.Claims{
			UserID:    99,
			Email:     "user@example.com",
			Username:  "user99",
			TokenType: auth.AccessToken,
		})
		c.Next()
	})
	r.POST("/e2ee/device/register", h.RegisterWebDevice)
	return r
}

// ─── тесты ────────────────────────────────────────────────────────────────────

func TestRegisterWebDevice_HTTP_SuccessWithBackup(t *testing.T) {
	t.Parallel()

	algo := "argon2id"
	repo := &e2eeHandlerMockRepo{
		backupFn: func(_ context.Context, _ int64) (*domain.UserKeyBackup, error) {
			return &domain.UserKeyBackup{KDFAlgorithm: algo}, nil
		},
	}

	body := RegisterWebDeviceReq{
		DeviceID:  "test-device-uuid",
		PublicKey: testValidHTTPX25519PublicKeyBase64,
	}
	bodyJSON, _ := json.Marshal(body)

	r := buildE2EETestRouter(repo)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/e2ee/device/register", bytes.NewReader(bodyJSON))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp service.RegisterWebDeviceResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "test-device-uuid", resp.DeviceID)
	assert.True(t, resp.HasBackup)
	require.NotNil(t, resp.BackupAlgo)
	assert.Equal(t, "argon2id", *resp.BackupAlgo)
	assert.Equal(t, "device registered successfully", resp.Message)
}

func TestRegisterWebDevice_HTTP_SuccessWithoutBackup(t *testing.T) {
	t.Parallel()

	repo := &e2eeHandlerMockRepo{
		backupFn: func(_ context.Context, _ int64) (*domain.UserKeyBackup, error) {
			return nil, nil // нет бэкапа
		},
	}

	body := RegisterWebDeviceReq{
		DeviceID:  "device-no-backup",
		PublicKey: testValidHTTPX25519PublicKeyBase64,
	}
	bodyJSON, _ := json.Marshal(body)

	r := buildE2EETestRouter(repo)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/e2ee/device/register", bytes.NewReader(bodyJSON))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp service.RegisterWebDeviceResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.False(t, resp.HasBackup)
	assert.Nil(t, resp.BackupAlgo)
}

func TestRegisterWebDevice_HTTP_SuppressesLegacyCompatibilityBackup(t *testing.T) {
	t.Parallel()

	repo := &e2eeHandlerMockRepo{
		backupFn: func(_ context.Context, _ int64) (*domain.UserKeyBackup, error) {
			return &domain.UserKeyBackup{
				KDFAlgorithm: "pbkdf2-sha256",
				Version:      1,
			}, nil
		},
	}

	body := RegisterWebDeviceReq{
		DeviceID:  "legacy-http-device",
		PublicKey: testValidHTTPX25519PublicKeyBase64,
	}
	bodyJSON, _ := json.Marshal(body)

	r := buildE2EETestRouter(repo)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/e2ee/device/register", bytes.NewReader(bodyJSON))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp service.RegisterWebDeviceResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "legacy-http-device", resp.DeviceID)
	assert.False(t, resp.HasBackup)
	assert.Nil(t, resp.BackupAlgo)
	assert.Nil(t, resp.Backup)
}

func TestRegisterWebDevice_HTTP_MissingDeviceIDReturns400(t *testing.T) {
	t.Parallel()

	r := buildE2EETestRouter(&e2eeHandlerMockRepo{})
	body := `{"public_key":"somekey"}` // device_id отсутствует

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/e2ee/device/register", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRegisterWebDevice_HTTP_MissingPublicKeyReturns400(t *testing.T) {
	t.Parallel()

	r := buildE2EETestRouter(&e2eeHandlerMockRepo{})
	body := `{"device_id":"valid-uuid"}` // public_key отсутствует

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/e2ee/device/register", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRegisterWebDevice_HTTP_InvalidCanonicalPublicKeyReturns400(t *testing.T) {
	t.Parallel()

	r := buildE2EETestRouter(&e2eeHandlerMockRepo{})
	body := `{"device_id":"valid-uuid","public_key":"broken"}`

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/e2ee/device/register", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRegisterWebDevice_HTTP_InvalidJSONReturns400(t *testing.T) {
	t.Parallel()

	r := buildE2EETestRouter(&e2eeHandlerMockRepo{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/e2ee/device/register", bytes.NewBufferString("{invalid json"))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRegisterWebDevice_HTTP_RepoErrorReturns500(t *testing.T) {
	t.Parallel()

	repo := &e2eeHandlerMockRepo{
		registerFn: func(_ context.Context, _ int64, _, _ string, _, _ *string, _ *domain.ClientMetadata) error {
			return fmt.Errorf("db write failed")
		},
	}

	body := RegisterWebDeviceReq{
		DeviceID:  "uuid-err",
		PublicKey: testValidHTTPX25519PublicKeyBase64,
	}
	bodyJSON, _ := json.Marshal(body)

	r := buildE2EETestRouter(repo)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/e2ee/device/register", bytes.NewReader(bodyJSON))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestRegisterWebDevice_HTTP_PlatformAndDeviceNamePassed(t *testing.T) {
	t.Parallel()

	var gotDeviceName *string
	var gotPlatform *string

	repo := &e2eeHandlerMockRepo{
		registerFn: func(_ context.Context, _ int64, _, _ string, dn, plat *string, _ *domain.ClientMetadata) error {
			gotDeviceName = dn
			gotPlatform = plat
			return nil
		},
	}

	name := "Firefox on Linux"
	plat := "web"
	body := RegisterWebDeviceReq{
		DeviceID:   "full-device",
		PublicKey:  testValidHTTPX25519PublicKeyBase64,
		DeviceName: &name,
		Platform:   &plat,
	}
	bodyJSON, _ := json.Marshal(body)

	r := buildE2EETestRouter(repo)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/e2ee/device/register", bytes.NewReader(bodyJSON))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, gotDeviceName)
	assert.Equal(t, "Firefox on Linux", *gotDeviceName)
	require.NotNil(t, gotPlatform)
	assert.Equal(t, "web", *gotPlatform)
}

func TestRegisterWebDevice_HTTP_UserIDFromContext(t *testing.T) {
	t.Parallel()

	var capturedUserID int64
	repo := &e2eeHandlerMockRepo{
		registerFn: func(_ context.Context, uid int64, _, _ string, _, _ *string, _ *domain.ClientMetadata) error {
			capturedUserID = uid
			return nil
		},
	}

	body := RegisterWebDeviceReq{DeviceID: "uid-check-dev", PublicKey: testValidHTTPX25519PublicKeyBase64}
	bodyJSON, _ := json.Marshal(body)

	// Используем роутер с конкретным user_id = 77
	gin.SetMode(gin.TestMode)
	svc := service.NewE2EEService(repo, zap.NewNop())
	h := NewE2EEHandler(svc, zap.NewNop())
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("user_id", int64(77))
		c.Next()
	})
	r.POST("/e2ee/device/register", h.RegisterWebDevice)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/e2ee/device/register", bytes.NewReader(bodyJSON))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, int64(77), capturedUserID)
}

func TestRegisterWebDevice_HTTP_IssuesLinkedWebTokensAndRevokesOtherBrowsers(t *testing.T) {
	t.Parallel()

	storage := newMemoryTokenStorage()
	tokenManager := auth.NewTokenManager(&config.JWTConfig{
		Secret:             "test-secret",
		Issuer:             "test-issuer",
		Audience:           "test-audience",
		AccessTokenExpiry:  time.Hour,
		RefreshTokenExpiry: 24 * time.Hour,
	}, storage)

	repo := &e2eeHandlerMockRepo{
		userFn: func(_ context.Context, userID int64) (*domain.User, error) {
			assert.Equal(t, int64(99), userID)
			return &domain.User{
				ID:        99,
				Email:     "user@example.com",
				Username:  "user99",
				FirstName: "Web",
				LastName:  "User",
			}, nil
		},
		revokeFn: func(_ context.Context, userID int64, keepDeviceID string) ([]string, error) {
			assert.Equal(t, int64(99), userID)
			assert.Equal(t, "rotated-web-device", keepDeviceID)
			return []string{"old-browser-device"}, nil
		},
	}

	body := RegisterWebDeviceReq{
		DeviceID:  "rotated-web-device",
		PublicKey: testValidHTTPX25519PublicKeyBase64,
	}
	bodyJSON, _ := json.Marshal(body)

	r := buildE2EETestRouterWithTokenManager(repo, tokenManager)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/e2ee/device/register", bytes.NewReader(bodyJSON))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp service.RegisterWebDeviceResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp.AccessToken)
	assert.NotEmpty(t, resp.RefreshToken)
	assert.Equal(t, "Bearer", resp.TokenType)
	assert.Equal(t, []string{"old-browser-device"}, resp.RevokedDeviceIDs)
	assert.Equal(t, []string{"rotated-web-device"}, storage.otherWebRevocations)

	claims, err := tokenManager.ExtractClaims(resp.AccessToken)
	require.NoError(t, err)
	require.NotNil(t, claims.Context)
	assert.Equal(t, "rotated-web-device", claims.Context.LinkedDeviceID)
	assert.True(t, claims.Context.Features["web_linked_device"])
}

func TestSaveKeyBackup_HTTP_PassesKDFParams(t *testing.T) {
	t.Parallel()

	var saved *domain.UserKeyBackup
	repo := &e2eeHandlerMockRepo{
		upsertBackupFn: func(_ context.Context, backup *domain.UserKeyBackup) error {
			saved = backup
			return nil
		},
	}

	body := `{
		"encrypted_private_key":"ciphertext",
		"salt":"MDEyMzQ1Njc4OWFiY2RlZg==",
		"iv":"MDEyMzQ1Njc4OWFi",
		"kdf_algorithm":"pbkdf2-sha256",
		"kdf_params":{
			"iterations":310000,
			"hash":"sha256",
			"key_length":32
		},
		"version":1
	}`

	r := buildE2EEBackupTestRouter(repo)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/e2ee/backup", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, saved)
	require.NotNil(t, saved.KDFParams)
	require.NotNil(t, saved.KDFParams.Iterations)
	assert.Equal(t, 310000, *saved.KDFParams.Iterations)
	require.NotNil(t, saved.KDFParams.Hash)
	assert.Equal(t, "sha256", *saved.KDFParams.Hash)
	require.NotNil(t, saved.KDFParams.KeyLength)
	assert.Equal(t, 32, *saved.KDFParams.KeyLength)
}
