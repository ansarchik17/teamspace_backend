package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

// ─────────────────────────────────────────────
// Signed Token (HMAC-SHA256, purpose=onlyoffice_download)
// ─────────────────────────────────────────────

// OnlyOfficeTokenClaims — payload signed download-token-а для OnlyOffice DocumentServer.
// TTL 20 минут — достаточно для конвертации и рендера.
type OnlyOfficeTokenClaims struct {
	FileID      string `json:"file_id"`
	Version     int    `json:"version,omitempty"`
	DocumentKey string `json:"document_key,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
	UserID      int64  `json:"user_id"`
	Purpose     string `json:"purpose"` // download, editor or callback capability
	Mode        string `json:"mode"`    // "view" | "edit" | "comment" | "review"
	Exp         int64  `json:"exp"`     // unix timestamp
}

const ooTokenPurpose = "onlyoffice_download"
const ooCallbackTokenPurpose = "onlyoffice_callback"
const ooEditorTokenPurpose = "onlyoffice_editor"
const ooTokenTTL = 20 * time.Minute
const ooEditorTokenTTL = 10 * time.Minute

// GenerateOnlyOfficeToken создаёт подписанный download-token для DocumentServer.
// secret — это JWT.Secret из конфига с суффиксом ":oo", чтобы исключить
// коллизии с user Bearer-токенами.
func GenerateOnlyOfficeToken(fileID uuid.UUID, userID int64, mode, jwtSecret string) (string, error) {
	return generateOnlyOfficeToken(fileID, userID, mode, ooTokenPurpose, ooTokenTTL, jwtSecret)
}

// GenerateOnlyOfficeEditorToken создаёт короткоживущий token для открытия HTML editor page в WebView.
func GenerateOnlyOfficeEditorToken(fileID uuid.UUID, userID int64, mode, jwtSecret string) (string, error) {
	return generateOnlyOfficeToken(fileID, userID, mode, ooEditorTokenPurpose, ooEditorTokenTTL, jwtSecret)
}

func generateOnlyOfficeToken(fileID uuid.UUID, userID int64, mode, purpose string, ttl time.Duration, jwtSecret string) (string, error) {
	claims := OnlyOfficeTokenClaims{
		FileID:  fileID.String(),
		UserID:  userID,
		Purpose: purpose,
		Mode:    mode,
		Exp:     time.Now().Add(ttl).Unix(),
	}
	return signOnlyOfficeClaims(claims, jwtSecret)
}

func signOnlyOfficeClaims(claims OnlyOfficeTokenClaims, jwtSecret string) (string, error) {
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("marshal oo token claims: %w", err)
	}
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	sig := ooHMAC(encodedPayload, jwtSecret)
	return encodedPayload + "." + sig, nil
}

// ValidateOnlyOfficeToken проверяет подпись и срок действия token-а.
func ValidateOnlyOfficeToken(token, jwtSecret string) (*OnlyOfficeTokenClaims, error) {
	return validateOnlyOfficeTokenPurpose(token, jwtSecret, ooTokenPurpose)
}

// ValidateOnlyOfficeEditorToken проверяет signed editor session token.
func ValidateOnlyOfficeEditorToken(token, jwtSecret string) (*OnlyOfficeTokenClaims, error) {
	return validateOnlyOfficeTokenPurpose(token, jwtSecret, ooEditorTokenPurpose)
}

func validateOnlyOfficeTokenPurpose(token, jwtSecret, purpose string) (*OnlyOfficeTokenClaims, error) {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return nil, apperrors.Unauthorized("invalid oo token format")
	}
	encodedPayload, sig := parts[0], parts[1]

	// Проверяем HMAC
	expectedSig := ooHMAC(encodedPayload, jwtSecret)
	if !hmac.Equal([]byte(sig), []byte(expectedSig)) {
		return nil, apperrors.Unauthorized("invalid oo token signature")
	}

	// Декодируем payload
	raw, err := base64.RawURLEncoding.DecodeString(encodedPayload)
	if err != nil {
		return nil, apperrors.Unauthorized("invalid oo token encoding")
	}
	var claims OnlyOfficeTokenClaims
	if err := json.Unmarshal(raw, &claims); err != nil {
		return nil, apperrors.Unauthorized("invalid oo token payload")
	}
	if claims.Purpose != purpose {
		return nil, apperrors.Unauthorized("invalid oo token purpose")
	}
	if time.Now().Unix() > claims.Exp {
		return nil, apperrors.Unauthorized("oo token expired")
	}
	return &claims, nil
}

func ooHMAC(data, secret string) string {
	key := []byte(secret + ":oo")
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

// ─────────────────────────────────────────────
// OnlyOffice Editor Config
// ─────────────────────────────────────────────

// OnlyOfficeEditorConfig — всё что нужно handler-у чтобы отрендерить HTML.
type OnlyOfficeEditorConfig struct {
	DocumentType     string
	FileType         string
	DocumentKey      string
	Title            string
	DocumentURL      string // signed download URL
	CallbackURL      string
	Mode             string // "view" | "edit" | "comment" | "review"
	UserID           string
	UserName         string
	UserImage        string
	CollaborationURL string
	CanEdit          bool
	APIJSURL         string
	Token            string
}

// BuildOnlyOfficeConfig проверяет доступ, собирает config и генерирует signed URL.
func (s *DriveService) BuildOnlyOfficeConfig(
	ctx context.Context,
	userID int64,
	fileID uuid.UUID,
	mode string,
	jwtSecret string,
	publicURL string,
) (*OnlyOfficeEditorConfig, error) {
	start := time.Now()
	mode = NormalizeOnlyOfficeMode(mode)

	// Любой режим записи требует editor; просмотр — viewer.
	requiredRole := domain.RoleViewer
	if OnlyOfficeModeCanSave(mode) {
		requiredRole = domain.RoleEditor
	}

	meta, err := s.canAccessFile(ctx, userID, fileID, requiredRole)
	if err != nil {
		// Если нет прав на edit — проверим хотя бы view
		if OnlyOfficeModeCanSave(mode) {
			meta, err = s.canAccessFile(ctx, userID, fileID, domain.RoleViewer)
			if err != nil {
				return nil, err
			}
			mode = "view" // downgrade до view
		} else {
			return nil, err
		}
	}

	canEdit := mode == "edit"
	docKey := activeOnlyOfficeKey(meta)
	fileType := resolveOnlyOfficeFileType(meta.RealName, meta.MimeType)
	docType := resolveOnlyOfficeDocType(fileType)
	if docType == "" {
		return nil, apperrors.New(apperrors.ErrCodeInvalidFileType, "unsupported editor file type", http.StatusUnsupportedMediaType)
	}

	if mode == "review" && fileType != "docx" {
		return nil, apperrors.New(apperrors.ErrCodeInvalidFileType, "review mode requires DOCX", http.StatusUnsupportedMediaType)
	}

	// Signed download URL для DocumentServer (не пользовательский token)
	signedToken, err := signOnlyOfficeClaims(OnlyOfficeTokenClaims{FileID: fileID.String(), UserID: userID, Mode: mode, Purpose: ooTokenPurpose, Version: meta.Version, Exp: time.Now().Add(ooTokenTTL).Unix()}, jwtSecret)
	if err != nil {
		return nil, apperrors.Internal("failed to generate oo token")
	}
	downloadBase := os.Getenv("ONLYOFFICE_DOWNLOAD_BASE_URL")
	if downloadBase == "" {
		downloadBase = publicURL
	}
	base := strings.TrimRight(downloadBase, "/")
	documentURL := fmt.Sprintf("%s/api/v1/drive/onlyoffice/download?file_id=%s&token=%s",
		base, fileID.String(), signedToken)
	callbackToken, err := signOnlyOfficeClaims(OnlyOfficeTokenClaims{FileID: fileID.String(), UserID: userID, Mode: mode, Purpose: ooCallbackTokenPurpose, DocumentKey: docKey, Exp: time.Now().Add(24 * time.Hour).Unix()}, jwtSecret)
	if err != nil {
		return nil, apperrors.Internal("failed to generate callback token")
	}
	callbackURL := fmt.Sprintf("%s/api/v1/drive/onlyoffice/callback?file_id=%s&token=%s",
		base, fileID.String(), callbackToken)

	s.logger.Info("onlyoffice_config_built",
		zap.String("file_id", fileID.String()),
		zap.Int64("user_id", userID),
		zap.String("filename", meta.RealName),
		zap.String("file_type", fileType),
		zap.String("mime_type", meta.MimeType),
		zap.Int64("size_bytes", meta.Size),
		zap.Int("version", meta.Version),
		zap.String("document_key", docKey),
		zap.String("document_url_host", base),
		zap.String("mode", mode),
		zap.String("permission_result", "allowed"),
		zap.Bool("can_edit", canEdit),
		zap.Int64("total_ms", time.Since(start).Milliseconds()),
	)

	profiles, err := s.repo.OfficeUserProfiles(ctx, []int64{userID})
	if err != nil {
		return nil, apperrors.InternalWithErr("load editor profile", err)
	}
	if len(profiles) != 1 {
		return nil, apperrors.Unauthorized("user profile unavailable")
	}
	collaborationToken, err := signOnlyOfficeClaims(OnlyOfficeTokenClaims{FileID: fileID.String(), UserID: userID, Mode: mode, Purpose: ooCollaborationPurpose, DocumentKey: docKey, SessionID: uuid.NewString(), Exp: time.Now().Add(24 * time.Hour).Unix()}, jwtSecret)
	if err != nil {
		return nil, err
	}
	cfg := &OnlyOfficeEditorConfig{
		DocumentType:     docType,
		FileType:         fileType,
		DocumentKey:      docKey,
		Title:            meta.RealName,
		DocumentURL:      documentURL,
		CallbackURL:      callbackURL,
		Mode:             mode,
		UserID:           fmt.Sprintf("%d", userID),
		UserName:         profiles[0].Name,
		UserImage:        officeAvatar(profiles[0], publicURL),
		CollaborationURL: strings.TrimRight(publicURL, "/") + "/api/v1/drive/onlyoffice/collaboration?file_id=" + fileID.String() + "&token=" + url.QueryEscape(collaborationToken),
		CanEdit:          canEdit,
	}
	return prepareOnlyOfficeConfig(cfg)
}

func prepareOnlyOfficeConfig(cfg *OnlyOfficeEditorConfig) (*OnlyOfficeEditorConfig, error) {
	apiURL, err := OnlyOfficeAPIURL()
	if err != nil {
		return nil, err
	}
	cfg.APIJSURL = apiURL
	if secret := os.Getenv("ONLYOFFICE_JWT_SECRET"); secret != "" {
		cfg.Token, err = jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims(cfg.BrowserConfig())).SignedString([]byte(secret))
		if err != nil {
			return nil, apperrors.Internal("failed to sign editor configuration")
		}
	}
	return cfg, nil
}

// ─────────────────────────────────────────────
// Signed Download (для DocumentServer)
// ─────────────────────────────────────────────

// DownloadFileForOnlyOffice стримит файл по validated signed token.
// Возвращает (reader, mimeType, size, filename, error).
func (s *DriveService) DownloadFileForOnlyOffice(
	ctx context.Context,
	fileID uuid.UUID,
	token string,
	jwtSecret string,
) (interface{}, string, int64, string, error) {
	claims, err := ValidateOnlyOfficeToken(token, jwtSecret)
	if err != nil {
		return nil, "", 0, "", err
	}
	if claims.FileID != fileID.String() {
		return nil, "", 0, "", apperrors.Unauthorized("oo token file_id mismatch")
	}

	meta, err := s.repo.GetFileByID(ctx, fileID)
	if err != nil {
		if err.Error() == "file not found" {
			return nil, "", 0, "", apperrors.NotFound("file not found")
		}
		return nil, "", 0, "", apperrors.InternalWithErr("failed to load drive file metadata", err)
	}
	if meta == nil || meta.DeletedAt != nil {
		return nil, "", 0, "", apperrors.NotFound("file not found")
	}

	if claims.Version > 0 {
		reader, size, name, err := s.DownloadFileVersion(ctx, claims.UserID, fileID, claims.Version)
		return reader, meta.MimeType, size, name, err
	}
	return s.DownloadFileByID(ctx, claims.UserID, fileID)
}

// ─────────────────────────────────────────────
// File-type helpers
// ─────────────────────────────────────────────

func resolveOnlyOfficeFileType(filename, mimeType string) string {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(filename), "."))
	if ext != "" {
		return ext
	}
	switch {
	case strings.Contains(mimeType, "wordprocessingml"):
		return "docx"
	case strings.Contains(mimeType, "spreadsheetml"):
		return "xlsx"
	case strings.Contains(mimeType, "presentationml"):
		return "pptx"
	case mimeType == "application/vnd.oasis.opendocument.text":
		return "odt"
	case mimeType == "application/vnd.oasis.opendocument.spreadsheet":
		return "ods"
	case mimeType == "application/vnd.oasis.opendocument.presentation":
		return "odp"
	case mimeType == "text/plain":
		return "txt"
	case mimeType == "text/csv":
		return "csv"
	case mimeType == "application/rtf", mimeType == "text/rtf":
		return "rtf"
	case strings.Contains(mimeType, "msword"):
		return "doc"
	case strings.Contains(mimeType, "ms-excel"):
		return "xls"
	case strings.Contains(mimeType, "ms-powerpoint"):
		return "ppt"
	}
	return ""
}

func resolveOnlyOfficeDocType(fileType string) string {
	switch fileType {
	case "doc", "docx", "odt", "txt", "rtf":
		return "word"
	case "xls", "xlsx", "ods", "csv":
		return "cell"
	case "ppt", "pptx", "odp":
		return "slide"
	default:
		return ""
	}
}

// BrowserConfig is the exact payload signed for DocumentServer and sent to DocsAPI.
func (cfg *OnlyOfficeEditorConfig) BrowserConfig() map[string]interface{} {
	permissions := map[string]interface{}{
		"edit":                 cfg.CanEdit,
		"download":             true,
		"print":                true,
		"review":               cfg.DocumentType == "word" && (cfg.CanEdit || cfg.Mode == "review"),
		"comment":              cfg.canSave(),
		"fillForms":            cfg.CanEdit,
		"modifyFilter":         cfg.CanEdit,
		"modifyContentControl": cfg.CanEdit,
	}
	if cfg.Mode == "review" {
		permissions["reviewGroups"] = []string{} // Suggestions cannot accept/reject other changes.
	}
	config := map[string]interface{}{
		"documentType": cfg.DocumentType,
		"document": map[string]interface{}{
			"fileType":    cfg.FileType,
			"key":         cfg.DocumentKey,
			"title":       cfg.Title,
			"url":         cfg.DocumentURL,
			"permissions": permissions,
		},
		"editorConfig": buildEditorConfig(cfg),
		"height":       "100%",
		"width":        "100%",
	}

	return config
}

func buildEditorConfig(cfg *OnlyOfficeEditorConfig) map[string]interface{} {
	mode := "view"
	if cfg.canSave() {
		mode = "edit" // DocsAPI uses edit even for comment/review-only sessions.
	}
	ec := map[string]interface{}{
		"lang":   "ru",
		"region": "kk-KZ",
		"mode":   mode,
		"user": map[string]interface{}{
			"id":    cfg.UserID,
			"name":  cfg.UserName,
			"image": cfg.UserImage,
		},
		"coEditing": map[string]interface{}{
			"mode":   "fast",
			"change": false,
		},
		"customization": map[string]interface{}{
			"autosave":       cfg.canSave(),
			"forcesave":      cfg.canSave(),
			"compactToolbar": false,
			"toolbarNoTabs":  false,
			"hideRightMenu":  false,
			"features": map[string]interface{}{
				"spellcheck": cfg.CanEdit,
			},
		},
	}
	if cfg.DocumentType == "word" {
		review := map[string]interface{}{"reviewDisplay": "markup"}
		if cfg.Mode == "review" {
			review["trackChanges"] = true
			review["showReviewChanges"] = true
		}
		ec["customization"].(map[string]interface{})["review"] = review
	}
	if cfg.canSave() && cfg.CallbackURL != "" {
		ec["callbackUrl"] = cfg.CallbackURL
	}
	return ec
}

func OnlyOfficeAPIURL() (string, error) {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("ONLYOFFICE_PUBLIC_URL")), "/")
	if base == "" {
		base = "/onlyoffice"
	}
	u, err := url.Parse(base)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (base != "/onlyoffice" && (u.Host == "" || (u.Scheme != "https" && u.Scheme != "http"))) {
		return "", apperrors.Internal("invalid ONLYOFFICE_PUBLIC_URL")
	}
	return base + "/web-apps/apps/api/documents/api.js", nil
}

// ValidateOnlyOfficeCallbackToken accepts existing short-lived sessions during rollout.
func ValidateOnlyOfficeCallbackToken(token, secret string) (*OnlyOfficeTokenClaims, error) {
	claims, err := validateOnlyOfficeTokenPurpose(token, secret, ooCallbackTokenPurpose)
	if err != nil {
		return ValidateOnlyOfficeToken(token, secret)
	}
	return claims, nil
}

func activeOnlyOfficeKey(file *domain.DriveFile) string {
	if file.Key != nil && *file.Key != "" {
		return *file.Key
	}
	return onlyOfficeDocumentKey(file.ID, file.Version)
}

// NormalizeOnlyOfficeMode preserves the historical read-only fallback for unknown modes.
func NormalizeOnlyOfficeMode(mode string) string {
	switch mode {
	case "edit", "comment", "review":
		return mode
	default:
		return "view"
	}
}

func OnlyOfficeModeCanSave(mode string) bool {
	return mode == "edit" || mode == "comment" || mode == "review"
}

func (cfg *OnlyOfficeEditorConfig) canSave() bool {
	return cfg.CanEdit || cfg.Mode == "comment" || cfg.Mode == "review"
}
