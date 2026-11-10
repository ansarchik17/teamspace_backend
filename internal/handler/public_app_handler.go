package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	_ "github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

type PublicAppHandler struct {
	service *service.PublicAppService
	logger  *zap.Logger
}

func NewPublicAppHandler(service *service.PublicAppService, logger *zap.Logger) *PublicAppHandler {
	return &PublicAppHandler{
		service: service,
		logger:  logger.With(zap.String("handler", "public_app")),
	}
}

// GetPublicApps godoc
// @Summary List all public apps
// @Description Get a list of all public apps
// @Tags public_apps
// @Produce json
// @Success 200 {array} domain.PublicApp
// @Param name query string false "Filter by app name"
// @Param category query string false "Filter by category: ИИ, Документация, Прочие"
// @Param downloaded query boolean false "Filter by downloaded status (true/false)"
// @Param sort query string false "Sort mode: position (default) or rating"
// @Security BearerAuth
// @Router /public-apps [get]
func (h *PublicAppHandler) GetPublicApps(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	nameQuery := c.Query("name")
	category := c.Query("category")
	sortMode := c.Query("sort")

	var downloaded *bool
	if val, ok := c.GetQuery("downloaded"); ok {
		if boolVal, err := strconv.ParseBool(val); err == nil {
			downloaded = &boolVal
		}
	}

	apps, err := h.service.GetAll(c.Request.Context(), userID, nameQuery, downloaded, sortMode, category)
	if err != nil {
		h.logger.Error("Failed to fetch public apps", zap.Error(err))
		respondJSON(c, http.StatusInternalServerError, gin.H{"error": "failed to fetch public apps"})
		return
	}
	respondJSON(c, http.StatusOK, apps)
}

// GetPublicApp godoc
// @Summary Get Public App by ID
// @Description Get a specific public app by ID
// @Tags public_apps
// @Produce json
// @Param id path int true "Public App ID"
// @Success 200 {object} domain.PublicApp
// @Security BearerAuth
// @Router /public-apps/{id} [get]
func (h *PublicAppHandler) GetPublicApp(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "invalid public app id"})
		return
	}
	app, err := h.service.GetByID(c.Request.Context(), id, userID)
	if err != nil {
		h.logger.Error("Failed to fetch public app", zap.Error(err))
		if errors.Is(err, pgx.ErrNoRows) {
			respondJSON(c, http.StatusNotFound, gin.H{"error": "public app not found"})
			return
		}
		respondJSON(c, http.StatusInternalServerError, gin.H{"error": "failed to fetch public app"})
		return
	}
	respondJSON(c, http.StatusOK, app)
}

// CreatePublicApp godoc
// @Summary Create a Public App
// @Description Create a new public app
// @Tags public_apps
// @Accept json
// @Produce json
// @Param request body object{app_name=string,link=string,app_photo=string,web_view=boolean,position=integer,is_global=boolean,is_private=boolean,category=string,tags=[]string,organization_ids=[]integer} true "Request"
// @Success 201 {object} domain.PublicApp
// @Security BearerAuth
// @Router /public-apps [post]
func (h *PublicAppHandler) CreatePublicApp(c *gin.Context) {
	middleware.MustGetUserID(c)
	var req struct {
		AppName         string   `json:"app_name" binding:"required"`
		Link            string   `json:"link" binding:"required"`
		AppPhoto        string   `json:"app_photo"`
		WebView         bool     `json:"web_view"`
		Position        int      `json:"position"`
		IsGlobal        bool     `json:"is_global"`
		IsPrivate       bool     `json:"is_private"`
		Category        string   `json:"category"`
		Tags            []string `json:"tags"`
		OrganizationIDs []int64  `json:"organization_ids"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		respondJSON(c, http.StatusBadRequest, err)
		return
	}

	app, err := h.service.Create(c.Request.Context(), req.AppName, req.Link, req.AppPhoto, req.WebView, req.Position, req.IsGlobal, req.IsPrivate, req.Tags, req.Category, req.OrganizationIDs)
	if err != nil {
		h.logger.Error("Failed to create public app", zap.Error(err))
		respondJSON(c, http.StatusInternalServerError, err)
		return
	}
	respondJSON(c, http.StatusCreated, app)
}

// UpdatePublicApp godoc
// @Summary Update Public App
// @Description Update public app details
// @Tags public_apps
// @Accept json
// @Produce json
// @Param id path int true "Public App ID"
// @Param request body object{app_name=string,link=string,app_photo=string,web_view=boolean,position=integer,is_global=boolean,is_private=boolean,category=string,tags=[]string,organization_ids=[]integer} true "Request"
// @Success 200 {object} domain.PublicApp
// @Security BearerAuth
// @Router /public-apps/{id} [put]
func (h *PublicAppHandler) UpdatePublicApp(c *gin.Context) {
	middleware.MustGetUserID(c)
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "invalid public app id"})
		return
	}
	var req struct {
		AppName         *string   `json:"app_name"`
		Link            *string   `json:"link"`
		AppPhoto        *string   `json:"app_photo"`
		WebView         *bool     `json:"web_view"`
		Position        *int      `json:"position"`
		IsGlobal        *bool     `json:"is_global"`
		IsPrivate       *bool     `json:"is_private"`
		Category        *string   `json:"category"`
		Tags            *[]string `json:"tags"`
		OrganizationIDs *[]int64  `json:"organization_ids"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		respondJSON(c, http.StatusBadRequest, err)
		return
	}

	app, err := h.service.Update(c.Request.Context(), id, req.AppName, req.Link, req.AppPhoto, req.WebView, req.Position, req.IsGlobal, req.IsPrivate, req.Tags, req.Category, req.OrganizationIDs)
	if err != nil {
		h.logger.Error("Failed to update public app", zap.Error(err))
		if errors.Is(err, pgx.ErrNoRows) {
			respondJSON(c, http.StatusNotFound, gin.H{"error": "public app not found"})
			return
		}
		respondJSON(c, http.StatusInternalServerError, err)
		return
	}
	respondJSON(c, http.StatusOK, app)
}

