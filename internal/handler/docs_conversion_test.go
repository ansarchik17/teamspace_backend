package handler

import (
	"bytes"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"mime/multipart"
	"net/http/httptest"
	"testing"
)

func TestDocumentExportRequestValidation(t *testing.T) {
	h := &DriveHandler{logger: zap.NewNop()}
	r := gin.New()
	r.GET("/docs/:id/export", h.ExportDocument)
	for _, path := range []string{"/docs/invalid/export", "/docs/" + uuid.NewString() + "/export?version=0", "/docs/" + uuid.NewString() + "/export?version=bad", "/docs/" + uuid.NewString() + "/export?version=-1"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		require.Equal(t, 400, w.Code)
		require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	}
}

func TestDocumentImportRequiresSingleFile(t *testing.T) {
	h := &DriveHandler{logger: zap.NewNop()}
	r := gin.New()
	r.POST("/docs/import", h.ImportDocument)
	for _, count := range []int{0, 2} {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		for i := 0; i < count; i++ {
			part, err := writer.CreateFormFile("file", "report.docx")
			require.NoError(t, err)
			part.Write([]byte("file"))
		}
		writer.Close()
		req := httptest.NewRequest("POST", "/docs/import", &body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, 400, w.Code)
	}
}
