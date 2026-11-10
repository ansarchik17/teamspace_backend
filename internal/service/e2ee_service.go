package service

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

type e2eeRepository interface {
	UpsertPublicKey(ctx context.Context, key *domain.UserPublicKey) error
	GetKeysByUser(ctx context.Context, userID int64) (map[string]string, error)
	UpsertKeyBackup(ctx context.Context, backup *domain.UserKeyBackup) error
	GetKeyBackup(ctx context.Context, userID int64) (*domain.UserKeyBackup, error)
	DeleteKeyBackup(ctx context.Context, userID int64) error
	CreateWebPairingSession(ctx context.Context, session *domain.WebPairingSession) error
	GetWebPairingSessionBySessionToken(ctx context.Context, pairingID uuid.UUID, sessionTokenHash string) (*domain.WebPairingSession, error)
	GetWebPairingSessionByQRToken(ctx context.Context, pairingID uuid.UUID, qrTokenHash string) (*domain.WebPairingSession, error)
	ApproveWebPairingSession(ctx context.Context, pairingID uuid.UUID, userID int64, loginCodeHash *string, deviceName, platform *string) (*domain.WebPairingSession, error)
	RejectWebPairingSession(ctx context.Context, pairingID uuid.UUID, userID int64) (*domain.WebPairingSession, error)
	MarkWebPairingHistorySynced(ctx context.Context, pairingID uuid.UUID) error
	RedeemWebPairingSession(ctx context.Context, pairingID uuid.UUID, sessionTokenHash string) (*domain.WebPairingSession, error)
	AppendHistoryKeysForDevice(ctx context.Context, userID int64, pairingID uuid.UUID, webDeviceID string, items []domain.WebHistorySyncItem) (int, error)
	UpdateLinkedDeviceAccessMetadata(ctx context.Context, userID int64, deviceID string, meta *domain.ClientMetadata) error
	ListLinkedDevices(ctx context.Context, userID int64) ([]*domain.LinkedDevice, error)
	RevokeLinkedDevice(ctx context.Context, userID int64, deviceID string) error
	GetUserBasicProfile(ctx context.Context, userID int64) (*domain.User, error)
	// RegisterWebDeviceFromPasswordLogin — регистрирует web-устройство напрямую по паролю (без QR).
	RegisterWebDeviceFromPasswordLogin(ctx context.Context, userID int64, deviceID, publicKey string, deviceName, platform *string, meta *domain.ClientMetadata) error
	RevokeOtherWebDevices(ctx context.Context, userID int64, keepDeviceID string) ([]string, error)
	CreateWebLoginNotification(ctx context.Context, userID int64, deviceID string, deviceName, platform *string, meta *domain.ClientMetadata, revokedDeviceIDs []string) error
}

type E2EEService struct {
	repo   e2eeRepository
	logger *zap.Logger
}

func NewE2EEService(repo e2eeRepository, logger *zap.Logger) *E2EEService {
	return &E2EEService{
		repo:   repo,
		logger: logger.With(zap.String("service", "e2ee")),
	}
}

// UploadPublicKey сохраняет публичный ключ устройства пользователя.
// Вызывается при регистрации/входе с нового устройства.
func (s *E2EEService) UploadPublicKey(ctx context.Context, userID int64, deviceID, publicKey string) error {
	publicKey, err := validateCanonicalPublicKey(publicKey, "public_key")
	if err != nil {
		return apperrors.Validation(err.Error())
	}

	key := &domain.UserPublicKey{
		UserID:    userID,
		DeviceID:  deviceID,
		PublicKey: publicKey,
	}
	return s.repo.UpsertPublicKey(ctx, key)
}

// GetRecipientKeys возвращает все публичные ключи пользователя (device_id -> public_key).
// Используется отправителем для шифрования одного сообщения на каждое устройство получателя.
func (s *E2EEService) GetRecipientKeys(ctx context.Context, userID int64) (map[string]string, error) {
	return s.repo.GetKeysByUser(ctx, userID)
}

// ============================================================
// Key Backup методы (для поддержки смены устройства)
// ============================================================

