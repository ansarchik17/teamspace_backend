package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

type NotebookHandler struct {
	service          *service.NotebookService
	twoSlidesService *service.TwoSlidesService
	logger           *zap.Logger
	research         *service.NotebookResearchService
	jobs             *service.NotebookResearchJobService
}

func NewNotebookHandler(service *service.NotebookService, twoSlidesService *service.TwoSlidesService, logger *zap.Logger, research ...*service.NotebookResearchService) *NotebookHandler {
	h := &NotebookHandler{
		service:          service,
		twoSlidesService: twoSlidesService,
		logger:           logger,
	}
	if len(research) > 0 {
		h.research = research[0]
	}
	return h
}

// Ask godoc
// @Summary Answer a question or run a Studio preset over the selected notebook sources
// @Description Documents that do not fit into one model context are analyzed chunk by chunk (map-reduce).
// @Description status: answered | partial | not_found | no_sources | unreadable. Only answered, partial and not_found are results.
// @Tags notebook
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "Chat ID"
// @Param request body service.NotebookResearchRequest true "Question or preset"
// @Success 200 {object} service.NotebookResearchResponse
// @Router /notebook/chats/{id}/ask [post]
func (h *NotebookHandler) Ask(c *gin.Context) {
	h.researchRequest(c, false)
}

// StudioPresets godoc
// @Summary Ready-made Studio prompts (cards) for notebook research
// @Tags notebook
// @Security BearerAuth
// @Produce json
// @Param lang query string false "ru (default) or kz"
// @Success 200 {array} service.NotebookStudioPreset
// @Router /notebook/studio/presets [get]
func (h *NotebookHandler) StudioPresets(c *gin.Context) {
	respondJSON(c, http.StatusOK, service.NotebookStudioPresets(c.Query("lang")))
}

func (h *NotebookHandler) ResearchContext(c *gin.Context) {
	h.researchRequest(c, true)
}

func (h *NotebookHandler) researchRequest(c *gin.Context, contextOnly bool) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 210*time.Second)
	defer cancel()
	c.Request = c.Request.WithContext(ctx)
	// Research can legitimately exceed the generic server's write timeout.
	// Keep a bounded deadline for this route instead of extending all requests.
	_ = http.NewResponseController(c.Writer).SetWriteDeadline(time.Now().Add(240 * time.Second))
	if h.research == nil {
		h.respondError(c, apperrors.Internal("research service unavailable"))
		return
	}
	chatID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid chat id"))
		return
	}
	var req service.NotebookResearchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid research request"))
		return
	}
	userID := middleware.MustGetUserID(c)
	if contextOnly {
		sources, err := h.research.Context(c.Request.Context(), userID, chatID, req)
		if err != nil {
			h.respondError(c, apperrors.ToAppError(err))
			return
		}
		respondJSON(c, http.StatusOK, gin.H{"sources": sources})
		return
	}
	notebookOperationID(c, &req)
	result, err := h.research.Ask(c.Request.Context(), userID, chatID, req)
	if err != nil {
		if IsAICallError(err) {
			WriteAICallError(c, err)
			return
		}
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, result)
}

// notebookOperationID takes the stable operation id from the body or, when it
// is absent, from the X-Operation-ID or Idempotency-Key header.
func notebookOperationID(c *gin.Context, req *service.NotebookResearchRequest) {
	if req.OperationID == "" {
		if opHeader := c.GetHeader("X-Operation-ID"); opHeader != "" {
			req.OperationID = opHeader
		} else if idempHeader := c.GetHeader("Idempotency-Key"); idempHeader != "" {
			req.OperationID = idempHeader
		}
	}
}

