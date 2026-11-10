package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/keycloak"
	"github.com/west6ide/workspace-backend/internal/repository"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

type QuickPinService struct {
	tokenManager            *TokenManager
	userRepo                *repository.UserRepository
	pinRepo                 *repository.QuickPinRepository
	kc                      *keycloak.Client
	kcClients               map[string]*keycloak.Client
	crypto                  *service.CryptoService
	bcryptCost              int
	loginLogRepo            *repository.LoginLogRepository
	loginMonitor            LoginMonitor
	logger                  *zap.Logger
	refreshLocks            sync.Map
	quickPinDefaultClientID string
}

func NewQuickPinService(
	userRepo *repository.UserRepository,
	pinRepo *repository.QuickPinRepository,
	kc *keycloak.Client,
	crypto *service.CryptoService,
	bcryptCost int,
	logger *zap.Logger,
) *QuickPinService {
	return &QuickPinService{
		userRepo:   userRepo,
		pinRepo:    pinRepo,
		kc:         kc,
		crypto:     crypto,
		bcryptCost: bcryptCost,
		logger:     logger.With(zap.String("service", "quick_pin")),
	}
}

func (s *QuickPinService) WithLoginLogRepo(repo *repository.LoginLogRepository) *QuickPinService {
	s.loginLogRepo = repo
	return s
}

func (s *QuickPinService) WithLoginMonitor(loginMonitor LoginMonitor) *QuickPinService {
	s.loginMonitor = loginMonitor
	return s
}

func (s *QuickPinService) WithKeycloakClients(clients map[string]*keycloak.Client) *QuickPinService {
	s.kcClients = clients
	return s
}

func (s *QuickPinService) WithQuickPinDefaultClientID(id string) *QuickPinService {
	s.quickPinDefaultClientID = strings.TrimSpace(id)
	return s
}

func (s *QuickPinService) WithTokenManager(tm *TokenManager) *QuickPinService {
	s.tokenManager = tm
	return s
}
func (s *QuickPinService) checkRecoveryCredentials(ctx context.Context, id int64, raw string) error {
	if s.tokenManager == nil {
		return nil
	}
	claims := jwt.MapClaims{}
	if _, _, err := jwt.NewParser().ParseUnverified(raw, claims); err != nil {
		return apperrors.ReauthRequired("Please sign in again")
	}
	if err := s.tokenManager.CheckCredentialClaims(ctx, id, claims); err != nil {
		return apperrors.ReauthRequired("Please sign in again")
	}
	return nil
}

const pinLength = 4

type PinSetupRequest struct {
	Pin          string `json:"pin" binding:"required"`
	DeviceID     string `json:"device_id" binding:"required"`
	RefreshToken string `json:"refresh_token" binding:"required"`
}

type PinLoginRequest struct {
	Identifier string `json:"identifier" binding:"required"`
	Pin        string `json:"pin" binding:"required"`
	DeviceID   string `json:"device_id" binding:"required"`
}

type PinLoginResponse struct {
	UserID int64 `json:"-"`
	*TokenPair
}

func (s *QuickPinService) SetupPin(ctx context.Context, userID int64, req *PinSetupRequest, clientID *string) error {
	deviceID, err := normalizeDeviceID(req.DeviceID)
	if err != nil {
		return err
	}
	if err := validatePin(req.Pin); err != nil {
		return err
	}
	if strings.TrimSpace(req.RefreshToken) == "" {
		return apperrors.BadRequest("refresh_token is required")
	}

	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil || user == nil {
		return apperrors.NotFound("user not found")
	}
	if !user.IsActive {
		return apperrors.Forbidden("user is disabled")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Pin), s.bcryptCost)
	if err != nil {
		return apperrors.InternalWithErr("failed to hash pin", err)
	}

	encRt, err := s.crypto.Encrypt(req.RefreshToken)
	if err != nil {
		return apperrors.InternalWithErr("failed to encrypt refresh token", err)
	}

	now := time.Now()

	var storedClientID *string
	if clientID != nil && strings.TrimSpace(*clientID) != "" {
		v := strings.TrimSpace(*clientID)
		storedClientID = &v
	}

	cred := &domain.QuickPinCredential{
		UserID:                user.ID,
		DeviceID:              deviceID,
		PinHash:               string(hash),
		EncryptedRefreshToken: encRt,
		ClientID:              storedClientID,
		Active:                true,
		CreatedAt:             now,
		UpdatedAt:             now,
	}

	if err := s.pinRepo.Upsert(ctx, cred); err != nil {
		if s.logger != nil {
			s.logger.Error("failed to save pin", zap.Error(err))
		}
		return apperrors.InternalWithErr("failed to save pin", err)
	}

	return nil
}