// SaveKeyBackupRequest — DTO для сохранения бэкапа ключа.
type SaveKeyBackupRequest struct {
	EncryptedPrivateKey string                      `json:"encrypted_private_key"`
	Salt                string                      `json:"salt"`
	IV                  string                      `json:"iv"`
	KDFAlgorithm        string                      `json:"kdf_algorithm"` // Например "argon2id" или "pbkdf2-sha256"
	KDFParams           *domain.KeyDerivationParams `json:"kdf_params,omitempty"`
	Version             int                         `json:"version"`
	BackupKeyType       string                      `json:"backup_key_type"`
	BackupKeyFormat     string                      `json:"backup_key_format"`
	BackupKeyScope      string                      `json:"backup_key_scope"`
	BackupFormatVersion int                         `json:"backup_format_version"`
	CipherAlgorithm     string                      `json:"cipher_algorithm"`
	CipherTagEmbedded   *bool                       `json:"cipher_tag_embedded,omitempty"`
	AADMode             string                      `json:"aad_mode"`
	PasswordProcessing  string                      `json:"password_processing"`
}

const (
	defaultBackupKDFAlgorithm          = "argon2id"
	legacyPBKDF2SHA256Version          = 1
	legacyPBKDF2SHA256Iterations       = 310000
	legacyPBKDF2SHA256DerivedKeyLength = 32
)

// SaveKeyBackup сохраняет зашифрованный бэкап приватного ключа.
//
// ВАЖНО: ключ должен быть зашифрован НА СТОРОНЕ КЛИЕНТА мастер-паролем
// перед отправкой. Сервер не знает пароль.
func (s *E2EEService) SaveKeyBackup(ctx context.Context, userID int64, req *SaveKeyBackupRequest) error {
	backup, err := BuildValidatedKeyBackup(userID, req)
	if err != nil {
		return err
	}
	return s.repo.UpsertKeyBackup(ctx, backup)
}

func BuildValidatedKeyBackup(userID int64, req *SaveKeyBackupRequest) (*domain.UserKeyBackup, error) {
	if req == nil {
		return nil, apperrors.Validation("e2ee_backup is required")
	}
	if req.EncryptedPrivateKey == "" || req.Salt == "" || req.IV == "" {
		return nil, apperrors.Validation("encrypted_private_key, salt, and iv are required")
	}

	kdfAlgo := req.KDFAlgorithm
	if kdfAlgo == "" {
		kdfAlgo = defaultBackupKDFAlgorithm
	}

	version := req.Version
	if version == 0 {
		version = 1
	}

	backup := &domain.UserKeyBackup{
		UserID:              userID,
		EncryptedPrivateKey: req.EncryptedPrivateKey,
		Salt:                req.Salt,
		IV:                  req.IV,
		KDFAlgorithm:        kdfAlgo,
		KDFParams:           cloneKDFParams(req.KDFParams),
		Version:             version,
		BackupKeyType:       req.BackupKeyType,
		BackupKeyFormat:     req.BackupKeyFormat,
		BackupKeyScope:      req.BackupKeyScope,
		BackupFormatVersion: req.BackupFormatVersion,
		CipherAlgorithm:     req.CipherAlgorithm,
		AADMode:             req.AADMode,
		PasswordProcessing:  req.PasswordProcessing,
	}
	if req.CipherTagEmbedded != nil {
		backup.CipherTagEmbedded = *req.CipherTagEmbedded
	}
	normalizeKeyBackupContract(backup)
	if err := validateKeyBackupContract(backup); err != nil {
		return nil, apperrors.Validation(err.Error())
	}

	return backup, nil
}

func BuildStrictValidatedKeyBackup(userID int64, req *SaveKeyBackupRequest) (*domain.UserKeyBackup, error) {
	if err := validateExplicitKeyBackupContract(req); err != nil {
		return nil, apperrors.Validation(err.Error())
	}
	return BuildValidatedKeyBackup(userID, req)
}

// GetKeyBackup возвращает зашифрованный бэкап приватного ключа пользователя.
// Результат передаётся новому устройству, которое запрашивает мастер-пароль и расшифровывает ключ локально.
func (s *E2EEService) GetKeyBackup(ctx context.Context, userID int64) (*domain.UserKeyBackup, error) {
	backup, err := s.repo.GetKeyBackup(ctx, userID)
	if err != nil || backup == nil {
		return backup, err
	}
	normalizeKeyBackupContract(backup)
	return backup, nil
}

