package handler

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	_ "github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

type SearchHandler struct {
	searchService   *service.SearchService
	kanbanService   *service.KanbanService
	calendarService *service.CalendarService
	driveService    *service.DriveService
	logger          *zap.Logger
}

func NewSearchHandler(
	searchService *service.SearchService,
	kanbanService *service.KanbanService,
	calendarService *service.CalendarService,
	driveService *service.DriveService,
	logger *zap.Logger,
) *SearchHandler {
	return &SearchHandler{
		searchService:   searchService,
		kanbanService:   kanbanService,
		calendarService: calendarService,
		driveService:    driveService,
		logger:          logger.With(zap.String("handler", "search")),
	}
}

// SearchUsers godoc
// @Summary Search users
// @Description Search for users by username, email or name
// @Tags search
// @Security BearerAuth
// @Param q query string true "Search query"
// @Param limit query int false "Limit"
// @Produce json
// @Success 200 {array} domain.UserResponse
// @Router /search/users [get]
func (h *SearchHandler) SearchUsers(c *gin.Context) {
	query := c.Query("q")
	if query == "" {
		h.respondError(c, apperrors.BadRequest("query parameter 'q' is required"))
		return
	}

	var params struct {
		Limit int `form:"limit" binding:"omitempty,min=1,max=50"`
	}

	if err := c.ShouldBindQuery(&params); err != nil {
		h.respondError(c, apperrors.Validation("invalid query parameters"))
		return
	}

	if params.Limit == 0 {
		params.Limit = 20
	}

	users, err := h.searchService.SearchUsers(c.Request.Context(), middleware.MustGetUserID(c), query, params.Limit)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{
		"query": query,
		"users": users,
		"count": len(users),
	})
}

// SearchMessages godoc
// @Summary Search messages
// @Description Search messages in a conversation
// @Tags search
// @Security BearerAuth
// @Param conversation_id query string true "Conversation ID"
// @Param q query string true "Search query"
// @Param limit query int false "Limit"
// @Produce json
// @Success 200 {array} domain.MessageView
// @Router /search/messages [get]
func (h *SearchHandler) SearchMessages(c *gin.Context) {
	conversationIDStr := c.Query("conversation_id")
	query := c.Query("q")

	if conversationIDStr == "" {
		h.respondError(c, apperrors.BadRequest("conversation_id is required"))
		return
	}

	if query == "" {
		h.respondError(c, apperrors.BadRequest("query parameter 'q' is required"))
		return
	}

	conversationID, err := uuid.Parse(conversationIDStr)
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid conversation ID"))
		return
	}

	var params struct {
		Limit int `form:"limit" binding:"omitempty,min=1,max=100"`
	}

	if err := c.ShouldBindQuery(&params); err != nil {
		h.respondError(c, apperrors.Validation("invalid query parameters"))
		return
	}

	if params.Limit == 0 {
		params.Limit = 50
	}

	userID := middleware.MustGetUserID(c)

	messages, err := h.searchService.SearchMessages(c.Request.Context(), conversationID, userID, query, params.Limit)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{
		"query":    query,
		"messages": messages,
		"count":    len(messages),
	})
}

// SearchGlobal godoc
// @Summary Global search
// @Description Search across users and conversations
// @Tags search
// @Security BearerAuth
// @Param q query string true "Search query"
// @Param limit query int false "Limit"
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /search [get]
func (h *SearchHandler) SearchGlobal(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	query := c.Query("q")

	if query == "" {
		h.respondError(c, apperrors.BadRequest("query parameter 'q' is required"))
		return
	}

	var params struct {
		Limit int `form:"limit" binding:"omitempty,min=1,max=50"`
	}

	if err := c.ShouldBindQuery(&params); err != nil {
		h.respondError(c, apperrors.Validation("invalid query parameters"))
		return
	}

	if params.Limit == 0 {
		params.Limit = 20
	}

	results, err := h.searchService.SearchGlobal(c.Request.Context(), userID, query, params.Limit)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, results)
}

// FindIdenticalMessages godoc
// @Summary Find identical messages
// @Description Find all messages that match the content of the provided message
// @Tags search
// @Security BearerAuth
// @Param content query string true "Content to match"
// @Produce json
// @Success 200 {array} domain.MessageView
// @Router /search/identical [get]
func (h *SearchHandler) FindIdenticalMessages(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	content := c.Query("content")
	if content == "" {
		h.respondError(c, apperrors.BadRequest("content parameter is required"))
		return
	}

	messages, err := h.searchService.FindIdenticalMessages(c.Request.Context(), userID, content)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{
		"messages": messages,
		"count":    len(messages),
	})
}

