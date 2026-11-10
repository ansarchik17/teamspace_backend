package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/auth"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"github.com/west6ide/workspace-backend/internal/service"
	ws "github.com/west6ide/workspace-backend/internal/websocket"
	"go.uber.org/zap"
)

type realtimeSessionTerminator interface {
	CloseUserSessionsBySessionKeys(userID int64, sessionKeys []string) int
}

type E2EEHandler struct {
	service                   *service.E2EEService
	logger                    *zap.Logger
	tokenManager              *auth.TokenManager
	realtimeSessionTerminator realtimeSessionTerminator
}

func NewE2EEHandler(service *service.E2EEService, logger *zap.Logger, tokenManagers ...*auth.TokenManager) *E2EEHandler {
	h := &E2EEHandler{
		service: service,
		logger:  logger.With(zap.String("handler", "e2ee")),
	}
	if len(tokenManagers) > 0 {
		h.tokenManager = tokenManagers[0]
	}
	return h
}

func (h *E2EEHandler) SetTokenManager(tokenManager *auth.TokenManager) {
	h.tokenManager = tokenManager
}

func (h *E2EEHandler) SetRealtimeSessionTerminator(terminator realtimeSessionTerminator) {
	h.realtimeSessionTerminator = terminator
}

func (h *E2EEHandler) terminateLinkedWebRealtimeSessions(userID int64, deviceIDs []string) {
	if h == nil || h.realtimeSessionTerminator == nil || userID <= 0 || len(deviceIDs) == 0 {
		return
	}

	sessionKeys := make([]string, 0, len(deviceIDs)*5)
	seen := make(map[string]struct{}, len(deviceIDs)*5)
	for _, deviceID := range deviceIDs {
		for _, sessionKey := range webDeviceRealtimeSessionKeys(deviceID) {
			if sessionKey == "" {
				continue
			}
			if _, exists := seen[sessionKey]; exists {
				continue
			}
			seen[sessionKey] = struct{}{}
			sessionKeys = append(sessionKeys, sessionKey)
		}
	}

	if len(sessionKeys) == 0 {
		return
	}

	closed := h.realtimeSessionTerminator.CloseUserSessionsBySessionKeys(userID, sessionKeys)
	if closed > 0 {
		h.logger.Info("closed linked web realtime sessions",
			zap.Int64("user_id", userID),
			zap.Int("closed_sessions", closed),
			zap.Int("device_count", len(deviceIDs)),
		)
	}
}

func webDeviceRealtimeSessionKeys(deviceID string) []string {
	if deviceID == "" {
		return nil
	}

	sources := []string{
		"linked_device:" + deviceID,
		"web_device_id:" + deviceID,
		"device_id:" + deviceID,
		"X-Web-Device-ID:" + deviceID,
		"X-Device-ID:" + deviceID,
	}

	keys := make([]string, 0, len(sources))
	for _, source := range sources {
		keys = append(keys, ws.BuildSessionKey(source))
	}
	return keys
}

// UploadKeyReq struct
type UploadKeyReq struct {
	DeviceID  string `json:"device_id" binding:"required"`
	PublicKey string `json:"public_key" binding:"required"`
}

// UploadKey godoc
// @Summary Upload E2EE Public Key
// @Description Uploads the public key for the current device to enable E2EE
// @Tags e2ee
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body UploadKeyReq true "Key Data"
// @Success 200 {object} map[string]string
// @Router /e2ee/keys [post]
func (h *E2EEHandler) UploadKey(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	var req UploadKeyReq

	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	err := h.service.UploadPublicKey(c.Request.Context(), userID, req.DeviceID, req.PublicKey)
	if err != nil {
		h.respondError(c, apperrors.InternalWithErr("failed to upload public key", err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "public key uploaded successfully")})
}

