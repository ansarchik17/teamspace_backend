package handler

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSDCounterRejectsClientIdentityQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	h := &OrganizationHandler{}
	router.GET("/count", h.SDOpenTicketCount)
	for _, query := range []string{"company_id=other", "user_id=6", "host=other.test", "url=https://other.test"} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest("GET", "/count?"+query, nil))
		require.Equal(t, 400, response.Code)
		require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	}
}
