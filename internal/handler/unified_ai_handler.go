package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

type UnifiedAIHandler struct {
	service *service.UnifiedAIService
	logger  *zap.Logger
}

func NewUnifiedAIHandler(service *service.UnifiedAIService, logger *zap.Logger) *UnifiedAIHandler {
	return &UnifiedAIHandler{
		service: service,
		logger:  logger.With(zap.String("handler", "unified_ai")),
	}
}

// CreateOrGetChat creates or returns user's singleton AI chat
// @Summary Create or get user's AI chat
// @Description Creates the user's singleton AI chat if doesn't exist, or returns existing one (WhatsApp style). Uses fixed default model.
// @Tags Unified AI
// @Produce json
// @Security BearerAuth
// @Success 200 {object} repository.UnifiedAIChat
// @Router /unified-ai/chat [post]
func (h *UnifiedAIHandler) CreateOrGetChat(c *gin.Context) {
	userID := c.GetInt64("user_id")

	chat, err := h.service.GetOrCreateChat(c.Request.Context(), userID)
	if err != nil {
		h.logger.Error("Failed to create or get chat", zap.Error(err))
		respondJSON(c, http.StatusInternalServerError, err)
		return
	}

	respondJSON(c, http.StatusOK, chat)
}

// GetChat retrieves chat details by ID
// @Summary Get AI chat details
// @Description Get details of a specific AI chat
// @Tags Unified AI
// @Produce json
// @Security BearerAuth
// @Param id path string true "Chat ID"
// @Success 200 {object} repository.UnifiedAIChat
// @Router /unified-ai/chats/{id} [get]
func (h *UnifiedAIHandler) GetChat(c *gin.Context) {
	chatID := c.Param("id")
	userID := c.GetInt64("user_id")

	chat, err := h.service.GetChat(c.Request.Context(), chatID, userID)
	if err != nil {
		h.logger.Error("Failed to get chat", zap.Error(err))
		respondJSON(c, http.StatusNotFound, gin.H{"error": "Chat not found or unauthorized"})
		return
	}

	respondJSON(c, http.StatusOK, chat)
}

type UpdateModelRequest struct {
	Model string `json:"model" binding:"required"`
}

// UpdateModel changes the AI model for the chat
// @Summary Update AI model
// @Description Change the AI model used in the chat
// @Tags Unified AI
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "Chat ID"
// @Param request body UpdateModelRequest true "Model update"
// @Success 200 {object} map[string]string
// @Router /unified-ai/chats/{id}/model [put]
func (h *UnifiedAIHandler) UpdateModel(c *gin.Context) {
	chatID := c.Param("id")
	userID := c.GetInt64("user_id")

	var req UpdateModelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondJSON(c, http.StatusBadRequest, err)
		return
	}

	if err := h.service.UpdateModel(c.Request.Context(), chatID, userID, req.Model); err != nil {
		h.logger.Error("Failed to update model", zap.Error(err))
		respondJSON(c, http.StatusInternalServerError, err)
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "Model updated successfully"), "model": req.Model})
}

type UpdateTitleRequest struct {
	Title string `json:"title" binding:"required"`
}

// UpdateTitle changes the chat title
// @Summary Update chat title
// @Description Change the title of the AI chat
// @Tags Unified AI
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "Chat ID"
// @Param request body UpdateTitleRequest true "Title update"
// @Success 200 {object} map[string]string
// @Router /unified-ai/chats/{id}/title [put]
func (h *UnifiedAIHandler) UpdateTitle(c *gin.Context) {
	chatID := c.Param("id")
	userID := c.GetInt64("user_id")

	var req UpdateTitleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondJSON(c, http.StatusBadRequest, err)
		return
	}

	if err := h.service.UpdateTitle(c.Request.Context(), chatID, userID, req.Title); err != nil {
		h.logger.Error("Failed to update title", zap.Error(err))
		respondJSON(c, http.StatusInternalServerError, err)
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "Title updated successfully"), "title": req.Title})
}

// GetMessages retrieves all messages for a chat
// @Summary Get chat messages
// @Description Get all messages from the AI chat
// @Tags Unified AI
// @Produce json
// @Security BearerAuth
// @Param id path string true "Chat ID"
// @Success 200 {array} repository.UnifiedAIMessage
// @Router /unified-ai/chats/{id}/messages [get]
func (h *UnifiedAIHandler) GetMessages(c *gin.Context) {
	chatID := c.Param("id")
	userID := c.GetInt64("user_id")

	messages, err := h.service.GetMessages(c.Request.Context(), chatID, userID)
	if err != nil {
		h.logger.Error("Failed to get messages", zap.Error(err))
		respondJSON(c, http.StatusInternalServerError, err)
		return
	}

	respondJSON(c, http.StatusOK, messages)
}