// GetKeys godoc
// @Summary Get User Public Keys
// @Description Get public keys for a specific user (returns map of device_id -> public_key)
// @Tags e2ee
// @Security BearerAuth
// @Produce json
// @Param user_id path int true "Target User ID"
// @Success 200 {object} map[string]string
// @Router /e2ee/keys/{user_id} [get]
func (h *E2EEHandler) GetKeys(c *gin.Context) {
	// Парсим ID собеседника из URL
	peerIDStr := c.Param("user_id")
	peerID, err := strconv.ParseInt(peerIDStr, 10, 64)
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid user id"))
		return
	}

	keys, err := h.service.GetRecipientKeys(c.Request.Context(), peerID)
	if err != nil {
		h.respondError(c, apperrors.InternalWithErr("failed to get keys", err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{
		"user_id": peerID,
		"keys":    keys, // Frontend получит {"device_uuid": "base64key...", "device_uuid2": "..."}
	})
}

// ============================================================
// Key Backup эндпоинты (для смены устройства)
// ============================================================

// SaveKeyBackupReq — тело запроса для сохранения Key Backup
type SaveKeyBackupReq struct {
	// Приватный ключ, зашифрованный AES-GCM с ключом выведенным через KDF из мастер-пароля (Base64)
	EncryptedPrivateKey string `json:"encrypted_private_key" binding:"required"`
	// Случайная соль для KDF (Base64, min 16 bytes)
	Salt string `json:"salt" binding:"required"`
	// Nonce/IV для AES-GCM (Base64, 12 bytes)
	IV string `json:"iv" binding:"required"`
	// Алгоритм вывода ключа: например "argon2id" или "pbkdf2-sha256"
	KDFAlgorithm string `json:"kdf_algorithm"`
	// Полный KDF contract. Для PBKDF2 здесь обязательно должны быть точные iterations.
	KDFParams *domain.KeyDerivationParams `json:"kdf_params,omitempty"`
	// Версия схемы шифрования (для будущей миграции)
	Version int `json:"version"`
	// Явный тип ключевого материала внутри backup plaintext contract.
	BackupKeyType string `json:"backup_key_type,omitempty"`
	// Явный формат сериализации ключевого материала до AES-GCM.
	BackupKeyFormat string `json:"backup_key_format,omitempty"`
	// Семантика backup: восстановимый identity key для deterministic password-based restore.
	BackupKeyScope string `json:"backup_key_scope,omitempty"`
	// Отдельная версия формата backup plaintext contract.
	BackupFormatVersion int `json:"backup_format_version,omitempty"`
	// Алгоритм симметричного шифрования encrypted_private_key.
	CipherAlgorithm string `json:"cipher_algorithm,omitempty"`
	// Включен ли GCM tag в encrypted_private_key.
	CipherTagEmbedded *bool `json:"cipher_tag_embedded,omitempty"`
	// Additional authenticated data mode.
	AADMode string `json:"aad_mode,omitempty"`
	// Нормализация пароля перед KDF.
	PasswordProcessing string `json:"password_processing,omitempty"`
}

// SaveKeyBackup godoc
// @Summary Save E2EE Key Backup
// @Description Saves an encrypted backup of the user's private key to the cloud.
// @Description The private key MUST be encrypted on the CLIENT SIDE with the user's master password before sending.
// @Description The server only stores an encrypted blob and CANNOT decrypt it without the password.
// @Tags e2ee
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body SaveKeyBackupReq true "Encrypted Key Backup"
// @Success 200 {object} map[string]string
// @Failure 400 {object} map[string]interface{}
// @Router /e2ee/backup [post]
func (h *E2EEHandler) SaveKeyBackup(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	var req SaveKeyBackupReq
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	svcReq := &service.SaveKeyBackupRequest{
		EncryptedPrivateKey: req.EncryptedPrivateKey,
		Salt:                req.Salt,
		IV:                  req.IV,
		KDFAlgorithm:        req.KDFAlgorithm,
		KDFParams:           req.KDFParams,
		Version:             req.Version,
		BackupKeyType:       req.BackupKeyType,
		BackupKeyFormat:     req.BackupKeyFormat,
		BackupKeyScope:      req.BackupKeyScope,
		BackupFormatVersion: req.BackupFormatVersion,
		CipherAlgorithm:     req.CipherAlgorithm,
		CipherTagEmbedded:   req.CipherTagEmbedded,
		AADMode:             req.AADMode,
		PasswordProcessing:  req.PasswordProcessing,
	}

	if err := h.service.SaveKeyBackup(c.Request.Context(), userID, svcReq); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "key backup saved successfully")})
}

