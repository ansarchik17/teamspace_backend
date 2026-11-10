package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestDriveVersionRequestValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &DriveHandler{}
	router := gin.New()
	router.POST("/files/:id/versions/:version/restore", h.RestoreFileVersion)
	router.POST("/files/:id/versions", h.UploadFileVersion)
	id := uuid.NewString()
	for _, tc := range []struct{ path, body string }{
		{"/files/invalid/versions/1/restore", `{"expected_version":1}`},
		{"/files/" + id + "/versions/0/restore", `{"expected_version":1}`},
		{"/files/" + id + "/versions/bad/restore", `{"expected_version":1}`},
		{"/files/" + id + "/versions/1/restore", `{}`},
		{"/files/" + id + "/versions/1/restore", `{"expected_version":-1}`},
		{"/files/" + id + "/versions", `{}`},
	} {
		t.Run(tc.path+tc.body, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, req)
			require.Equal(t, 400, response.Code)
		})
	}
}
