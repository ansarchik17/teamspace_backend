package handler

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
)

type resolveMeetUsersRequest struct {
	UserIDs []int64 `json:"userIds" binding:"required,min=1,max=100,dive,gt=0"`
}

type meetUserProjection struct {
	ID               int64    `json:"id"`
	KeycloakID       *string  `json:"keycloak_id"`
	FirstName        string   `json:"first_name"`
	LastName         string   `json:"last_name"`
	FullName         string   `json:"full_name"`
	FullNameLocal    *string  `json:"full_name_local,omitempty"`
	Username         string   `json:"username"`
	AvatarURL        *string  `json:"avatar_url,omitempty"`
	OrganizationName string   `json:"organization_name,omitempty"`
	Roles            []string `json:"roles"`
}

// ResolveMeetUsers is a narrow compatibility contract for alem-meet-backend.
// Both the service token and the end-user bearer token are validated by router middleware.
func (h *AuthHandler) ResolveMeetUsers(c *gin.Context) {
	var request resolveMeetUsersRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		h.respondError(c, apperrors.BadRequest("invalid userIds payload"))
		return
	}

	users, err := h.authService.ResolveMeetUsers(c.Request.Context(), request.UserIDs)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	items := make([]meetUserProjection, 0, len(users))
	for _, user := range users {
		name := strings.TrimSpace(user.FullName)
		if name == "" {
			name = strings.TrimSpace(user.FirstName + " " + user.LastName)
		}
		items = append(items, meetUserProjection{
			ID: user.ID, KeycloakID: user.KeycloakID, FirstName: user.FirstName,
			LastName: user.LastName, FullName: name, FullNameLocal: user.FullNameLocal,
			Username: user.Username, AvatarURL: user.AvatarURL,
			OrganizationName: user.OrganizationName, Roles: []string{string(user.Role)},
		})
	}
	respondJSON(c, http.StatusOK, gin.H{"items": items})
}
