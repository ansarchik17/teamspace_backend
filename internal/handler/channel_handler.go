package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

// ChannelHandler handles HTTP requests for the channel module
type ChannelHandler struct {
	svc    *service.ChannelService
	logger *zap.Logger
}

// NewChannelHandler creates a new ChannelHandler
func NewChannelHandler(svc *service.ChannelService, logger *zap.Logger) *ChannelHandler {
	return &ChannelHandler{
		svc:    svc,
		logger: logger.With(zap.String("handler", "channel")),
	}
}

// ─────────────────────────── helper ───────────────────────────

func (h *ChannelHandler) respondError(c *gin.Context, err *apperrors.AppError) {
	respondAppError(c, err)
}

func (h *ChannelHandler) mapServiceErr(err error) *apperrors.AppError {
	if appErr := apperrors.GetAppError(err); appErr != nil {
		return appErr
	}
	switch err {
	case service.ErrNotChannelOwner, service.ErrNotChannelAdmin, service.ErrNotSubscribed:
		return apperrors.Forbidden(err.Error())
	case service.ErrChannelNotFound:
		return apperrors.NotFound(err.Error())
	case service.ErrChannelMemberMissing:
		return apperrors.NotFound(err.Error())
	case service.ErrUsernameTaken:
		return apperrors.Conflict(err.Error())
	case service.ErrInvalidChannelReply, service.ErrInvalidReaction:
		return apperrors.BadRequest(err.Error())
	default:
		return apperrors.Internal(err.Error())
	}
}

// ─────────────────────────── CHANNELS ───────────────────────────

// POST /api/v1/channels
func (h *ChannelHandler) CreateChannel(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	var req service.CreateChannelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation(err.Error()))
		return
	}
	ch, err := h.svc.CreateChannel(c.Request.Context(), userID, req)
	if err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}
	respondJSON(c, http.StatusCreated, ch)
}

// GET /api/v1/channels/:channel_id
func (h *ChannelHandler) GetChannel(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	channelID, err := uuid.Parse(c.Param("channel_id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid channel_id"))
		return
	}
	ch, err := h.svc.GetChannel(c.Request.Context(), channelID, userID)
	if err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}
	respondJSON(c, http.StatusOK, ch)
}

// GET /api/v1/channels
func (h *ChannelHandler) GetMyChannels(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	channels, err := h.svc.GetMyChannels(c.Request.Context(), userID)
	if err != nil {
		h.respondError(c, apperrors.Internal(err.Error()))
		return
	}
	respondJSON(c, http.StatusOK, gin.H{"channels": channels, "count": len(channels)})
}

// GET /api/v1/channels/search?q=...
func (h *ChannelHandler) SearchChannels(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	q := c.Query("q")
	if q == "" {
		h.respondError(c, apperrors.BadRequest("query parameter 'q' is required"))
		return
	}
	channels, err := h.svc.SearchChannels(c.Request.Context(), q, userID)
	if err != nil {
		h.respondError(c, apperrors.Internal(err.Error()))
		return
	}
	respondJSON(c, http.StatusOK, gin.H{"channels": channels, "count": len(channels)})
}

// GET /api/v1/channels/username/:username
func (h *ChannelHandler) GetChannelByUsername(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	username := c.Param("username")
	if username == "" {
		h.respondError(c, apperrors.BadRequest("username is required"))
		return
	}
	ch, err := h.svc.GetChannelByUsername(c.Request.Context(), username, userID)
	if err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}
	respondJSON(c, http.StatusOK, ch)
}

// PUT /api/v1/channels/:channel_id
func (h *ChannelHandler) UpdateChannel(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	channelID, err := uuid.Parse(c.Param("channel_id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid channel_id"))
		return
	}
	var req service.UpdateChannelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation(err.Error()))
		return
	}
	ch, err := h.svc.UpdateChannel(c.Request.Context(), channelID, userID, req)
	if err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}
	respondJSON(c, http.StatusOK, ch)
}

// DELETE /api/v1/channels/:channel_id
func (h *ChannelHandler) DeleteChannel(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	channelID, err := uuid.Parse(c.Param("channel_id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid channel_id"))
		return
	}
	if err := h.svc.DeleteChannel(c.Request.Context(), channelID, userID); err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}
	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "channel deleted")})
}

