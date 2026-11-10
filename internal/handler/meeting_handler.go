package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/repository"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

type MeetingHandler struct {
	meetingService *service.MeetingService
	userRepo       *repository.UserRepository
	logger         *zap.Logger
}

func NewMeetingHandler(meetingService *service.MeetingService, userRepo *repository.UserRepository, logger *zap.Logger) *MeetingHandler {
	return &MeetingHandler{
		meetingService: meetingService,
		userRepo:       userRepo,
		logger:         logger.With(zap.String("handler", "meeting")),
	}
}

type CreateMeetingRequest struct {
	RoomName string `json:"room_name" binding:"required"`
}

type MeetingResponse struct {
	Link string `json:"link"`
}

// CreateMeetingLink generates a Jitsi meeting link
// @Summary Generate Jitsi link
// @Description Generate a secure Jitsi meeting link with JWT
// @Tags meetings
// @Accept json
// @Produce json
// @Param request body CreateMeetingRequest true "Meeting request"
// @Success 200 {object} MeetingResponse
// @Security BearerAuth
// @Router /meetings/link [post]
func (h *MeetingHandler) CreateMeetingLink(c *gin.Context) {
	var req CreateMeetingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(apperrors.BadRequest("invalid request body"))
		return
	}

	userID, exists := c.Get("user_id")
	if !exists {
		_ = c.Error(apperrors.Unauthorized("user not found in context"))
		return
	}

	// Fetch user details
	// Fetch user details
	user, err := h.userRepo.GetByID(c.Request.Context(), userID.(int64))
	if err != nil {
		h.logger.Error("Failed to get user", zap.Error(err))
		_ = c.Error(apperrors.Internal("failed to get user details"))
		return
	}

	isAdmin := false
	// Logic for admin can be extended here. For now default to false.

	fullName := user.FirstName + " " + user.LastName
	if user.FirstName == "" && user.LastName == "" {
		fullName = user.Username
	}

	avatar := ""
	if user.AvatarURL != nil {
		avatar = *user.AvatarURL
	}

	link, err := h.meetingService.GenerateMeetingLink(req.RoomName, fullName, user.Email, avatar, isAdmin)
	if err != nil {
		_ = c.Error(apperrors.Internal("failed to generate meeting link"))
		return
	}

	respondJSON(c, http.StatusOK, MeetingResponse{
		Link: link,
	})
}
