package handler

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

// AIAssistantHandler обрабатывает AI-специфичные endpoints.
// Возвращает компактные ответы с готовым speech_text для TTS.
// Значительно быстрее общих endpoint'ов: нет лишних полей, меньше JSON.
type AIAssistantHandler struct {
	service *service.AIAssistantService
	logger  *zap.Logger
}

func NewAIAssistantHandler(svc *service.AIAssistantService, logger *zap.Logger) *AIAssistantHandler {
	return &AIAssistantHandler{
		service: svc,
		logger:  logger.With(zap.String("handler", "ai_assistant")),
	}
}

func (h *AIAssistantHandler) logStage(c *gin.Context, stage string, startedAt time.Time, fields ...zap.Field) {
	traceID := middleware.GetTraceID(c)
	baseFields := []zap.Field{
		zap.String("trace_id", traceID),
		zap.String("stage", stage),
		zap.Float64("duration_ms", float64(time.Since(startedAt).Milliseconds())),
	}
	baseFields = append(baseFields, fields...)
	h.logger.Info("ai_latency_stage", baseFields...)
}

// GetCalendarToday — GET /api/v1/ai/calendar/today
// @Summary AI: Сводка календаря на сегодня
// @Description Возвращает список событий пользователя на сегодня с готовым speech_text для TTS.
// @Tags AI Assistant
// @Produce json
// @Security BearerAuth
// @Param timezone query string false "Timezone (e.g. Asia/Almaty). Default: Asia/Almaty"
// @Success 200 {object} service.AICalendarSummary
// @Router /api/v1/ai/calendar/today [get]
func (h *AIAssistantHandler) GetCalendarToday(c *gin.Context) {
	startedAt := time.Now()
	userID := middleware.MustGetUserID(c)
	timezone := c.DefaultQuery("timezone", "Asia/Almaty")

	traceID := middleware.GetTraceID(c)
	if traceID != "" {
		h.logger.Debug("ai_calendar_today", zap.String("trace_id", traceID), zap.Int64("user_id", userID))
	}

	result, err := h.service.GetCalendarToday(c.Request.Context(), userID, timezone)
	if err != nil {
		h.logStage(c, "ai_calendar_today_failed", startedAt, zap.Int64("user_id", userID))
		h.logger.Error("ai_calendar_today_failed", zap.Error(err), zap.Int64("user_id", userID))
		respondJSON(c, http.StatusInternalServerError, gin.H{"error": "Не удалось получить данные календаря"})
		return
	}

	h.logStage(c, "ai_calendar_today", startedAt, zap.Int64("user_id", userID))
	respondJSON(c, http.StatusOK, result)
}

// GetTasksSummary — GET /api/v1/ai/tasks/summary
// @Summary AI: Сводка задач пользователя
// @Description Возвращает список активных задач (TODO + IN_PROGRESS) с готовым speech_text.
// @Tags AI Assistant
// @Produce json
// @Security BearerAuth
// @Success 200 {object} service.AITasksSummary
// @Router /api/v1/ai/tasks/summary [get]
func (h *AIAssistantHandler) GetTasksSummary(c *gin.Context) {
	startedAt := time.Now()
	userID := middleware.MustGetUserID(c)

	result, err := h.service.GetTasksSummary(c.Request.Context(), userID)
	if err != nil {
		h.logStage(c, "ai_tasks_summary_failed", startedAt, zap.Int64("user_id", userID))
		h.logger.Error("ai_tasks_summary_failed", zap.Error(err), zap.Int64("user_id", userID))
		respondJSON(c, http.StatusInternalServerError, gin.H{"error": "Не удалось получить задачи"})
		return
	}

	h.logStage(c, "ai_tasks_summary", startedAt, zap.Int64("user_id", userID))
	respondJSON(c, http.StatusOK, result)
}

// SearchContacts — GET /api/v1/ai/contacts/search
// @Summary AI: Поиск контактов
// @Description Возвращает компактный список контактов (id, name, email) с готовым speech_text.
// @Tags AI Assistant
// @Produce json
// @Security BearerAuth
// @Param q query string true "Search query"
// @Success 200 {object} service.AIContactsResult
// @Router /api/v1/ai/contacts/search [get]
func (h *AIAssistantHandler) SearchContacts(c *gin.Context) {
	startedAt := time.Now()
	userID := middleware.MustGetUserID(c)
	query := c.Query("q")

	if query == "" {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Параметр 'q' обязателен"})
		return
	}

	result, err := h.service.SearchContacts(c.Request.Context(), userID, query)
	if err != nil {
		h.logStage(c, "ai_contacts_search_failed", startedAt, zap.String("query", query))
		h.logger.Error("ai_contacts_search_failed", zap.Error(err), zap.String("query", query))
		respondJSON(c, http.StatusInternalServerError, gin.H{"error": "Не удалось выполнить поиск"})
		return
	}

	h.logStage(c, "ai_contacts_search", startedAt, zap.String("query", query))
	respondJSON(c, http.StatusOK, result)
}

// Execute — POST /api/v1/ai/execute
func (h *AIAssistantHandler) Execute(c *gin.Context) {
	startedAt := time.Now()
	userID := middleware.MustGetUserID(c)

	var req service.AIExecuteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Некорректный AI execute request"})
		return
	}

	result, err := h.service.Execute(c.Request.Context(), userID, req)
	if err != nil {
		h.logStage(c, "ai_execute_failed", startedAt, zap.String("intent", req.Intent), zap.Int64("user_id", userID))
		h.logger.Error("ai_execute_failed", zap.Error(err), zap.String("intent", req.Intent), zap.Int64("user_id", userID))
		respondJSON(c, http.StatusInternalServerError, gin.H{"error": "Не удалось выполнить AI запрос"})
		return
	}

	h.logStage(c, "ai_execute", startedAt, zap.String("intent", req.Intent), zap.Int64("user_id", userID))
	respondJSON(c, http.StatusOK, result)
}
