package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

const (
	testValidX25519PublicKeyBase64 = "MTIzNDU2Nzg5MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTI="
	testLegacyRawBackupCiphertext  = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	testKeyBackupSaltBase64        = "MDEyMzQ1Njc4OWFiY2RlZg=="
	testKeyBackupIVBase64          = "MDEyMzQ1Njc4OWFi"
)

type mockE2EERepo struct {
	upsertPublicKeyFn             func(ctx context.Context, key *domain.UserPublicKey) error
	getKeysByUserFn               func(ctx context.Context, userID int64) (map[string]string, error)
	upsertKeyBackupFn             func(ctx context.Context, backup *domain.UserKeyBackup) error
	getKeyBackupFn                func(ctx context.Context, userID int64) (*domain.UserKeyBackup, error)
	deleteKeyBackupFn             func(ctx context.Context, userID int64) error
	createWebPairingSessionFn     func(ctx context.Context, session *domain.WebPairingSession) error
	getWebPairingBySessionTokenFn func(ctx context.Context, pairingID uuid.UUID, sessionTokenHash string) (*domain.WebPairingSession, error)
	getWebPairingByQRTokenFn      func(ctx context.Context, pairingID uuid.UUID, qrTokenHash string) (*domain.WebPairingSession, error)
	approveWebPairingSessionFn    func(ctx context.Context, pairingID uuid.UUID, userID int64, loginCodeHash *string, deviceName, platform *string) (*domain.WebPairingSession, error)
	rejectWebPairingSessionFn     func(ctx context.Context, pairingID uuid.UUID, userID int64) (*domain.WebPairingSession, error)

	redeemWebPairingSessionFn func(ctx context.Context, pairingID uuid.UUID, sessionTokenHash string) (*domain.WebPairingSession, error)

	updateLinkedDeviceAccessMetaFn       func(ctx context.Context, userID int64, deviceID string, meta *domain.ClientMetadata) error
	listLinkedDevicesFn                  func(ctx context.Context, userID int64) ([]*domain.LinkedDevice, error)
	revokeLinkedDeviceFn                 func(ctx context.Context, userID int64, deviceID string) error
	getUserBasicProfileFn                func(ctx context.Context, userID int64) (*domain.User, error)
	registerWebDeviceFromPasswordLoginFn func(ctx context.Context, userID int64, deviceID, publicKey string, deviceName, platform *string, meta *domain.ClientMetadata) error
	revokeOtherWebDevicesFn              func(ctx context.Context, userID int64, keepDeviceID string) ([]string, error)
	createWebLoginNotificationFn         func(ctx context.Context, userID int64, deviceID string, deviceName, platform *string, meta *domain.ClientMetadata, revokedDeviceIDs []string) error
}

func (m *mockE2EERepo) UpsertPublicKey(ctx context.Context, key *domain.UserPublicKey) error {
	if m.upsertPublicKeyFn != nil {
		return m.upsertPublicKeyFn(ctx, key)
	}
	return nil
}

func (m *mockE2EERepo) GetKeysByUser(ctx context.Context, userID int64) (map[string]string, error) {
	if m.getKeysByUserFn != nil {
		return m.getKeysByUserFn(ctx, userID)
	}
	return nil, nil
}

func (m *mockE2EERepo) UpsertKeyBackup(ctx context.Context, backup *domain.UserKeyBackup) error {
	if m.upsertKeyBackupFn != nil {
		return m.upsertKeyBackupFn(ctx, backup)
	}
	return nil
}

func (m *mockE2EERepo) GetKeyBackup(ctx context.Context, userID int64) (*domain.UserKeyBackup, error) {
	if m.getKeyBackupFn != nil {
		return m.getKeyBackupFn(ctx, userID)
	}
	return nil, nil
}

func (m *mockE2EERepo) DeleteKeyBackup(ctx context.Context, userID int64) error {
	if m.deleteKeyBackupFn != nil {
		return m.deleteKeyBackupFn(ctx, userID)
	}
	return nil
}

func (m *mockE2EERepo) CreateWebPairingSession(ctx context.Context, session *domain.WebPairingSession) error {
	if m.createWebPairingSessionFn != nil {
		return m.createWebPairingSessionFn(ctx, session)
	}
	return nil
}

func (m *mockE2EERepo) GetWebPairingSessionBySessionToken(ctx context.Context, pairingID uuid.UUID, sessionTokenHash string) (*domain.WebPairingSession, error) {
	if m.getWebPairingBySessionTokenFn != nil {
		return m.getWebPairingBySessionTokenFn(ctx, pairingID, sessionTokenHash)
	}
	return nil, nil
}

func (m *mockE2EERepo) GetWebPairingSessionByQRToken(ctx context.Context, pairingID uuid.UUID, qrTokenHash string) (*domain.WebPairingSession, error) {
	if m.getWebPairingByQRTokenFn != nil {
		return m.getWebPairingByQRTokenFn(ctx, pairingID, qrTokenHash)
	}
	return nil, nil
}

