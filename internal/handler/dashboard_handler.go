package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"github.com/west6ide/workspace-backend/internal/service"
)

type DashboardHandler struct {
	dashboardService *service.DashboardService
}

func NewDashboardHandler(dashboardService *service.DashboardService) *DashboardHandler {
	return &DashboardHandler{
		dashboardService: dashboardService,
	}
}

// GetSummary godoc
// @Summary Get dashboard summary
// @Description Returns aggregated dashboard data for the authenticated user
// @Tags dashboard
// @Produce json
// @Security BearerAuth
// @Success 200 {object} domain.DashboardSummaryResponse
// @Failure 401 {object} apperrors.AppError
// @Failure 500 {object} apperrors.AppError
// @Router /dashboard/summary [get]
func (h *DashboardHandler) GetSummary(c *gin.Context) {
	userId := middleware.MustGetUserID(c)

	summary, err := h.dashboardService.GetSummary(c.Request.Context(), userId)
	if err != nil {
		respondJSON(c, http.StatusInternalServerError, gin.H{"error": "failed to get dashboard summary"})
		return
	}

	respondJSON(c, http.StatusOK, summary)
}