// DeleteKeyBackup удаляет бэкап приватного ключа (например, если пользователь хочет сбросить E2EE).
func (s *E2EEService) DeleteKeyBackup(ctx context.Context, userID int64) error {
	return s.repo.DeleteKeyBackup(ctx, userID)
}

// ============================================================
// Password-login web device registration
// ============================================================

// RegisterWebDeviceRequest — DTO для регистрации web-устройства без QR.
type RegisterWebDeviceRequest struct {
	// DeviceID — уникальный идентификатор устройства (генерируется на клиенте, например UUID).
	DeviceID string `json:"device_id"`
	// PublicKey — публичный ключ клиента в canonical формате:
	// Base64 encoded X25519 public key (32 bytes after decode).
	PublicKey string `json:"public_key"`
	// DeviceName — читаемое имя устройства (например "Chrome on MacOS").
	DeviceName *string `json:"device_name,omitempty"`
	// Platform — платформа ("web", "android", "ios"). По умолчанию "web".
	Platform *string `json:"platform,omitempty"`
	// ReplaceOtherWebSessions — отозвать остальные web-браузеры пользователя после успешного логина.
	// По умолчанию true.
	ReplaceOtherWebSessions *bool `json:"replace_other_web_sessions,omitempty"`
	// IncludeBackup — вернуть encrypted key backup в этом же ответе, чтобы web UI мог
	// сразу продолжить E2EE bootstrap без дополнительного запроса.
	// По умолчанию true.
	IncludeBackup *bool `json:"include_backup,omitempty"`
}

