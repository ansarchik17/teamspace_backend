package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/auth"
	"github.com/west6ide/workspace-backend/internal/middleware"
)

// UpdateLanguage godoc
// @Summary Update current user's interface language
// @Tags auth
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body auth.UpdateLanguageRequest true "Interface language: ru or kk"
// @Success 200 {object} domain.UserResponse
// @Failure 400 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{}
// @Router /auth/me/language [put]
func (h *AuthHandler) UpdateLanguage(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	var req auth.UpdateLanguageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.BadRequest("language must be ru or kk"))
		return
	}
	user, err := h.authService.UpdateLanguage(c.Request.Context(), userID, req.Language)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, user)
}