func (m *mockE2EERepo) ApproveWebPairingSession(ctx context.Context, pairingID uuid.UUID, userID int64, loginCodeHash *string, deviceName, platform *string) (*domain.WebPairingSession, error) {
	if m.approveWebPairingSessionFn != nil {
		return m.approveWebPairingSessionFn(ctx, pairingID, userID, loginCodeHash, deviceName, platform)
	}
	return nil, nil
}

func (m *mockE2EERepo) RejectWebPairingSession(ctx context.Context, pairingID uuid.UUID, userID int64) (*domain.WebPairingSession, error) {
	if m.rejectWebPairingSessionFn != nil {
		return m.rejectWebPairingSessionFn(ctx, pairingID, userID)
	}
	return nil, nil
}

func (m *mockE2EERepo) RedeemWebPairingSession(ctx context.Context, pairingID uuid.UUID, sessionTokenHash string) (*domain.WebPairingSession, error) {
	if m.redeemWebPairingSessionFn != nil {
		return m.redeemWebPairingSessionFn(ctx, pairingID, sessionTokenHash)
	}
	return nil, nil
}

func (m *mockE2EERepo) UpdateLinkedDeviceAccessMetadata(ctx context.Context, userID int64, deviceID string, meta *domain.ClientMetadata) error {
	if m.updateLinkedDeviceAccessMetaFn != nil {
		return m.updateLinkedDeviceAccessMetaFn(ctx, userID, deviceID, meta)
	}
	return nil
}

func (m *mockE2EERepo) ListLinkedDevices(ctx context.Context, userID int64) ([]*domain.LinkedDevice, error) {
	if m.listLinkedDevicesFn != nil {
		return m.listLinkedDevicesFn(ctx, userID)
	}
	return nil, nil
}

func (m *mockE2EERepo) RevokeLinkedDevice(ctx context.Context, userID int64, deviceID string) error {
	if m.revokeLinkedDeviceFn != nil {
		return m.revokeLinkedDeviceFn(ctx, userID, deviceID)
	}
	return nil
}

func (m *mockE2EERepo) GetUserBasicProfile(ctx context.Context, userID int64) (*domain.User, error) {
	if m.getUserBasicProfileFn != nil {
		return m.getUserBasicProfileFn(ctx, userID)
	}
	return nil, nil
}

func (m *mockE2EERepo) RegisterWebDeviceFromPasswordLogin(ctx context.Context, userID int64, deviceID, publicKey string, deviceName, platform *string, meta *domain.ClientMetadata) error {
	if m.registerWebDeviceFromPasswordLoginFn != nil {
		return m.registerWebDeviceFromPasswordLoginFn(ctx, userID, deviceID, publicKey, deviceName, platform, meta)
	}
	return nil
}

func (m *mockE2EERepo) RevokeOtherWebDevices(ctx context.Context, userID int64, keepDeviceID string) ([]string, error) {
	if m.revokeOtherWebDevicesFn != nil {
		return m.revokeOtherWebDevicesFn(ctx, userID, keepDeviceID)
	}
	return nil, nil
}

func (m *mockE2EERepo) CreateWebLoginNotification(ctx context.Context, userID int64, deviceID string, deviceName, platform *string, meta *domain.ClientMetadata, revokedDeviceIDs []string) error {
	if m.createWebLoginNotificationFn != nil {
		return m.createWebLoginNotificationFn(ctx, userID, deviceID, deviceName, platform, meta, revokedDeviceIDs)
	}
	return nil
}

func TestCreateWebPairingSession_PersistsMetadata(t *testing.T) {
	t.Parallel()

	var saved *domain.WebPairingSession
	repo := &mockE2EERepo{
		createWebPairingSessionFn: func(ctx context.Context, session *domain.WebPairingSession) error {
			saved = session
			return nil
		},
	}

	svc := NewE2EEService(repo, zap.NewNop())
	meta := &domain.ClientMetadata{
		IP:        strPtr("203.0.113.10"),
		Country:   strPtr("KZ"),
		City:      strPtr("Almaty"),
		UserAgent: strPtr("Mozilla/5.0 Test"),
	}

	resp, err := svc.CreateWebPairingSession(context.Background(), &CreateWebPairingSessionRequest{
		WebDeviceID:  "web-device-1",
		WebPublicKey: testValidX25519PublicKeyBase64,
	}, meta)
	require.NoError(t, err)
	require.NotNil(t, saved)

	assert.Equal(t, domain.WebPairingStatusPending, resp.Status)
	assert.Equal(t, "web-device-1", saved.WebDeviceID)
	assert.Equal(t, testValidX25519PublicKeyBase64, saved.WebPublicKey)
	assert.Equal(t, meta.IP, saved.RequestIP)
	assert.Equal(t, meta.Country, saved.RequestCountry)
	assert.Equal(t, meta.City, saved.RequestCity)
	assert.Equal(t, meta.UserAgent, saved.RequestUserAgent)
	assert.NotEmpty(t, resp.QRToken)
	assert.NotEmpty(t, resp.SessionToken)
	assert.NotEmpty(t, saved.QRTokenHash)
	assert.NotEmpty(t, saved.SessionTokenHash)
}

