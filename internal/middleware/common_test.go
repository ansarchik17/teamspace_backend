package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/config"
)

func TestRecordAppErrorStoresContextFields(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	err := apperrors.BadRequest("QR code has expired")
	RecordAppError(c, err)

	errorCode, exists := c.Get("error_code")
	require.True(t, exists)
	assert.Equal(t, string(err.Code), errorCode)

	errorMessage, exists := c.Get("error_message")
	require.True(t, exists)
	assert.Equal(t, err.Message, errorMessage)

	require.Len(t, c.Errors, 1)
	assert.Contains(t, c.Errors.String(), err.Message)
}

func TestRespondAppErrorIncludesUserMessage(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	RespondAppError(c, apperrors.InvalidCredentials())

	require.Equal(t, http.StatusUnauthorized, recorder.Code)

	var response apperrors.ErrorResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	assert.Equal(t, apperrors.ErrCodeInvalidCredentials, response.Error.Code)
	assert.Equal(t, "Неверный логин или пароль", response.Error.Message)
	assert.Contains(t, response.Error.UserMessage, "Проверьте данные")
}

func TestCORSPreflightUsesNumericMaxAgeAndRequestedHeaders(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(CORS(config.CORSConfig{
		AllowedOrigins: []string{"http://localhost:5173"},
		AllowedMethods: []string{"GET", "POST", "OPTIONS"},
		AllowedHeaders: []string{"Origin", "Content-Type"},
		MaxAge:         3600,
	}))
	router.POST("/api/v1/auth/login", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/auth/login", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "content-type,x-web-device-id,x-client-platform")

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusNoContent, recorder.Code)
	assert.Equal(t, "http://localhost:5173", recorder.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "true", recorder.Header().Get("Access-Control-Allow-Credentials"))
	assert.Equal(t, "content-type,x-web-device-id,x-client-platform", recorder.Header().Get("Access-Control-Allow-Headers"))
	assert.Equal(t, "3600", recorder.Header().Get("Access-Control-Max-Age"))
}

func TestCORSWildcardOriginEchoesRequestOriginWhenCredentialsAreEnabled(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(CORS(config.CORSConfig{
		AllowedOrigins: []string{"*"},
		AllowedMethods: []string{"GET", "POST", "OPTIONS"},
		AllowedHeaders: []string{"Origin", "Content-Type"},
		MaxAge:         3600,
	}))
	router.POST("/api/v1/auth/refresh", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", nil)
	req.Header.Set("Origin", "https://frontend.example.com")

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "https://frontend.example.com", recorder.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "true", recorder.Header().Get("Access-Control-Allow-Credentials"))
}