// SearchAll godoc
// @Summary Global search across backend entities
// @Description Search for people, tasks, organizations, messages, meetings, documents and other available entities
// @Tags search
// @Security BearerAuth
// @Param q query string true "Search query"
// @Param limit query int false "Limit for merged search results"
// @Produce json
// @Success 200 {object} domain.SearchAllResponse
// @Failure 400 {object} apperrors.AppError
// @Failure 401 {object} apperrors.AppError
// @Failure 500 {object} apperrors.AppError
// @Router /search/all [get]
func (h *SearchHandler) SearchAll(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	query := c.Query("q")
	if query == "" {
		h.respondError(c, apperrors.BadRequest("query parameter 'q' is required"))
		return
	}

	var params struct {
		Limit int `form:"limit" binding:"omitempty,min=1,max=50"`
	}

	if err := c.ShouldBindQuery(&params); err != nil {
		h.respondError(c, apperrors.Validation("invalid query parameters"))
		return
	}

	if params.Limit == 0 {
		params.Limit = 10
	}

	if h.searchService == nil {
		h.respondError(c, apperrors.Internal("search service is not configured"))
		return
	}

	result, err := h.searchService.SearchAll(c.Request.Context(), userID, query, params.Limit, service.SearchAllDependencies{
		Kanban:   h.kanbanService,
		Calendar: h.calendarService,
		Drive:    h.driveService,
	})
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, result)
}

// SearchMessagesGlobal godoc
// @Summary Global message search
// @Description Search messages across all accessible chats (personal, groups, channels)
// @Tags search
// @Security BearerAuth
// @Param q query string true "Search query (min 2 chars)"
// @Param limit query int false "Limit (max 100, default 20)"
// @Param offset query int false "Offset for pagination (default 0)"
// @Param chat_type query string false "Filter by chat type: personal, group, channel"
// @Param chat_id query string false "Filter by specific chat ID"
// @Param attachment_type query string false "Filter by attachment type: image, video, audio, file, voice"
// @Param from_date query string false "Filter from date (ISO 8601)"
// @Param to_date query string false "Filter to date (ISO 8601)"
// @Param sender_id query int false "Filter by sender ID"
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{}
// @Router /search/messages/global [get]
func (h *SearchHandler) SearchMessagesGlobal(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	query := c.Query("q")

	if query == "" {
		h.respondError(c, apperrors.BadRequest("query parameter 'q' is required"))
		return
	}

	var params struct {
		Limit          int    `form:"limit" binding:"omitempty,min=1,max=100"`
		Offset         int    `form:"offset" binding:"omitempty,min=0"`
		ChatType       string `form:"chat_type" binding:"omitempty,oneof=personal group channel"`
		ChatID         string `form:"chat_id"`
		AttachmentType string `form:"attachment_type" binding:"omitempty,oneof=image video audio file voice"`
		FromDate       string `form:"from_date"`
		ToDate         string `form:"to_date"`
		SenderID       int64  `form:"sender_id" binding:"omitempty,min=1"`
	}

	if err := c.ShouldBindQuery(&params); err != nil {
		h.respondError(c, apperrors.Validation("invalid query parameters"))
		return
	}

	if params.Limit == 0 {
		params.Limit = 20
	}

	// Собираем фильтры
	filters := make(map[string]interface{})

	if params.ChatType != "" {
		filters["chat_type"] = params.ChatType
	}

	if params.ChatID != "" {
		chatID, err := uuid.Parse(params.ChatID)
		if err != nil {
			h.respondError(c, apperrors.BadRequest("invalid chat_id"))
			return
		}
		filters["chat_id"] = chatID
	}

	if params.AttachmentType != "" {
		filters["attachment_type"] = params.AttachmentType
	}

	if params.SenderID > 0 {
		filters["sender_id"] = params.SenderID
	}

	if params.FromDate != "" {
		fromDate, err := time.Parse(time.RFC3339, params.FromDate)
		if err != nil {
			h.respondError(c, apperrors.BadRequest("invalid from_date format, use ISO 8601"))
			return
		}
		filters["from_date"] = fromDate
	}

	if params.ToDate != "" {
		toDate, err := time.Parse(time.RFC3339, params.ToDate)
		if err != nil {
			h.respondError(c, apperrors.BadRequest("invalid to_date format, use ISO 8601"))
			return
		}
		filters["to_date"] = toDate
	}
	if fromDate, fromOK := filters["from_date"].(time.Time); fromOK {
		if toDate, toOK := filters["to_date"].(time.Time); toOK && fromDate.After(toDate) {
			h.respondError(c, apperrors.Validation("from_date must not be after to_date"))
			return
		}
	}

	messages, totalCount, err := h.searchService.SearchMessagesGlobal(c.Request.Context(), userID, query, filters, params.Limit, params.Offset)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{
		"query":       query,
		"results":     messages,
		"total_count": totalCount,
		"has_more":    totalCount > params.Offset+len(messages),
	})
}