func TestApproveWebPairingSession_RejectsMismatchedDevice(t *testing.T) {
	t.Parallel()

	pairingID := uuid.New()
	repo := &mockE2EERepo{
		getWebPairingByQRTokenFn: func(ctx context.Context, gotPairingID uuid.UUID, qrTokenHash string) (*domain.WebPairingSession, error) {
			assert.Equal(t, pairingID, gotPairingID)
			assert.Equal(t, hashSecret("qr-token"), qrTokenHash)
			return &domain.WebPairingSession{
				ID:           pairingID,
				WebDeviceID:  "expected-device",
				WebPublicKey: "expected-public-key",
				Status:       domain.WebPairingStatusPending,
				ExpiresAt:    time.Now().Add(time.Minute),
			}, nil
		},
	}

	svc := NewE2EEService(repo, zap.NewNop())
	_, err := svc.ApproveWebPairingSession(context.Background(), 42, pairingID, &ApproveWebPairingRequest{
		QRToken:     "qr-token",
		WebDeviceID: "another-device",
	})
	require.Error(t, err)

	appErr := apperrors.GetAppError(err)
	require.NotNil(t, appErr)
	assert.Equal(t, apperrors.ErrCodeBadRequest, appErr.Code)
}

func TestRedeemWebPairingSession_UpdatesAccessMetadata(t *testing.T) {
	t.Parallel()

	pairingID := uuid.New()
	var updatedMeta *domain.ClientMetadata
	repo := &mockE2EERepo{
		redeemWebPairingSessionFn: func(ctx context.Context, gotPairingID uuid.UUID, sessionTokenHash string) (*domain.WebPairingSession, error) {
			assert.Equal(t, pairingID, gotPairingID)
			assert.Equal(t, hashSecret("session-token"), sessionTokenHash)
			userID := int64(77)
			return &domain.WebPairingSession{
				ID:          pairingID,
				UserID:      &userID,
				WebDeviceID: "web-device-1",
				Status:      domain.WebPairingStatusRedeemed,
			}, nil
		},
		getUserBasicProfileFn: func(ctx context.Context, userID int64) (*domain.User, error) {
			assert.Equal(t, int64(77), userID)
			return &domain.User{
				ID:        77,
				Email:     "askar@example.com",
				Username:  "askar",
				FirstName: "Askar",
				LastName:  "Abyl",
			}, nil
		},
		updateLinkedDeviceAccessMetaFn: func(ctx context.Context, userID int64, deviceID string, meta *domain.ClientMetadata) error {
			assert.Equal(t, int64(77), userID)
			assert.Equal(t, "web-device-1", deviceID)
			updatedMeta = meta
			return nil
		},
	}

	svc := NewE2EEService(repo, zap.NewNop())
	meta := &domain.ClientMetadata{
		IP:        strPtr("198.51.100.5"),
		Country:   strPtr("KZ"),
		City:      strPtr("Astana"),
		UserAgent: strPtr("Chrome Test UA"),
	}

	resp, err := svc.RedeemWebPairingSession(context.Background(), pairingID, &RedeemWebPairingSessionRequest{
		SessionToken: "session-token",
	}, meta)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, updatedMeta)

	assert.Equal(t, "web-device-1", resp.WebDeviceID)
	assert.Equal(t, meta.IP, updatedMeta.IP)
	assert.Equal(t, meta.Country, updatedMeta.Country)
	assert.Equal(t, meta.City, updatedMeta.City)
	assert.Equal(t, meta.UserAgent, updatedMeta.UserAgent)
}

func TestRedeemWebPairingSession_ExpiredMappedToBadRequest(t *testing.T) {
	t.Parallel()

	pairingID := uuid.New()
	repo := &mockE2EERepo{
		redeemWebPairingSessionFn: func(ctx context.Context, gotPairingID uuid.UUID, sessionTokenHash string) (*domain.WebPairingSession, error) {
			return nil, errString("web pairing session expired")
		},
	}

	svc := NewE2EEService(repo, zap.NewNop())
	_, err := svc.RedeemWebPairingSession(context.Background(), pairingID, &RedeemWebPairingSessionRequest{
		SessionToken: "session-token",
	}, nil)
	require.Error(t, err)

	appErr := apperrors.GetAppError(err)
	require.NotNil(t, appErr)
	assert.Equal(t, apperrors.ErrCodeBadRequest, appErr.Code)
	assert.Equal(t, "QR code has expired. Please generate a new QR code and try again.", appErr.Message)
}

func TestRedeemWebPairingSession_NotApprovedMappedToConflict(t *testing.T) {
	t.Parallel()

	pairingID := uuid.New()
	repo := &mockE2EERepo{
		redeemWebPairingSessionFn: func(ctx context.Context, gotPairingID uuid.UUID, sessionTokenHash string) (*domain.WebPairingSession, error) {
			return nil, errString("web pairing session is not approved yet")
		},
	}

	svc := NewE2EEService(repo, zap.NewNop())
	_, err := svc.RedeemWebPairingSession(context.Background(), pairingID, &RedeemWebPairingSessionRequest{
		SessionToken: "session-token",
	}, nil)
	require.Error(t, err)

	appErr := apperrors.GetAppError(err)
	require.NotNil(t, appErr)
	assert.Equal(t, apperrors.ErrCodeConflict, appErr.Code)
	assert.Equal(t, "Please confirm the login from your mobile app first.", appErr.Message)
}

