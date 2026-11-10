package handler

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

type AnalyticsHandler struct {
	repo   *repository.LoginLogRepository
	logger *zap.Logger
}

func NewAnalyticsHandler(repo *repository.LoginLogRepository, logger *zap.Logger) *AnalyticsHandler {
	return &AnalyticsHandler{
		repo:   repo,
		logger: logger.With(zap.String("handler", "analytics")),
	}
}

// GetLoginAnalytics
// @Summary Get login analytics
// @Description Get usage statistics about user logins. Filters: start_date/end_date (RFC3339) OR start_day/end_day ("2026-02-10").
// @Tags analytics
// @Produce json
// @Param start_date query string false "Start timestamp (ISO8601)"
// @Param end_date query string false "End timestamp (ISO8601)"
// @Param start_day query string false "Start day (YYYY-MM-DD)"
// @Param end_day query string false "End day (YYYY-MM-DD)"
// @Param limit query int false "Limit"
// @Param offset query int false "Offset"
// @Success 200 {object} domain.LoginAnalyticsResponse
// @Router /analytics/logins [get]
func (h *AnalyticsHandler) GetLoginAnalytics(c *gin.Context) {
	req := parseLoginStatsRequest(c)
	requesterID := middleware.MustGetUserID(c)

	result, err := h.repo.GetScopedAnalytics(c.Request.Context(), requesterID, req)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, result)
}

// TrackSectionVisit
// @Summary Track analytics section visit
// @Description Stores an authenticated section open event for ministry analytics.
// @Tags analytics
// @Accept json
// @Produce json
// @Param request body domain.SectionVisitTrackRequest true "Section visit payload"
// @Success 202 {object} map[string]string
// @Router /analytics/section-visits [post]
func (h *AnalyticsHandler) TrackSectionVisit(c *gin.Context) {
	var req domain.SectionVisitTrackRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	requesterID := middleware.MustGetUserID(c)
	if err := h.repo.TrackSectionVisit(c.Request.Context(), requesterID, req); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusAccepted, gin.H{"status": "accepted"})
}

// GetMinistryAnalytics
// @Summary Get ministry analytics and employee rating
// @Description Returns scoped ministry statistics, section usage and employee ranking.
// @Tags analytics
// @Produce json
// @Param start_date query string false "Start timestamp (ISO8601)"
// @Param end_date query string false "End timestamp (ISO8601)"
// @Param start_day query string false "Start day (YYYY-MM-DD)"
// @Param end_day query string false "End day (YYYY-MM-DD)"
// @Param ministry_id query int false "Ministry ID (global roles only, otherwise must match own ministry)"
// @Param state_body_id query int false "State body ID"
// @Param department_id query int false "Department ID"
// @Param division_id query int false "Division ID"
// @Param limit query int false "Top rows limit"
// @Success 200 {object} domain.MinistryAnalyticsResponse
// Legacy government analytics route removed from the public API.
func (h *AnalyticsHandler) GetMinistryAnalytics(c *gin.Context) {
	req := parseMinistryAnalyticsRequest(c)
	requesterID := middleware.MustGetUserID(c)

	result, err := h.repo.GetMinistryAnalytics(c.Request.Context(), requesterID, req)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, result)
}

// GetStateBodyUsersAnalytics
// @Summary Get users and login activity for a state body
// @Description Returns all active users in the selected state body with login counts and timestamps for the chosen period.
// @Tags analytics
// @Security BearerAuth
// @Produce json
// @Param state_body_id query int false "State body ID"
// @Param q query string false "Search by name, username or email"
// @Param only_logged_in query bool false "Return only users who logged in during selected period"
// @Param start_date query string false "Start timestamp (ISO8601)"
// @Param end_date query string false "End timestamp (ISO8601)"
// @Param start_day query string false "Start day (YYYY-MM-DD)"
// @Param end_day query string false "End day (YYYY-MM-DD)"
// @Param limit query int false "Page size"
// @Param offset query int false "Offset"
// @Success 200 {object} domain.StateBodyUsersAnalyticsResponse
// Legacy government analytics route removed from the public API.
func (h *AnalyticsHandler) GetStateBodyUsersAnalytics(c *gin.Context) {
	req := parseStateBodyUsersAnalyticsRequest(c)
	requesterID := middleware.MustGetUserID(c)

	result, err := h.repo.GetStateBodyUsersAnalytics(c.Request.Context(), requesterID, req)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, result)
}

// CheckMinistryAnalyticsAccess
// @Summary Check whether current user can view ministry analytics
// @Tags analytics
// @Security BearerAuth
// @Produce json
// @Param ministry_id query int true "Ministry ID"
// @Success 200 {object} domain.MinistryAnalyticsAccessCheckResponse
// Legacy government analytics route removed from the public API.
func (h *AnalyticsHandler) CheckMinistryAnalyticsAccess(c *gin.Context) {
	requesterID := middleware.MustGetUserID(c)
	ministryID, err := strconv.ParseInt(c.Query("ministry_id"), 10, 64)
	if err != nil || ministryID <= 0 {
		h.respondError(c, apperrors.Validation("ministry_id is required"))
		return
	}

	resp, err := h.repo.CheckMinistryAnalyticsAccess(c.Request.Context(), requesterID, ministryID)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, resp)
}

