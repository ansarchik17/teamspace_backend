package handler

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

func TestOnlyOfficeBrowserPageRequiresSignedSession(t *testing.T) {
	h := &DriveHandler{jwtSecret: "secret", logger: zap.NewNop()}
	for _, token := range []string{"", "invalid"} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/?file_id="+uuid.NewString()+"&token="+token, nil)
		h.OnlyOfficeEditorPage(c)
		require.Equal(t, 401, w.Code)
		require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	}
}

func TestOnlyOfficeExternalServerAndEscaping(t *testing.T) {
	t.Setenv("ONLYOFFICE_PUBLIC_URL", "https://office.example.com")
	apiURL, err := service.OnlyOfficeAPIURL()
	require.NoError(t, err)
	page := buildOnlyOfficeHTML(&service.OnlyOfficeEditorConfig{APIJSURL: apiURL, Title: "</script><script>alert(1)</script>", Token: "signed-config", DocumentType: "slide"}, "")
	require.Contains(t, page, `src="https://office.example.com/web-apps/apps/api/documents/api.js"`)
	require.Contains(t, page, `"token":"signed-config"`)
	require.NotContains(t, page, "</script><script>alert")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	setDrivePreviewEmbedding(c, true)
	require.Contains(t, w.Header().Get("Content-Security-Policy"), "frame-src 'self' https://office.example.com")
	require.NotContains(t, ooErrorHTML("<script>", "<img onerror=alert(1)>"), "<img")
}

func TestOnlyOfficeCallbackJWT(t *testing.T) {
	t.Setenv("ONLYOFFICE_JWT_SECRET", "document-server-secret")
	for _, nested := range []bool{false, true} {
		claims := jwt.MapClaims{"key": "key-v1", "status": 2, "url": "https://office.example.com/cache/file"}
		if nested {
			claims = jwt.MapClaims{"payload": claims}
		}
		token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte("document-server-secret"))
		require.NoError(t, err)
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/", strings.NewReader("{}"))
		req := onlyOfficeCallbackReq{Key: "key-v1", Status: 2, URL: "https://office.example.com/cache/file", Token: token}
		require.NoError(t, validateOnlyOfficeCallbackJWT(c, req))
		req.URL = "http://attacker.invalid"
		require.Error(t, validateOnlyOfficeCallbackJWT(c, req))
		req.Token = ""
		require.Error(t, validateOnlyOfficeCallbackJWT(c, req))
	}
}

func TestOnlyOfficeCallbackErrorsUseProtocolResponse(t *testing.T) {
	h := &DriveHandler{jwtSecret: "secret", logger: zap.NewNop()}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/?file_id=bad", strings.NewReader(`{}`))
	h.OnlyOfficeCallback(c)
	require.Equal(t, 400, w.Code)
	require.JSONEq(t, `{"error":1,"message":"invalid file_id"}`, w.Body.String())
}

func TestOnlyOfficeCallbackRejectsDifferentSessionKey(t *testing.T) {
	id := uuid.New()
	claims := service.OnlyOfficeTokenClaims{FileID: id.String(), UserID: 3, Purpose: "onlyoffice_callback", Mode: "edit", DocumentKey: "active-key", Exp: time.Now().Add(time.Hour).Unix()}
	raw, err := json.Marshal(claims)
	require.NoError(t, err)
	payload := base64.RawURLEncoding.EncodeToString(raw)
	mac := hmac.New(sha256.New, []byte("secret:oo"))
	mac.Write([]byte(payload))
	token := payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	_, err = resolveOnlyOfficeCallbackUserID(id, token, "secret", "active-key")
	require.NoError(t, err)
	_, err = resolveOnlyOfficeCallbackUserID(id, token, "secret", "different-key")
	require.Error(t, err)
	_, err = resolveOnlyOfficeCallbackUserID(id, token, "secret")
	require.Error(t, err)
}

func TestOnlyOfficeReviewCallbackModes(t *testing.T) {
	for _, mode := range []string{"edit", "comment", "review", "view", "invalid"} {
		id := uuid.New()
		// Legacy download capabilities still pass through the same mode gate.
		token, err := service.GenerateOnlyOfficeToken(id, 3, mode, "secret")
		require.NoError(t, err)
		user, err := resolveOnlyOfficeCallbackUserID(id, token, "secret")
		if mode == "view" || mode == "invalid" {
			require.Error(t, err)
		} else {
			require.NoError(t, err)
			require.Equal(t, int64(3), user)
		}
		require.Equal(t, service.NormalizeOnlyOfficeMode(mode), normalizeOnlyOfficeMode(mode))
	}
}
