package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/auth"
	"github.com/west6ide/workspace-backend/internal/i18n"
	"github.com/west6ide/workspace-backend/internal/middleware"
)

type RecoveryEmailCodeRequest struct {
	Email    string `json:"email" binding:"max=254"`
	Purpose  string `json:"purpose" binding:"required,oneof=bind remove"`
	Password string `json:"password" binding:"required,max=1024"`
}
type RecoveryEmailConfirmRequest struct {
	ID       string `json:"id" binding:"required,uuid"`
	Code     string `json:"code" binding:"required,len=6"`
	Password string `json:"password" binding:"required,max=1024"`
}
type PasswordRecoveryRequest struct {
	Login string `json:"login" binding:"required,max=254"`
}
type PasswordRecoveryProof struct {
	Token string `json:"token" binding:"required,len=43"`
}
type PasswordRecoveryCompleteRequest struct {
	Token    string `json:"token" binding:"required,len=43"`
	Password string `json:"new_password" binding:"required,min=8,max=128"`
}

func (h *AuthHandler) SetRecoveryService(s *auth.RecoveryService) { h.recovery = s }
func (h *AuthHandler) recoveryReady(c *gin.Context) bool {
	c.Header("Cache-Control", "no-store")
	c.Header("Referrer-Policy", "no-referrer")
	if h.recovery == nil {
		h.respondError(c, apperrors.RecoveryUnavailable())
		return false
	}
	return true
}
func recoveryLocale(c *gin.Context) string {
	return string(i18n.ResolveHTTP(c.GetHeader("Accept-Language"), ""))
}

// RecoveryEmailState returns only the authenticated user's recovery settings.
// @Summary Get recovery email settings
// @Tags account-recovery
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 429 {object} map[string]interface{}
// @Failure 503 {object} map[string]interface{}
// @Router /auth/recovery-email [get]
func (h *AuthHandler) RecoveryEmailState(c *gin.Context) {
	if !h.recoveryReady(c) {
		return
	}
	out, err := h.recovery.State(c.Request.Context(), middleware.MustGetUserID(c))
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, out)
}

// @Summary Send recovery email confirmation code
// @Tags account-recovery
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body RecoveryEmailCodeRequest true "Request"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 429 {object} map[string]interface{}
// @Failure 503 {object} map[string]interface{}
// @Router /auth/recovery-email/code [post]
func (h *AuthHandler) BeginRecoveryEmail(c *gin.Context) {
	if !h.recoveryReady(c) {
		return
	}
	var req RecoveryEmailCodeRequest
	if !h.invitationBody(c, &req) {
		return
	}
	out, err := h.recovery.BeginEmail(c.Request.Context(), middleware.MustGetUserID(c), req.Email, req.Purpose, req.Password, recoveryLocale(c))
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, out)
}

// @Summary Confirm recovery email change or removal
// @Tags account-recovery
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body RecoveryEmailConfirmRequest true "Request"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 429 {object} map[string]interface{}
// @Failure 503 {object} map[string]interface{}
// @Router /auth/recovery-email/confirm [post]
func (h *AuthHandler) ConfirmRecoveryEmail(c *gin.Context) {
	if !h.recoveryReady(c) {
		return
	}
	var req RecoveryEmailConfirmRequest
	if !h.invitationBody(c, &req) {
		return
	}
	id, _ := uuid.Parse(req.ID)
	if err := h.recovery.ConfirmEmail(c.Request.Context(), middleware.MustGetUserID(c), id, req.Code, req.Password, recoveryLocale(c)); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, gin.H{"ok": true})
}

// @Summary Request password recovery without exposing account existence
// @Tags account-recovery
// @Accept json
// @Produce json
// @Param request body PasswordRecoveryRequest true "Request"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 429 {object} map[string]interface{}
// @Failure 503 {object} map[string]interface{}
// @Router /auth/recovery/request [post]
func (h *AuthHandler) RequestPasswordRecovery(c *gin.Context) {
	if !h.recoveryReady(c) {
		return
	}
	var req PasswordRecoveryRequest
	if !h.invitationBody(c, &req) {
		return
	}
	if err := h.recovery.RequestPassword(c.Request.Context(), req.Login, c.ClientIP(), recoveryLocale(c)); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, gin.H{"ok": true})
}

// @Summary Validate a recovery link without exposing account details
// @Tags account-recovery
// @Accept json
// @Produce json
// @Param request body PasswordRecoveryProof true "Request"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 429 {object} map[string]interface{}
// @Failure 503 {object} map[string]interface{}
// @Router /auth/recovery/preview [post]
func (h *AuthHandler) PreviewPasswordRecovery(c *gin.Context) {
	if !h.recoveryReady(c) {
		return
	}
	var req PasswordRecoveryProof
	if !h.invitationBody(c, &req) {
		return
	}
	if err := h.recovery.Preview(c.Request.Context(), req.Token, c.ClientIP()); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, gin.H{"ok": true})
}

// @Summary Reset password and revoke previous sessions
// @Tags account-recovery
// @Accept json
// @Produce json
// @Param request body PasswordRecoveryCompleteRequest true "Request"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 429 {object} map[string]interface{}
// @Failure 503 {object} map[string]interface{}
// @Router /auth/recovery/complete [post]
func (h *AuthHandler) CompletePasswordRecovery(c *gin.Context) {
	if !h.recoveryReady(c) {
		return
	}
	var req PasswordRecoveryCompleteRequest
	if !h.invitationBody(c, &req) {
		return
	}
	if err := h.recovery.Complete(c.Request.Context(), req.Token, req.Password, c.ClientIP(), recoveryLocale(c)); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, gin.H{"ok": true})
}