type RegisteredWebDeviceUser struct {
	ID        int64  `json:"id"`
	IIN       string `json:"iin,omitempty"`
	Email     string `json:"email"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

// RegisterWebDeviceResponse — ответ на успешную регистрацию веб-устройства.
type RegisterWebDeviceResponse struct {
	DeviceID                 string                   `json:"device_id"`
	Message                  string                   `json:"message"`
	HasBackup                bool                     `json:"has_backup"` // true — бэкап ключа существует, клиент может его получить
	BackupAlgo               *string                  `json:"backup_algo,omitempty"`
	Backup                   *domain.UserKeyBackup    `json:"backup,omitempty"`
	User                     *RegisteredWebDeviceUser `json:"user,omitempty"`
	CurrentLogin             *domain.ClientMetadata   `json:"current_login,omitempty"`
	RevokedDeviceIDs         []string                 `json:"revoked_device_ids,omitempty"`
	ReplaceOtherWebSessions  bool                     `json:"replace_other_web_sessions"`
	AccessToken              string                   `json:"access_token,omitempty"`
	RefreshToken             string                   `json:"refresh_token,omitempty"`
	TokenType                string                   `json:"token_type,omitempty"`
	ExpiresIn                int64                    `json:"expires_in,omitempty"`
	ExpiresAt                *time.Time               `json:"expires_at,omitempty"`
	MustChangePassword       bool                     `json:"must_change_password,omitempty"`
	CannotSkipPasswordChange bool                     `json:"cannot_skip_password_change,omitempty"`
}

// RegisterWebDevice регистрирует web-устройство, пришедшее через password login (без QR).
//
// Этот метод:
//  1. Валидирует входные данные
//  2. Нормализует platform (lowercase, trim, default "web")
//  3. Вставляет/обновляет запись в user_public_keys (= linked_devices)
//  4. Проверяет наличие key backup (чтобы клиент знал, нужно ли запрашивать мастер-пароль)
//
// После успеха:
//   - Отправители будут шифровать новые сообщения и на этот device_id
//   - Клиент может получить GET /e2ee/backup для explicit recovery flows
//   - Если login использует восстановленный identity key из backup, старая история может быть доступна и без QR
//   - Если login использует fresh web key, старая история требует QR pairing + history sync
func (s *E2EEService) RegisterWebDevice(
	ctx context.Context,
	userID int64,
	req *RegisterWebDeviceRequest,
	meta *domain.ClientMetadata,
) (*RegisterWebDeviceResponse, error) {
	if req == nil {
		return nil, apperrors.Validation("request body is required")
	}

	// Используем пакетные хелперы normalizeOptionalString / normalizePlatform
	// которые уже объявлены в e2ee_web_service.go (тот же пакет service).
	deviceIDPtr := normalizeOptionalString(&req.DeviceID)
	if deviceIDPtr == nil {
		return nil, apperrors.Validation("device_id is required")
	}
	deviceID := *deviceIDPtr

	publicKeyPtr := normalizeOptionalString(&req.PublicKey)
	if publicKeyPtr == nil {
		return nil, apperrors.Validation("public_key is required")
	}
	publicKey, err := validateCanonicalPublicKey(*publicKeyPtr, "public_key")
	if err != nil {
		return nil, apperrors.Validation(err.Error())
	}

	platform := normalizePlatform(req.Platform)           // guaranteed non-nil, defaults to "web"
	deviceName := normalizeOptionalString(req.DeviceName) // nil if blank
	replaceOtherWebSessions := req.ReplaceOtherWebSessions == nil || *req.ReplaceOtherWebSessions
	includeBackup := req.IncludeBackup == nil || *req.IncludeBackup

	if err := s.repo.RegisterWebDeviceFromPasswordLogin(
		ctx, userID, deviceID, publicKey, deviceName, platform, meta,
	); err != nil {
		return nil, apperrors.InternalWithErr("failed to register web device", err)
	}

	revokedDeviceIDs := make([]string, 0)
	if replaceOtherWebSessions && platform != nil && *platform == "web" {
		revokedDeviceIDs, err = s.repo.RevokeOtherWebDevices(ctx, userID, deviceID)
		if err != nil {
			return nil, apperrors.InternalWithErr("failed to revoke previous web sessions", err)
		}
	}

	var backup *domain.UserKeyBackup
	if includeBackup {
		// Ошибка здесь не критична — устройство уже зарегистрировано.
		backup, err = s.GetKeyBackup(ctx, userID)
		if err != nil {
			s.logger.Warn("failed to check key backup after device registration",
				zap.Int64("user_id", userID),
				zap.String("device_id", deviceID),
				zap.Error(err),
			)
			backup = nil
		}
		if backup != nil && !shouldAdvertiseBackupInPasswordLogin(backup) {
			s.logger.Info("suppressing legacy compatibility backup in password-login bootstrap",
				zap.Int64("user_id", userID),
				zap.String("device_id", deviceID),
				zap.String("kdf_algorithm", backup.KDFAlgorithm),
				zap.Int("version", backup.Version),
			)
			backup = nil
		}
	}

	hasBackup := backup != nil
	var backupAlgo *string
	if hasBackup {
		backupAlgo = &backup.KDFAlgorithm
	}

	user, err := s.repo.GetUserBasicProfile(ctx, userID)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to load user for web device registration", err)
	}

	if err := s.repo.CreateWebLoginNotification(ctx, userID, deviceID, deviceName, platform, meta, revokedDeviceIDs); err != nil {
		s.logger.Warn("failed to create web login notification",
			zap.Int64("user_id", userID),
			zap.String("device_id", deviceID),
			zap.Error(err),
		)
	}

	s.logger.Info("web device registered via password login",
		zap.Int64("user_id", userID),
		zap.String("device_id", deviceID),
		zap.Bool("has_backup", hasBackup),
		zap.Int("revoked_devices_count", len(revokedDeviceIDs)),
	)

	var respUser *RegisteredWebDeviceUser
	if user != nil {
		respUser = &RegisteredWebDeviceUser{
			ID: user.ID,
			IIN: func() string {
				if user.IIN != nil {
					return *user.IIN
				}
				return ""
			}(),
			Email:     user.Email,
			Username:  user.Username,
			FirstName: user.FirstName,
			LastName:  user.LastName,
		}
	}

	mustChangePassword := false
	cannotSkip := false
	if user != nil && user.IsFirstLogin {
		mustChangePassword = true
		cannotSkip = true
	}

	return &RegisterWebDeviceResponse{
		DeviceID:                 deviceID,
		Message:                  "device registered successfully",
		HasBackup:                hasBackup,
		BackupAlgo:               backupAlgo,
		Backup:                   backup,
		User:                     respUser,
		CurrentLogin:             meta,
		RevokedDeviceIDs:         revokedDeviceIDs,
		ReplaceOtherWebSessions:  replaceOtherWebSessions,
		MustChangePassword:       mustChangePassword,
		CannotSkipPasswordChange: cannotSkip,
	}, nil
}

func normalizeKeyBackupContract(backup *domain.UserKeyBackup) {
	if backup == nil {
		return
	}

	backup.KDFAlgorithm = normalizeBackupKDFAlgorithm(backup.KDFAlgorithm)
	if backup.Version == 0 {
		backup.Version = 1
	}
	normalizeKeyBackupMetadata(backup)

	backup.KDFParams = mergeKDFParams(
		normalizeKDFParams(backup.KDFParams),
		inferredLegacyKDFParams(backup.KDFAlgorithm, backup.Version),
	)
}

func normalizeBackupKDFAlgorithm(value string) string {
	normalized := strings.TrimSpace(strings.ToLower(value))
	if normalized == "" {
		return defaultBackupKDFAlgorithm
	}
	return normalized
}

func normalizeKDFParams(params *domain.KeyDerivationParams) *domain.KeyDerivationParams {
	if params == nil {
		return nil
	}

	normalized := cloneKDFParams(params)
	if normalized.Hash != nil {
		hash := strings.TrimSpace(strings.ToLower(*normalized.Hash))
		if hash == "" {
			normalized.Hash = nil
		} else {
			normalized.Hash = &hash
		}
	}

	if normalized.Iterations == nil &&
		normalized.MemoryKiB == nil &&
		normalized.Parallelism == nil &&
		normalized.KeyLength == nil &&
		normalized.Hash == nil {
		return nil
	}

	return normalized
}

func inferredLegacyKDFParams(algo string, version int) *domain.KeyDerivationParams {
	if version == 0 {
		version = 1
	}

	switch normalizeBackupKDFAlgorithm(algo) {
	case "pbkdf2", "pbkdf2-sha256":
		if version == legacyPBKDF2SHA256Version {
			iterations := legacyPBKDF2SHA256Iterations
			keyLength := legacyPBKDF2SHA256DerivedKeyLength
			hash := "sha256"
			return &domain.KeyDerivationParams{
				Iterations: &iterations,
				KeyLength:  &keyLength,
				Hash:       &hash,
			}
		}
	}

	return nil
}

func mergeKDFParams(current, defaults *domain.KeyDerivationParams) *domain.KeyDerivationParams {
	if current == nil {
		return cloneKDFParams(defaults)
	}
	if defaults == nil {
		return current
	}

	merged := cloneKDFParams(current)
	if merged.Iterations == nil && defaults.Iterations != nil {
		value := *defaults.Iterations
		merged.Iterations = &value
	}
	if merged.MemoryKiB == nil && defaults.MemoryKiB != nil {
		value := *defaults.MemoryKiB
		merged.MemoryKiB = &value
	}
	if merged.Parallelism == nil && defaults.Parallelism != nil {
		value := *defaults.Parallelism
		merged.Parallelism = &value
	}
	if merged.KeyLength == nil && defaults.KeyLength != nil {
		value := *defaults.KeyLength
		merged.KeyLength = &value
	}
	if merged.Hash == nil && defaults.Hash != nil {
		value := *defaults.Hash
		merged.Hash = &value
	}

	return merged
}

func cloneKDFParams(params *domain.KeyDerivationParams) *domain.KeyDerivationParams {
	if params == nil {
		return nil
	}

	clone := &domain.KeyDerivationParams{}
	if params.Iterations != nil {
		value := *params.Iterations
		clone.Iterations = &value
	}
	if params.MemoryKiB != nil {
		value := *params.MemoryKiB
		clone.MemoryKiB = &value
	}
	if params.Parallelism != nil {
		value := *params.Parallelism
		clone.Parallelism = &value
	}
	if params.KeyLength != nil {
		value := *params.KeyLength
		clone.KeyLength = &value
	}
	if params.Hash != nil {
		value := *params.Hash
		clone.Hash = &value
	}

	return clone
}
