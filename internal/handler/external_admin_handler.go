package handler

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/auth"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"go.uber.org/zap"
)

type ExternalAdminHandler struct {
	authService *auth.Service
	logger      *zap.Logger
}

type ExternalAdminResetPasswordRequest struct {
	Password  string `json:"password" binding:"required,min=8"`
	LogoutAll *bool  `json:"logout_all,omitempty"`
}

type ExternalAdminUsersListResponse struct {
	Users  []*domain.UserResponse `json:"users"`
	Count  int                    `json:"count"`
	Limit  int                    `json:"limit"`
	Offset int                    `json:"offset"`
}

type ExternalAdminActionResponse struct {
	Message   string `json:"message"`
	UserID    int64  `json:"user_id"`
	LogoutAll *bool  `json:"logout_all,omitempty"`
}

func NewExternalAdminHandler(authService *auth.Service, logger *zap.Logger) *ExternalAdminHandler {
	return &ExternalAdminHandler{
		authService: authService,
		logger:      logger.With(zap.String("handler", "external_admin")),
	}
}

// ListUsers godoc
// @Summary List external-admin users
// @Description Returns users visible to the external admin within the allowed scope.
// @Tags external-admin
// @Security BearerAuth
// @Produce json
// @Param q query string false "Search query"
// @Param limit query int false "Limit" minimum(1) maximum(100)
// @Param offset query int false "Offset" minimum(0)
// @Success 200 {object} handler.ExternalAdminUsersListResponse
// @Router /external-admin/users [get]
func (h *ExternalAdminHandler) ListUsers(c *gin.Context) {
	requesterID := middleware.MustGetUserID(c)

	var params struct {
		Query  string `form:"q"`
		Limit  int    `form:"limit" binding:"omitempty,min=1,max=100"`
		Offset int    `form:"offset" binding:"omitempty,min=0"`
	}
	if err := c.ShouldBindQuery(&params); err != nil {
		h.respondError(c, apperrors.Validation("invalid query parameters"))
		return
	}
	if params.Limit == 0 {
		params.Limit = 20
	}

	users, err := h.authService.ListExternalAdminUsers(c.Request.Context(), requesterID, strings.TrimSpace(params.Query), params.Limit, params.Offset)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, ExternalAdminUsersListResponse{
		Users:  users,
		Count:  len(users),
		Limit:  params.Limit,
		Offset: params.Offset,
	})
}

// GetUser godoc
// @Summary Get external-admin user
// @Description Returns full details for a user if the external admin can access that user by scope.
// @Tags external-admin
// @Security BearerAuth
// @Produce json
// @Param user_id path int true "User ID"
// @Success 200 {object} domain.UserDetailResponse
// @Router /external-admin/users/{user_id} [get]
func (h *ExternalAdminHandler) GetUser(c *gin.Context) {
	requesterID := middleware.MustGetUserID(c)
	targetID, ok := parseExternalAdminUserID(c, "user_id", h.respondError)
	if !ok {
		return
	}

	if _, _, err := h.authService.EnsureExternalAdminUserInScope(c.Request.Context(), requesterID, targetID); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	resp, err := h.authService.GetUserFullDetails(c.Request.Context(), targetID, requesterID)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, resp)
}

// CreateUser godoc
// @Summary Create user from external admin
// @Description Creates a new scoped user through the external admin surface. Employees in an organization must have a team_id belonging to that organization.
// @Tags external-admin
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body auth.CreateUserRequest true "Create user request"
// @Success 201 {object} domain.UserResponse
// @Router /external-admin/users [post]
func (h *ExternalAdminHandler) CreateUser(c *gin.Context) {
	requesterID := middleware.MustGetUserID(c)

	var req auth.CreateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation(err.Error()))
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	req.Username = strings.TrimSpace(req.Username)
	req.FirstName = strings.TrimSpace(req.FirstName)
	req.LastName = strings.TrimSpace(req.LastName)
	req.IIN = strings.TrimSpace(req.IIN)

	if err := h.authService.ValidateExternalAdminUserCreate(c.Request.Context(), requesterID, &req); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	user, err := h.authService.CreateUser(c.Request.Context(), 0, &req)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusCreated, user)
}

