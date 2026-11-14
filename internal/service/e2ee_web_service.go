package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

const (
	defaultWebPairingTTL = 90 * time.Second
	maxWebPairingTTL     = 5 * time.Minute
)

type CreateWebPairingSessionRequest struct {
	WebDeviceID     string  `json:"web_device_id"`
	WebPublicKey    string  `json:"web_public_key"`
	WebEphemeralPub *string `json:"web_ephemeral_pub,omitempty"`
	TTLSeconds      *int    `json:"ttl_seconds,omitempty"`
}

type WebPairingQRPayload struct {
	Type            string    `json:"type"`
	PairingID       uuid.UUID `json:"pairing_id"`
	QRToken         string    `json:"qr_token"`
	WebDeviceID     string    `json:"web_device_id"`
	WebPublicKey    string    `json:"web_public_key"`
	WebEphemeralPub *string   `json:"web_ephemeral_pub,omitempty"`
	ExpiresAt       time.Time `json:"expires_at"`
}

type WebPairingPreviewResponse struct {
	PairingID        uuid.UUID `json:"pairing_id"`
	WebDeviceID      string    `json:"web_device_id"`
	WebPublicKey     string    `json:"web_public_key"`
	RequestIP        *string   `json:"request_ip,omitempty"`
	RequestCountry   *string   `json:"request_country,omitempty"`
	RequestCity      *string   `json:"request_city,omitempty"`
	RequestUserAgent *string   `json:"request_user_agent,omitempty"`
	ExpiresAt        time.Time `json:"expires_at"`
	Status           string    `json:"status"`
	CreatedAt        time.Time `json:"created_at"`
}

type CreateWebPairingSessionResponse struct {
	PairingID       uuid.UUID           `json:"pairing_id"`
	QRToken         string              `json:"qr_token"`
	SessionToken    string              `json:"session_token"`
	WebDeviceID     string              `json:"web_device_id"`
	WebPublicKey    string              `json:"web_public_key"`
	WebEphemeralPub *string             `json:"web_ephemeral_pub,omitempty"`
	Status          string              `json:"status"`
	ExpiresAt       time.Time           `json:"expires_at"`
	QRPayload       WebPairingQRPayload `json:"qr_payload"`
}

type WebPairingSessionStatusResponse struct {
	PairingID   uuid.UUID  `json:"pairing_id"`
	WebDeviceID string     `json:"web_device_id"`
	Status      string     `json:"status"`
	Approved    bool       `json:"approved"`
	Synced      bool       `json:"synced"`
	Redeemed    bool       `json:"redeemed"`
	ExpiresAt   time.Time  `json:"expires_at"`
	ApprovedAt  *time.Time `json:"approved_at,omitempty"`
	SyncedAt    *time.Time `json:"synced_at,omitempty"`
	RedeemedAt  *time.Time `json:"redeemed_at,omitempty"`
}

type ApproveWebPairingRequest struct {
	QRToken      string  `json:"qr_token"`
	WebDeviceID  string  `json:"web_device_id"`
	WebPublicKey string  `json:"web_public_key"`
	DeviceName   *string `json:"device_name,omitempty"`
	Platform     *string `json:"platform,omitempty"`
	HistorySync  bool    `json:"history_sync"`
}

type RejectWebPairingRequest struct {
	QRToken string `json:"qr_token"`
}

type RedeemWebPairingSessionRequest struct {
	SessionToken string `json:"session_token"`
}

type RedeemWebPairingSessionResult struct {
	AuthenticationTime time.Time `json:"-"`
	PairingID          uuid.UUID `json:"pairing_id"`
	WebDeviceID        string    `json:"web_device_id"`
	UserID             int64     `json:"user_id"`
	IIN                string    `json:"iin,omitempty"`
	Email              string    `json:"email"`
	Username           string    `json:"username"`
	FirstName          string    `json:"first_name"`
	LastName           string    `json:"last_name"`
	IsFirstLogin       bool      `json:"is_first_login"`
}