// SendMessage sends a message to AI
// @Summary Send message to AI
// @Description Send a text message (and optionally file URL) to AI and get response. Uses fixed default model.
// @Tags Unified AI
// @Accept multipart/form-data
// @Produce json
// @Security BearerAuth
// @Param id path string true "Chat ID"
// @Param content formData string true "Message text"
// @Param file_url formData string false "Image URL"
// @Success 200 {object} alem.ChatCompletionResponse
// @Router /unified-ai/chats/{id}/messages [post]
func (h *UnifiedAIHandler) SendMessage(c *gin.Context) {
	chatID := c.Param("id")
	userID := c.GetInt64("user_id")
	content := c.PostForm("content")

	// Handle File URL
	var fileURL *string
	if directURL := c.PostForm("file_url"); directURL != "" {
		fileURL = &directURL
	}

	if content == "" && fileURL == nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Content or file required"})
		return
	}

	resp, err := h.service.SendMessage(c.Request.Context(), chatID, userID, content, fileURL)
	if err != nil {
		h.logger.Error("Failed to send message", zap.Error(err))
		writeAlemChatError(c, err)
		return
	}

	respondJSON(c, http.StatusOK, resp)
}

// ClearHistory deletes all messages from the chat
// @Summary Clear chat history
// @Description Delete all messages from the chat (keeps the chat itself)
// @Tags Unified AI
// @Produce json
// @Security BearerAuth
// @Param id path string true "Chat ID"
// @Success 200 {object} map[string]string
// @Router /unified-ai/chats/{id}/messages [delete]
func (h *UnifiedAIHandler) ClearHistory(c *gin.Context) {
	chatID := c.Param("id")
	userID := c.GetInt64("user_id")

	if err := h.service.ClearHistory(c.Request.Context(), chatID, userID); err != nil {
		h.logger.Error("Failed to clear history", zap.Error(err))
		respondJSON(c, http.StatusInternalServerError, err)
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "Chat history cleared successfully")})
}

// DeleteChat deletes the entire chat
// @Summary Delete AI chat
// @Description Delete the entire AI chat and all messages (allows user to start fresh)
// @Tags Unified AI
// @Produce json
// @Security BearerAuth
// @Param id path string true "Chat ID"
// @Success 200 {object} map[string]string
// @Router /unified-ai/chats/{id} [delete]
func (h *UnifiedAIHandler) DeleteChat(c *gin.Context) {
	chatID := c.Param("id")
	userID := c.GetInt64("user_id")

	if err := h.service.DeleteChat(c.Request.Context(), chatID, userID); err != nil {
		h.logger.Error("Failed to delete chat", zap.Error(err))
		respondJSON(c, http.StatusInternalServerError, err)
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "Chat deleted successfully")})
}

// DeleteMessage deletes a specific message
// @Summary Delete single AI message
// @Description Delete a specific message from the chat (user or AI)
// @Tags Unified AI
// @Produce json
// @Security BearerAuth
// @Param id path string true "Chat ID"
// @Param message_id path string true "Message ID"
// @Success 200 {object} map[string]string
// @Router /unified-ai/chats/{id}/messages/{message_id} [delete]
func (h *UnifiedAIHandler) DeleteMessage(c *gin.Context) {
	chatID := c.Param("id")
	messageID := c.Param("message_id")
	userID := c.GetInt64("user_id")

	if err := h.service.DeleteMessage(c.Request.Context(), chatID, messageID, userID); err != nil {
		h.logger.Error("Failed to delete message", zap.Error(err))
		if err.Error() == "unauthorized" {
			respondJSON(c, http.StatusForbidden, gin.H{"error": "Unauthorized"})
			return
		}
		if err.Error() == "message not found" {
			respondJSON(c, http.StatusNotFound, gin.H{"error": "Message not found"})
			return
		}
		respondJSON(c, http.StatusInternalServerError, err)
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "Message deleted successfully")})
}

// ListModels proxy to get available AI models
// @Summary List available AI models
// @Description Get list of available AI models from Alem.Cloud
// @Tags Unified AI
// @Produce json
// @Security BearerAuth
// @Success 200 {object} alem.ModelListResponse
// @Router /unified-ai/models [get]
func (h *UnifiedAIHandler) ListModels(c *gin.Context) {
	resp, err := h.service.ListModels(c.Request.Context())
	if err != nil {
		h.logger.Error("Failed to list models", zap.Error(err))
		respondJSON(c, http.StatusInternalServerError, err)
		return
	}

	respondJSON(c, http.StatusOK, resp)
}

// CreateEmbeddings proxy to generate embeddings
// @Summary Generate embeddings
// @Description Proxy to Alem.Cloud embeddings API
// @Tags Unified AI
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body alem.EmbeddingRequest true "Request"
// @Success 200 {object} alem.EmbeddingResponse
// @Router /unified-ai/embeddings [post]
func (h *UnifiedAIHandler) CreateEmbeddings(c *gin.Context) {
	respondJSON(c, http.StatusForbidden, gin.H{
		"code":  string(apperrors.ErrCodeAIChatAccess),
		"error": "Платный вызов embeddings временно недоступен: контракт учёта расхода не подтверждён",
	})
}

// Rerank proxy to rerank documents
// @Summary Rerank documents
// @Description Proxy to Alem.Cloud rerank API
// @Tags Unified AI
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body alem.RerankRequest true "Request"
// @Success 200 {object} alem.RerankResponse
// @Router /unified-ai/rerank [post]
func (h *UnifiedAIHandler) Rerank(c *gin.Context) {
	respondJSON(c, http.StatusForbidden, gin.H{
		"code":  string(apperrors.ErrCodeAIChatAccess),
		"error": "Платный вызов rerank временно недоступен: контракт учёта расхода не подтверждён",
	})
}
