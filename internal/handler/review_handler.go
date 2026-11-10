package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

type ReviewHandler struct {
	reviewService *service.ReviewService
	logger        *zap.Logger
}

func NewReviewHandler(reviewService *service.ReviewService, logger *zap.Logger) *ReviewHandler {
	return &ReviewHandler{
		reviewService: reviewService,
		logger:        logger.With(zap.String("handler", "review")),
	}
}

// CreateReview godoc
// @Summary Submit a user review
// @Description Rate another user on 5 skills. Only allowed if you have a chat history and haven't rated them this month.
// @Tags reviews
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body domain.CreateReviewRequest true "Review Data"
// @Success 201 {object} map[string]string
// @Failure 400 {object} map[string]interface{}
// @Failure 403 {object} map[string]interface{}
// @Router /reviews [post]
func (h *ReviewHandler) CreateReview(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	var req domain.CreateReviewRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	if err := h.reviewService.CreateReview(c.Request.Context(), userID, req); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusCreated, gin.H{"message": apiMessage(c, "review submitted successfully")})
}

// GetMyStats godoc
// @Summary Get my personal review stats
// @Description Get aggregated review scores for the current month
// @Tags reviews
// @Security BearerAuth
// @Produce json
// @Success 200 {object} domain.UserReviewStats
// @Router /reviews/my-stats [get]
func (h *ReviewHandler) GetMyStats(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	stats, err := h.reviewService.GetMyMonthlyStats(c.Request.Context(), userID)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, stats)
}

func (h *ReviewHandler) respondError(c *gin.Context, err *apperrors.AppError) {
	respondAppError(c, err)
}

// CheckReviewStatus
// @Summary Check if I reviewed a user
// @Tags reviews
// @Param target_id query int true "Target User ID"
// @Success 200 {object} map[string]bool
// @Router /reviews/status [get]
func (h *ReviewHandler) CheckReviewStatus(c *gin.Context) {
	reviewerID := middleware.MustGetUserID(c)
	targetID, _ := strconv.ParseInt(c.Query("target_id"), 10, 64)

	reviewed, err := h.reviewService.CheckReviewStatus(c.Request.Context(), reviewerID, targetID)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"has_reviewed": reviewed})
}