func (s *QuickPinService) SyncRefreshToken(ctx context.Context, userID int64, deviceID, newRefreshToken string, clientID *string) error {
	deviceID, err := normalizeDeviceID(deviceID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(newRefreshToken) == "" {
		return nil
	}

	unlock := s.lockRefreshPath(userID, deviceID)
	defer unlock()

	cred, err := s.pinRepo.GetActiveByUserDevice(ctx, userID, deviceID)
	if err != nil {
		if s.logger != nil {
			s.logger.Info("sync_refresh_token: no active pin credential found",
				zap.Int64("user_id", userID),
				zap.String("device_id", deviceID),
			)
		}
		return nil
	}

	clientIDStr := ""
	if clientID != nil {
		clientIDStr = strings.TrimSpace(*clientID)
	}
	if s.logger != nil {
		s.logger.Info("sync_refresh_token: updating pin credential",
			zap.Int64("user_id", userID),
			zap.String("device_id", deviceID),
			zap.String("client_id", clientIDStr),
			zap.String("old_token_suffix", rtSuffix(cred.EncryptedRefreshToken)),
			zap.String("new_token_suffix", rtSuffix(newRefreshToken)),
		)
	}

	encRt, err := s.crypto.Encrypt(newRefreshToken)
	if err != nil {
		return err
	}

	cred.EncryptedRefreshToken = encRt
	if clientIDStr != "" {
		cred.ClientID = &clientIDStr
	}
	cred.UpdatedAt = time.Now()

	if err := s.pinRepo.Upsert(ctx, cred); err != nil {
		if s.logger != nil {
			s.logger.Error("sync_refresh_token: upsert failed",
				zap.Int64("user_id", userID),
				zap.String("device_id", deviceID),
				zap.Error(err),
			)
		}
		return err
	}

	if s.logger != nil {
		s.logger.Info("sync_refresh_token: ok",
			zap.Int64("user_id", userID),
			zap.String("device_id", deviceID),
			zap.String("client_id", clientIDStr),
		)
	}
	return nil
}

func (s *QuickPinService) LoginByPin(ctx context.Context, req *PinLoginRequest) (*PinLoginResponse, error) {
	deviceID, err := normalizeDeviceID(req.DeviceID)
	if err != nil {
		return nil, err
	}
	if err := validatePin(req.Pin); err != nil {
		return nil, err
	}

	identifier := strings.TrimSpace(req.Identifier)
	if identifier == "" {
		return nil, apperrors.BadRequest("identifier is required")
	}

	user, _ := s.userRepo.GetByEmail(ctx, strings.ToLower(identifier))
	if user == nil {
		user, _ = s.userRepo.GetByUsername(ctx, identifier)
	}
	if user == nil || !user.IsActive {
		return nil, apperrors.BadRequest("user account is disabled or not found")
	}

	if s.loginMonitor != nil {
		blocked, blockErr := s.loginMonitor.CheckBlocked(ctx, user.ID, req.DeviceID)
		if blockErr == nil && blocked {
			go s.notifyLoginEvent(context.Background(), buildLoginEventFromUser(ctx, user.ID, user.Email, user.Username, strings.TrimSpace(user.FirstName+" "+user.LastName), "pin"))
			return nil, apperrors.Forbidden("device is blocked")
		}
	}

	unlock := s.lockRefreshPath(user.ID, deviceID)
	defer unlock()

	cred, err := s.pinRepo.GetActiveByUserDevice(ctx, user.ID, deviceID)
	if err != nil || cred == nil {
		return nil, apperrors.ReauthRequired("PIN session is not configured for this device")
	}

	if err := bcrypt.CompareHashAndPassword([]byte(cred.PinHash), []byte(req.Pin)); err != nil {
		return nil, apperrors.InvalidPIN()
	}

	refreshToken, err := s.crypto.Decrypt(cred.EncryptedRefreshToken)
	if err != nil || strings.TrimSpace(refreshToken) == "" {
		if s.logger != nil {
			s.logger.Error("PIN login: failed to decrypt refresh token",
				zap.Int64("user_id", user.ID),
				zap.String("device_id", deviceID),
				zap.Bool("has_stored_refresh_token", strings.TrimSpace(cred.EncryptedRefreshToken) != ""),
				zap.String("error_code", string(apperrors.ErrCodeReauthRequired)),
				zap.Error(err),
			)
		}
		return nil, apperrors.ReauthRequired("Stored PIN session is no longer valid")
	}

	if err := s.checkRecoveryCredentials(ctx, user.ID, refreshToken); err != nil {
		return nil, err
	}
	tok, refreshToken, err := s.refreshForPinLogin(ctx, user.ID, deviceID, cred, refreshToken)
	if err != nil {
		if s.logger != nil {
			appErr := apperrors.ToAppError(err)
			s.logger.Warn("PIN login: keycloak refresh failed",
				zap.Int64("user_id", user.ID),
				zap.String("device_id", deviceID),
				zap.Bool("has_stored_refresh_token", strings.TrimSpace(cred.EncryptedRefreshToken) != ""),
				zap.String("token_type", "refresh"),
				zap.String("error_code", string(appErr.Code)),
				zap.Error(err),
			)
		}
		return nil, err
	}

	if err := s.checkRecoveryCredentials(ctx, user.ID, tok.AccessToken); err != nil {
		return nil, err
	}
	if tok.RefreshToken != "" && tok.RefreshToken != refreshToken {
		encNew, encErr := s.crypto.Encrypt(tok.RefreshToken)
		if encErr == nil {
			cred.EncryptedRefreshToken = encNew
			cred.UpdatedAt = time.Now()
			if upsertErr := s.pinRepo.Upsert(ctx, cred); upsertErr != nil && s.logger != nil {
				s.logger.Warn("PIN login: failed to persist rotated refresh token",
					zap.Int64("user_id", user.ID),
					zap.String("device_id", deviceID),
					zap.Bool("refresh_token_rotated", true),
					zap.Error(upsertErr),
				)
			}
		}
	}

	_ = s.userRepo.UpdateLastLogin(ctx, user.ID, time.Now())

	if s.loginLogRepo != nil {
		go func(uid int64, email string) {
			_ = s.loginLogRepo.Create(context.Background(), uid, email)
		}(user.ID, user.Email)
	}

	go s.notifyLoginEvent(context.Background(), buildLoginEventFromUser(ctx, user.ID, user.Email, user.Username, strings.TrimSpace(user.FirstName+" "+user.LastName), "pin"))

	return &PinLoginResponse{
		UserID: user.ID,
		TokenPair: &TokenPair{
			AccessToken:  tok.AccessToken,
			RefreshToken: tok.RefreshToken,
			IDToken:      tok.IDToken,
			TokenType:    tok.TokenType,
			ExpiresIn:    tok.ExpiresIn,
			ExpiresAt:    time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second),
		},
	}, nil
}