// GetKeyBackup godoc
// @Summary Get E2EE Key Backup
// @Description Retrieves the encrypted private key backup for the current user.
// @Description The new device must decrypt it locally using the master password.
// @Tags e2ee
// @Security BearerAuth
// @Produce json
// @Success 200 {object} domain.UserKeyBackup
// @Success 204 "No backup found — user has not set up a key backup yet"
// @Failure 500 {object} map[string]interface{}
// @Router /e2ee/backup [get]
func (h *E2EEHandler) GetKeyBackup(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	backup, err := h.service.GetKeyBackup(c.Request.Context(), userID)
	if err != nil {
		h.respondError(c, apperrors.InternalWithErr("failed to get key backup", err))
		return
	}

	if backup == nil {
		// Бэкап ещё не создан — 204 No Content
		c.Status(http.StatusNoContent)
		return
	}

	respondJSON(c, http.StatusOK, backup)
}

// DeleteKeyBackup godoc
// @Summary Delete E2EE Key Backup
// @Description Deletes the encrypted key backup (e.g. when user wants to reset E2EE)
// @Tags e2ee
// @Security BearerAuth
// @Produce json
// @Success 200 {object} map[string]string
// @Failure 500 {object} map[string]interface{}
// @Router /e2ee/backup [delete]
func (h *E2EEHandler) DeleteKeyBackup(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	if err := h.service.DeleteKeyBackup(c.Request.Context(), userID); err != nil {
		h.respondError(c, apperrors.InternalWithErr("failed to delete key backup", err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "key backup deleted successfully")})
}

// RegisterWebDeviceReq — тело запроса для регистрации web-устройства без QR.
type RegisterWebDeviceReq struct {
	// DeviceID — уникальный UUID устройства, сгенерированный на клиенте.
	// Должен быть стабильным (сохраняться в localStorage/IndexedDB).
	DeviceID string `json:"device_id" binding:"required"`
	// PublicKey — публичный ключ E2EE устройства в canonical формате:
	// Base64 encoded X25519 public key (32 bytes after decode).
	PublicKey string `json:"public_key" binding:"required"`
	// DeviceName — читаемое имя устройства (например "Chrome on MacOS").
	DeviceName *string `json:"device_name,omitempty"`
	// Platform — платформа ("web", "android", "ios"). По умолчанию "web".
	Platform *string `json:"platform,omitempty"`
	// ReplaceOtherWebSessions — true по умолчанию. Если включено, backend завершит остальные web-браузеры пользователя.
	ReplaceOtherWebSessions *bool `json:"replace_other_web_sessions,omitempty"`
	// IncludeBackup — true по умолчанию. Если включено, backend сразу вернёт encrypted key backup в ответе.
	IncludeBackup *bool `json:"include_backup,omitempty"`
}