func TestApproveWebPairingSession_BoundToAnotherUserMappedToForbidden(t *testing.T) {
	t.Parallel()

	pairingID := uuid.New()
	repo := &mockE2EERepo{
		getWebPairingByQRTokenFn: func(ctx context.Context, gotPairingID uuid.UUID, qrTokenHash string) (*domain.WebPairingSession, error) {
			return &domain.WebPairingSession{
				ID:           pairingID,
				WebDeviceID:  "expected-device",
				WebPublicKey: "expected-public-key",
				Status:       domain.WebPairingStatusPending,
				ExpiresAt:    time.Now().Add(time.Minute),
			}, nil
		},
		approveWebPairingSessionFn: func(ctx context.Context, pairingID uuid.UUID, userID int64, loginCodeHash *string, deviceName, platform *string) (*domain.WebPairingSession, error) {
			return nil, errString("web pairing session already bound to another user")
		},
	}

	svc := NewE2EEService(repo, zap.NewNop())
	_, err := svc.ApproveWebPairingSession(context.Background(), 42, pairingID, &ApproveWebPairingRequest{
		QRToken:      "qr-token",
		WebDeviceID:  "expected-device",
		WebPublicKey: "expected-public-key",
	})
	require.Error(t, err)

	appErr := apperrors.GetAppError(err)
	require.NotNil(t, appErr)
	assert.Equal(t, apperrors.ErrCodeForbidden, appErr.Code)
	assert.Equal(t, "This web login request belongs to another user.", appErr.Message)
}

func TestRevokeLinkedDevice_NotFoundMappedToAppError(t *testing.T) {
	t.Parallel()

	repo := &mockE2EERepo{
		revokeLinkedDeviceFn: func(ctx context.Context, userID int64, deviceID string) error {
			return assert.AnError
		},
	}

	svc := NewE2EEService(repo, zap.NewNop())
	err := svc.RevokeLinkedDevice(context.Background(), 1, "device-1")
	require.Error(t, err)
	appErr := apperrors.GetAppError(err)
	require.NotNil(t, appErr)
	assert.Equal(t, apperrors.ErrCodeInternal, appErr.Code)
}

func TestRevokeLinkedDevice_NotFoundStringMappedToNotFound(t *testing.T) {
	t.Parallel()

	repo := &mockE2EERepo{
		revokeLinkedDeviceFn: func(ctx context.Context, userID int64, deviceID string) error {
			return errString("linked device not found")
		},
	}

	svc := NewE2EEService(repo, zap.NewNop())
	err := svc.RevokeLinkedDevice(context.Background(), 1, "device-1")
	require.Error(t, err)
	appErr := apperrors.GetAppError(err)
	require.NotNil(t, appErr)
	assert.Equal(t, apperrors.ErrCodeNotFound, appErr.Code)
}

type errString string

func (e errString) Error() string { return string(e) }

func strPtr(v string) *string { return &v }

// ============================================================
// RegisterWebDevice tests
// ============================================================

