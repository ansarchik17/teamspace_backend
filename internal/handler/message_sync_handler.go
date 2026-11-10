package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

// MessageSyncHandler handles message synchronization HTTP requests
type MessageSyncHandler struct {
	syncService *service.MessageSyncService
	logger      *zap.Logger
}

func NewMessageSyncHandler(syncService *service.MessageSyncService, logger *zap.Logger) *MessageSyncHandler {
	return &MessageSyncHandler{
		syncService: syncService,
		logger:      logger.With(zap.String("handler", "message_sync")),
	}
}

// SyncMessagesRequest represents a sync request
type SyncMessagesRequest struct {
	DeviceID       string     `json:"device_id" binding:"required"`
	ConversationID *uuid.UUID `json:"conversation_id,omitempty"`
	AfterSequence  *int64     `json:"after_sequence,omitempty"`
	Limit          int        `json:"limit,omitempty"`
}

// SyncMessages godoc
// @Summary Synchronize missed messages
// @Description Retrieves messages that were missed during disconnection for the specified device
// @Tags messages,sync
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body SyncMessagesRequest true "Sync request"
// @Success 200 {object} service.SyncResponse
// @Failure 400 {object} ErrorResponse
// @Failure 401 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/messages/sync [post]
func (h *MessageSyncHandler) SyncMessages(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	var req SyncMessagesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("Invalid sync request", zap.Error(err))
		respondJSON(c, http.StatusBadRequest, ErrorResponse{Error: "invalid request: " + err.Error()})
		return
	}
	if !service.IsValidSyncDeviceID(req.DeviceID) {
		respondJSON(c, http.StatusBadRequest, ErrorResponse{Error: "invalid device_id"})
		return
	}

	// Default limit
	if req.Limit < 0 || req.Limit > 500 {
		respondJSON(c, http.StatusBadRequest, ErrorResponse{Error: "limit must be between 1 and 500"})
		return
	}
	if req.Limit == 0 {
		req.Limit = 100
	}
	if req.AfterSequence != nil && *req.AfterSequence < 0 {
		respondJSON(c, http.StatusBadRequest, ErrorResponse{Error: "after_sequence must be non-negative"})
		return
	}

	syncReq := service.SyncRequest{
		UserID:         userID,
		DeviceID:       req.DeviceID,
		ConversationID: req.ConversationID,
		AfterSequence:  req.AfterSequence,
		Limit:          req.Limit,
	}

	response, err := h.syncService.SyncMessages(c.Request.Context(), syncReq)
	if err != nil {
		h.logger.Error("Failed to sync messages",
			zap.Error(err),
			zap.Int64("user_id", userID),
			zap.String("device_id", req.DeviceID),
		)
		respondJSON(c, http.StatusInternalServerError, ErrorResponse{Error: "failed to sync messages"})
		return
	}

	respondJSON(c, http.StatusOK, response)
}

// UpdateSyncPositionRequest represents a request to update sync position
type UpdateSyncPositionRequest struct {
	DeviceID       string     `json:"device_id" binding:"required"`
	ConversationID *uuid.UUID `json:"conversation_id,omitempty"`
	Sequence       int64      `json:"sequence" binding:"gte=0"`
}

// UpdateSyncPosition godoc
// @Summary Update sync position
// @Description Manually updates the last synchronized message position for a device
// @Tags messages,sync
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body UpdateSyncPositionRequest true "Update request"
// @Success 200 {object} map[string]string
// @Failure 400 {object} ErrorResponse
// @Failure 401 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/messages/sync/position [put]
func (h *MessageSyncHandler) UpdateSyncPosition(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	var req UpdateSyncPositionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("Invalid update sync position request", zap.Error(err))
		respondJSON(c, http.StatusBadRequest, ErrorResponse{Error: "invalid request: " + err.Error()})
		return
	}
	if !service.IsValidSyncDeviceID(req.DeviceID) {
		respondJSON(c, http.StatusBadRequest, ErrorResponse{Error: "invalid device_id"})
		return
	}

	err := h.syncService.UpdateSyncPosition(c.Request.Context(), userID, req.DeviceID, req.ConversationID, req.Sequence)
	if err != nil {
		h.logger.Error("Failed to update sync position",
			zap.Error(err),
			zap.Int64("user_id", userID),
			zap.String("device_id", req.DeviceID),
		)
		respondJSON(c, http.StatusInternalServerError, ErrorResponse{Error: "failed to update sync position"})
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "sync position updated")})
}

// GetSyncStatus godoc
// @Summary Get sync status
// @Description Returns the current synchronization status for a device
// @Tags messages,sync
// @Produce json
// @Security BearerAuth
// @Param device_id query string true "Device ID"
// @Param conversation_id query string false "Conversation ID (optional)"
// @Success 200 {object} repository.SyncCursor
// @Failure 400 {object} ErrorResponse
// @Failure 401 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/messages/sync/status [get]
func (h *MessageSyncHandler) GetSyncStatus(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	deviceID := c.Query("device_id")
	if deviceID == "" {
		respondJSON(c, http.StatusBadRequest, ErrorResponse{Error: "device_id is required"})
		return
	}
	if !service.IsValidSyncDeviceID(deviceID) {
		respondJSON(c, http.StatusBadRequest, ErrorResponse{Error: "invalid device_id"})
		return
	}

	var conversationID *uuid.UUID
	if convIDStr := c.Query("conversation_id"); convIDStr != "" {
		convID, err := uuid.Parse(convIDStr)
		if err != nil {
			respondJSON(c, http.StatusBadRequest, ErrorResponse{Error: "invalid conversation_id"})
			return
		}
		conversationID = &convID
	}

	status, err := h.syncService.GetSyncStatus(c.Request.Context(), userID, deviceID, conversationID)
	if err != nil {
		h.logger.Error("Failed to get sync status",
			zap.Error(err),
			zap.Int64("user_id", userID),
			zap.String("device_id", deviceID),
		)
		respondJSON(c, http.StatusInternalServerError, ErrorResponse{Error: "failed to get sync status"})
		return
	}

	respondJSON(c, http.StatusOK, status)
}

// ErrorResponse represents an error response
type ErrorResponse struct {
	Error string `json:"error"`
}
