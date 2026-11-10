package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOfficeRecoveryValidation(t *testing.T) {
	h := &DriveHandler{logger: zap.NewNop()}
	router := gin.New()
	router.POST("/docs/:id/recover", h.RecoverOfficeSession)
	for _, body := range []string{`{}`, `{"expected_version":0}`, `{"expected_version":-1}`, `invalid`} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("POST", "/docs/"+uuid.NewString()+"/recover", strings.NewReader(body)))
		require.Equal(t, 400, w.Code)
	}
}

func TestOfficeErrorCallbackRequiresCapability(t *testing.T) {
	t.Setenv("ONLYOFFICE_JWT_SECRET", "")
	h := &DriveHandler{logger: zap.NewNop(), jwtSecret: "secret"}
	for _, status := range []string{"3", "7"} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/?file_id="+uuid.NewString(), strings.NewReader(`{"key":"active","status":`+status+`}`))
		h.OnlyOfficeCallback(c)
		require.Equal(t, 401, w.Code)
		require.Contains(t, w.Body.String(), `"error":1`)
	}
}