func TestRegisterWebDevice_SuccessWithBackup(t *testing.T) {
	t.Parallel()

	const (
		userID   = int64(42)
		deviceID = "web-device-uuid-1234"
		pubKey   = testValidX25519PublicKeyBase64
	)
	var (
		capturedDeviceID  string
		capturedPublicKey string
		capturedPlatform  *string
		capturedRevoked   []string
	)

	repo := &mockE2EERepo{
		registerWebDeviceFromPasswordLoginFn: func(ctx context.Context, uid int64, did, pk string, dn, plat *string, meta *domain.ClientMetadata) error {
			assert.Equal(t, userID, uid)
			capturedDeviceID = did
			capturedPublicKey = pk
			capturedPlatform = plat
			return nil
		},
		revokeOtherWebDevicesFn: func(ctx context.Context, uid int64, keepDeviceID string) ([]string, error) {
			assert.Equal(t, userID, uid)
			assert.Equal(t, deviceID, keepDeviceID)
			return []string{"old-web-1", "old-web-2"}, nil
		},
		getKeyBackupFn: func(ctx context.Context, uid int64) (*domain.UserKeyBackup, error) {
			assert.Equal(t, userID, uid)
			algo := "argon2id"
			return &domain.UserKeyBackup{
				UserID:              uid,
				EncryptedPrivateKey: "ciphertext",
				Salt:                "salt",
				IV:                  "iv",
				KDFAlgorithm:        algo,
			}, nil
		},
		getUserBasicProfileFn: func(ctx context.Context, uid int64) (*domain.User, error) {
			assert.Equal(t, userID, uid)
			return &domain.User{
				ID:        userID,
				Email:     "askar@example.com",
				Username:  "askar",
				FirstName: "Askar",
				LastName:  "Abylkhaiyrov",
			}, nil
		},
		createWebLoginNotificationFn: func(ctx context.Context, uid int64, deviceID string, deviceName, platform *string, meta *domain.ClientMetadata, revokedDeviceIDs []string) error {
			assert.Equal(t, userID, uid)
			assert.Equal(t, "web-device-uuid-1234", deviceID)
			capturedRevoked = append([]string(nil), revokedDeviceIDs...)
			return nil
		},
	}

	svc := NewE2EEService(repo, zap.NewNop())
	resp, err := svc.RegisterWebDevice(context.Background(), userID, &RegisterWebDeviceRequest{
		DeviceID:  deviceID,
		PublicKey: pubKey,
	}, &domain.ClientMetadata{IP: strPtr("1.2.3.4")})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, deviceID, resp.DeviceID)
	assert.Equal(t, deviceID, capturedDeviceID)
	assert.Equal(t, pubKey, capturedPublicKey)
	assert.True(t, resp.HasBackup)
	require.NotNil(t, resp.BackupAlgo)
	assert.Equal(t, "argon2id", *resp.BackupAlgo)
	require.NotNil(t, resp.Backup)
	assert.Equal(t, "ciphertext", resp.Backup.EncryptedPrivateKey)
	// platform defaults to "web" when not provided
	require.NotNil(t, capturedPlatform)
	assert.Equal(t, "web", *capturedPlatform)
	assert.Equal(t, []string{"old-web-1", "old-web-2"}, resp.RevokedDeviceIDs)
	assert.Equal(t, []string{"old-web-1", "old-web-2"}, capturedRevoked)
	assert.True(t, resp.ReplaceOtherWebSessions)
	require.NotNil(t, resp.User)
	assert.Equal(t, "askar@example.com", resp.User.Email)
}

func TestRegisterWebDevice_SuccessWithoutBackup(t *testing.T) {
	t.Parallel()

	repo := &mockE2EERepo{
		// registerWebDeviceFromPasswordLoginFn is nil → returns nil (success)
		getKeyBackupFn: func(ctx context.Context, uid int64) (*domain.UserKeyBackup, error) {
			return nil, nil // no backup exists
		},
	}

	svc := NewE2EEService(repo, zap.NewNop())
	resp, err := svc.RegisterWebDevice(context.Background(), 1, &RegisterWebDeviceRequest{
		DeviceID:  "device-no-backup",
		PublicKey: testValidX25519PublicKeyBase64,
	}, nil)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "device-no-backup", resp.DeviceID)
	assert.False(t, resp.HasBackup)
	assert.Nil(t, resp.BackupAlgo)
}

func TestRegisterWebDevice_EmptyDeviceIDReturnsError(t *testing.T) {
	t.Parallel()

	svc := NewE2EEService(&mockE2EERepo{}, zap.NewNop())
	_, err := svc.RegisterWebDevice(context.Background(), 1, &RegisterWebDeviceRequest{
		DeviceID:  "   ", // whitespace only
		PublicKey: testValidX25519PublicKeyBase64,
	}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "device_id")
}

func TestRegisterWebDevice_EmptyPublicKeyReturnsError(t *testing.T) {
	t.Parallel()

	svc := NewE2EEService(&mockE2EERepo{}, zap.NewNop())
	_, err := svc.RegisterWebDevice(context.Background(), 1, &RegisterWebDeviceRequest{
		DeviceID:  "valid-device-id",
		PublicKey: "",
	}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "public_key")
}

func TestRegisterWebDevice_NilRequestReturnsError(t *testing.T) {
	t.Parallel()

	svc := NewE2EEService(&mockE2EERepo{}, zap.NewNop())
	_, err := svc.RegisterWebDevice(context.Background(), 1, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "request body")
}

func TestRegisterWebDevice_PlatformNormalized(t *testing.T) {
	t.Parallel()

	var capturedPlatform *string
	repo := &mockE2EERepo{
		registerWebDeviceFromPasswordLoginFn: func(_ context.Context, _ int64, _, _ string, _, plat *string, _ *domain.ClientMetadata) error {
			capturedPlatform = plat
			return nil
		},
	}

	webPlatform := "  WEB  "
	svc := NewE2EEService(repo, zap.NewNop())
	_, err := svc.RegisterWebDevice(context.Background(), 1, &RegisterWebDeviceRequest{
		DeviceID:  "dev-1",
		PublicKey: testValidX25519PublicKeyBase64,
		Platform:  &webPlatform,
	}, nil)

	require.NoError(t, err)
	require.NotNil(t, capturedPlatform)
	assert.Equal(t, "web", *capturedPlatform, "platform must be lowercased and trimmed")
}

