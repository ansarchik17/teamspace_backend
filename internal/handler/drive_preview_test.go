package handler

import (
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"github.com/west6ide/workspace-backend/internal/service"
)

func TestDrivePreviewHTTPRanges(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, method, ranges, body, contentRange string
		status                                   int
	}{
		{"full", "GET", "", "0123456789", "", 200},
		{"partial", "GET", "bytes=2-5", "2345", "bytes 2-5/10", 206},
		{"suffix", "GET", "bytes=-3", "789", "bytes 7-9/10", 206},
		{"open-ended", "GET", "bytes=8-", "89", "bytes 8-9/10", 206},
		{"head", "HEAD", "", "", "", 200},
		{"unsatisfiable", "GET", "bytes=10-20", "", "bytes */10", 416},
		{"invalid", "GET", "bytes=abc", "", "", 416},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(tc.method, "/preview", nil)
			c.Request.Header.Set("Range", tc.ranges)
			serveDrivePreview(c, bytes.NewReader([]byte("0123456789")), &domain.DrivePreview{Name: "файл.pdf", MIMEType: "application/pdf"})
			require.Equal(t, tc.status, w.Code)
			if tc.status != 416 {
				require.Equal(t, tc.body, w.Body.String())
			}
			require.Equal(t, tc.contentRange, w.Header().Get("Content-Range"))
			require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
			require.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
			if tc.status < 400 {
				require.Equal(t, "bytes", w.Header().Get("Accept-Ranges"))
				disposition, params, err := mime.ParseMediaType(w.Header().Get("Content-Disposition"))
				require.NoError(t, err)
				require.Equal(t, "inline", disposition)
				require.Equal(t, "файл.pdf", params["filename"])
			}
		})
	}
}

func TestDrivePreviewMultipartAndEmpty(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	c.Request.Header.Set("Range", "bytes=0-1,8-9")
	serveDrivePreview(c, bytes.NewReader([]byte("0123456789")), &domain.DrivePreview{Name: "video.mp4", MIMEType: "video/mp4"})
	require.Equal(t, 206, w.Code)
	mt, params, err := mime.ParseMediaType(w.Header().Get("Content-Type"))
	require.NoError(t, err)
	require.Equal(t, "multipart/byteranges", mt)
	parts := multipart.NewReader(w.Body, params["boundary"])
	for _, expected := range []string{"01", "89"} {
		part, err := parts.NextPart()
		require.NoError(t, err)
		data, err := io.ReadAll(part)
		require.NoError(t, err)
		require.Equal(t, expected, string(data))
	}
	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	serveDrivePreview(c, bytes.NewReader(nil), &domain.DrivePreview{Name: "empty.pdf", MIMEType: "application/pdf"})
	require.Equal(t, 200, w.Code)
	require.Equal(t, "0", w.Header().Get("Content-Length"))
	require.Empty(t, w.Body.Bytes())
}

func TestOfficePreviewHTMLIsReadOnly(t *testing.T) {
	html := buildOnlyOfficeHTML(&service.OnlyOfficeEditorConfig{Mode: "view", CanEdit: false, FileType: "docx", DocumentType: "word", CallbackURL: "must-not-be-used"}, "https://example.com")
	require.Contains(t, html, `"mode":"view"`)
	require.Contains(t, html, `"edit":false`)
	require.Contains(t, html, `"comment":false`)
	require.NotContains(t, html, `"callbackUrl"`)
	require.Contains(t, html, `src="/onlyoffice/web-apps/apps/api/documents/api.js"`)
	require.Contains(t, html, "drive-office-ready")
	require.Contains(t, html, "drive-office-error")
}

func TestOfficePreviewTokenCannotSave(t *testing.T) {
	id := uuid.New()
	view, err := service.GenerateOnlyOfficeToken(id, 42, "view", "secret")
	require.NoError(t, err)
	_, err = resolveOnlyOfficeCallbackUserID(id, view, "secret")
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	edit, err := service.GenerateOnlyOfficeToken(id, 42, "edit", "secret")
	require.NoError(t, err)
	userID, err := resolveOnlyOfficeCallbackUserID(id, edit, "secret")
	require.NoError(t, err)
	require.Equal(t, int64(42), userID)
	_, err = resolveOnlyOfficeCallbackUserID(uuid.New(), edit, "secret")
	require.Equal(t, 401, apperrors.ToAppError(err).StatusCode)
	_, err = resolveOnlyOfficeCallbackUserID(id, "", "secret")
	require.Equal(t, 401, apperrors.ToAppError(err).StatusCode)
	_, err = resolveOnlyOfficeCallbackUserID(id, edit, "wrong-secret")
	require.Equal(t, 401, apperrors.ToAppError(err).StatusCode)
}

func TestDrivePreviewOverridesGlobalFrameDenial(t *testing.T) {
	for _, office := range []bool{false, true} {
		r := gin.New()
		r.Use(middleware.SecureHeaders())
		r.GET("/preview", func(c *gin.Context) { setDrivePreviewEmbedding(c, office); c.Status(200) })
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/preview", nil))
		require.Equal(t, "SAMEORIGIN", w.Header().Get("X-Frame-Options"))
		require.Contains(t, w.Header().Get("Content-Security-Policy"), "frame-ancestors 'self'")
		if office {
			require.Contains(t, w.Header().Get("Content-Security-Policy"), "script-src 'self' 'unsafe-inline'")
		}
	}
}
