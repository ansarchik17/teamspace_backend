package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

type aiUserLimitsService interface {
	GetUserAILimits(ctx context.Context, userID int64) (*domain.UserAILimits, error)
}

// writeAlemChatError delegates to unified WriteAICallError for backward compatibility.
func writeAlemChatError(c *gin.Context, err error) {
	WriteAICallError(c, err)
}

type AlemHandler struct {
	alemService       *service.AlemService
	workspaceProvider *service.WorkspaceAIProvider
	documents         *service.AIDocumentService
	limitsService     aiUserLimitsService
	logger            *zap.Logger
}

func (h *AlemHandler) SetWorkspaceAIProvider(provider *service.WorkspaceAIProvider) {
	h.workspaceProvider = provider
}

func (h *AlemHandler) WorkspaceAICompletion(c *gin.Context) {
	if h.workspaceProvider == nil {
		WriteAICallError(c, domain.ErrAIAccountingDisabled)
		return
	}
	WorkspaceAICompletion(h.workspaceProvider)(c)
}

func NewAlemHandler(alemService *service.AlemService, logger *zap.Logger) *AlemHandler {
	return &AlemHandler{
		alemService: alemService,
		logger:      logger,
	}
}

func (h *AlemHandler) SetDocumentService(documents *service.AIDocumentService) {
	h.documents = documents
}

func (h *AlemHandler) SetLimitsService(limits aiUserLimitsService) {
	h.limitsService = limits
}

// GetAILimits
// @Summary Get user's AI limits and access status
// @Description Returns 5-hour and weekly AI budget limits, consumption, and access blockers for authenticated user
// @Tags AI
// @Produce json
// @Security BearerAuth
// @Router /ai/limits [get]
func (h *AlemHandler) GetAILimits(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	userID, ok := middleware.GetUserID(c)
	if !ok || userID <= 0 {
		respondAppError(c, apperrors.Unauthorized("authentication required"))
		return
	}
	if h.limitsService == nil {
		respondJSON(c, http.StatusServiceUnavailable, gin.H{
			"code":  string(apperrors.ErrCodeAIAccountingUnavailable),
			"error": "Учёт AI-лимитов временно недоступен",
		})
		return
	}
	limits, err := h.limitsService.GetUserAILimits(c.Request.Context(), userID)
	if err != nil {
		if errors.Is(err, domain.ErrAIAccountingDisabled) {
			respondJSON(c, http.StatusServiceUnavailable, gin.H{
				"code":  string(apperrors.ErrCodeAIAccountingUnavailable),
				"error": "Учёт AI-лимитов временно отключен",
			})
			return
		}
		var appErr *apperrors.AppError
		if errors.As(err, &appErr) {
			respondAppError(c, appErr)
			return
		}
		h.logger.Error("failed to get user ai limits", zap.Int64("user_id", userID), zap.Error(err))
		respondAppError(c, apperrors.Internal("failed to get ai limits"))
		return
	}
	respondJSON(c, http.StatusOK, aiUserLimitsResponse(limits))
}

// ListModels
// @Summary List available AI models
// @Description Get models from Alem.Cloud
// @Tags AI
// @Produce json
// @Security BearerAuth
// @Success 200 {object} alem.ModelListResponse
// @Router /ai/models [get]
func (h *AlemHandler) ListModels(c *gin.Context) {
	resp, err := h.alemService.ListModels(c.Request.Context())
	if err != nil {
		respondJSON(c, http.StatusInternalServerError, err)
		return
	}
	respondJSON(c, http.StatusOK, resp)
}

// GetUserChats
// @Summary Get user's AI conversations
// @Tags AI
// @Produce json
// @Security BearerAuth
// @Router /ai/chats [get]
func (h *AlemHandler) GetUserChats(c *gin.Context) {
	userID := c.GetInt64("user_id") // Assumes AuthMiddleware sets this
	chats, err := h.alemService.GetOrganizedChats(c.Request.Context(), userID, c.Query("include_archived") == "true")
	if err != nil {
		respondJSON(c, http.StatusInternalServerError, err)
		return
	}
	respondJSON(c, http.StatusOK, chats)
}

type CreateChatRequest struct {
	SectionID *string `json:"section_id"`
	Model     string  `json:"model"`
	Title     string  `json:"title"`
}

// CreateChat
// @Summary Start a new AI chat
// @Tags AI
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body CreateChatRequest true "Init"
// @Router /ai/chats [post]
func (h *AlemHandler) CreateChat(c *gin.Context) {
	// Middleware sets "user_id" (int64), not "userID"
	userID := c.GetInt64("user_id")
	var req CreateChatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondJSON(c, http.StatusBadRequest, err)
		return
	}

	id, err := h.alemService.CreateChatInSection(c.Request.Context(), userID, req.Model, req.Title, req.SectionID)
	if err != nil {
		respondAppError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusCreated, gin.H{"id": id})
}

