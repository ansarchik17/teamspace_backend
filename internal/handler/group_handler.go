package handler

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

type GroupHandler struct {
	groupService *service.GroupService
	noteService  *service.NoteService
	logger       *zap.Logger
}

// CreateGroupEvent godoc
// @Summary Create group calendar event
// @Description Creates a calendar event inside a group and automatically adds all current group members as attendees.
// @Tags groups
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param group_id path string true "Group ID"
// @Param force query bool false "Create despite availability conflicts"
// @Param request body domain.CreateEventRequest true "Group event creation request"
// @Success 201 {object} domain.Event
// @Router /groups/{group_id}/events [post]
func (h *GroupHandler) CreateGroupEvent(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	groupID, err := uuid.Parse(c.Param("group_id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid group ID"))
		return
	}

	var req domain.CreateEventRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	force := false
	if raw := c.Query("force"); raw != "" {
		parsed, parseErr := strconv.ParseBool(raw)
		if parseErr != nil {
			h.respondError(c, apperrors.BadRequest("invalid force flag"))
			return
		}
		force = parsed
	}

	event, serviceErr := h.groupService.CreateGroupEvent(c.Request.Context(), groupID, userID, &req, force)
	if serviceErr != nil {
		h.respondError(c, apperrors.ToAppError(serviceErr))
		return
	}

	respondJSON(c, http.StatusCreated, event)
}

// GetGroupUpcomingEvents godoc
// @Summary Get upcoming group events
// @Description Returns upcoming calendar events linked to the group.
// @Tags groups
// @Security BearerAuth
// @Produce json
// @Param group_id path string true "Group ID"
// @Param limit query int false "Limit"
// @Success 200 {object} domain.GroupEventsResponse
// @Router /groups/{group_id}/events [get]
func (h *GroupHandler) GetGroupUpcomingEvents(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	groupID, err := uuid.Parse(c.Param("group_id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid group ID"))
		return
	}

	limit := 10
	if raw := c.Query("limit"); raw != "" {
		parsed, parseErr := strconv.Atoi(raw)
		if parseErr != nil || parsed <= 0 || parsed > 50 {
			h.respondError(c, apperrors.BadRequest("invalid limit"))
			return
		}
		limit = parsed
	}

	response, serviceErr := h.groupService.ListUpcomingGroupEvents(c.Request.Context(), groupID, userID, limit)
	if serviceErr != nil {
		h.respondError(c, apperrors.ToAppError(serviceErr))
		return
	}

	respondJSON(c, http.StatusOK, response)
}

func NewGroupHandler(groupService *service.GroupService, noteService *service.NoteService, logger *zap.Logger) *GroupHandler {
	return &GroupHandler{
		groupService: groupService,
		noteService:  noteService,
		logger:       logger.With(zap.String("handler", "group")),
	}
}

// CreateGroupRequest represents the request to create a group
type CreateGroupRequest struct {
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
	Type        string `json:"type" binding:"omitempty,oneof=public private"`
	MaxMembers  int    `json:"max_members"`
}

// UpdateGroupRequest represents the request to update a group
type UpdateGroupRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// AddGroupMemberRequest represents the request to add a member to a group
// AddGroupMemberRequest represents the request to add a member to a group
type AddGroupMemberRequest struct {
	UserIDs []int64 `json:"user_ids"`
	UserID  *int64  `json:"user_id"`
}

// UpdateGroupMemberRoleRequest represents the request to update a member's role
type UpdateGroupMemberRoleRequest struct {
	Role string `json:"role" binding:"required,oneof=member admin"`
}

// GroupSendMessageRequest represents the request to send a message to a group
type GroupSendMessageRequest struct {
	Content        string     `json:"content"`
	ContentHash    string     `json:"content_hash"` // Hash of the plaintext content
	Type           string     `json:"type" binding:"omitempty,oneof=text image video file audio voice video_message"`
	AttachmentURL  *string    `json:"attachment_url,omitempty"`
	AttachmentType *string    `json:"attachment_type,omitempty"`
	AttachmentSize *int64     `json:"attachment_size,omitempty"`
	AttachmentName *string    `json:"attachment_name,omitempty"`
	ReplyToID      *uuid.UUID `json:"reply_to_id,omitempty"` // ID сообщения, на которое отвечаем
	Metadata       *string    `json:"metadata,omitempty"`    // JSON-метаданные (например, mentions)
}

