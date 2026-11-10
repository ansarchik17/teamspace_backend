package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestUpdateLanguageRejectsInvalidPayload(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, body := range []string{`{}`, `{"language":null}`, `{"language":""}`, `{"language":"en"}`, `{"language":"RU"}`, `{"language":" kk "}`, `{"language":123}`, `{`} {
		t.Run(body, func(t *testing.T) {
			router := gin.New()
			router.PUT("/language", func(c *gin.Context) {
				c.Set("user_id", int64(1))
				(&AuthHandler{}).UpdateLanguage(c)
			})
			recorder := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPut, "/language", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(recorder, req)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
			}
		})
	}
}
