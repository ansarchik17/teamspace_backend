package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/middleware"
)

// GetMultiBoardStats godoc
// @Summary Получить статистику по нескольким доскам AlemBoard
// @Description Возвращает общую статистику по выбранным доскам. Если board_ids пуст, возвращает по всем доступным доскам.
// @Tags kanban
// @Security BearerAuth
// @Produce json
// @Param board_ids query []string false "Список ID досок (UUID)" collectionFormat="multi"
// @Param date_from query string false "Дата начала периода (RFC3339 или YYYY-MM-DD)"
// @Param date_to query string false "Дата конца периода (RFC3339 или YYYY-MM-DD)"
// @Success 200 {object} domain.BoardStatsResponse
// @Failure 400 {object} map[string]any
// @Failure 403 {object} map[string]any
// @Failure 500 {object} map[string]any
// @Router /kanban/stats [get]
func (h *KanbanHandler) GetMultiBoardStats(c *gin.Context) {
	var req struct {
		BoardIDs []string `form:"board_ids"`
	}
	if err := c.ShouldBindQuery(&req); err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверные параметры запроса", "details": err.Error()})
		return
	}

	dateFrom, err := parseOptionalKanbanStatsTime(c.Query("date_from"), false)
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный date_from"})
		return
	}
	dateTo, err := parseOptionalKanbanStatsTime(c.Query("date_to"), true)
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный date_to"})
		return
	}

	userID := middleware.MustGetUserID(c)
	stats, err := h.svc.GetMultiBoardStats(c.Request.Context(), userID, req.BoardIDs, dateFrom, dateTo)
	if err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}

	respondJSON(c, http.StatusOK, stats)
}
