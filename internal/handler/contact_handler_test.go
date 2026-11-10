package handler

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestParseContactFilter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, query := range []string{"limit=0", "limit=101", "limit=abc", "offset=-1", "offset=x", "unit_id=bad", "unit_id=00000000-0000-0000-0000-000000000000", "q=" + strings.Repeat("a", 201), "position=" + strings.Repeat("b", 256)} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("GET", "/contacts?"+query, nil)
		_, err := parseContactFilter(c)
		require.Error(t, err, query)
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/contacts?q=%20anna%20&position=%20Dev%20&limit=20&offset=5", nil)
	f, err := parseContactFilter(c)
	require.NoError(t, err)
	require.Equal(t, "anna", f.Query)
	require.Equal(t, "Dev", f.Position)
	require.Equal(t, 20, f.Limit)
	require.Equal(t, 5, f.Offset)
}

func TestContactProfileRejectsInvalidWrites(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &OrganizationHandler{}
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("user_id", int64(1)); c.Next() })
	router.PATCH("/contacts/:user_id", h.UpdateContact)
	router.POST("/contacts/:user_id/avatar", h.UploadContactAvatar)
	for _, body := range []string{`{"manager_id":2}`, `{"phone":123}`, `{"phone":"+77011234567"} {}`, `{`} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest("PATCH", "/contacts/1", strings.NewReader(body))
		router.ServeHTTP(response, request)
		require.Equal(t, 400, response.Code, body)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest("POST", "/contacts/2/avatar", nil))
	require.Equal(t, 403, response.Code)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest("POST", "/contacts/1/avatar", strings.NewReader("not multipart")))
	require.Equal(t, 400, response.Code)
}