func (s *QuickPinService) notifyLoginEvent(ctx context.Context, event LoginEvent) {
	if s.loginMonitor == nil {
		return
	}
	_ = s.loginMonitor.NotifyLogin(ctx, event)
}

func (s *QuickPinService) lockRefreshPath(userID int64, deviceID string) func() {
	key := fmt.Sprintf("%d:%s", userID, deviceID)
	value, _ := s.refreshLocks.LoadOrStore(key, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func (s *QuickPinService) refreshForPinLogin(
	ctx context.Context,
	userID int64,
	deviceID string,
	cred *domain.QuickPinCredential,
	refreshToken string,
) (*keycloak.TokenResponse, string, error) {
	kcClient, err := resolveClientForPinLogin(cred.ClientID, refreshToken, s.quickPinDefaultClientID, s.kc, s.kcClients)
	if err != nil {
		return nil, refreshToken, apperrors.InternalWithErr("cannot resolve keycloak client for PIN login", err)
	}

	if s.logger != nil {
		storedCID := ""
		if cred.ClientID != nil {
			storedCID = *cred.ClientID
		}
		s.logger.Info("pin_login: keycloak refresh attempt",
			zap.Int64("user_id", userID),
			zap.String("device_id", deviceID),
			zap.String("stored_client_id", storedCID),
			zap.String("default_client_id", s.quickPinDefaultClientID),
			zap.String("token_suffix", rtSuffix(refreshToken)),
		)
	}

	tok, err := kcClient.Refresh(ctx, refreshToken)
	if err == nil {
		return tok, refreshToken, nil
	}

	kcErr := &keycloak.RequestError{}
	if !errors.As(err, &kcErr) {
		return nil, refreshToken, apperrors.RefreshFailed("Failed to refresh PIN session").WithError(err)
	}

	if !kcErr.IsInvalidGrant() {
		return nil, refreshToken, apperrors.RefreshFailed("Failed to refresh PIN session").
			WithDetail("keycloak_status", kcErr.StatusCode).
			WithDetail("keycloak_error", kcErr.ErrorCode).
			WithError(err)
	}

	reloaded, reloadErr := s.pinRepo.GetActiveByUserDevice(ctx, userID, deviceID)
	if reloadErr == nil && reloaded != nil && reloaded.EncryptedRefreshToken != cred.EncryptedRefreshToken {
		latestToken, decryptErr := s.crypto.Decrypt(reloaded.EncryptedRefreshToken)
		if decryptErr == nil && strings.TrimSpace(latestToken) != "" && latestToken != refreshToken {
			retryClient, resolveErr := resolveClientForPinLogin(cred.ClientID, latestToken, s.quickPinDefaultClientID, s.kc, s.kcClients)
			if resolveErr == nil {
				retryTok, retryErr := retryClient.Refresh(ctx, latestToken)
				if retryErr == nil {
					return retryTok, latestToken, nil
				}
				err = retryErr
				_ = errors.As(retryErr, &kcErr)
			}
		}
	}

	if deactivationErr := s.pinRepo.Deactivate(ctx, cred.ID); deactivationErr != nil && s.logger != nil {
		s.logger.Warn("PIN login: failed to deactivate expired PIN session",
			zap.Int64("user_id", userID),
			zap.String("device_id", deviceID),
			zap.Int64("quick_pin_credential_id", cred.ID),
			zap.Error(deactivationErr),
		)
	}

	appErr := apperrors.ReauthRequired("Session expired. Please sign in again.").
		WithDetail("keycloak_status", kcErr.StatusCode).
		WithDetail("keycloak_error", kcErr.ErrorCode)
	if kcErr.IsOfflineSessionNotFound() {
		appErr = appErr.WithDetail("reason", "offline_session_not_found")
	} else {
		appErr = appErr.WithDetail("reason", "invalid_grant")
	}
	return nil, refreshToken, appErr.WithError(err)
}

func validatePin(pin string) error {
	if pin == "" || len(pin) != pinLength {
		return apperrors.BadRequest("pin must be 4 digits")
	}
	for _, ch := range pin {
		if ch < '0' || ch > '9' {
			return apperrors.BadRequest("pin must be 4 digits")
		}
	}
	return nil
}

func normalizeDeviceID(raw string) (string, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "", apperrors.BadRequest("device_id is required")
	}
	return v, nil
}

func resolveClientForPinLogin(
	storedClientID *string,
	refreshToken string,
	defaultClientID string,
	defaultClient *keycloak.Client,
	clients map[string]*keycloak.Client,
) (*keycloak.Client, error) {
	if storedClientID != nil && strings.TrimSpace(*storedClientID) != "" {
		id := strings.TrimSpace(*storedClientID)
		if c, ok := clients[id]; ok && c != nil {
			return c, nil
		}
		return nil, fmt.Errorf("keycloak client %q is not configured", id)
	}

	if azp := extractAZPFromJWT(refreshToken); azp != "" {
		if c, ok := clients[azp]; ok && c != nil {
			return c, nil
		}
	}

	if defaultClientID != "" {
		if c, ok := clients[defaultClientID]; ok && c != nil {
			return c, nil
		}
		return nil, fmt.Errorf("keycloak client %q (default) is not configured", defaultClientID)
	}

	return defaultClient, nil
}

func rtSuffix(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= 6 {
		return "..."
	}
	return "..." + s[len(s)-6:]
}