// PUT /api/v1/channels/:channel_id/avatar
func (h *ChannelHandler) UpdateAvatar(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	channelID, err := uuid.Parse(c.Param("channel_id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid channel_id"))
		return
	}
	var req struct {
		AvatarURL string `json:"avatar_url" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation(err.Error()))
		return
	}
	ch, err := h.svc.UpdateAvatar(c.Request.Context(), channelID, userID, req.AvatarURL)
	if err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}
	respondJSON(c, http.StatusOK, ch)
}

// ─────────────────────────── SUBSCRIPTIONS ───────────────────────────

// POST /api/v1/channels/:channel_id/subscribe
func (h *ChannelHandler) Subscribe(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	channelID, err := uuid.Parse(c.Param("channel_id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid channel_id"))
		return
	}
	ch, err := h.svc.Subscribe(c.Request.Context(), channelID, userID)
	if err != nil {
		h.respondError(c, apperrors.Internal(err.Error()))
		return
	}
	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "subscribed"), "channel": ch})
}

// DELETE /api/v1/channels/:channel_id/subscribe
func (h *ChannelHandler) Unsubscribe(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	channelID, err := uuid.Parse(c.Param("channel_id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid channel_id"))
		return
	}
	ch, err := h.svc.Unsubscribe(c.Request.Context(), channelID, userID)
	if err != nil {
		h.respondError(c, apperrors.Internal(err.Error()))
		return
	}
	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "unsubscribed"), "channel": ch})
}

// PUT /api/v1/channels/:channel_id/mute
func (h *ChannelHandler) MuteChannel(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	channelID, err := uuid.Parse(c.Param("channel_id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid channel_id"))
		return
	}
	var req service.MuteChannelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation(err.Error()))
		return
	}
	if err := h.svc.MuteChannel(c.Request.Context(), channelID, userID, req.Mute); err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}
	respondJSON(c, http.StatusOK, gin.H{"muted": req.Mute})
}

// ─────────────────────────── MEMBERS ───────────────────────────

// GET /api/v1/channels/:channel_id/members
func (h *ChannelHandler) GetAdmins(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	channelID, err := uuid.Parse(c.Param("channel_id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid channel_id"))
		return
	}
	admins, err := h.svc.GetAdmins(c.Request.Context(), channelID, userID)
	if err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}
	respondJSON(c, http.StatusOK, gin.H{"members": admins})
}

// GET /api/v1/channels/:channel_id/members
func (h *ChannelHandler) GetMembers(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	channelID, err := uuid.Parse(c.Param("channel_id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid channel_id"))
		return
	}
	roleStr := c.Query("role")
	var role *domain.ChannelRole
	if roleStr != "" {
		r := domain.ChannelRole(roleStr)
		if r != domain.ChannelRoleOwner && r != domain.ChannelRoleAdmin && r != domain.ChannelRoleSubscriber {
			h.respondError(c, apperrors.Validation("role must be owner, admin, or subscriber"))
			return
		}
		role = &r
	}
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "100"))
	if err != nil || limit < 1 || limit > 100 {
		h.respondError(c, apperrors.Validation("limit must be between 1 and 100"))
		return
	}
	offset, err := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if err != nil || offset < 0 || offset > 10000 {
		h.respondError(c, apperrors.Validation("offset must be between 0 and 10000"))
		return
	}

	members, err := h.svc.GetMembers(c.Request.Context(), channelID, userID, role, limit, offset)
	if err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}
	respondJSON(c, http.StatusOK, gin.H{"members": members, "count": len(members)})
}

// PUT /api/v1/channels/:channel_id/members/:user_id/role
func (h *ChannelHandler) UpdateMemberRole(c *gin.Context) {
	callerID := middleware.MustGetUserID(c)
	channelID, err := uuid.Parse(c.Param("channel_id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid channel_id"))
		return
	}
	targetUserID, err := strconv.ParseInt(c.Param("user_id"), 10, 64)
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid user_id"))
		return
	}
	var req struct {
		Role domain.ChannelRole `json:"role" binding:"required,oneof=admin subscriber"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation(err.Error()))
		return
	}
	if err := h.svc.UpdateMemberRole(c.Request.Context(), channelID, callerID, targetUserID, req.Role); err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}
	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "role updated")})
}

// KickMember forces a member to leave (sets left_at)
func (h *ChannelHandler) KickMember(c *gin.Context) {
	callerID := middleware.MustGetUserID(c)
	channelID, err := uuid.Parse(c.Param("channel_id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid channel_id"))
		return
	}
	targetUserID, err := strconv.ParseInt(c.Param("user_id"), 10, 64)
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid user_id"))
		return
	}
	if err := h.svc.KickMember(c.Request.Context(), channelID, callerID, targetUserID); err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}
	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "member removed")})
}