// GetChatHistory
// @Summary Get messages of a chat
// @Tags AI
// @Produce json
// @Security BearerAuth
// @Param id path string true "Chat ID"
// @Router /ai/chats/{id}/messages [get]
func (h *AlemHandler) GetChatHistory(c *gin.Context) {
	chatID := c.Param("id")
	msgs, err := h.alemService.GetChatHistory(c.Request.Context(), chatID, c.GetInt64("user_id"))
	if err != nil {
		if strings.Contains(err.Error(), "unauthorized") {
			respondJSON(c, http.StatusForbidden, gin.H{"error": "У вас нет доступа к этому чату"})
			return
		}
		respondJSON(c, http.StatusInternalServerError, err)
		return
	}
	respondJSON(c, http.StatusOK, msgs)
}

// DeleteChat
// @Summary Delete an AI chat
// @Tags AI
// @Produce json
// @Security BearerAuth
// @Param id path string true "Chat ID"
// @Router /ai/chats/{id} [delete]
func (h *AlemHandler) DeleteChat(c *gin.Context) {
	chatID := c.Param("id")
	userID := c.GetInt64("user_id")

	err := h.alemService.DeleteChat(c.Request.Context(), chatID, userID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			respondJSON(c, http.StatusNotFound, gin.H{"error": "Чат не найден"})
			return
		}
		if strings.Contains(err.Error(), "unauthorized") || strings.Contains(err.Error(), "permission denied") {
			respondJSON(c, http.StatusForbidden, gin.H{"error": "У вас нет прав для удаления этого чата"})
			return
		}

		h.logger.Error("Failed to delete chat", zap.Error(err))
		respondJSON(c, http.StatusInternalServerError, gin.H{"error": "Невозможно удалить чат. Попробуйте позже."})
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "Чат успешно удален")})
}

// SendMessage
// @Summary Send message to AI (Text + Optional File)
// @Tags AI
// @Accept multipart/form-data
// @Produce json
// @Security BearerAuth
// @Param id path string true "Chat ID"
// @Param content formData string true "Message text"
// @Param model formData string false "Switch Model (optional)"
// @Param file_url formData string false "Image URL"
// @Router /ai/chats/{id}/messages [post]
func (h *AlemHandler) SendMessage(c *gin.Context) {
	ctx := service.WithAIDataIdentity(c.Request.Context(), service.AIDataIdentity{
		UserID: c.GetInt64("user_id"), ChatID: c.Param("id"), Authorization: c.GetHeader("Authorization"), Cookie: c.GetHeader("Cookie"), Host: c.Request.Host,
	})
	if strings.HasPrefix(strings.ToLower(c.GetHeader("Content-Type")), "multipart/") {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, service.AIDocumentMaxBytes+1024*1024)
	}
	chatID := c.Param("id")
	userID := c.GetInt64("user_id")
	content := c.PostForm("content")
	model := c.PostForm("model") // Allow switching model on the fly
	if token := c.PostForm("confirmation_token"); token != "" {
		decision := c.PostForm("action_decision")
		if (decision != "confirm" && decision != "cancel") || len(token) > 100 {
			respondJSON(c, http.StatusBadRequest, gin.H{"error": "Некорректное подтверждение действия."})
			return
		}
		ctx = service.WithAIActionDecision(ctx, token, decision == "confirm")
	}

	// Prefer a real multipart upload. The service stores the original file and
	// extracts PDF/DOCX/TXT/XLSX/PPTX text before the model is called.
	if uploaded, uploadErr := c.FormFile("file"); uploadErr == nil && uploaded != nil {
		if uploaded.Size > maxConvertToTextFileSize {
			respondJSON(c, http.StatusRequestEntityTooLarge, gin.H{"error": "Файл слишком большой. Максимальный размер — 50 МБ."})
			return
		}
		resp, err := h.alemService.SendMessageWithAttachment(
			ctx, chatID, userID, content, uploaded, model,
		)
		if err != nil {
			h.logger.Error("Alem Service attachment error", zap.Error(err))
			writeAlemChatError(c, err)
			return
		}
		respondJSON(c, http.StatusOK, resp)
		return
	}

	// Handle File URL
	var fileURL *string

	// Check if direct URL is provided
	if directURL := c.PostForm("file_url"); directURL != "" {
		fileURL = &directURL
	}

	if content == "" && fileURL == nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Напишите вопрос или прикрепите файл."})
		return
	}

	resp, err := h.alemService.SendMessage(ctx, chatID, userID, content, fileURL, model)
	if err != nil {
		h.logger.Error("Alem Service Error", zap.Error(err))
		writeAlemChatError(c, err)
		return
	}

	respondJSON(c, http.StatusOK, resp)
}

