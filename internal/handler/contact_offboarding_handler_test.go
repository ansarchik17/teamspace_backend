package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOffboardingRejectsMalformedRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &OrganizationHandler{}
	router := gin.New()
	router.POST("/contacts/:user_id/offboarding", h.OffboardContact)
	for _, test := range []struct{ path, body string }{{"/contacts/2/offboarding?dry_run=yes", `{}`}, {"/contacts/0/offboarding", `{}`}, {"/contacts/2/offboarding", `{"successor_id":3,"role":"admin"}`}, {"/contacts/2/offboarding", `{"successor_id":"3"}`}, {"/contacts/2/offboarding", `{} {}`}, {"/contacts/2/offboarding", `{`}} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("POST", test.path, strings.NewReader(test.body)))
		require.Equal(t, 400, w.Code)
	}
}
