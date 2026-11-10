package service

import (
	"context"
	"crypto/hmac"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

const wopiTokenPurpose = "alem_drive_wopi"
const wopiTokenTTL = 8 * time.Hour

// WOPIAccessClaims scopes each Collabora access token to one file and mode.
type WOPIAccessClaims struct {
	FileID      string `json:"file_id"`
	UserID      int64  `json:"user_id,omitempty"`
	PublicToken string `json:"public_token,omitempty"`
	CanWrite    bool   `json:"can_write"`
	Purpose     string `json:"purpose"`
	Exp         int64  `json:"exp"`
}

type WOPISession struct {
	FileID                 uuid.UUID
	Title                  string
	UserID                 string
	UserName               string
	CanWrite               bool
	AccessToken            string
	AccessTokenExpiresAtMs int64
	WOPISrc                string
}

func GenerateWOPIAccessToken(fileID uuid.UUID, userID int64, publicToken string, canWrite bool, secret string) (string, error) {
	return generateWOPIAccessTokenUntil(fileID, userID, publicToken, canWrite, secret, time.Now().Add(wopiTokenTTL))
}

func generateWOPIAccessTokenUntil(fileID uuid.UUID, userID int64, publicToken string, canWrite bool, secret string, expiresAt time.Time) (string, error) {
	claims := WOPIAccessClaims{
		FileID: fileID.String(), UserID: userID, PublicToken: publicToken,
		CanWrite: canWrite, Purpose: wopiTokenPurpose, Exp: expiresAt.Unix(),
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("marshal WOPI claims: %w", err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	return encoded + "." + ooHMAC(encoded, secret+":wopi"), nil
}

func ValidateWOPIAccessToken(token, secret string) (*WOPIAccessClaims, error) {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 || !hmacEqual(parts[1], ooHMAC(parts[0], secret+":wopi")) {
		return nil, apperrors.Unauthorized("invalid WOPI access token")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, apperrors.Unauthorized("invalid WOPI access token")
	}
	var claims WOPIAccessClaims
	if err := json.Unmarshal(raw, &claims); err != nil || claims.Purpose != wopiTokenPurpose || claims.Exp < time.Now().Unix() {
		return nil, apperrors.Unauthorized("expired or invalid WOPI access token")
	}
	if _, err := uuid.Parse(claims.FileID); err != nil || (claims.UserID <= 0 && claims.PublicToken == "") {
		return nil, apperrors.Unauthorized("invalid WOPI access scope")
	}
	return &claims, nil
}

func hmacEqual(a, b string) bool {
	return hmac.Equal([]byte(a), []byte(b))
}

func (s *DriveService) BuildWOPISession(ctx context.Context, userID int64, fileID uuid.UUID, mode, jwtSecret string) (*WOPISession, error) {
	if mode != "view" && mode != "edit" {
		mode = "view"
	}
	need := domain.RoleViewer
	if mode == "edit" {
		need = domain.RoleEditor
	}
	meta, err := s.canAccessFile(ctx, userID, fileID, need)
	if err != nil && mode == "edit" {
		mode = "view"
		meta, err = s.canAccessFile(ctx, userID, fileID, domain.RoleViewer)
	}
	if err != nil {
		return nil, err
	}
	userName, err := s.GetWOPIUserName(ctx, WOPIAccessClaims{UserID: userID})
	if err != nil {
		return nil, err
	}
	canWrite := mode == "edit"
	expiresAt := time.Now().Add(wopiTokenTTL)
	token, err := generateWOPIAccessTokenUntil(fileID, userID, "", canWrite, jwtSecret, expiresAt)
	if err != nil {
		return nil, apperrors.Internal("failed to create WOPI access token")
	}
	return &WOPISession{
		FileID: fileID, Title: meta.RealName, UserID: fmt.Sprintf("%d", userID),
		UserName: userName, CanWrite: canWrite,
		AccessToken: token, AccessTokenExpiresAtMs: expiresAt.Unix() * 1000,
		WOPISrc: fmt.Sprintf("%s/api/v1/drive/wopi/files/%s", strings.TrimRight(s.publicURL, "/"), fileID),
	}, nil
}

// GetWOPIUserName resolves the authenticated Workspace profile on each metadata
// request, including sessions whose access tokens were issued before a rename.
func (s *DriveService) GetWOPIUserName(ctx context.Context, claims WOPIAccessClaims) (string, error) {
	if claims.PublicToken != "" {
		return "Гость", nil
	}
	name, err := s.repo.GetUserNameByID(ctx, claims.UserID)
	if err != nil {
		return "", apperrors.Internal("failed to load WOPI user profile").WithError(err)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Пользователь"
	}
	return name, nil
}

func (s *DriveService) BuildPublicWOPISession(ctx context.Context, publicToken, fileID uuid.UUID, jwtSecret string) (*WOPISession, error) {
	item, err := s.GetPublicItemByToken(ctx, publicToken, PublicLinkTarget{Type: domain.ResourceFile, ID: fileID})
	if err != nil {
		return nil, err
	}
	if item.File == nil {
		return nil, apperrors.NotFound("file not found")
	}
	expiresAt := time.Now().Add(wopiTokenTTL)
	token, err := generateWOPIAccessTokenUntil(fileID, 0, publicToken.String(), false, jwtSecret, expiresAt)
	if err != nil {
		return nil, apperrors.Internal("failed to create WOPI access token")
	}
	return &WOPISession{
		FileID: fileID, Title: item.File.RealName, UserID: "guest", UserName: "Гость", CanWrite: false,
		AccessToken: token, AccessTokenExpiresAtMs: expiresAt.Unix() * 1000,
		WOPISrc: fmt.Sprintf("%s/api/v1/drive/wopi/files/%s", strings.TrimRight(s.publicURL, "/"), fileID),
	}, nil
}

func (s *DriveService) AcquireWOPILock(ctx context.Context, fileID uuid.UUID, value string) (string, bool, error) {
	return s.repo.AcquireWOPILock(ctx, fileID, value, 30*time.Minute)
}

func (s *DriveService) GetWOPILock(ctx context.Context, fileID uuid.UUID) (string, error) {
	return s.repo.GetWOPILock(ctx, fileID)
}

func (s *DriveService) RefreshWOPILock(ctx context.Context, fileID uuid.UUID, value string) (bool, error) {
	return s.repo.RefreshWOPILock(ctx, fileID, value, 30*time.Minute)
}

func (s *DriveService) ReplaceWOPILock(ctx context.Context, fileID uuid.UUID, oldValue, newValue string) (string, bool, error) {
	return s.repo.ReplaceWOPILock(ctx, fileID, oldValue, newValue, 30*time.Minute)
}

func (s *DriveService) ReleaseWOPILock(ctx context.Context, fileID uuid.UUID, value string) (bool, error) {
	return s.repo.ReleaseWOPILock(ctx, fileID, value)
}