func (h *NotebookHandler) SourceExcerpt(c *gin.Context) {
	if h.research == nil {
		h.respondError(c, apperrors.Internal("research service unavailable"))
		return
	}
	chatID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid chat id"))
		return
	}
	fileID, err := uuid.Parse(c.Param("doc_id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid source id"))
		return
	}
	var query struct {
		Start    int    `form:"start_line" binding:"required,min=1"`
		End      int    `form:"end_line" binding:"required,min=1"`
		Revision string `form:"revision" binding:"omitempty,len=64,hexadecimal"`
	}
	if err := c.ShouldBindQuery(&query); err != nil {
		h.respondError(c, apperrors.Validation("invalid source range"))
		return
	}
	source, err := h.research.Source(c.Request.Context(), middleware.MustGetUserID(c), chatID, fileID, query.Start, query.End, query.Revision)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, source)
}

// CreateChat godoc
// @Summary Create a new notebook AI chat session
// @Tags notebook
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body domain.CreateNotebookChatRequest true "Create chat request"
// @Success 201 {object} domain.NotebookChat
// @Router /notebook/chats [post]
func (h *NotebookHandler) CreateChat(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	var req domain.CreateNotebookChatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	chat, err := h.service.CreateChat(c.Request.Context(), userID, &req)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusCreated, chat)
}

// GetChats godoc
// @Summary Get user's notebook AI chat sessions
// @Tags notebook
// @Security BearerAuth
// @Produce json
// @Success 200 {array} domain.NotebookChat
// @Router /notebook/chats [get]
func (h *NotebookHandler) GetChats(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	chats, err := h.service.GetUserChats(c.Request.Context(), userID)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, chats)
}

// GetChatHistory godoc
// @Summary Get message history for a notebook chat
// @Tags notebook
// @Security BearerAuth
// @Produce json
// @Param id path string true "Chat ID"
// @Success 200 {array} domain.NotebookMessage
// @Router /notebook/chats/{id}/messages [get]
func (h *NotebookHandler) GetChatHistory(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	chatID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid chat id"))
		return
	}

	messages, err := h.service.GetChatHistory(c.Request.Context(), userID, chatID)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, messages)
}

// SyncMessages godoc
// @Summary Sync external AI query and response to history
// @Tags notebook
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "Chat ID"
// @Param request body domain.SaveNotebookMessageRequest true "Sync request"
// @Success 200 {object} map[string]string
// @Router /notebook/chats/{id}/sync [post]
func (h *NotebookHandler) SyncMessages(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	chatID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid chat id"))
		return
	}

	var req domain.SaveNotebookMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	if err := h.service.SyncMessages(c.Request.Context(), userID, chatID, &req); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"status": "synchronized"})
}

// DeleteChat godoc
// @Summary Delete a notebook chat session
// @Tags notebook
// @Security BearerAuth
// @Param id path string true "Chat ID"
// @Success 200 {object} map[string]string
// @Router /notebook/chats/{id} [delete]
func (h *NotebookHandler) DeleteChat(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	chatID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid chat id"))
		return
	}

	if err := h.service.DeleteChat(c.Request.Context(), userID, chatID); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"status": "deleted"})
}

// SaveChatDocuments godoc
// @Summary Save documents to notebook chat context
// @Tags notebook
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "Chat ID"
// @Param request body domain.SaveNotebookDocsRequest true "Documents list"
// @Success 200 {object} map[string]string
// @Router /notebook/chats/{id}/documents [post]
func (h *NotebookHandler) SaveChatDocuments(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	chatID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid chat id"))
		return
	}

	var req domain.SaveNotebookDocsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	if err := h.service.SaveChatDocuments(c.Request.Context(), userID, chatID, req.DocumentIDs); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"status": "saved"})
}

// GetChatDocuments godoc
// @Summary Get documents in notebook chat context
// @Tags notebook
// @Security BearerAuth
// @Produce json
// @Param id path string true "Chat ID"
// @Success 200 {array} domain.DriveFile
// @Router /notebook/chats/{id}/documents [get]
func (h *NotebookHandler) GetChatDocuments(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	chatID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid chat id"))
		return
	}

	docs, err := h.service.GetChatDocuments(c.Request.Context(), userID, chatID)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, docs)
}