func TestRegisterWebDevice_DeviceNameTrimmed(t *testing.T) {
	t.Parallel()

	var capturedDeviceName *string
	repo := &mockE2EERepo{
		registerWebDeviceFromPasswordLoginFn: func(_ context.Context, _ int64, _, _ string, dn, _ *string, _ *domain.ClientMetadata) error {
			capturedDeviceName = dn
			return nil
		},
	}

	rawName := "  Chrome on MacOS  "
	svc := NewE2EEService(repo, zap.NewNop())
	_, err := svc.RegisterWebDevice(context.Background(), 1, &RegisterWebDeviceRequest{
		DeviceID:   "dev-1",
		PublicKey:  testValidX25519PublicKeyBase64,
		DeviceName: &rawName,
	}, nil)

	require.NoError(t, err)
	require.NotNil(t, capturedDeviceName)
	assert.Equal(t, "Chrome on MacOS", *capturedDeviceName)
}

func TestRegisterWebDevice_RepoErrorPropagated(t *testing.T) {
	t.Parallel()

	repo := &mockE2EERepo{
		registerWebDeviceFromPasswordLoginFn: func(_ context.Context, _ int64, _, _ string, _, _ *string, _ *domain.ClientMetadata) error {
			return errString("db connection lost")
		},
	}

	svc := NewE2EEService(repo, zap.NewNop())
	_, err := svc.RegisterWebDevice(context.Background(), 1, &RegisterWebDeviceRequest{
		DeviceID:  "dev-err",
		PublicKey: testValidX25519PublicKeyBase64,
	}, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "db connection lost")
}

func TestRegisterWebDevice_BackupCheckErrorDoesNotFailRegistration(t *testing.T) {
	t.Parallel()

	// GetKeyBackup failing should NOT fail the whole operation —
	// device registration succeeds, has_backup simply returns false.
	repo := &mockE2EERepo{
		// registerWebDeviceFromPasswordLoginFn is nil → success
		getKeyBackupFn: func(_ context.Context, _ int64) (*domain.UserKeyBackup, error) {
			return nil, errString("redis timeout")
		},
	}

	svc := NewE2EEService(repo, zap.NewNop())
	resp, err := svc.RegisterWebDevice(context.Background(), 5, &RegisterWebDeviceRequest{
		DeviceID:  "dev-fallback",
		PublicKey: testValidX25519PublicKeyBase64,
	}, nil)

	require.NoError(t, err, "registration must succeed even when backup check fails")
	require.NotNil(t, resp)
	assert.False(t, resp.HasBackup, "has_backup must be false when backup check fails")
	assert.Nil(t, resp.BackupAlgo)
}

func TestRegisterWebDevice_CanDisableWebSessionReplacementAndBackupPayload(t *testing.T) {
	t.Parallel()

	repo := &mockE2EERepo{
		revokeOtherWebDevicesFn: func(_ context.Context, _ int64, _ string) ([]string, error) {
			t.Fatalf("revokeOtherWebDevices must not be called when disabled")
			return nil, nil
		},
		getKeyBackupFn: func(_ context.Context, _ int64) (*domain.UserKeyBackup, error) {
			t.Fatalf("getKeyBackup must not be called when include_backup is false")
			return nil, nil
		},
	}

	replaceOther := false
	includeBackup := false
	svc := NewE2EEService(repo, zap.NewNop())
	resp, err := svc.RegisterWebDevice(context.Background(), 15, &RegisterWebDeviceRequest{
		DeviceID:                "dev-15",
		PublicKey:               testValidX25519PublicKeyBase64,
		ReplaceOtherWebSessions: &replaceOther,
		IncludeBackup:           &includeBackup,
	}, nil)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.False(t, resp.ReplaceOtherWebSessions)
	assert.False(t, resp.HasBackup)
	assert.Nil(t, resp.Backup)
	assert.Empty(t, resp.RevokedDeviceIDs)
}

func TestRegisterWebDevice_MetadataPassedThrough(t *testing.T) {
	t.Parallel()

	var capturedMeta *domain.ClientMetadata
	repo := &mockE2EERepo{
		registerWebDeviceFromPasswordLoginFn: func(_ context.Context, _ int64, _, _ string, _, _ *string, meta *domain.ClientMetadata) error {
			capturedMeta = meta
			return nil
		},
	}

	inputMeta := &domain.ClientMetadata{
		IP:        strPtr("10.0.0.1"),
		Country:   strPtr("KZ"),
		City:      strPtr("Almaty"),
		UserAgent: strPtr("Go-test/1.0"),
	}

	svc := NewE2EEService(repo, zap.NewNop())
	_, err := svc.RegisterWebDevice(context.Background(), 7, &RegisterWebDeviceRequest{
		DeviceID:  "meta-dev",
		PublicKey: testValidX25519PublicKeyBase64,
	}, inputMeta)

	require.NoError(t, err)
	require.NotNil(t, capturedMeta)
	assert.Equal(t, inputMeta.IP, capturedMeta.IP)
	assert.Equal(t, inputMeta.Country, capturedMeta.Country)
	assert.Equal(t, inputMeta.City, capturedMeta.City)
	assert.Equal(t, inputMeta.UserAgent, capturedMeta.UserAgent)
}