func (s *E2EEService) CreateWebPairingSession(ctx context.Context, req *CreateWebPairingSessionRequest, meta *domain.ClientMetadata) (*CreateWebPairingSessionResponse, error) {
	if req == nil {
		return nil, apperrors.Validation("request body is required")
	}

	webDeviceID := strings.TrimSpace(req.WebDeviceID)
	if webDeviceID == "" {
		webDeviceID = uuid.NewString()
	}

	webPublicKey := strings.TrimSpace(req.WebPublicKey)
	if webPublicKey == "" {
		return nil, apperrors.Validation("web_public_key is required")
	}
	webPublicKey, err := validateCanonicalPublicKey(webPublicKey, "web_public_key")
	if err != nil {
		return nil, apperrors.Validation(err.Error())
	}

	ttl := defaultWebPairingTTL
	if req.TTLSeconds != nil && *req.TTLSeconds > 0 {
		ttl = time.Duration(*req.TTLSeconds) * time.Second
	}
	if ttl > maxWebPairingTTL {
		ttl = maxWebPairingTTL
	}

	qrToken, err := generateOpaqueSecret(32)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to generate qr token", err)
	}
	sessionToken, err := generateOpaqueSecret(32)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to generate session token", err)
	}

	expiresAt := time.Now().Add(ttl)
	session := &domain.WebPairingSession{
		ID:               uuid.New(),
		WebDeviceID:      webDeviceID,
		WebPublicKey:     webPublicKey,
		WebEphemeralPub:  req.WebEphemeralPub,
		Status:           domain.WebPairingStatusPending,
		RequestIP:        clientMetaValue(meta, "ip"),
		RequestCountry:   clientMetaValue(meta, "country"),
		RequestCity:      clientMetaValue(meta, "city"),
		RequestUserAgent: clientMetaValue(meta, "user_agent"),
		ExpiresAt:        expiresAt,
		QRTokenHash:      hashSecret(qrToken),
		SessionTokenHash: hashSecret(sessionToken),
	}

	if err = s.repo.CreateWebPairingSession(ctx, session); err != nil {
		return nil, apperrors.InternalWithErr("failed to create web pairing session", err)
	}

	qrPayload := WebPairingQRPayload{
		Type:            "web_pairing",
		PairingID:       session.ID,
		QRToken:         qrToken,
		WebDeviceID:     webDeviceID,
		WebPublicKey:    webPublicKey,
		WebEphemeralPub: req.WebEphemeralPub,
		ExpiresAt:       expiresAt,
	}

	return &CreateWebPairingSessionResponse{
		PairingID:       session.ID,
		QRToken:         qrToken,
		SessionToken:    sessionToken,
		WebDeviceID:     webDeviceID,
		WebPublicKey:    webPublicKey,
		WebEphemeralPub: req.WebEphemeralPub,
		Status:          session.Status,
		ExpiresAt:       expiresAt,
		QRPayload:       qrPayload,
	}, nil
}

func (s *E2EEService) PreviewWebPairingSession(ctx context.Context, pairingID uuid.UUID, qrToken string) (*WebPairingPreviewResponse, error) {
	qrToken = strings.TrimSpace(qrToken)
	if qrToken == "" {
		return nil, apperrors.Validation("qr_token is required")
	}

	session, err := s.repo.GetWebPairingSessionByQRToken(ctx, pairingID, hashSecret(qrToken))
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to load web pairing preview", err)
	}
	if session == nil {
		return nil, apperrors.NotFound("web pairing session")
	}
	if time.Now().After(session.ExpiresAt) {
		return nil, apperrors.BadRequest("QR code has expired. Please refresh the login page and scan a new QR code.")
	}
	if session.Status == domain.WebPairingStatusRevoked {
		return nil, apperrors.BadRequest("This web login request was rejected or revoked. Please generate a new QR code.")
	}
	if session.Status == domain.WebPairingStatusRedeemed {
		return nil, apperrors.Conflict("This web login request has already been used.")
	}

	return &WebPairingPreviewResponse{
		PairingID:        session.ID,
		WebDeviceID:      session.WebDeviceID,
		WebPublicKey:     session.WebPublicKey,
		RequestIP:        session.RequestIP,
		RequestCountry:   session.RequestCountry,
		RequestCity:      session.RequestCity,
		RequestUserAgent: session.RequestUserAgent,
		ExpiresAt:        session.ExpiresAt,
		Status:           session.Status,
		CreatedAt:        session.CreatedAt,
	}, nil
}

func (s *E2EEService) GetWebPairingSessionStatus(ctx context.Context, pairingID uuid.UUID, sessionToken string) (*WebPairingSessionStatusResponse, error) {
	sessionToken = strings.TrimSpace(sessionToken)
	if sessionToken == "" {
		return nil, apperrors.Validation("session_token is required")
	}

	session, err := s.repo.GetWebPairingSessionBySessionToken(ctx, pairingID, hashSecret(sessionToken))
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to get web pairing session", err)
	}
	if session == nil {
		return nil, apperrors.NotFound("web pairing session")
	}

	status := session.Status
	if time.Now().After(session.ExpiresAt) && status != domain.WebPairingStatusRedeemed && status != domain.WebPairingStatusRevoked {
		status = domain.WebPairingStatusExpired
	}

	return &WebPairingSessionStatusResponse{
		PairingID:   session.ID,
		WebDeviceID: session.WebDeviceID,
		Status:      status,
		Approved:    status == domain.WebPairingStatusApproved || status == domain.WebPairingStatusSynced || status == domain.WebPairingStatusRedeemed,
		Synced:      session.SyncedAt != nil || status == domain.WebPairingStatusSynced || status == domain.WebPairingStatusRedeemed,
		Redeemed:    status == domain.WebPairingStatusRedeemed,
		ExpiresAt:   session.ExpiresAt,
		ApprovedAt:  session.ApprovedAt,
		SyncedAt:    session.SyncedAt,
		RedeemedAt:  session.RedeemedAt,
	}, nil
}

