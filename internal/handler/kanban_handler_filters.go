package handler

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"github.com/west6ide/workspace-backend/internal/service"
)

// GetBoardFiltered godoc
// @Summary Get kanban board with filters
// @Tags kanban
// @Security BearerAuth
// @Produce json
// @Param user_id query int false "Legacy default board owner; does not grant access"
// @Param board_id query string false "Board UUID"
// @Param q query string false "Search text"
// @Param author_ids query []int false "Task authors" collectionFormat="multi"
// @Param statuses query []string false "Statuses" collectionFormat="multi"
// @Param limit query int false "Page size, default 100, maximum 200"
// @Param offset query int false "Page offset"
// @Param status query string false "Filter by status (column name or key)"
// @Param date query string false "Filter by due date (YYYY-MM-DD format)"
// @Param name query string false "Filter by task name/title (partial match)"
// @Success 200 {object} domain.BoardResponse
// @Failure 400 {object} map[string]any
// @Failure 401 {object} map[string]any
// @Failure 500 {object} map[string]any
// @Router /kanban/board/filtered [get]
func (h *KanbanHandler) GetBoardFiltered(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	filters := &service.GetBoardFilters{}
	if err := c.ShouldBindQuery(&filters.SearchTasksRequest); err != nil {
		respondJSON(c, 400, gin.H{"error": "Некорректные фильтры"})
		return
	}

	// Parse user_id filter
	if userIDStr := c.Query("user_id"); userIDStr != "" {
		if parsedUID, err := strconv.ParseInt(userIDStr, 10, 64); err == nil && parsedUID > 0 {
			filters.UserID = &parsedUID
		} else {
			respondJSON(c, 400, gin.H{"error": "Некорректный user_id"})
			return
		}
	}

	// Parse status filter
	if status := c.Query("status"); status != "" {
		filters.Status = &status
	}

	// Parse date filter
	if dateStr := c.Query("date"); dateStr != "" {
		if parsedDate, err := time.Parse("2006-01-02", dateStr); err == nil {
			filters.Date = &parsedDate
		} else {
			respondJSON(c, 400, gin.H{"error": "Некорректная дата: используйте YYYY-MM-DD"})
			return
		}
	}

	// Parse name filter
	if name := c.Query("name"); name != "" {
		filters.Name = &name
	}

	board, err := h.svc.GetBoardWithFilters(c.Request.Context(), userID, filters)
	if err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}

	respondJSON(c, http.StatusOK, board)
}

// GetOrgStats godoc
// @Summary Get kanban statistics by organization unit
// @Tags kanban
// @Security BearerAuth
// @Produce json
// @Param state_body_id query int true "State body ID"
// @Success 200 {object} domain.OrgKanbanStatsResponse
// @Failure 400 {object} map[string]any
// @Failure 403 {object} map[string]any
// @Failure 500 {object} map[string]any
// @Router /kanban/org/stats [get]