// POST /api/v1/channels/:channel_id/members
func (h *ChannelHandler) AddMembers(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	channelID, err := uuid.Parse(c.Param("channel_id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid channel_id"))
		return
	}
	var req struct {
		UserIDs []int64 `json:"user_ids" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation(err.Error()))
		return
	}
	if err := h.svc.AddMembers(c.Request.Context(), channelID, userID, req.UserIDs); err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}
	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "members added")})
}

// ─────────────────────────── POSTS ───────────────────────────

// POST /api/v1/channels/:channel_id/posts
func (h *ChannelHandler) CreatePost(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	channelID, err := uuid.Parse(c.Param("channel_id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid channel_id"))
		return
	}
	var req service.CreatePostRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation(err.Error()))
		return
	}
	req.ChannelID = channelID

	post, err := h.svc.PublishPost(c.Request.Context(), userID, req)
	if err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}
	respondJSON(c, http.StatusCreated, post)
}

// GET /api/v1/channels/:channel_id/posts
func (h *ChannelHandler) GetPosts(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	channelID, err := uuid.Parse(c.Param("channel_id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid channel_id"))
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "30"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	posts, err := h.svc.GetPosts(c.Request.Context(), channelID, userID, limit, offset)
	if err != nil {
		h.respondError(c, apperrors.Internal(err.Error()))
		return
	}
	respondJSON(c, http.StatusOK, gin.H{"posts": posts, "count": len(posts)})
}

// GET /api/v1/channels/:channel_id/posts/pinned
func (h *ChannelHandler) GetPinnedPosts(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	channelID, err := uuid.Parse(c.Param("channel_id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid channel_id"))
		return
	}
	posts, err := h.svc.GetPinnedPosts(c.Request.Context(), channelID, userID)
	if err != nil {
		h.respondError(c, apperrors.Internal(err.Error()))
		return
	}
	respondJSON(c, http.StatusOK, gin.H{"posts": posts, "count": len(posts)})
}

// GET /api/v1/channels/posts/:post_id
func (h *ChannelHandler) GetPost(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	postID, err := uuid.Parse(c.Param("post_id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid post_id"))
		return
	}
	post, err := h.svc.GetPost(c.Request.Context(), postID, userID)
	if err != nil {
		h.respondError(c, apperrors.Internal(err.Error()))
		return
	}
	respondJSON(c, http.StatusOK, post)
}

// GET /api/v1/channels/posts/:post_id/thread
func (h *ChannelHandler) GetPostThread(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	postID, err := uuid.Parse(c.Param("post_id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid post_id"))
		return
	}
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "200"))
	if err != nil || limit < 1 || limit > 200 {
		h.respondError(c, apperrors.Validation("limit must be between 1 and 200"))
		return
	}
	thread, err := h.svc.GetPostThread(c.Request.Context(), postID, userID, limit)
	if err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}
	respondJSON(c, http.StatusOK, thread)
}

// PUT /api/v1/channels/posts/:post_id
func (h *ChannelHandler) EditPost(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	postID, err := uuid.Parse(c.Param("post_id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid post_id"))
		return
	}
	var req service.UpdatePostRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation(err.Error()))
		return
	}
	post, err := h.svc.EditPost(c.Request.Context(), postID, userID, req)
	if err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}
	respondJSON(c, http.StatusOK, post)
}

// DELETE /api/v1/channels/posts/:post_id
func (h *ChannelHandler) DeletePost(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	postID, err := uuid.Parse(c.Param("post_id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid post_id"))
		return
	}
	if err := h.svc.DeletePostByID(c.Request.Context(), postID, userID); err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}
	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "post deleted")})
}

// POST /api/v1/channels/posts/:post_id/view
func (h *ChannelHandler) MarkViewed(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	postID, err := uuid.Parse(c.Param("post_id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid post_id"))
		return
	}
	_ = h.svc.MarkViewed(c.Request.Context(), postID, userID)
	respondJSON(c, http.StatusOK, gin.H{"ok": true})
}

// GET /api/v1/channels/posts/:post_id/viewers
func (h *ChannelHandler) GetPostViewers(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	postID, err := uuid.Parse(c.Param("post_id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid post_id"))
		return
	}
	viewers, err := h.svc.GetPostViewers(c.Request.Context(), postID, userID)
	if err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}
	respondJSON(c, http.StatusOK, viewers)
}

// POST /api/v1/channels/posts/:post_id/pin
func (h *ChannelHandler) PinPost(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	postID, err := uuid.Parse(c.Param("post_id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid post_id"))
		return
	}
	var req struct {
		Pinned bool `json:"pinned"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation(err.Error()))
		return
	}
	if err := h.svc.PinPost(c.Request.Context(), postID, userID, req.Pinned); err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}
	respondJSON(c, http.StatusOK, gin.H{"pinned": req.Pinned})
}

// ─────────────────────────── REACTIONS ───────────────────────────

// POST /api/v1/channels/posts/:post_id/reactions
func (h *ChannelHandler) AddReaction(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	postID, err := uuid.Parse(c.Param("post_id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid post_id"))
		return
	}
	var req struct {
		Reaction string `json:"reaction" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation(err.Error()))
		return
	}
	reactions, err := h.svc.AddReaction(c.Request.Context(), postID, userID, req.Reaction)
	if err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}
	respondJSON(c, http.StatusOK, gin.H{"reactions": reactions})
}

// DELETE /api/v1/channels/posts/:post_id/reactions/:reaction
func (h *ChannelHandler) RemoveReaction(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	postID, err := uuid.Parse(c.Param("post_id"))
	if err != nil {
		h.respondError(c, apperrors.BadRequest("invalid post_id"))
		return
	}
	reaction := c.Param("reaction")
	if reaction == "" {
		h.respondError(c, apperrors.BadRequest("reaction is required"))
		return
	}
	reactions, err := h.svc.RemoveReaction(c.Request.Context(), postID, userID, reaction)
	if err != nil {
		h.respondError(c, h.mapServiceErr(err))
		return
	}
	respondJSON(c, http.StatusOK, gin.H{"reactions": reactions})
}
