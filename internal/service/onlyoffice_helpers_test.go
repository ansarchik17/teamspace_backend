package service

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestOnlyOfficeDocumentKeyStableForSameVersion(t *testing.T) {
	fileID := uuid.New()

	first := onlyOfficeDocumentKey(fileID, 3)
	second := onlyOfficeDocumentKey(fileID, 3)

	if first != second {
		t.Fatalf("expected stable key for same version, got %q and %q", first, second)
	}
	if first != fileID.String()+"-v3" {
		t.Fatalf("expected file_id-version key, got %q", first)
	}
}

func TestOnlyOfficeDocumentKeyChangesAfterVersionChange(t *testing.T) {
	fileID := uuid.New()

	first := onlyOfficeDocumentKey(fileID, 3)
	second := onlyOfficeDocumentKey(fileID, 4)

	if first == second {
		t.Fatalf("expected different keys for different versions, got %q", first)
	}
}

func TestOnlyOfficeTokenValidatesSignedClaims(t *testing.T) {
	fileID := uuid.New()
	token, err := GenerateOnlyOfficeToken(fileID, 32, "view", "secret")
	if err != nil {
		t.Fatalf("GenerateOnlyOfficeToken: %v", err)
	}

	claims, err := ValidateOnlyOfficeToken(token, "secret")
	if err != nil {
		t.Fatalf("ValidateOnlyOfficeToken: %v", err)
	}

	if claims.FileID != fileID.String() || claims.UserID != 32 || claims.Purpose != ooTokenPurpose || claims.Mode != "view" {
		t.Fatalf("unexpected claims: %#v", claims)
	}
}

func TestOnlyOfficeTokenRejectsExpiredToken(t *testing.T) {
	token := signedOnlyOfficeTokenForTest(t, OnlyOfficeTokenClaims{
		FileID:  uuid.NewString(),
		UserID:  32,
		Purpose: ooTokenPurpose,
		Mode:    "view",
		Exp:     time.Now().Add(-time.Minute).Unix(),
	}, "secret")

	if _, err := ValidateOnlyOfficeToken(token, "secret"); err == nil {
		t.Fatal("expected expired token to be rejected")
	}
}

func TestOnlyOfficeTokenRejectsWrongPurpose(t *testing.T) {
	token := signedOnlyOfficeTokenForTest(t, OnlyOfficeTokenClaims{
		FileID:  uuid.NewString(),
		UserID:  32,
		Purpose: "download",
		Mode:    "view",
		Exp:     time.Now().Add(time.Minute).Unix(),
	}, "secret")

	if _, err := ValidateOnlyOfficeToken(token, "secret"); err == nil {
		t.Fatal("expected wrong purpose token to be rejected")
	}
}

func TestOnlyOfficeEditorTokenValidatesOnlyEditorPurpose(t *testing.T) {
	fileID := uuid.New()
	token, err := GenerateOnlyOfficeEditorToken(fileID, 32, "edit", "secret")
	if err != nil {
		t.Fatalf("GenerateOnlyOfficeEditorToken: %v", err)
	}

	claims, err := ValidateOnlyOfficeEditorToken(token, "secret")
	if err != nil {
		t.Fatalf("ValidateOnlyOfficeEditorToken: %v", err)
	}
	if claims.Purpose != ooEditorTokenPurpose || claims.FileID != fileID.String() || claims.Mode != "edit" {
		t.Fatalf("unexpected editor claims: %#v", claims)
	}

	if _, err := ValidateOnlyOfficeToken(token, "secret"); err == nil {
		t.Fatal("download token validator must reject editor token purpose")
	}
}

func TestOnlyOfficeTokenRejectsWrongSignature(t *testing.T) {
	fileID := uuid.New()
	token, err := GenerateOnlyOfficeToken(fileID, 32, "view", "secret")
	if err != nil {
		t.Fatalf("GenerateOnlyOfficeToken: %v", err)
	}

	if _, err := ValidateOnlyOfficeToken(token, "other-secret"); err == nil {
		t.Fatal("expected token with wrong signature to be rejected")
	}
}

func TestOnlyOfficeTokenCarriesFileIDForDownloadMismatchCheck(t *testing.T) {
	fileID := uuid.New()
	otherFileID := uuid.New()
	token, err := GenerateOnlyOfficeToken(fileID, 32, "view", "secret")
	if err != nil {
		t.Fatalf("GenerateOnlyOfficeToken: %v", err)
	}

	claims, err := ValidateOnlyOfficeToken(token, "secret")
	if err != nil {
		t.Fatalf("ValidateOnlyOfficeToken: %v", err)
	}
	if claims.FileID == otherFileID.String() {
		t.Fatal("test setup generated equal file IDs")
	}
	if claims.FileID != fileID.String() {
		t.Fatalf("expected token file_id %q, got %q", fileID.String(), claims.FileID)
	}
}

func signedOnlyOfficeTokenForTest(t *testing.T, claims OnlyOfficeTokenClaims, secret string) string {
	t.Helper()

	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}

	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	return strings.Join([]string{encodedPayload, ooHMAC(encodedPayload, secret)}, ".")
}

