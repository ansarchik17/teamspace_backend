package handler

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractClientMetadata_UsesHeadersAndUserAgent(t *testing.T) {
	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest("GET", "/api/v1/web/pairing/sessions", nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 Test Browser")
	req.Header.Set("CF-IPCountry", "KZ")
	req.Header.Set("X-Geo-City", "Almaty")
	req.RemoteAddr = "203.0.113.10:12345"
	c.Request = req

	meta := extractClientMetadata(c)
	require.NotNil(t, meta)
	require.NotNil(t, meta.IP)
	require.NotNil(t, meta.Country)
	require.NotNil(t, meta.City)
	require.NotNil(t, meta.UserAgent)

	assert.Equal(t, "203.0.113.10", *meta.IP)
	assert.Equal(t, "KZ", *meta.Country)
	assert.Equal(t, "Almaty", *meta.City)
	assert.Equal(t, "Mozilla/5.0 Test Browser", *meta.UserAgent)
}

func TestFirstMetaHeader_PicksFirstNonEmptyValue(t *testing.T) {
	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Geo-City", "Astana")
	req.Header.Set("X-City", "Almaty")
	c.Request = req

	got := firstMetaHeader(c, "Missing", "X-Geo-City", "X-City")
	require.NotNil(t, got)
	assert.Equal(t, "Astana", *got)
}

func TestNormalizeMetaString_EmptyReturnsNil(t *testing.T) {
	assert.Nil(t, normalizeMetaString("   "))
	got := normalizeMetaString(" Chrome ")
	require.NotNil(t, got)
	assert.Equal(t, "Chrome", *got)
}