func TestSaveKeyBackup_FillsLegacyPBKDF2ParamsWhenMissing(t *testing.T) {
	t.Parallel()

	var saved *domain.UserKeyBackup
	repo := &mockE2EERepo{
		upsertKeyBackupFn: func(_ context.Context, backup *domain.UserKeyBackup) error {
			saved = backup
			return nil
		},
	}

	svc := NewE2EEService(repo, zap.NewNop())
	err := svc.SaveKeyBackup(context.Background(), 77, &SaveKeyBackupRequest{
		EncryptedPrivateKey: "ciphertext",
		Salt:                testKeyBackupSaltBase64,
		IV:                  testKeyBackupIVBase64,
		KDFAlgorithm:        "pbkdf2-sha256",
		Version:             1,
	})

	require.NoError(t, err)
	require.NotNil(t, saved)
	assert.Equal(t, "pbkdf2-sha256", saved.KDFAlgorithm)
	require.NotNil(t, saved.KDFParams)
	require.NotNil(t, saved.KDFParams.Iterations)
	assert.Equal(t, legacyPBKDF2SHA256Iterations, *saved.KDFParams.Iterations)
	require.NotNil(t, saved.KDFParams.Hash)
	assert.Equal(t, "sha256", *saved.KDFParams.Hash)
	require.NotNil(t, saved.KDFParams.KeyLength)
	assert.Equal(t, legacyPBKDF2SHA256DerivedKeyLength, *saved.KDFParams.KeyLength)
}

func TestGetKeyBackup_FillsLegacyPBKDF2ParamsWhenMissing(t *testing.T) {
	t.Parallel()

	repo := &mockE2EERepo{
		getKeyBackupFn: func(_ context.Context, userID int64) (*domain.UserKeyBackup, error) {
			return &domain.UserKeyBackup{
				UserID:              userID,
				EncryptedPrivateKey: "ciphertext",
				Salt:                "salt",
				IV:                  "iv",
				KDFAlgorithm:        "pbkdf2-sha256",
				Version:             1,
			}, nil
		},
	}

	svc := NewE2EEService(repo, zap.NewNop())
	backup, err := svc.GetKeyBackup(context.Background(), 55)

	require.NoError(t, err)
	require.NotNil(t, backup)
	require.NotNil(t, backup.KDFParams)
	require.NotNil(t, backup.KDFParams.Iterations)
	assert.Equal(t, legacyPBKDF2SHA256Iterations, *backup.KDFParams.Iterations)
	require.NotNil(t, backup.KDFParams.Hash)
	assert.Equal(t, "sha256", *backup.KDFParams.Hash)
}

func TestSaveKeyBackup_InfersLegacyRawBackupFormatFromCiphertextLength(t *testing.T) {
	t.Parallel()

	var saved *domain.UserKeyBackup
	repo := &mockE2EERepo{
		upsertKeyBackupFn: func(_ context.Context, backup *domain.UserKeyBackup) error {
			saved = backup
			return nil
		},
	}

	svc := NewE2EEService(repo, zap.NewNop())
	err := svc.SaveKeyBackup(context.Background(), 77, &SaveKeyBackupRequest{
		EncryptedPrivateKey: testLegacyRawBackupCiphertext,
		Salt:                testKeyBackupSaltBase64,
		IV:                  testKeyBackupIVBase64,
		KDFAlgorithm:        "pbkdf2-sha256",
		Version:             1,
	})

	require.NoError(t, err)
	require.NotNil(t, saved)
	assert.Equal(t, legacyRawBackupKeyFormat, saved.BackupKeyFormat)
	assert.Equal(t, canonicalBackupKeyScope, saved.BackupKeyScope)
}

func TestGetKeyBackup_OverridesBackfilledPKCS8WhenLegacyCiphertextLooksRaw(t *testing.T) {
	t.Parallel()

	repo := &mockE2EERepo{
		getKeyBackupFn: func(_ context.Context, userID int64) (*domain.UserKeyBackup, error) {
			return &domain.UserKeyBackup{
				UserID:              userID,
				EncryptedPrivateKey: testLegacyRawBackupCiphertext,
				Salt:                "salt",
				IV:                  "iv",
				KDFAlgorithm:        "pbkdf2-sha256",
				Version:             1,
				BackupKeyFormat:     canonicalBackupKeyFormat,
			}, nil
		},
	}

	svc := NewE2EEService(repo, zap.NewNop())
	backup, err := svc.GetKeyBackup(context.Background(), 55)

	require.NoError(t, err)
	require.NotNil(t, backup)
	assert.Equal(t, legacyRawBackupKeyFormat, backup.BackupKeyFormat)
	assert.Equal(t, canonicalBackupKeyScope, backup.BackupKeyScope)
}

