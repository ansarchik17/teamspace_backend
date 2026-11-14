package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
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

// SearchGlobal godoc
// @Summary Global search
// @Description Search visible users
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

func (h *SearchHandler) respondError(c *gin.Context, err *apperrors.AppError) {
	respondAppError(c, err)
}
