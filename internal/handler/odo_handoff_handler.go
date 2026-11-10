package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/auth"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"go.uber.org/zap"
)

type odoHandoffCreateResponse struct {
	Code      string `json:"code"`
	ExpiresIn int64  `json:"expires_in"`
}

type odoHandoffRedeemRequest struct {
	Code string `json:"code" binding:"required"`
}

type odoHandoffRedeemResponse struct {
	WorkspaceToken string `json:"workspace_token"`
}

func (h *AuthHandler) SetODOHandoffStore(store auth.ODOHandoffStore) {
	h.odoHandoffStore = store
}

func (h *AuthHandler) SetODOHandoffAccessCheck(check func(context.Context, string) error) {
	h.odoHandoffAccessCheck = check
}

// CreateODOHandoff creates a 60-second, single-use code for opening ODO.
func (h *AuthHandler) CreateODOHandoff(c *gin.Context) {
	if h.odoHandoffStore == nil {
		respondJSON(c, http.StatusServiceUnavailable, gin.H{"error": "ODO handoff is not configured"})
		return
	}
	workspaceToken := middleware.ExtractToken(c)
	if strings.TrimSpace(workspaceToken) == "" {
		respondJSON(c, http.StatusUnauthorized, gin.H{"error": "authentication token is required"})
		return
	}
	code, err := h.odoHandoffStore.Create(c.Request.Context(), workspaceToken)
	if err != nil {
		h.logger.Error("Failed to create ODO handoff", zap.Error(err))
		respondJSON(c, http.StatusInternalServerError, gin.H{"error": "failed to create ODO handoff"})
		return
	}
	c.Header("Cache-Control", "no-store")
	respondJSON(c, http.StatusCreated, odoHandoffCreateResponse{Code: code, ExpiresIn: int64(auth.ODOHandoffTTL.Seconds())})
}

// RedeemODOHandoff is protected by the ODO-specific internal token at routing
// level. A successful redemption permanently consumes the code.
func (h *AuthHandler) RedeemODOHandoff(c *gin.Context) {
	if h.odoHandoffStore == nil {
		respondJSON(c, http.StatusServiceUnavailable, gin.H{"error": "ODO handoff is not configured"})
		return
	}
	var req odoHandoffRedeemRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "code is required"})
		return
	}
	workspaceToken, err := h.odoHandoffStore.Redeem(c.Request.Context(), req.Code)
	if errors.Is(err, auth.ErrODOHandoffInvalid) {
		respondJSON(c, http.StatusGone, gin.H{"error": "handoff code is invalid or expired"})
		return
	}
	if err != nil {
		h.logger.Error("Failed to redeem ODO handoff", zap.Error(err))
		respondJSON(c, http.StatusInternalServerError, gin.H{"error": "failed to redeem ODO handoff"})
		return
	}
	if h.odoHandoffAccessCheck != nil {
		if err := h.odoHandoffAccessCheck(c.Request.Context(), workspaceToken); err != nil {
			h.respondError(c, apperrors.ToAppError(err))
			return
		}
	}
	c.Header("Cache-Control", "no-store")
	respondJSON(c, http.StatusOK, odoHandoffRedeemResponse{WorkspaceToken: workspaceToken})
}