// GrantMinistryAnalyticsAccess
// @Summary Grant delegated access to ministry analytics
// @Tags analytics
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param ministry_id path int true "Ministry ID"
// @Param request body domain.GrantMinistryAnalyticsAccessRequest true "Request"
// @Success 200 {object} map[string]string
// Legacy government analytics route removed from the public API.
func (h *AnalyticsHandler) GrantMinistryAnalyticsAccess(c *gin.Context) {
	performerID := middleware.MustGetUserID(c)
	ministryID, err := strconv.ParseInt(c.Param("ministry_id"), 10, 64)
	if err != nil || ministryID <= 0 {
		h.respondError(c, apperrors.Validation("invalid ministry_id"))
		return
	}

	var req domain.GrantMinistryAnalyticsAccessRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	if err := h.repo.GrantMinistryAnalyticsAccess(c.Request.Context(), performerID, ministryID, req.UserID); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"status": "granted"})
}

// RevokeMinistryAnalyticsAccess
// @Summary Revoke delegated access to ministry analytics
// @Tags analytics
// @Security BearerAuth
// @Produce json
// @Param ministry_id path int true "Ministry ID"
// @Param user_id path int true "User ID"
// @Success 200 {object} map[string]string
// Legacy government analytics route removed from the public API.
func (h *AnalyticsHandler) RevokeMinistryAnalyticsAccess(c *gin.Context) {
	performerID := middleware.MustGetUserID(c)
	ministryID, err := strconv.ParseInt(c.Param("ministry_id"), 10, 64)
	if err != nil || ministryID <= 0 {
		h.respondError(c, apperrors.Validation("invalid ministry_id"))
		return
	}
	targetUserID, err := strconv.ParseInt(c.Param("user_id"), 10, 64)
	if err != nil || targetUserID <= 0 {
		h.respondError(c, apperrors.Validation("invalid user_id"))
		return
	}

	if err := h.repo.RevokeMinistryAnalyticsAccess(c.Request.Context(), performerID, ministryID, targetUserID); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"status": "revoked"})
}

// ListMinistryAnalyticsAccess
// @Summary List users with delegated access to ministry analytics
// @Tags analytics
// @Security BearerAuth
// @Produce json
// @Param ministry_id path int true "Ministry ID"
// @Success 200 {array} domain.MinistryAnalyticsAccessEntry
// Legacy government analytics route removed from the public API.
func (h *AnalyticsHandler) ListMinistryAnalyticsAccess(c *gin.Context) {
	performerID := middleware.MustGetUserID(c)
	ministryID, err := strconv.ParseInt(c.Param("ministry_id"), 10, 64)
	if err != nil || ministryID <= 0 {
		h.respondError(c, apperrors.Validation("invalid ministry_id"))
		return
	}

	items, err := h.repo.ListMinistryAnalyticsAccess(c.Request.Context(), performerID, ministryID)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, items)
}

func parseLoginStatsRequest(c *gin.Context) domain.LoginStatsRequest {
	var req domain.LoginStatsRequest
	req.Limit = 50
	if c.Query("limit") != "" || c.Query("offset") != "" {
		_ = c.ShouldBindQuery(&req)
	}

	if s := c.Query("start_date"); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			req.StartDate = &t
		}
	}
	if s := c.Query("end_date"); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			req.EndDate = &t
		}
	}
	if s := c.Query("start_day"); s != "" {
		if _, err := time.Parse("2006-01-02", s); err == nil {
			req.StartDay = &s
		}
	}
	if s := c.Query("end_day"); s != "" {
		if _, err := time.Parse("2006-01-02", s); err == nil {
			req.EndDay = &s
		}
	}
	return req
}

func parseMinistryAnalyticsRequest(c *gin.Context) domain.MinistryAnalyticsRequest {
	var req domain.MinistryAnalyticsRequest
	req.Limit = 20
	if c.Query("limit") != "" || c.Query("offset") != "" || c.Query("ministry_id") != "" {
		_ = c.ShouldBindQuery(&req)
	}
	if s := c.Query("start_date"); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			req.StartDate = &t
		}
	}
	if s := c.Query("end_date"); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			req.EndDate = &t
		}
	}
	if s := c.Query("start_day"); s != "" {
		if _, err := time.Parse("2006-01-02", s); err == nil {
			req.StartDay = &s
		}
	}
	if s := c.Query("end_day"); s != "" {
		if _, err := time.Parse("2006-01-02", s); err == nil {
			req.EndDay = &s
		}
	}
	req.Normalize()
	return req
}

func parseStateBodyUsersAnalyticsRequest(c *gin.Context) domain.StateBodyUsersAnalyticsRequest {
	var req domain.StateBodyUsersAnalyticsRequest
	req.Limit = 20
	if c.Query("limit") != "" || c.Query("offset") != "" || c.Query("state_body_id") != "" || c.Query("q") != "" {
		_ = c.ShouldBindQuery(&req)
	}
	if s := c.Query("start_date"); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			req.StartDate = &t
		}
	}
	if s := c.Query("end_date"); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			req.EndDate = &t
		}
	}
	if s := c.Query("start_day"); s != "" {
		if _, err := time.Parse("2006-01-02", s); err == nil {
			req.StartDay = &s
		}
	}
	if s := c.Query("end_day"); s != "" {
		if _, err := time.Parse("2006-01-02", s); err == nil {
			req.EndDay = &s
		}
	}
	if s := c.Query("only_logged_in"); s != "" {
		if b, err := strconv.ParseBool(s); err == nil {
			req.OnlyLoggedIn = &b
		}
	}
	req.Normalize()
	return req
}

func (h *AnalyticsHandler) respondError(c *gin.Context, err *apperrors.AppError) {
	respondAppError(c, err)
}
