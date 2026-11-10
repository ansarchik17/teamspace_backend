package handler

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestPublicLinkTTLValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, ttl := range []string{"-1", "9223372037", "18446744074", "9223372036854775807", "1.5"} {
		t.Run(ttl, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("POST", "/", strings.NewReader(`{"resource_type":"file","resource_id":"a6d5b44a-70dd-47b0-a80e-a3b4523c621d","role":"viewer","expires_in_sec":`+ttl+`}`))
			c.Request.Header.Set("Content-Type", "application/json")
			// No service: invalid durations must fail before touching the database.
			(&DriveHandler{}).CreatePublicLink(c)
			require.Equal(t, 400, w.Code)
			require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
		})
	}
	for _, ttl := range []string{"0", "3600", "9223372036"} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/", strings.NewReader(`{"resource_type":"folder","resource_id":"a6d5b44a-70dd-47b0-a80e-a3b4523c621d","role":"manager","expires_in_sec":`+ttl+`}`))
		c.Request.Header.Set("Content-Type", "application/json")
		var req createLinkReq
		require.NoError(t, c.ShouldBindJSON(&req))
	}
}

func TestPublicLinkErrorResponsesAreNotCached(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &DriveHandler{}
	for _, handler := range []gin.HandlerFunc{h.GetPublicItem, h.DownloadPublicFile, h.AcceptPublicLink, h.GetPublicFilePreview, h.PreviewPublicOfficeFile} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Params = gin.Params{{Key: "token", Value: "invalid"}}
		c.Request = httptest.NewRequest("GET", "/", nil)
		handler(c)
		require.Equal(t, 400, w.Code)
		require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
		require.Equal(t, "no-referrer", w.Header().Get("Referrer-Policy"))
	}
}

func TestPublicLinkTargetValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, query := range []string{
		"resource_type=file", "resource_id=invalid&resource_type=file",
		"resource_id=a6d5b44a-70dd-47b0-a80e-a3b4523c621d",
		"resource_id=00000000-0000-0000-0000-000000000000&resource_type=file",
		"resource_id=a6d5b44a-70dd-47b0-a80e-a3b4523c621d&resource_type=user",
	} {
		for _, handler := range []gin.HandlerFunc{(&DriveHandler{}).GetPublicItem, (&DriveHandler{}).DownloadPublicFile, (&DriveHandler{}).PreviewPublicFile, (&DriveHandler{}).GetPublicFilePreview, (&DriveHandler{}).PreviewPublicOfficeFile} {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Params = gin.Params{{Key: "token", Value: "a6d5b44a-70dd-47b0-a80e-a3b4523c621d"}}
			c.Request = httptest.NewRequest("GET", "/?"+query, nil)
			handler(c)
			require.Equal(t, 400, w.Code, query)
			require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
		}
	}
}