func TestRegisterWebDevice_SuppressesLegacyCompatibilityBackupInPasswordBootstrap(t *testing.T) {
	t.Parallel()

	repo := &mockE2EERepo{
		getKeyBackupFn: func(_ context.Context, uid int64) (*domain.UserKeyBackup, error) {
			return &domain.UserKeyBackup{
				UserID:              uid,
				EncryptedPrivateKey: "ciphertext",
				Salt:                "salt",
				IV:                  "iv",
				KDFAlgorithm:        "pbkdf2-sha256",
				Version:             1,
			}, nil
		},
	}

	svc := NewE2EEService(repo, zap.NewNop())
	resp, err := svc.RegisterWebDevice(context.Background(), 100, &RegisterWebDeviceRequest{
		DeviceID:  "legacy-kdf-dev",
		PublicKey: testValidX25519PublicKeyBase64,
	}, nil)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.False(t, resp.HasBackup)
	assert.Nil(t, resp.BackupAlgo)
	assert.Nil(t, resp.Backup)
}

func TestSaveKeyBackup_FillsCanonicalBackupMetadataWhenMissing(t *testing.T) {
	t.Parallel()

	var saved *domain.UserKeyBackup
	repo := &mockE2EERepo{
		upsertKeyBackupFn: func(_ context.Context, backup *domain.UserKeyBackup) error {
			saved = backup
			return nil
		},
	}

	svc := NewE2EEService(repo, zap.NewNop())
	err := svc.SaveKeyBackup(context.Background(), 7, &SaveKeyBackupRequest{
		EncryptedPrivateKey: "ciphertext",
		Salt:                testKeyBackupSaltBase64,
		IV:                  testKeyBackupIVBase64,
		KDFAlgorithm:        "argon2id",
		Version:             1,
	})

	require.NoError(t, err)
	require.NotNil(t, saved)
	assert.Equal(t, canonicalDeviceKeyType, saved.BackupKeyType)
	assert.Equal(t, canonicalBackupKeyFormat, saved.BackupKeyFormat)
	assert.Equal(t, canonicalBackupKeyScope, saved.BackupKeyScope)
	assert.Equal(t, canonicalBackupFormatVersion, saved.BackupFormatVersion)
	assert.Equal(t, canonicalBackupCipher, saved.CipherAlgorithm)
	assert.True(t, saved.CipherTagEmbedded)
	assert.Equal(t, canonicalBackupAADMode, saved.AADMode)
	assert.Equal(t, canonicalPasswordProcessing, saved.PasswordProcessing)
}

func TestGetKeyBackup_FillsCanonicalBackupMetadataWhenMissing(t *testing.T) {
	t.Parallel()

	repo := &mockE2EERepo{
		getKeyBackupFn: func(_ context.Context, userID int64) (*domain.UserKeyBackup, error) {
			return &domain.UserKeyBackup{
				UserID:              userID,
				EncryptedPrivateKey: "ciphertext",
				Salt:                testKeyBackupSaltBase64,
				IV:                  testKeyBackupIVBase64,
				KDFAlgorithm:        "argon2id",
				Version:             1,
			}, nil
		},
	}

	svc := NewE2EEService(repo, zap.NewNop())
	backup, err := svc.GetKeyBackup(context.Background(), 9)

	require.NoError(t, err)
	require.NotNil(t, backup)
	assert.Equal(t, canonicalDeviceKeyType, backup.BackupKeyType)
	assert.Equal(t, canonicalBackupKeyFormat, backup.BackupKeyFormat)
	assert.Equal(t, canonicalBackupKeyScope, backup.BackupKeyScope)
	assert.Equal(t, canonicalBackupFormatVersion, backup.BackupFormatVersion)
	assert.Equal(t, canonicalBackupCipher, backup.CipherAlgorithm)
	assert.True(t, backup.CipherTagEmbedded)
	assert.Equal(t, canonicalBackupAADMode, backup.AADMode)
	assert.Equal(t, canonicalPasswordProcessing, backup.PasswordProcessing)
}

func TestUploadPublicKey_RejectsInvalidCanonicalPublicKey(t *testing.T) {
	t.Parallel()

	svc := NewE2EEService(&mockE2EERepo{}, zap.NewNop())
	err := svc.UploadPublicKey(context.Background(), 1, "device-1", "not-base64")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "public_key")
	assert.Contains(t, err.Error(), canonicalDeviceKeyType)
}

func TestRegisterWebDevice_RejectsInvalidCanonicalPublicKey(t *testing.T) {
	t.Parallel()

	svc := NewE2EEService(&mockE2EERepo{}, zap.NewNop())
	_, err := svc.RegisterWebDevice(context.Background(), 1, &RegisterWebDeviceRequest{
		DeviceID:  "device-invalid-key",
		PublicKey: "short",
	}, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "public_key")
	assert.Contains(t, err.Error(), canonicalDeviceKeyType)
}

func TestCreateWebPairingSession_RejectsInvalidCanonicalPublicKey(t *testing.T) {
	t.Parallel()

	svc := NewE2EEService(&mockE2EERepo{}, zap.NewNop())
	_, err := svc.CreateWebPairingSession(context.Background(), &CreateWebPairingSessionRequest{
		WebDeviceID:  "web-device-1",
		WebPublicKey: "broken",
	}, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "web_public_key")
	assert.Contains(t, err.Error(), canonicalDeviceKeyType)
}
