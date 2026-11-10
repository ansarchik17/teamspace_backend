package handler

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/service"
)

type oversizedDriveBody struct{}

func (oversizedDriveBody) Read([]byte) (int, error) {
	return 0, &http.MaxBytesError{Limit: service.DriveMaxRequestBytes}
}

func TestDriveUploadLimits(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &DriveHandler{}
	router := gin.New()
	router.POST("/upload", h.UploadFile)
	t.Run("declared oversized request", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/upload", bytes.NewReader(nil))
		req.ContentLength = service.DriveMaxRequestBytes + 1
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		require.Equal(t, 413, rec.Code)
		require.Contains(t, rec.Body.String(), "FILE_TOO_BIG")
	})
	t.Run("streamed oversized request", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/upload", io.NopCloser(oversizedDriveBody{}))
		req.Header.Set("Content-Type", "multipart/form-data; boundary=test")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		require.Equal(t, 413, rec.Code)
	})
	t.Run("too many files before service or storage calls", func(t *testing.T) {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		for range service.DriveMaxFilesPerRequest + 1 {
			part, err := writer.CreateFormFile("file", "file.txt")
			require.NoError(t, err)
			_, err = part.Write([]byte("x"))
			require.NoError(t, err)
		}
		require.NoError(t, writer.Close())
		req := httptest.NewRequest(http.MethodPost, "/upload", &body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		require.Equal(t, 400, rec.Code)
		require.Contains(t, rec.Body.String(), "max_files_per_request")
	})
}