func TestOnlyOfficeFormats(t *testing.T) {
	for _, tc := range []struct{ name, mime, file, doc string }{
		{"Report.DOCX", "application/octet-stream", "docx", "word"},
		{"budget.xlsx", "application/zip", "xlsx", "cell"},
		{"slides.pptx", "application/zip", "pptx", "slide"},
		{"report", "application/vnd.oasis.opendocument.text", "odt", "word"},
		{"data", "text/csv", "csv", "cell"},
		{"slides", "application/vnd.oasis.opendocument.presentation", "odp", "slide"},
		{"archive.zip", "application/zip", "zip", ""},
		{"unknown", "application/octet-stream", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := resolveOnlyOfficeFileType(tc.name, tc.mime)
			if file != tc.file || resolveOnlyOfficeDocType(file) != tc.doc {
				t.Fatalf("unexpected format %s/%s", file, resolveOnlyOfficeDocType(file))
			}
		})
	}
}

func TestOnlyOfficeAPIURL(t *testing.T) {
	for _, base := range []string{"javascript:alert(1)", "//evil.test", "https://user:pass@office.test", "https://office.test?x=1"} {
		t.Setenv("ONLYOFFICE_PUBLIC_URL", base)
		if _, err := OnlyOfficeAPIURL(); err == nil {
			t.Fatalf("accepted invalid URL: %s", base)
		}
	}
	t.Setenv("ONLYOFFICE_PUBLIC_URL", "")
	if got, err := OnlyOfficeAPIURL(); err != nil || got != "/onlyoffice/web-apps/apps/api/documents/api.js" {
		t.Fatalf("unexpected default %s: %v", got, err)
	}
}

func TestOnlyOfficeCallbackSessionOutlivesDownload(t *testing.T) {
	token, err := generateOnlyOfficeToken(uuid.New(), 32, "edit", ooCallbackTokenPurpose, 24*time.Hour, "secret")
	if err != nil {
		t.Fatal(err)
	}
	claims, err := ValidateOnlyOfficeCallbackToken(token, "secret")
	if err != nil {
		t.Fatal(err)
	}
	if claims.Exp < time.Now().Add(23*time.Hour).Unix() {
		t.Fatal("callback session too short")
	}
	if _, err := ValidateOnlyOfficeToken(token, "secret"); err == nil {
		t.Fatal("callback token allowed file download")
	}
	if _, err := ValidateOnlyOfficeEditorToken(token, "secret"); err == nil {
		t.Fatal("callback token allowed editor launch")
	}
}

func TestOnlyOfficeSignedBrowserConfig(t *testing.T) {
	t.Setenv("ONLYOFFICE_JWT_SECRET", "office-secret")
	t.Setenv("ONLYOFFICE_PUBLIC_URL", "https://office.example.com")
	for _, kind := range []string{"word", "cell", "slide"} {
		for _, edit := range []bool{false, true} {
			cfg, err := prepareOnlyOfficeConfig(&OnlyOfficeEditorConfig{DocumentType: kind, DocumentKey: "key-v1", CanEdit: edit, CallbackURL: "https://api.example.com/callback", Mode: "view"})
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := jwt.Parse(cfg.Token, func(*jwt.Token) (interface{}, error) { return []byte("office-secret"), nil }, jwt.WithValidMethods([]string{"HS256"}))
			if err != nil {
				t.Fatal(err)
			}
			expected, _ := json.Marshal(cfg.BrowserConfig())
			actual, _ := json.Marshal(parsed.Claims)
			if string(expected) != string(actual) {
				t.Fatal("signed payload differs from browser configuration")
			}
			if !edit && strings.Contains(string(actual), "callbackUrl") {
				t.Fatal("viewer may not save")
			}
		}
	}
}

func TestOnlyOfficeStableKeyAndBoundClaims(t *testing.T) {
	id := uuid.New()
	key := onlyOfficeDocumentKey(id, 1)
	file := &domain.DriveFile{ID: id, Version: 3, Key: &key}
	if activeOnlyOfficeKey(file) != key {
		t.Fatal("force-save must preserve collaborative key")
	}
	token, err := signOnlyOfficeClaims(OnlyOfficeTokenClaims{FileID: id.String(), UserID: 3, Mode: "edit", Purpose: ooCallbackTokenPurpose, DocumentKey: key, Exp: time.Now().Add(time.Hour).Unix()}, "secret")
	if err != nil {
		t.Fatal(err)
	}
	claims, err := ValidateOnlyOfficeCallbackToken(token, "secret")
	if err != nil {
		t.Fatal(err)
	}
	if claims.DocumentKey != key {
		t.Fatal("callback session key lost")
	}
	cfg := &OnlyOfficeEditorConfig{CanEdit: true}
	customization := buildEditorConfig(cfg)["customization"].(map[string]interface{})
	if customization["forcesave"] != true || customization["autosave"] != true {
		t.Fatal("editable sessions must support automatic and forced saves")
	}
}