// UploadDocument accepts one document, stores it durably and queues parsing.
// The HTTP request never waits for extraction or indexing.
func (h *AlemHandler) UploadDocument(c *gin.Context) {
	if h.documents == nil {
		respondJSON(c, http.StatusServiceUnavailable, gin.H{"error": "document processing is unavailable"})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, service.AIDocumentMaxBytes+1024*1024)
	file, err := c.FormFile("file")
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(strings.ToLower(err.Error()), "request body too large") {
			status = http.StatusRequestEntityTooLarge
		}
		respondJSON(c, status, gin.H{"error": "file is required or request is too large"})
		return
	}
	doc, err := h.documents.Create(c.Request.Context(), c.GetInt64("user_id"), c.Param("id"), file)
	if err != nil {
		message := err.Error()
		status := http.StatusUnprocessableEntity
		if strings.Contains(message, "unauthorized") {
			status = http.StatusForbidden
		}
		if strings.Contains(message, "size") {
			status = http.StatusRequestEntityTooLarge
		}
		if strings.Contains(message, "unsupported") {
			status = http.StatusUnsupportedMediaType
		}
		if strings.Contains(message, "quota") {
			status = http.StatusTooManyRequests
		}
		respondJSON(c, status, gin.H{"error": message})
		return
	}
	respondJSON(c, http.StatusAccepted, doc)
}

func (h *AlemHandler) ListDocuments(c *gin.Context) {
	if h.documents == nil {
		respondJSON(c, http.StatusServiceUnavailable, gin.H{"error": "document processing is unavailable"})
		return
	}
	items, err := h.documents.List(c.Request.Context(), c.GetInt64("user_id"), c.Param("id"))
	if err != nil {
		h.documentError(c, err)
		return
	}
	respondJSON(c, http.StatusOK, items)
}

func (h *AlemHandler) GetDocument(c *gin.Context) {
	if h.documents == nil {
		respondJSON(c, http.StatusServiceUnavailable, gin.H{"error": "document processing is unavailable"})
		return
	}
	doc, err := h.documents.Get(c.Request.Context(), c.GetInt64("user_id"), c.Param("id"), c.Param("documentId"))
	if err != nil {
		h.documentError(c, err)
		return
	}
	if doc == nil {
		respondJSON(c, http.StatusNotFound, gin.H{"error": "document not found"})
		return
	}
	respondJSON(c, http.StatusOK, doc)
}

func (h *AlemHandler) DeleteDocument(c *gin.Context) {
	if h.documents == nil {
		respondJSON(c, http.StatusServiceUnavailable, gin.H{"error": "document processing is unavailable"})
		return
	}
	err := h.documents.Delete(c.Request.Context(), c.GetInt64("user_id"), c.Param("id"), c.Param("documentId"))
	if err != nil {
		h.documentError(c, err)
		return
	}
	respondJSON(c, http.StatusOK, gin.H{"deleted": true})
}

func (h *AlemHandler) DownloadDocument(c *gin.Context) {
	if h.documents == nil {
		respondJSON(c, http.StatusServiceUnavailable, gin.H{"error": "document processing is unavailable"})
		return
	}
	url, err := h.documents.DownloadURL(c.Request.Context(), c.GetInt64("user_id"), c.Param("id"), c.Param("documentId"))
	if err != nil {
		h.documentError(c, err)
		return
	}
	respondJSON(c, http.StatusOK, gin.H{"url": url})
}

func (h *AlemHandler) RetryDocument(c *gin.Context) {
	if h.documents == nil {
		respondJSON(c, http.StatusServiceUnavailable, gin.H{"error": "document processing is unavailable"})
		return
	}
	if err := h.documents.Retry(c.Request.Context(), c.GetInt64("user_id"), c.Param("id"), c.Param("documentId")); err != nil {
		h.documentError(c, err)
		return
	}
	respondJSON(c, http.StatusAccepted, gin.H{"status": "uploaded"})
}

func (h *AlemHandler) documentError(c *gin.Context, err error) {
	if strings.Contains(err.Error(), "unauthorized") {
		respondJSON(c, http.StatusForbidden, gin.H{"error": "У вас нет доступа к этому чату"})
		return
	}
	if strings.Contains(err.Error(), "not found") {
		respondJSON(c, http.StatusNotFound, gin.H{"error": "Документ не найден"})
		return
	}
	h.logger.Error("AI document error", zap.Error(err))
	respondJSON(c, http.StatusInternalServerError, gin.H{"error": "Не удалось обработать документ"})
}

// CreateEmbeddings
// @Summary Generate embeddings
// @Description Proxy to Alem.Cloud embeddings
// @Tags AI
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body alem.EmbeddingRequest true "Request"
// @Router /ai/embeddings [post]
func (h *AlemHandler) CreateEmbeddings(c *gin.Context) {
	respondJSON(c, http.StatusForbidden, gin.H{
		"code":  string(apperrors.ErrCodeAIChatAccess),
		"error": "Платный вызов embeddings временно недоступен: контракт учёта расхода не подтверждён",
	})
}

// Rerank
// @Summary Rerank documents
// @Description Proxy to Alem.Cloud rerank
// @Tags AI
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body alem.RerankRequest true "Request"
// @Router /ai/rerank [post]
func (h *AlemHandler) Rerank(c *gin.Context) {
	respondJSON(c, http.StatusForbidden, gin.H{
		"code":  string(apperrors.ErrCodeAIChatAccess),
		"error": "Платный вызов rerank временно недоступен: контракт учёта расхода не подтверждён",
	})
}