// CreateGroup godoc
// @Summary Create group
// @Description Create a new group
// @Tags groups
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body CreateGroupRequest true "Group creation request"
// @Success 201 {object} domain.Group
// @Router /groups [post]
func (h *GroupHandler) CreateGroup(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	var req CreateGroupRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	if req.Type == "" {
		req.Type = "private"
	}

	group, err := h.groupService.CreateGroup(c.Request.Context(), userID, req.Name, req.Description, req.Type, req.MaxMembers)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusCreated, group)
}

// GetGroup godoc
// @Summary Get group
// @Description Get group details
// @Tags groups
// @Security BearerAuth
// @Produce json
// @Param group_id path string true "Group ID"
// @Success 200 {object} domain.GroupDetails
// @Router /groups/{group_id} [get]
func (h *GroupHandler) GetGroup(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	groupIDStr := c.Param("group_id")
	groupID, err := uuid.Parse(groupIDStr)
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid group ID"))
		return
	}

	group, err := h.groupService.GetGroup(c.Request.Context(), groupID, userID)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, group)
}

// UpdateGroup godoc
// @Summary Update group
// @Description Update group details
// @Tags groups
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param group_id path string true "Group ID"
// @Param request body UpdateGroupRequest true "Update request"
// @Success 200 {object} map[string]interface{}
// @Router /groups/{group_id} [put]
func (h *GroupHandler) UpdateGroup(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	groupIDStr := c.Param("group_id")
	groupID, err := uuid.Parse(groupIDStr)
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid group ID"))
		return
	}

	var req UpdateGroupRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	if err := h.groupService.UpdateGroup(c.Request.Context(), groupID, userID, req.Name, req.Description); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "group updated successfully")})
}

// DeleteGroup godoc
// @Summary Delete group
// @Description Delete a group (only owner)
// @Tags groups
// @Security BearerAuth
// @Produce json
// @Param group_id path string true "Group ID"
// @Success 200 {object} map[string]interface{}
// @Router /groups/{group_id} [delete]
func (h *GroupHandler) DeleteGroup(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	groupIDStr := c.Param("group_id")
	groupID, err := uuid.Parse(groupIDStr)
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid group ID"))
		return
	}

	if err := h.groupService.DeleteGroup(c.Request.Context(), groupID, userID); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "group deleted successfully")})
}

// GetUserGroups godoc
// @Summary Get user groups
// @Description Get all groups for current user
// @Tags groups
// @Security BearerAuth
// @Produce json
// @Param limit query int false "Limit"
// @Param offset query int false "Offset"
// @Success 200 {array} domain.GroupView
// @Router /groups [get]
func (h *GroupHandler) GetUserGroups(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	var params struct {
		Limit  int `form:"limit" binding:"omitempty,min=1,max=100"`
		Offset int `form:"offset" binding:"omitempty,min=0"`
	}

	if err := c.ShouldBindQuery(&params); err != nil {
		h.respondError(c, apperrors.Validation("invalid query parameters"))
		return
	}

	if params.Limit == 0 {
		params.Limit = 20
	}

	groups, err := h.groupService.GetUserGroups(c.Request.Context(), userID, params.Limit, params.Offset)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{
		"groups": groups,
		"count":  len(groups),
	})
}

// AddMember godoc
// @Summary Add member to group
// @Description Add a new member to group
// @Tags groups
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param group_id path string true "Group ID"
// @Param request body AddGroupMemberRequest true "Member request"
// @Success 200 {object} map[string]interface{}
// @Router /groups/{group_id}/members [post]
func (h *GroupHandler) AddMember(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	groupIDStr := c.Param("group_id")
	groupID, err := uuid.Parse(groupIDStr)
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid group ID"))
		return
	}

	var req AddGroupMemberRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	// Collect all user IDs
	var userIDs []int64
	if req.UserID != nil {
		userIDs = append(userIDs, *req.UserID)
	}
	userIDs = append(userIDs, req.UserIDs...)

	if len(userIDs) == 0 {
		h.respondError(c, apperrors.Validation("no user IDs provided"))
		return
	}

	// Iterate and add each member
	// Ideally we should have a batch add method in service
	for _, targetUserID := range userIDs {
		if err := h.groupService.AddMember(c.Request.Context(), groupID, userID, targetUserID); err != nil {
			// If one fails, we stop? Or continue? For now let's stop and return error.
			// Or we could return partial success.
			h.respondError(c, apperrors.ToAppError(err))
			return
		}
	}

	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "members added successfully")})
}