func (s *E2EEService) ApproveWebPairingSession(ctx context.Context, userID int64, pairingID uuid.UUID, req *ApproveWebPairingRequest) (*WebPairingSessionStatusResponse, error) {
	if req == nil {
		return nil, apperrors.Validation("request body is required")
	}

	qrToken := strings.TrimSpace(req.QRToken)
	if qrToken == "" {
		return nil, apperrors.Validation("qr_token is required")
	}

	session, err := s.repo.GetWebPairingSessionByQRToken(ctx, pairingID, hashSecret(qrToken))
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to validate web pairing session", err)
	}
	if session == nil {
		return nil, apperrors.NotFound("web pairing session")
	}
	if time.Now().After(session.ExpiresAt) {
		return nil, apperrors.BadRequest("QR code has expired. Please generate a new QR code and try again.")
	}

	if req.WebDeviceID != "" && req.WebDeviceID != session.WebDeviceID {
		return nil, apperrors.BadRequest("This QR code does not match the current web device. Please refresh the page and scan the new QR code.")
	}
	if req.WebPublicKey != "" && req.WebPublicKey != session.WebPublicKey {
		return nil, apperrors.BadRequest("This QR code does not match the current web encryption key. Please refresh the page and scan the new QR code.")
	}

	approved, err := s.repo.ApproveWebPairingSession(
		ctx,
		pairingID,
		userID,
		nil,
		normalizeOptionalString(req.DeviceName),
		normalizePlatform(req.Platform),
	)
	if err != nil {
		return nil, mapWebPairingRepoError(err, "approve")
	}
	if approved == nil {
		return nil, apperrors.NotFound("web pairing session")
	}

	status := approved.Status
	return &WebPairingSessionStatusResponse{
		PairingID:   approved.ID,
		WebDeviceID: approved.WebDeviceID,
		Status:      status,
		Approved:    true,
		Synced:      approved.SyncedAt != nil,
		Redeemed:    approved.RedeemedAt != nil,
		ExpiresAt:   approved.ExpiresAt,
		ApprovedAt:  approved.ApprovedAt,
		SyncedAt:    approved.SyncedAt,
		RedeemedAt:  approved.RedeemedAt,
	}, nil
}

func (s *E2EEService) RejectWebPairingSession(ctx context.Context, userID int64, pairingID uuid.UUID, req *RejectWebPairingRequest) (*WebPairingSessionStatusResponse, error) {
	if req == nil {
		return nil, apperrors.Validation("request body is required")
	}

	qrToken := strings.TrimSpace(req.QRToken)
	if qrToken == "" {
		return nil, apperrors.Validation("qr_token is required")
	}

	session, err := s.repo.GetWebPairingSessionByQRToken(ctx, pairingID, hashSecret(qrToken))
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to validate web pairing session", err)
	}
	if session == nil {
		return nil, apperrors.NotFound("web pairing session")
	}
	if time.Now().After(session.ExpiresAt) {
		return nil, apperrors.BadRequest("QR code has expired. Please ask the web client to generate a new QR code.")
	}
	if session.UserID != nil && *session.UserID != userID {
		return nil, apperrors.Forbidden("This web login request belongs to another user.")
	}

	rejected, err := s.repo.RejectWebPairingSession(ctx, pairingID, userID)
	if err != nil {
		return nil, mapWebPairingRepoError(err, "reject")
	}
	if rejected == nil {
		return nil, apperrors.NotFound("web pairing session")
	}

	return &WebPairingSessionStatusResponse{
		PairingID:   rejected.ID,
		WebDeviceID: rejected.WebDeviceID,
		Status:      rejected.Status,
		Approved:    false,
		Synced:      false,
		Redeemed:    false,
		ExpiresAt:   rejected.ExpiresAt,
		ApprovedAt:  rejected.ApprovedAt,
		SyncedAt:    rejected.SyncedAt,
		RedeemedAt:  rejected.RedeemedAt,
	}, nil
}