// DeletePublicApp godoc
// @Summary Delete Public App
// @Description Delete a public app
// @Tags public_apps
// @Param id path int true "Public App ID"
// @Success 200 {string} string "ok"
// @Security BearerAuth
// @Router /public-apps/{id} [delete]
func (h *PublicAppHandler) DeletePublicApp(c *gin.Context) {
	middleware.MustGetUserID(c)
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "invalid public app id"})
		return
	}
	if err := h.service.Delete(c.Request.Context(), id); err != nil {
		h.logger.Error("Failed to delete public app", zap.Error(err))
		respondJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.Status(http.StatusOK)
}

// AddFavorite godoc
// @Summary Add app to favorites
// @Tags public_apps
// @Security BearerAuth
// @Param request body object{app_id=int} true "Request"
// @Success 200 {string} string "ok"
// @Router /public-apps/favorites [post]
func (h *PublicAppHandler) AddFavorite(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	var req struct {
		AppID int64 `json:"app_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respondJSON(c, http.StatusBadRequest, err)
		return
	}

	if err := h.service.AddFavorite(c.Request.Context(), userID, req.AppID); err != nil {
		h.logger.Error("Failed to add favorite", zap.Error(err))
		respondJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.Status(http.StatusOK)
}

// RemoveFavorite godoc
// @Summary Remove app from favorites
// @Tags public_apps
// @Security BearerAuth
// @Param app_id path int true "App ID"
// @Success 200 {string} string "ok"
// @Router /public-apps/favorites/{app_id} [delete]
func (h *PublicAppHandler) RemoveFavorite(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	appID, _ := strconv.ParseInt(c.Param("app_id"), 10, 64)

	if err := h.service.RemoveFavorite(c.Request.Context(), userID, appID); err != nil {
		h.logger.Error("Failed to remove favorite", zap.Error(err))
		respondJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.Status(http.StatusOK)
}

// GetFavorites godoc
// @Summary List favorite apps
// @Tags public_apps
// @Security BearerAuth
// @Success 200 {array} domain.PublicApp
// @Router /public-apps/favorites [get]
func (h *PublicAppHandler) GetFavorites(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	apps, err := h.service.GetFavorites(c.Request.Context(), userID)
	if err != nil {
		h.logger.Error("Failed to fetching favorites", zap.Error(err))
		respondJSON(c, http.StatusInternalServerError, err)
		return
	}
	respondJSON(c, http.StatusOK, apps)
}

// Reorder godoc
// @Summary Reorder public apps
// @Description Update the order of public apps. Use `app_ids` for full reorder OR `app_id` + `new_position` for single item move (shift).
// @Tags public_apps
// @Accept json
// @Produce json
// @Param request body object{app_ids=[]int,app_id=int,new_position=int} true "Reorder params"
// @Success 200 {string} string "ok"
// @Security BearerAuth
// @Router /public-apps/reorder [put]
func (h *PublicAppHandler) Reorder(c *gin.Context) {
	userID := middleware.MustGetUserID(c) // Ensure user is authenticated

	var req struct {
		AppIDs      []int64 `json:"app_ids"`
		AppID       *int64  `json:"app_id"`
		NewPosition *int    `json:"new_position"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		respondJSON(c, http.StatusBadRequest, err)
		return
	}

	// Handle single item move (shift)
	if req.AppID != nil && req.NewPosition != nil {
		// When using the "favorites" reorder logic, we assume the user is reordering their favorites list
		if err := h.service.UpdateFavoritePosition(c.Request.Context(), userID, *req.AppID, *req.NewPosition); err != nil {
			h.logger.Error("Failed to update user favorite app position", zap.Error(err))
			respondJSON(c, http.StatusInternalServerError, err)
			return
		}
		respondJSON(c, http.StatusOK, gin.H{"status": "ok"})
		return
	}

	// Handle full list reorder
	if len(req.AppIDs) > 0 {
		if err := h.service.ReorderFavorites(c.Request.Context(), userID, req.AppIDs); err != nil {
			h.logger.Error("Failed to reorder favorite apps", zap.Error(err))
			respondJSON(c, http.StatusInternalServerError, err)
			return
		}
		respondJSON(c, http.StatusOK, gin.H{"status": "ok"})
		return
	}

	respondJSON(c, http.StatusBadRequest, gin.H{"error": "invalid request: provide app_ids OR (app_id and new_position)"})
}