// RemoveMember godoc
// @Summary Remove member from group
// @Description Remove a member from group
// @Tags groups
// @Security BearerAuth
// @Produce json
// @Param group_id path string true "Group ID"
// @Param user_id path int true "User ID"
// @Success 200 {object} map[string]interface{}
// @Router /groups/{group_id}/members/{user_id} [delete]
func (h *GroupHandler) RemoveMember(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	groupIDStr := c.Param("group_id")
	groupID, err := uuid.Parse(groupIDStr)
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid group ID"))
		return
	}

	var uriParams struct {
		UserID int64 `uri:"user_id" binding:"required"`
	}
	if err := c.ShouldBindUri(&uriParams); err != nil {
		h.respondError(c, apperrors.BadRequest("invalid user ID"))
		return
	}
	targetUserID := uriParams.UserID

	if err := h.groupService.RemoveMember(c.Request.Context(), groupID, userID, targetUserID); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "member removed successfully")})
}

// LeaveGroup godoc
// @Summary Leave group
// @Description Leave a group
// @Tags groups
// @Security BearerAuth
// @Produce json
// @Param group_id path string true "Group ID"
// @Success 200 {object} map[string]interface{}
// @Router /groups/{group_id}/leave [post]
func (h *GroupHandler) LeaveGroup(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	groupIDStr := c.Param("group_id")
	groupID, err := uuid.Parse(groupIDStr)
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid group ID"))
		return
	}

	if err := h.groupService.LeaveGroup(c.Request.Context(), groupID, userID); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "left group successfully")})
}

// UpdateMemberRole godoc
// @Summary Update member role
// @Description Update member role in group
// @Tags groups
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param group_id path string true "Group ID"
// @Param user_id path int true "User ID"
// @Param request body UpdateGroupMemberRoleRequest true "Role request"
// @Success 200 {object} map[string]interface{}
// @Router /groups/{group_id}/members/{user_id}/role [put]
func (h *GroupHandler) UpdateMemberRole(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	groupIDStr := c.Param("group_id")
	groupID, err := uuid.Parse(groupIDStr)
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid group ID"))
		return
	}

	var uriParams struct {
		UserID int64 `uri:"user_id" binding:"required"`
	}
	if err := c.ShouldBindUri(&uriParams); err != nil {
		h.respondError(c, apperrors.BadRequest("invalid user ID"))
		return
	}
	targetUserID := uriParams.UserID

	var req UpdateGroupMemberRoleRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	if err := h.groupService.UpdateMemberRole(c.Request.Context(), groupID, userID, targetUserID, req.Role); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "role updated successfully")})
}

// SendMessage godoc
// @Summary Send group message
// @Description Send a message in group
// @Tags groups
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param group_id path string true "Group ID"
// @Param request body GroupSendMessageRequest true "Message request"
// @Success 201 {object} domain.MessageView
// @Router /groups/{group_id}/messages [post]
func (h *GroupHandler) SendMessage(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	senderSessionKey := buildRealtimeSessionKey(c)

	groupIDStr := c.Param("group_id")
	groupID, err := uuid.Parse(groupIDStr)
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid group ID"))
		return
	}

	var req GroupSendMessageRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	attachment := &service.MessageAttachmentInput{
		URL:  req.AttachmentURL,
		Type: req.AttachmentType,
		Size: req.AttachmentSize,
		Name: req.AttachmentName,
	}

	if err := h.maybeGrantSharedNoteAccessForGroup(c.Request.Context(), req.Metadata, groupID, userID); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	message, err := h.groupService.SendMessage(c.Request.Context(), groupID, userID, req.Content, req.ContentHash, req.Type, req.Metadata, req.ReplyToID, attachment, senderSessionKey)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusCreated, message)
}

// MuteGroup godoc
// @Summary Mute or unmute group
// @Description Toggle push notifications for the current user in a group. Mentions still send notifications.
// @Tags groups
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param group_id path string true "Group ID"
// @Param request body MuteChatRequest true "Mute request"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 403 {object} map[string]interface{}
// @Router /groups/{group_id}/mute [put]
func (h *GroupHandler) MuteGroup(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	groupID, err := uuid.Parse(c.Param("group_id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid group ID"))
		return
	}

	var req MuteChatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}

	group, err := h.groupService.MuteGroup(c.Request.Context(), groupID, userID, req.Mute)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{
		"message":  apiMessage(c, "group mute setting updated"),
		"group_id": groupID.String(),
		"is_muted": req.Mute,
		"group":    group,
	})
}

