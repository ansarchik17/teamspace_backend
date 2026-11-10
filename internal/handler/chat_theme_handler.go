package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

type ChatThemeHandler struct {
	themeSvc *service.ChatThemeService
	logger   *zap.Logger
}

func NewChatThemeHandler(themeSvc *service.ChatThemeService, logger *zap.Logger) *ChatThemeHandler {
	return &ChatThemeHandler{
		themeSvc: themeSvc,
		logger:   logger.With(zap.String("handler", "chat_theme")),
	}
}

// @Summary Set global chat theme
// @Description Creates or updates the global chat theme for the current user
// @Tags chat_themes
// @Accept json
// @Produce json
// @Param request body domain.UpdateChatThemeRequest true "Chat Theme Configuration"
// @Success 200 {object} domain.ChatTheme
// @Failure 400 {object} apperrors.AppError
// @Failure 401 {object} apperrors.AppError
// @Security BearerAuth
// @Router /chat-themes [post]
func (h *ChatThemeHandler) SetTheme(c *gin.Context) {
	var req domain.UpdateChatThemeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(apperrors.BadRequest("invalid request body"))
		return
	}

	userID, exists := c.Get("user_id")
	if !exists {
		_ = c.Error(apperrors.Unauthorized("user not authenticated"))
		return
	}

	theme, err := h.themeSvc.SetTheme(c.Request.Context(), userID.(int64), req)
	if err != nil {
		_ = c.Error(apperrors.InternalWithErr("failed to set chat theme", err))
		return
	}

	respondJSON(c, http.StatusOK, theme)
}

// @Summary Get global chat theme
// @Description Gets the global chat theme for the current user
// @Tags chat_themes
// @Produce json
// @Success 200 {object} domain.ChatTheme
// @Failure 401 {object} apperrors.AppError
// @Failure 404 {object} apperrors.AppError
// @Security BearerAuth
// @Router /chat-themes [get]
func (h *ChatThemeHandler) GetTheme(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		_ = c.Error(apperrors.Unauthorized("user not authenticated"))
		return
	}

	theme, err := h.themeSvc.GetTheme(c.Request.Context(), userID.(int64))
	if err != nil {
		_ = c.Error(apperrors.InternalWithErr("failed to get chat theme", err))
		return
	}

	if theme == nil {
		_ = c.Error(apperrors.NotFound("chat theme not found"))
		return
	}

	respondJSON(c, http.StatusOK, theme)
}

// @Summary Delete global chat theme
// @Description Reverts the global chat configuration to default
// @Tags chat_themes
// @Produce json
// @Success 200 {object} map[string]string
// @Failure 400 {object} apperrors.AppError
// @Failure 401 {object} apperrors.AppError
// @Failure 404 {object} apperrors.AppError
// @Security BearerAuth
// @Router /chat-themes [delete]
func (h *ChatThemeHandler) DeleteTheme(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		_ = c.Error(apperrors.Unauthorized("user not authenticated"))
		return
	}

	err := h.themeSvc.DeleteTheme(c.Request.Context(), userID.(int64))
	if err != nil {
		if err.Error() == "theme not found" {
			_ = c.Error(apperrors.NotFound(err.Error()))
			return
		}
		_ = c.Error(apperrors.InternalWithErr("failed to delete chat theme", err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "chat theme reverted to default")})
}