// GrantAccess godoc
// @Summary Grant access to a private app
// @Tags public_apps
// @Security BearerAuth
// @Param id path int true "App ID"
// @Param request body object{user_id=int} true "Request"
// @Success 200 {string} string "ok"
// @Router /public-apps/{id}/access [post]
func (h *PublicAppHandler) GrantAccess(c *gin.Context) {
	performerID := middleware.MustGetUserID(c)
	appID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "invalid public app id"})
		return
	}
	var req struct {
		UserID int64 `json:"user_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respondJSON(c, http.StatusBadRequest, err)
		return
	}

	if err := h.service.GrantAccess(c.Request.Context(), performerID, appID, req.UserID); err != nil {
		h.logger.Error("Failed to grant app access", zap.Error(err))
		if appErr := apperrors.GetAppError(err); appErr != nil {
			respondJSON(c, appErr.StatusCode, appErr)
			return
		}
		respondJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.Status(http.StatusOK)
}

// RevokeAccess godoc
// @Summary Revoke access to a private app
// @Tags public_apps
// @Security BearerAuth
// @Param id path int true "App ID"
// @Param user_id path int true "User ID"
// @Success 200 {string} string "ok"
// @Router /public-apps/{id}/access/{user_id} [delete]
func (h *PublicAppHandler) RevokeAccess(c *gin.Context) {
	performerID := middleware.MustGetUserID(c)
	appID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "invalid public app id"})
		return
	}
	userID, err := strconv.ParseInt(c.Param("user_id"), 10, 64)
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "invalid user id"})
		return
	}

	if err := h.service.RevokeAccess(c.Request.Context(), performerID, appID, userID); err != nil {
		h.logger.Error("Failed to revoke app access", zap.Error(err))
		if appErr := apperrors.GetAppError(err); appErr != nil {
			respondJSON(c, appErr.StatusCode, appErr)
			return
		}
		respondJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.Status(http.StatusOK)
}

// RatePublicApp godoc
// @Summary Rate a public app anonymously
// @Description Create or update the current user's anonymous 1-5 star rating for the app
// @Tags public_apps
// @Accept json
// @Produce json
// @Param id path int true "Public App ID"
// @Param request body object{rating=int} true "Rating payload"
// @Success 200 {object} domain.PublicApp
// @Security BearerAuth
// @Router /public-apps/{id}/rating [post]
func (h *PublicAppHandler) RatePublicApp(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	appID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "invalid public app id"})
		return
	}

	var req struct {
		Rating int `json:"rating" binding:"required,min=1,max=5"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respondJSON(c, http.StatusBadRequest, err)
		return
	}

	app, err := h.service.UpsertRating(c.Request.Context(), userID, appID, req.Rating)
	if err != nil {
		h.logger.Error("Failed to rate public app", zap.Error(err))
		switch {
		case errors.Is(err, service.ErrInvalidPublicAppRating):
			respondJSON(c, http.StatusBadRequest, err)
		case errors.Is(err, pgx.ErrNoRows):
			respondJSON(c, http.StatusNotFound, gin.H{"error": "public app not found"})
		default:
			respondJSON(c, http.StatusInternalServerError, gin.H{"error": "failed to rate public app"})
		}
		return
	}

	respondJSON(c, http.StatusOK, app)
}

// RemovePublicAppRating godoc
// @Summary Remove the current user's anonymous rating
// @Description Deletes the current user's anonymous rating for the app
// @Tags public_apps
// @Produce json
// @Param id path int true "Public App ID"
// @Success 200 {object} domain.PublicApp
// @Security BearerAuth
// @Router /public-apps/{id}/rating [delete]
func (h *PublicAppHandler) RemovePublicAppRating(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	appID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "invalid public app id"})
		return
	}

	app, err := h.service.RemoveRating(c.Request.Context(), userID, appID)
	if err != nil {
		h.logger.Error("Failed to remove public app rating", zap.Error(err))
		if errors.Is(err, pgx.ErrNoRows) {
			respondJSON(c, http.StatusNotFound, gin.H{"error": "public app not found"})
			return
		}
		respondJSON(c, http.StatusInternalServerError, gin.H{"error": "failed to remove public app rating"})
		return
	}

	respondJSON(c, http.StatusOK, app)
}