// GetMessages godoc
// @Summary Get group messages
// @Description Get messages from group with pagination
// @Tags groups
// @Security BearerAuth
// @Produce json
// @Param group_id path string true "Group ID"
// @Param limit query int false "Limit" default(50)
// @Param offset query int false "Offset" default(0)
// @Success 200 {object} map[string]interface{}
// @Router /groups/{group_id}/messages [get]
func (h *GroupHandler) GetMessages(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	currentDeviceID := currentLinkedDeviceID(c)

	groupIDStr := c.Param("group_id")
	groupID, err := uuid.Parse(groupIDStr)
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid group ID"))
		return
	}

	var params struct {
		Limit  int `form:"limit" binding:"omitempty,min=1,max=100"`
		Offset int `form:"offset" binding:"omitempty,min=0"`
	}

	if err := c.ShouldBindQuery(&params); err != nil {
		h.respondError(c, apperrors.Validation("invalid query parameters"))
		return
	}

	if params.Limit == 0 {
		params.Limit = 50
	}

	messages, err := h.groupService.GetGroupMessages(c.Request.Context(), groupID, userID, currentDeviceID, params.Limit, params.Offset)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{
		"messages": messages,
		"count":    len(messages),
	})
}

// GetSharedMedia godoc
// @Summary Get group shared media
// @Description Get paginated image and video attachments from group history
// @Tags groups
// @Security BearerAuth
// @Produce json
// @Param group_id path string true "Group ID"
// @Param limit query int false "Limit" default(30)
// @Param offset query int false "Offset" default(0)
// @Success 200 {object} domain.SharedMediaResponse
// @Failure 400 {object} map[string]interface{}
// @Router /groups/{group_id}/shared-media [get]
func (h *GroupHandler) GetSharedMedia(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	groupID, err := uuid.Parse(c.Param("group_id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid group ID"))
		return
	}

	var params struct {
		Limit  int `form:"limit" binding:"omitempty,min=1,max=100"`
		Offset int `form:"offset" binding:"omitempty,min=0"`
	}

	if err := c.ShouldBindQuery(&params); err != nil {
		h.respondError(c, apperrors.Validation("invalid query parameters"))
		return
	}

	if params.Limit == 0 {
		params.Limit = 30
	}

	response, err := h.groupService.GetSharedMedia(ctx, groupID, userID, params.Limit, params.Offset)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, response)
}

// UpdateGroupAvatar godoc
// @Summary Update group avatar
// @Description Upload and update group avatar image
// @Tags groups
// @Security BearerAuth
// @Accept multipart/form-data
// @Produce json
// @Param group_id path string true "Group ID"
// @Param file formData file true "Avatar image file"
// @Success 200 {object} domain.Group
// @Router /groups/{group_id}/avatar [put]
func (h *GroupHandler) UpdateGroupAvatar(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	groupIDStr := c.Param("group_id")
	groupID, err := uuid.Parse(groupIDStr)
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid group ID"))
		return
	}

	// Get uploaded file
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		h.respondError(c, apperrors.BadRequest("file is required"))
		return
	}
	defer file.Close()

	// File size limit (e.g., 5MB)
	const maxSize = 5 * 1024 * 1024
	if header.Size > maxSize {
		h.respondError(c, apperrors.BadRequest("file too large (max 5MB)"))
		return
	}

	// Update avatar
	group, err := h.groupService.UpdateGroupAvatar(c.Request.Context(), groupID, userID, file, header)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, group)
}

// HideGroup godoc
// @Summary Hide group
// @Description Hide a group for current user (does not affect other members)
// @Tags groups
// @Security BearerAuth
// @Produce json
// @Param group_id path string true "Group ID"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 403 {object} map[string]interface{}
// @Router /groups/{group_id}/hide [delete]
func (h *GroupHandler) HideGroup(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	groupIDStr := c.Param("group_id")
	groupID, err := uuid.Parse(groupIDStr)
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid group ID"))
		return
	}

	if err := h.groupService.HideGroup(c.Request.Context(), groupID, userID); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{
		"message":  apiMessage(c, "group hidden successfully"),
		"group_id": groupID.String(),
	})
}

// UnhideGroup godoc
// @Summary Unhide group
// @Description Restore a hidden group for current user
// @Tags groups
// @Security BearerAuth
// @Produce json
// @Param group_id path string true "Group ID"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 403 {object} map[string]interface{}
// @Router /groups/{group_id}/unhide [post]
func (h *GroupHandler) UnhideGroup(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	groupIDStr := c.Param("group_id")
	groupID, err := uuid.Parse(groupIDStr)
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid group ID"))
		return
	}

	if err := h.groupService.UnhideGroup(c.Request.Context(), groupID, userID); err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	respondJSON(c, http.StatusOK, gin.H{
		"message":  apiMessage(c, "group restored successfully"),
		"group_id": groupID.String(),
	})
}

func (h *GroupHandler) respondError(c *gin.Context, err *apperrors.AppError) {
	respondAppError(c, err)
}