// RegisterWebDevice godoc
// @Summary Register web device for E2EE (password login)
// @Description Registers a web device for end-to-end encryption when logging in via username/password (without QR pairing).
// @Description
// @Description Flow:
// @Description 1. User logs in via POST /auth/login → receives JWT
// @Description 2. Client generates a new web X25519 key pair for this browser slot
// @Description 3. Client calls this endpoint to register the device public key
// @Description 4. Senders will now encrypt new messages for this device_id
// @Description 5. If the client restores the historical identity key from GET /e2ee/backup, old history may be accessible on normal login too
// @Description
// @Description The response includes `has_backup` flag: if true, client should prompt for master password
// @Description for explicit recovery flows. Fresh web keys still require QR history sync, while restored identity keys may open old history without QR.
// @Tags e2ee
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body RegisterWebDeviceReq true "Web device registration"
// @Success 200 {object} service.RegisterWebDeviceResponse
// @Failure 400 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{}
// @Router /e2ee/device/register [post]
func (h *E2EEHandler) RegisterWebDevice(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	var req RegisterWebDeviceReq
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("invalid register web device request",
			zap.Int64("user_id", userID),
			zap.Error(err),
		)
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	svcReq := &service.RegisterWebDeviceRequest{
		DeviceID:                req.DeviceID,
		PublicKey:               req.PublicKey,
		DeviceName:              req.DeviceName,
		Platform:                req.Platform,
		ReplaceOtherWebSessions: req.ReplaceOtherWebSessions,
		IncludeBackup:           req.IncludeBackup,
	}

	resp, err := h.service.RegisterWebDevice(c.Request.Context(), userID, svcReq, extractClientMetadata(c))
	if err != nil {
		h.logger.Error("failed to register web device",
			zap.Int64("user_id", userID),
			zap.String("device_id", req.DeviceID),
			zap.Error(err),
		)
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	if h.tokenManager != nil && resp.User != nil {
		hasPin := false
		if claims, ok := middleware.GetClaims(c); ok && claims != nil {
			hasPin = claims.HasPin
		}

		tokens, tokenErr := h.tokenManager.GenerateTokenPair(
			c.Request.Context(),
			resp.User.ID,
			resp.User.IIN,
			resp.User.Email,
			resp.User.Username,
			resp.User.FirstName,
			resp.User.LastName,
			hasPin,
			map[string]bool{
				"web_linked_device": true,
			},
			&auth.Context{
				LinkedDeviceID: resp.DeviceID,
			},
		)
		if tokenErr != nil {
			h.logger.Error("failed to issue linked web tokens during device registration",
				zap.Int64("user_id", userID),
				zap.String("device_id", resp.DeviceID),
				zap.Error(tokenErr),
			)
			h.respondError(c, apperrors.InternalWithErr("failed to issue linked web session tokens", tokenErr))
			return
		}

		resp.AccessToken = tokens.AccessToken
		resp.RefreshToken = tokens.RefreshToken
		resp.TokenType = tokens.TokenType
		resp.ExpiresIn = tokens.ExpiresIn
		resp.ExpiresAt = &tokens.ExpiresAt

		tokens.MustChangePassword = resp.MustChangePassword
		tokens.CannotSkipPasswordChange = resp.CannotSkipPasswordChange

		setAuthCookies(c, tokens)

		if resp.ReplaceOtherWebSessions {
			if revokeErr := h.tokenManager.RevokeOtherWebLinkedTokens(c.Request.Context(), userID, resp.DeviceID); revokeErr != nil {
				h.logger.Error("failed to revoke previous linked web tokens",
					zap.Int64("user_id", userID),
					zap.String("device_id", resp.DeviceID),
					zap.Error(revokeErr),
				)
				h.respondError(c, apperrors.InternalWithErr("failed to terminate previous web sessions", revokeErr))
				return
			}
			h.terminateLinkedWebRealtimeSessions(userID, resp.RevokedDeviceIDs)
		}
	}

	h.logger.Info("web device registered via password login",
		zap.Int64("user_id", userID),
		zap.String("device_id", resp.DeviceID),
		zap.Bool("has_backup", resp.HasBackup),
		zap.Int("revoked_devices_count", len(resp.RevokedDeviceIDs)),
	)
	respondJSON(c, http.StatusOK, resp)
}

func (h *E2EEHandler) respondError(c *gin.Context, err *apperrors.AppError) {
	respondAppError(c, err)
}
