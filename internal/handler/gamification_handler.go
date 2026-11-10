package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"github.com/west6ide/workspace-backend/internal/service"
)

type GamificationHandler struct {
	gamificationService *service.GamificationService
}

func NewGamificationHandler(gamificationService *service.GamificationService) *GamificationHandler {
	return &GamificationHandler{
		gamificationService: gamificationService,
	}
}

// GetDashboard godoc
// @Summary Get Gamification Dashboard (Leaderboard)
// @Description Returns a ranked list of users and their gamification points/levels.
// @Tags gamification
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param limit query int false "Number of users to fetch" default(50)
// @Success 200 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /gamification/dashboard [get]
func (h *GamificationHandler) GetDashboard(c *gin.Context) {
	limitStr := c.DefaultQuery("limit", "50")
	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit <= 0 {
		limit = 50
	}

	dashboard, err := h.gamificationService.GetDashboard(c.Request.Context(), limit)
	if err != nil {
		respondJSON(c, http.StatusInternalServerError, err)
		return
	}

	respondJSON(c, http.StatusOK, dashboard)
}

// GetMyStats godoc
// @Summary Get My Gamification Stats
// @Description Returns the current user's gamification points and level. Supports filtering total points earned within a specific date range.
// @Tags gamification
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param start_date query string false "Start date for period score (YYYY-MM-DD)"
// @Param end_date query string false "End date for period score (YYYY-MM-DD)"
// @Success 200 {object} domain.UserGamificationDashboard
// @Failure 500 {object} map[string]interface{}
// @Router /gamification/my-stats [get]
func (h *GamificationHandler) GetMyStats(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	startDate := c.Query("start_date")
	endDate := c.Query("end_date")

	stats, err := h.gamificationService.GetMyStats(c.Request.Context(), userID, startDate, endDate)
	if err != nil {
		respondJSON(c, http.StatusInternalServerError, err)
		return
	}

	respondJSON(c, http.StatusOK, stats)
}