// UpdateUser godoc
// @Summary Update user profile from external admin
// @Description Updates allowed profile fields for a scoped user. Password and role changes are not allowed here.
// @Tags external-admin
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param user_id path int true "User ID"
// @Param request body auth.UpdateUserRequest true "Update user request"
// @Success 200 {object} domain.UserResponse
// @Router /external-admin/users/{user_id} [patch]
func (h *ExternalAdminHandler) UpdateUser(c *gin.Context) {
	requesterID := middleware.MustGetUserID(c)
	targetID, ok := parseExternalAdminUserID(c, "user_id", h.respondError)
	if !ok {
		return
	}

	if _, _, err := h.authService.EnsureExternalAdminUserInScope(c.Request.Context(), requesterID, targetID); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	var req auth.UpdateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation(err.Error()))
		return
	}
	if req.Password != nil || req.Role != nil || req.CanViewFullDriveActivity != nil {
		h.respondError(c, apperrors.Forbidden("profile update does not allow password, role or drive access changes"))
		return
	}
	if req.Email != nil {
		lowerEmail := strings.ToLower(strings.TrimSpace(*req.Email))
		req.Email = &lowerEmail
	}

	user, updateErr := h.authService.UpdateUserAdmin(c.Request.Context(), 0, targetID, &req)
	if updateErr != nil {
		h.respondError(c, apperrors.ToAppError(updateErr))
		return
	}
	respondJSON(c, http.StatusOK, user)
}

// ResetPassword godoc
// @Summary Reset scoped user password
// @Description Resets the password for a scoped user and optionally logs out all active sessions.
// @Tags external-admin
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param user_id path int true "User ID"
// @Param request body handler.ExternalAdminResetPasswordRequest true "Reset password request"
// @Success 200 {object} handler.ExternalAdminActionResponse
// @Router /external-admin/users/{user_id}/reset-password [post]
func (h *ExternalAdminHandler) ResetPassword(c *gin.Context) {
	requesterID := middleware.MustGetUserID(c)
	targetID, ok := parseExternalAdminUserID(c, "user_id", h.respondError)
	if !ok {
		return
	}

	var req ExternalAdminResetPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation(err.Error()))
		return
	}

	logoutAll := true
	if req.LogoutAll != nil {
		logoutAll = *req.LogoutAll
	}
	if err := h.authService.ResetUserPasswordExternalAdmin(c.Request.Context(), requesterID, targetID, req.Password, logoutAll); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, ExternalAdminActionResponse{
		Message:   apiMessage(c, "password reset successfully"),
		UserID:    targetID,
		LogoutAll: &logoutAll,
	})
}

// LogoutAllSessions godoc
// @Summary Logout all scoped user sessions
// @Description Terminates all active Keycloak sessions for a scoped user.
// @Tags external-admin
// @Security BearerAuth
// @Produce json
// @Param user_id path int true "User ID"
// @Success 200 {object} handler.ExternalAdminActionResponse
// @Router /external-admin/users/{user_id}/logout-all [post]
func (h *ExternalAdminHandler) LogoutAllSessions(c *gin.Context) {
	requesterID := middleware.MustGetUserID(c)
	targetID, ok := parseExternalAdminUserID(c, "user_id", h.respondError)
	if !ok {
		return
	}

	if _, _, err := h.authService.EnsureExternalAdminUserInScope(c.Request.Context(), requesterID, targetID); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	if err := h.authService.LogoutAll(c.Request.Context(), targetID); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, ExternalAdminActionResponse{
		Message: apiMessage(c, "sessions terminated"),
		UserID:  targetID,
	})
}

func (h *ExternalAdminHandler) respondError(c *gin.Context, err *apperrors.AppError) {
	respondAppError(c, err)
}

func parseExternalAdminUserID(c *gin.Context, key string, respond func(*gin.Context, *apperrors.AppError)) (int64, bool) {
	value, err := strconv.ParseInt(c.Param(key), 10, 64)
	if err != nil {
		respond(c, apperrors.BadRequest("invalid user id"))
		return 0, false
	}
	return value, true
}