func (s *E2EEService) RedeemWebPairingSession(ctx context.Context, pairingID uuid.UUID, req *RedeemWebPairingSessionRequest, meta *domain.ClientMetadata) (*RedeemWebPairingSessionResult, error) {
	if req == nil {
		return nil, apperrors.Validation("request body is required")
	}

	sessionToken := strings.TrimSpace(req.SessionToken)
	if sessionToken == "" {
		return nil, apperrors.Validation("session_token is required")
	}

	session, err := s.repo.RedeemWebPairingSession(ctx, pairingID, hashSecret(sessionToken))
	if err != nil {
		return nil, mapWebPairingRepoError(err, "redeem")
	}
	if session == nil || session.UserID == nil {
		return nil, apperrors.NotFound("web pairing session")
	}

	user, err := s.repo.GetUserBasicProfile(ctx, *session.UserID)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to load user for web session", err)
	}
	if user == nil {
		return nil, apperrors.NotFound("user")
	}

	if err = s.repo.UpdateLinkedDeviceAccessMetadata(ctx, user.ID, session.WebDeviceID, meta); err != nil {
		return nil, apperrors.InternalWithErr("failed to update linked device access metadata", err)
	}

	return &RedeemWebPairingSessionResult{
		AuthenticationTime: session.CreatedAt,
		PairingID:          session.ID,
		WebDeviceID:        session.WebDeviceID,
		UserID:             user.ID,
		IIN: func() string {
			if user.IIN != nil {
				return *user.IIN
			}
			return ""
		}(),
		Email:        user.Email,
		Username:     user.Username,
		FirstName:    user.FirstName,
		LastName:     user.LastName,
		IsFirstLogin: user.IsFirstLogin,
	}, nil
}

func (s *E2EEService) ListLinkedDevices(ctx context.Context, userID int64) ([]*domain.LinkedDevice, error) {
	devices, err := s.repo.ListLinkedDevices(ctx, userID)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to list linked devices", err)
	}
	return devices, nil
}

func (s *E2EEService) RevokeLinkedDevice(ctx context.Context, userID int64, deviceID string) error {
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return apperrors.Validation("device_id is required")
	}
	if err := s.repo.RevokeLinkedDevice(ctx, userID, deviceID); err != nil {
		if errors.Is(err, context.Canceled) {
			return apperrors.BadRequest("request cancelled")
		}
		if strings.Contains(err.Error(), "not found") {
			return apperrors.NotFound("linked device")
		}
		return apperrors.InternalWithErr("failed to revoke linked device", err)
	}
	return nil
}

func generateOpaqueSecret(size int) (string, error) {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func hashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func normalizeOptionalString(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func normalizePlatform(platform *string) *string {
	normalized := normalizeOptionalString(platform)
	if normalized == nil {
		defaultPlatform := "web"
		return &defaultPlatform
	}
	lowered := strings.ToLower(*normalized)
	return &lowered
}

func timePtr(t time.Time) *time.Time {
	return &t
}

func clientMetaValue(meta *domain.ClientMetadata, field string) *string {
	if meta == nil {
		return nil
	}
	switch field {
	case "ip":
		return meta.IP
	case "country":
		return meta.Country
	case "city":
		return meta.City
	case "user_agent":
		return meta.UserAgent
	default:
		return nil
	}
}

func mapWebPairingRepoError(err error, action string) error {
	if err == nil {
		return nil
	}

	msg := strings.ToLower(err.Error())

	switch {
	case strings.Contains(msg, "expired"):
		return apperrors.BadRequest("QR code has expired. Please generate a new QR code and try again.")
	case strings.Contains(msg, "already redeemed"):
		return apperrors.Conflict("This web login request has already been used. Please start a new login.")
	case strings.Contains(msg, "not approved yet"):
		return apperrors.Conflict("Please confirm the login from your mobile app first.")
	case strings.Contains(msg, "not found or not approved"):
		return apperrors.BadRequest("History sync is available only after the web device has been approved.")
	case strings.Contains(msg, "belongs to another user"), strings.Contains(msg, "another user"):
		return apperrors.Forbidden("This web login request belongs to another user.")
	case strings.Contains(msg, "not active"):
		switch action {
		case "redeem":
			return apperrors.BadRequest("This web login request is no longer active. Please scan a new QR code.")
		case "approve", "reject":
			return apperrors.BadRequest("This QR code is no longer active. Please ask the web client to generate a new one.")
		case "history":
			return apperrors.BadRequest("History sync is not available for this web login request.")
		default:
			return apperrors.BadRequest("This web login request is no longer active.")
		}
	default:
		return apperrors.InternalWithErr("failed to "+action+" web pairing session", err)
	}
}