// GetChat godoc
// @Summary Get a notebook chat details
// @Tags notebook
// @Security BearerAuth
// @Produce json
// @Param id path string true "Chat ID"
// @Success 200 {object} domain.NotebookChat
// @Router /notebook/chats/{id} [get]
func (h *NotebookHandler) GetChat(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	chatID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid chat id"))
		return
	}

	chat, err := h.service.GetChat(c.Request.Context(), userID, chatID)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, chat)
}

// UpdateChat godoc
// @Summary Update notebook chat (title)
// @Tags notebook
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "Chat ID"
// @Param request body domain.UpdateNotebookChatRequest true "Update request"
// @Success 200 {object} map[string]string
// @Router /notebook/chats/{id} [put]
func (h *NotebookHandler) UpdateChat(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	chatID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid chat id"))
		return
	}

	var req domain.UpdateNotebookChatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	if err := h.service.UpdateChat(c.Request.Context(), userID, chatID, &req); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"status": "updated"})
}

// AddDocument godoc
// @Summary Add a document to chat context
// @Tags notebook
// @Security BearerAuth
// @Produce json
// @Param id path string true "Chat ID"
// @Param doc_id path string true "Document ID"
// @Success 200 {object} map[string]string
// @Router /notebook/chats/{id}/documents/{doc_id} [post]
func (h *NotebookHandler) AddDocument(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	chatID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid chat id"))
		return
	}
	docID, err := uuid.Parse(c.Param("doc_id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid doc id"))
		return
	}

	if err := h.service.AddDocument(c.Request.Context(), userID, chatID, docID); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"status": "added"})
}

// DeleteDocument godoc
// @Summary Remove a document from chat context
// @Tags notebook
// @Security BearerAuth
// @Produce json
// @Param id path string true "Chat ID"
// @Param doc_id path string true "Document ID"
// @Success 200 {object} map[string]string
// @Router /notebook/chats/{id}/documents/{doc_id} [delete]
func (h *NotebookHandler) DeleteDocument(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	chatID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid chat id"))
		return
	}
	docID, err := uuid.Parse(c.Param("doc_id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid doc id"))
		return
	}

	if err := h.service.RemoveDocument(c.Request.Context(), userID, chatID, docID); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"status": "removed"})
}

// GenerateSlidesWithTwoSlides godoc
// @Summary Generate slides with 2slides
// @Tags notebook
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body domain.TwoSlidesGenerateRequest true "2slides generation request"
// @Success 200 {object} map[string]interface{}
// @Router /notebook/slides/2slides/generate [post]
func (h *NotebookHandler) GenerateSlidesWithTwoSlides(c *gin.Context) {
	_ = middleware.MustGetUserID(c)

	var req domain.TwoSlidesGenerateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	resp, err := h.twoSlidesService.Generate(c.Request.Context(), req)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, resp)
}

// GetTwoSlidesJob godoc
// @Summary Get 2slides job status
// @Tags notebook
// @Security BearerAuth
// @Produce json
// @Param jobId path string true "2slides job ID"
// @Success 200 {object} map[string]interface{}
// @Router /notebook/slides/2slides/jobs/{jobId} [get]
func (h *NotebookHandler) GetTwoSlidesJob(c *gin.Context) {
	_ = middleware.MustGetUserID(c)

	resp, err := h.twoSlidesService.GetJob(c.Request.Context(), c.Param("jobId"))
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, resp)
}

// SearchTwoSlidesThemes godoc
// @Summary Search 2slides themes
// @Tags notebook
// @Security BearerAuth
// @Produce json
// @Param query query string true "Search keyword"
// @Param limit query int false "Limit"
// @Success 200 {object} map[string]interface{}
// @Router /notebook/slides/2slides/themes/search [get]
func (h *NotebookHandler) SearchTwoSlidesThemes(c *gin.Context) {
	_ = middleware.MustGetUserID(c)

	var req domain.TwoSlidesThemeSearchRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid query params"))
		return
	}

	resp, err := h.twoSlidesService.SearchThemes(c.Request.Context(), req)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, resp)
}

func (h *NotebookHandler) respondError(c *gin.Context, err *apperrors.AppError) {
	respondAppError(c, err)
}