// SearchAttachments godoc
// @Summary Search attachments
// @Description Search attachments across all accessible chats
// @Tags search
// @Security BearerAuth
// @Param attachment_type query string false "Filter by type: image, video, audio, file, voice, all (default all)"
// @Param q query string false "Search by filename"
// @Param limit query int false "Limit (max 100, default 20)"
// @Param offset query int false "Offset for pagination (default 0)"
// @Param chat_id query string false "Filter by specific chat ID"
// @Param from_date query string false "Filter from date (ISO 8601)"
// @Param to_date query string false "Filter to date (ISO 8601)"
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{}
// @Router /search/attachments [get]
func (h *SearchHandler) SearchAttachments(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	var params struct {
		AttachmentType string `form:"attachment_type" binding:"omitempty,oneof=image video audio file voice all"`
		Query          string `form:"q"`
		Limit          int    `form:"limit" binding:"omitempty,min=1,max=100"`
		Offset         int    `form:"offset" binding:"omitempty,min=0"`
		ChatID         string `form:"chat_id"`
		FromDate       string `form:"from_date"`
		ToDate         string `form:"to_date"`
	}

	if err := c.ShouldBindQuery(&params); err != nil {
		h.respondError(c, apperrors.Validation("invalid query parameters"))
		return
	}

	if params.Limit == 0 {
		params.Limit = 20
	}

	if params.AttachmentType == "" {
		params.AttachmentType = "all"
	}

	// Собираем фильтры
	filters := make(map[string]interface{})
	filters["attachment_type"] = params.AttachmentType

	if params.Query != "" {
		filters["query"] = params.Query
	}

	if params.ChatID != "" {
		chatID, err := uuid.Parse(params.ChatID)
		if err != nil {
			h.respondError(c, apperrors.BadRequest("invalid chat_id"))
			return
		}
		filters["chat_id"] = chatID
	}

	if params.FromDate != "" {
		fromDate, err := time.Parse(time.RFC3339, params.FromDate)
		if err != nil {
			h.respondError(c, apperrors.BadRequest("invalid from_date format, use ISO 8601"))
			return
		}
		filters["from_date"] = fromDate
	}

	if params.ToDate != "" {
		toDate, err := time.Parse(time.RFC3339, params.ToDate)
		if err != nil {
			h.respondError(c, apperrors.BadRequest("invalid to_date format, use ISO 8601"))
			return
		}
		filters["to_date"] = toDate
	}
	if fromDate, fromOK := filters["from_date"].(time.Time); fromOK {
		if toDate, toOK := filters["to_date"].(time.Time); toOK && fromDate.After(toDate) {
			h.respondError(c, apperrors.Validation("from_date must not be after to_date"))
			return
		}
	}

	attachments, totalCount, err := h.searchService.SearchAttachments(c.Request.Context(), userID, filters, params.Limit, params.Offset)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{
		"attachments": attachments,
		"total_count": totalCount,
		"has_more":    totalCount > params.Offset+len(attachments),
	})
}

// GetChatAttachmentStats godoc
// @Summary Get chat attachment statistics
// @Description Get statistics of attachments by type in a specific chat
// @Tags search
// @Security BearerAuth
// @Param chat_id path string true "Chat ID"
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{}
// @Failure 403 {object} map[string]interface{}
// @Router /chats/{chat_id}/attachments/stats [get]
func (h *SearchHandler) GetChatAttachmentStats(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	chatIDStr := c.Param("chat_id")

	chatID, err := uuid.Parse(chatIDStr)
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid chat_id"))
		return
	}

	stats, totalSize, err := h.searchService.GetAttachmentStats(c.Request.Context(), chatID, userID)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	// Подсчитываем общее количество файлов
	totalFiles := 0
	for _, count := range stats {
		totalFiles += count
	}

	respondJSON(c, http.StatusOK, gin.H{
		"chat_id":          chatID.String(),
		"stats":            stats,
		"total_files":      totalFiles,
		"total_size_bytes": totalSize,
	})
}

func (h *SearchHandler) respondError(c *gin.Context, err *apperrors.AppError) {
	respondAppError(c, err)
}
