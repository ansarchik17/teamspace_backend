package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"net/http"
)

func (h *KanbanHandler) ListMyTasks(c *gin.Context) {
	var query struct {
		BoardID   string `form:"board_id"`
		ProjectID string `form:"project_id"`
		Status    string `form:"status"`
		Priority  string `form:"priority"`
		Overdue   bool   `form:"overdue"`
		Limit     int    `form:"limit"`
		Offset    int    `form:"offset"`
	}
	if err := c.ShouldBindQuery(&query); err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверные параметры фильтра"})
		return
	}
	f := domain.MyTasksFilter{Status: query.Status, Priority: query.Priority, Overdue: query.Overdue, Limit: query.Limit, Offset: query.Offset}
	for _, field := range []struct {
		value  string
		target **uuid.UUID
	}{{query.BoardID, &f.BoardID}, {query.ProjectID, &f.ProjectID}} {
		if field.value != "" {
			id, err := uuid.Parse(field.value)
			if err != nil || id == uuid.Nil {
				respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный UUID фильтра"})
				return
			}
			*field.target = &id
		}
	}
	response, err := h.svc.ListMyTasks(c.Request.Context(), middleware.MustGetUserID(c), f)
	if err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}
	respondJSON(c, http.StatusOK, response)
}
