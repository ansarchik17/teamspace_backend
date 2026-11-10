package handler

import (
	"encoding/json"
	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/i18n"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"io"
)

func respondAppError(c *gin.Context, err *apperrors.AppError) {
	middleware.RespondAppError(c, err)
}

func respondJSON(c *gin.Context, status int, body any) {
	middleware.RespondJSON(c, status, publicPayload(c, body))
}

func respondAbortJSON(c *gin.Context, status int, body any) {
	middleware.AbortJSON(c, status, body)
}

func apiMessage(c *gin.Context, original string) string {
	return i18n.Text(middleware.RequestLocale(c), i18n.APIMessageKey(original), nil)
}

// Third-party error bodies are bounded and handled as public errors. Successful
// proxy responses continue streaming unchanged, including file downloads.
func respondUpstreamError(c *gin.Context, status int, body io.Reader) {
	payload := make(map[string]any)
	if err := json.NewDecoder(io.LimitReader(body, 64<<10)).Decode(&payload); err != nil {
		payload = nil
	}
	respondJSON(c, status, payload)
}
