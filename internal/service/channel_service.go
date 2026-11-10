package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/i18n"
	"github.com/west6ide/workspace-backend/internal/repository"
	ws "github.com/west6ide/workspace-backend/internal/websocket"
	"go.uber.org/zap"
)

// ───── Sentinel errors ─────

var (
	ErrNotChannelOwner      = errors.New("only channel owner can do this")
	ErrNotChannelAdmin      = errors.New("only channel admins can post")
	ErrNotSubscribed        = errors.New("not subscribed to channel")
	ErrChannelNotFound      = errors.New("channel not found")
	ErrUsernameTaken        = errors.New("username already taken")
	ErrInvalidChannelReply  = errors.New("replied post must exist in the same channel")
	ErrInvalidReaction      = errors.New("reaction must contain between 1 and 32 characters")
	ErrChannelMemberMissing = errors.New("channel member not found")
)

const maxChannelReplyTreePosts = 200

// ───── Request DTOs ─────

// CreateChannelRequest holds the data needed to create a new channel
type CreateChannelRequest struct {
	Name        string             `json:"name"        binding:"required,min=1,max=255"`
	Description *string            `json:"description"`
	Username    *string            `json:"username"`
	Type        domain.ChannelType `json:"type"        binding:"required,oneof=public private"`
}

// UpdateChannelRequest holds the editable channel fields
type UpdateChannelRequest struct {
	Name        *string             `json:"name"`
	Description *string             `json:"description"`
	Username    *string             `json:"username"`
	Type        *domain.ChannelType `json:"type"`
	AvatarURL   *string             `json:"avatar_url"`
}

// CreatePostRequest holds the data for publishing a post
type CreatePostRequest struct {
	ChannelID      uuid.UUID  `json:"channel_id"`
	Content        *string    `json:"content"`
	AttachmentURL  *string    `json:"attachment_url"`
	AttachmentType *string    `json:"attachment_type"`
	AttachmentSize *int64     `json:"attachment_size"`
	AttachmentName *string    `json:"attachment_name"`
	Metadata       *string    `json:"metadata"`
	ReplyToID      *uuid.UUID `json:"reply_to_id,omitempty"`
}

// UpdatePostRequest holds editable post fields
type UpdatePostRequest struct {
	Content        *string `json:"content"`
	AttachmentURL  *string `json:"attachment_url"`
	AttachmentType *string `json:"attachment_type"`
	AttachmentSize *int64  `json:"attachment_size"`
}

// MuteChannelRequest toggles mute notification
type MuteChannelRequest struct {
	Mute bool `json:"mute"`
}

// ───── ChannelService ─────

// ChannelService implements business logic for Telegram-style channels
type ChannelService struct {
	repo                *repository.ChannelRepository
	userRepo            *repository.UserRepository
	pinnedRepo          *repository.PinnedRepository
	hub                 *ws.Hub
	notificationService *NotificationService
	logger              *zap.Logger
}

// NewChannelService creates a new ChannelService
func NewChannelService(repo *repository.ChannelRepository, userRepo *repository.UserRepository, pinnedRepo *repository.PinnedRepository, hub *ws.Hub, notificationService *NotificationService, logger *zap.Logger) *ChannelService {
	return &ChannelService{
		repo:                repo,
		userRepo:            userRepo,
		pinnedRepo:          pinnedRepo,
		hub:                 hub,
		notificationService: notificationService,
		logger:              logger.With(zap.String("service", "channel")),
	}
}

// ─────────────────────────── CHANNELS ───────────────────────────

// CreateChannel creates a channel and subscribes the creator as owner
func (s *ChannelService) CreateChannel(ctx context.Context, userID int64, req CreateChannelRequest) (*domain.ChannelView, error) {
	ch := &domain.Channel{
		ID:          uuid.New(),
		Name:        req.Name,
		Description: req.Description,
		Username:    req.Username,
		Type:        req.Type,
		CreatedBy:   userID,
		IsActive:    true,
	}

	if err := s.repo.CreateChannel(ctx, ch); err != nil {
		return nil, err
	}
	// Creator becomes owner automatically
	if err := s.repo.Subscribe(ctx, ch.ID, userID); err != nil {
		return nil, err
	}
	if err := s.repo.UpdateMemberRole(ctx, ch.ID, userID, domain.ChannelRoleOwner); err != nil {
		return nil, err
	}
	return s.repo.GetChannelByID(ctx, ch.ID, userID)
}

// GetChannel returns a channel by ID
func (s *ChannelService) GetChannel(ctx context.Context, channelID uuid.UUID, userID int64) (*domain.ChannelView, error) {
	view, err := s.repo.GetChannelByID(ctx, channelID, userID)
	if err != nil {
		return nil, ErrChannelNotFound
	}
	return view, nil
}

// GetChannelByUsername resolves a channel by @handle
func (s *ChannelService) GetChannelByUsername(ctx context.Context, username string, userID int64) (*domain.ChannelView, error) {
	view, err := s.repo.GetChannelByUsername(ctx, username, userID)
	if err != nil {
		return nil, ErrChannelNotFound
	}
	return view, nil
}

// GetMyChannels returns all channels the user is subscribed to
func (s *ChannelService) GetMyChannels(ctx context.Context, userID int64) ([]domain.ChannelView, error) {
	return s.repo.GetUserChannels(ctx, userID)
}

// SearchChannels finds channels by name / username
func (s *ChannelService) SearchChannels(ctx context.Context, query string, userID int64) ([]domain.ChannelView, error) {
	return s.repo.SearchChannels(ctx, query, userID)
}

// UpdateChannel updates channel metadata (owner/admin only)
func (s *ChannelService) UpdateChannel(ctx context.Context, channelID uuid.UUID, userID int64, req UpdateChannelRequest) (*domain.ChannelView, error) {
	if err := s.requireAdmin(ctx, channelID, userID); err != nil {
		return nil, err
	}

	ch, err := s.repo.GetChannelByID(ctx, channelID, userID)
	if err != nil {
		return nil, ErrChannelNotFound
	}

	if req.Name != nil {
		ch.Name = *req.Name
	}
	if req.Description != nil {
		ch.Description = req.Description
	}
	if req.Username != nil {
		ch.Username = req.Username
	}
	if req.Type != nil {
		ch.Type = *req.Type
	}
	if req.AvatarURL != nil {
		ch.AvatarURL = req.AvatarURL
	}

	if err := s.repo.UpdateChannel(ctx, &ch.Channel); err != nil {
		return nil, err
	}
	return s.repo.GetChannelByID(ctx, channelID, userID)
}

// DeleteChannel soft-deletes a channel (owner only)
func (s *ChannelService) DeleteChannel(ctx context.Context, channelID uuid.UUID, userID int64) error {
	if err := s.requireOwner(ctx, channelID, userID); err != nil {
		return err
	}
	return s.repo.DeleteChannel(ctx, channelID)
}

// UpdateAvatar updates channel avatar URL (owner/admin)
func (s *ChannelService) UpdateAvatar(ctx context.Context, channelID uuid.UUID, userID int64, avatarURL string) (*domain.ChannelView, error) {
	if err := s.requireAdmin(ctx, channelID, userID); err != nil {
		return nil, err
	}
	ch, err := s.repo.GetChannelByID(ctx, channelID, userID)
	if err != nil {
		return nil, ErrChannelNotFound
	}
	ch.AvatarURL = &avatarURL
	if err := s.repo.UpdateChannel(ctx, &ch.Channel); err != nil {
		return nil, err
	}
	return s.repo.GetChannelByID(ctx, channelID, userID)
}

// ─────────────────────────── SUBSCRIPTIONS ───────────────────────────

// Subscribe subscribes a user to a channel and returns the updated channel state.
func (s *ChannelService) Subscribe(ctx context.Context, channelID uuid.UUID, userID int64) (*domain.ChannelView, error) {
	if err := s.repo.Subscribe(ctx, channelID, userID); err != nil {
		return nil, err
	}
	return s.repo.GetChannelByID(ctx, channelID, userID)
}

// Unsubscribe unsubscribes a user from a channel and returns the updated channel state.
func (s *ChannelService) Unsubscribe(ctx context.Context, channelID uuid.UUID, userID int64) (*domain.ChannelView, error) {
	if err := s.repo.Unsubscribe(ctx, channelID, userID); err != nil {
		return nil, err
	}
	return s.repo.GetChannelByID(ctx, channelID, userID)
}

// MuteChannel toggles channel notifications for the user
func (s *ChannelService) MuteChannel(ctx context.Context, channelID uuid.UUID, userID int64, mute bool) error {
	member, err := s.repo.GetMember(ctx, channelID, userID)
	if err != nil || member == nil || member.LeftAt != nil {
		return ErrNotSubscribed
	}
	return s.repo.MuteChannel(ctx, channelID, userID, mute)
}

// GetAdmins returns admins / owner of the channel
func (s *ChannelService) GetAdmins(ctx context.Context, channelID uuid.UUID, userID int64) ([]domain.ChannelMember, error) {
	member, err := s.repo.GetMember(ctx, channelID, userID)
	if err != nil || member == nil || member.LeftAt != nil {
		return nil, ErrNotSubscribed
	}
	return s.repo.GetAdmins(ctx, channelID)
}

// GetMembers returns all subscribers / admins for a channel
func (s *ChannelService) GetMembers(ctx context.Context, channelID uuid.UUID, userID int64, role *domain.ChannelRole, limit, offset int) ([]domain.ChannelMember, error) {
	if err := s.requireAdmin(ctx, channelID, userID); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 100
	}
	return s.repo.GetMembers(ctx, channelID, role, limit, offset)
}

// UpdateMemberRole changes a member's role (owner only)
func (s *ChannelService) UpdateMemberRole(ctx context.Context, channelID uuid.UUID, callerID, targetUserID int64, role domain.ChannelRole) error {
	if err := s.requireOwner(ctx, channelID, callerID); err != nil {
		return err
	}
	target, err := s.repo.GetMember(ctx, channelID, targetUserID)
	if err != nil || target == nil || target.LeftAt != nil {
		return ErrChannelMemberMissing
	}
	if err := validateChannelRoleChange(callerID, targetUserID, target.Role); err != nil {
		return err
	}
	return s.repo.UpdateMemberRole(ctx, channelID, targetUserID, role)
}

// KickMember removes a member (owner/admin only)
func (s *ChannelService) KickMember(ctx context.Context, channelID uuid.UUID, callerID, targetUserID int64) error {
	if err := s.requireAdmin(ctx, channelID, callerID); err != nil {
		return err
	}
	caller, err := s.repo.GetMember(ctx, channelID, callerID)
	if err != nil || caller == nil || caller.LeftAt != nil {
		return ErrNotSubscribed
	}
	target, err := s.repo.GetMember(ctx, channelID, targetUserID)
	if err != nil || target == nil || target.LeftAt != nil {
		return ErrChannelMemberMissing
	}
	if err := validateChannelMemberRemoval(callerID, targetUserID, caller.Role, target.Role); err != nil {
		return err
	}
	return s.repo.KickMember(ctx, channelID, targetUserID)
}

// AddMembers adds multiple users to a channel (owner/admin only)
func (s *ChannelService) AddMembers(ctx context.Context, channelID uuid.UUID, callerID int64, userIDs []int64) error {
	if err := s.requireAdmin(ctx, channelID, callerID); err != nil {
		return err
	}
	for _, id := range userIDs {
		if err := s.repo.Subscribe(ctx, channelID, id); err != nil {
			s.logger.Error("Failed to add member to channel", zap.Int64("user_id", id), zap.Error(err))
			continue
		}
	}
	return nil
}

// ─────────────────────────── POSTS ───────────────────────────

// PublishPost creates a post and broadcasts it to all subscribers (owner/admin only)
func (s *ChannelService) PublishPost(ctx context.Context, userID int64, req CreatePostRequest) (*domain.ChannelPostView, error) {
	member, err := s.repo.GetMember(ctx, req.ChannelID, userID)
	if err != nil || member == nil || member.LeftAt != nil {
		return nil, ErrNotSubscribed
	}
	if req.ReplyToID == nil && member.Role == domain.ChannelRoleSubscriber {
		return nil, ErrNotChannelAdmin
	}
	if req.ReplyToID != nil {
		parent, err := s.repo.GetPost(ctx, *req.ReplyToID, userID)
		if err != nil {
			return nil, ErrInvalidChannelReply
		}
		if parent.ChannelID != req.ChannelID {
			return nil, ErrInvalidChannelReply
		}
	}

	post := &domain.ChannelPost{
		ID:             uuid.New(),
		ChannelID:      req.ChannelID,
		AuthorID:       userID,
		ReplyToID:      req.ReplyToID,
		Content:        req.Content,
		AttachmentURL:  req.AttachmentURL,
		AttachmentType: req.AttachmentType,
		AttachmentSize: req.AttachmentSize,
		AttachmentName: req.AttachmentName,
		Metadata:       req.Metadata,
	}
	if err := s.repo.CreatePost(ctx, post); err != nil {
		return nil, err
	}

	view, err := s.repo.GetPost(ctx, post.ID, userID)
	if err != nil {
		return nil, err
	}

	mentionedUserIDs := mentionedUserIDsFromMetadata(ctx, s.userRepo, req.Metadata)
	memberMuteMap, memberErr := s.repo.GetActiveMemberMuteMap(ctx, req.ChannelID)
	if memberErr != nil {
		s.logger.Warn("Failed to load channel members for mentions", zap.Error(memberErr))
		memberMuteMap = map[int64]bool{}
	}
	mentionIDs := make([]int64, 0, len(mentionedUserIDs))
	for mentionID := range mentionedUserIDs {
		if _, activeMember := memberMuteMap[mentionID]; activeMember && mentionID != userID {
			mentionIDs = append(mentionIDs, mentionID)
		} else {
			delete(mentionedUserIDs, mentionID)
		}
	}
	if err := s.repo.CreatePostMentions(ctx, post.ID, mentionIDs); err != nil {
		s.logger.Error("Failed to save channel post mentions", zap.Error(err), zap.String("post_id", post.ID.String()))
	}

	for _, mentionID := range mentionIDs {
		if s.notificationService == nil {
			continue
		}
		notificationLocale := s.notificationService.recipientLocale(ctx, mentionID)
		body := i18n.SystemNotification(notificationLocale, "Вас упомянули в публикации канала")
		if req.Content != nil {
			trimmed := strings.TrimSpace(*req.Content)
			if trimmed != "" && len([]rune(trimmed)) <= 100 {
				body = trimmed
			}
		}
		s.notificationService.SendChatPushOnly(ctx, mentionID, i18n.SystemNotification(notificationLocale, "Упоминание в канале"), body, map[string]string{
			"kind":       "channel_mention",
			"channel_id": req.ChannelID.String(),
			"post_id":    post.ID.String(),
			"author_id":  fmt.Sprintf("%d", userID),
			"is_mention": "true",
		})
	}

	// Broadcast a personalized event so mentioned subscribers receive
	// is_mention=true while all other subscribers receive the same post normally.
	go s.broadcastNewChannelPost(req.ChannelID, view, mentionedUserIDs)

	return view, nil
}

// GetPost returns a single post
func (s *ChannelService) GetPost(ctx context.Context, postID uuid.UUID, userID int64) (*domain.ChannelPostView, error) {
	return s.repo.GetPost(ctx, postID, userID)
}

// GetPosts returns paginated channel posts
func (s *ChannelService) GetPosts(ctx context.Context, channelID uuid.UUID, userID int64, limit, offset int) ([]domain.ChannelPostView, error) {
	if limit == 0 {
		limit = 30
	}
	return s.repo.GetPosts(ctx, channelID, userID, limit, offset)
}

// GetPinnedPosts returns all pinned posts for a channel
func (s *ChannelService) GetPinnedPosts(ctx context.Context, channelID uuid.UUID, userID int64) ([]domain.ChannelPostView, error) {
	return s.repo.GetPinnedPosts(ctx, channelID, userID)
}

func (s *ChannelService) GetPostThread(ctx context.Context, rootID uuid.UUID, userID int64, limit int) (*domain.ChannelPostThreadResponse, error) {
	root, err := s.repo.GetPost(ctx, rootID, userID)
	if err != nil {
		return nil, ErrChannelNotFound
	}
	if limit <= 0 || limit > maxChannelReplyTreePosts {
		limit = maxChannelReplyTreePosts
	}
	descendants, truncated, err := s.repo.GetPostReplyDescendants(ctx, rootID, userID, limit)
	if err != nil {
		return nil, err
	}
	return buildChannelPostThread(root, descendants, truncated), nil
}

func buildChannelPostThread(root *domain.ChannelPostView, descendants []domain.ChannelPostView, truncated bool) *domain.ChannelPostThreadResponse {
	sort.SliceStable(descendants, func(i, j int) bool {
		if descendants[i].CreatedAt.Equal(descendants[j].CreatedAt) {
			return descendants[i].ID.String() < descendants[j].ID.String()
		}
		return descendants[i].CreatedAt.Before(descendants[j].CreatedAt)
	})

	nodes := make(map[uuid.UUID]*domain.ChannelPostReplyNode, len(descendants))
	for i := range descendants {
		post := &descendants[i]
		if post.ChannelID != root.ChannelID {
			continue
		}
		nodes[post.ID] = &domain.ChannelPostReplyNode{Post: post, Replies: make([]*domain.ChannelPostReplyNode, 0)}
	}
	topLevel := make([]*domain.ChannelPostReplyNode, 0)
	for i := range descendants {
		post := &descendants[i]
		node := nodes[post.ID]
		if node == nil || post.ReplyToID == nil {
			continue
		}
		if *post.ReplyToID == root.ID {
			topLevel = append(topLevel, node)
		} else if parent := nodes[*post.ReplyToID]; parent != nil {
			parent.Replies = append(parent.Replies, node)
		}
	}
	return &domain.ChannelPostThreadResponse{
		Root: root, Replies: topLevel, ReplyCount: len(nodes), Truncated: truncated,
	}
}

// EditPost updates a post. Authors have the regular edit window; channel
// owner/admin moderation is not time-limited.
func (s *ChannelService) EditPost(ctx context.Context, postID uuid.UUID, userID int64, req UpdatePostRequest) (*domain.ChannelPostView, error) {
	post, err := s.repo.GetPost(ctx, postID, userID)
	if err != nil {
		return nil, err
	}
	member, err := s.repo.GetMember(ctx, post.ChannelID, userID)
	if err != nil || member == nil || member.LeftAt != nil {
		return nil, ErrNotSubscribed
	}
	if !canAdminChannel(member.Role) {
		if post.AuthorID != userID {
			return nil, apperrors.CannotEditMessage()
		}
		if !isWithinModerationWindow(post.CreatedAt, time.Now(), MessageEditWindow) {
			return nil, apperrors.EditWindowExpired()
		}
	}

	updated := post.ChannelPost
	if req.Content != nil {
		updated.Content = req.Content
	}
	if req.AttachmentURL != nil {
		updated.AttachmentURL = req.AttachmentURL
	}
	if req.AttachmentType != nil {
		updated.AttachmentType = req.AttachmentType
	}
	if req.AttachmentSize != nil {
		updated.AttachmentSize = req.AttachmentSize
	}

	if err := s.repo.UpdatePost(ctx, &updated); err != nil {
		return nil, err
	}

	view, err := s.repo.GetPost(ctx, postID, userID)
	if err != nil {
		return nil, err
	}

	subscriberIDs, _ := s.repo.GetChannelSubscriberIDs(ctx, post.ChannelID)
	for _, uid := range subscriberIDs {
		if s.hub.IsUserOnline(uid) {
			s.hub.SendToUser(uid, &ws.Message{
				Type:           "message_edited",
				ConversationID: &post.ChannelID,
				MessageID:      &postID,
				Content:        view,
			})
		}
	}
	return view, nil
}

// DeletePost soft-deletes a post for everyone. Authors have the regular
// delete window; channel owner/admin moderation is not time-limited.
func (s *ChannelService) DeletePostByID(ctx context.Context, postID uuid.UUID, userID int64) error {
	post, err := s.repo.GetPost(ctx, postID, userID)
	if err != nil {
		return err
	}
	member, err := s.repo.GetMember(ctx, post.ChannelID, userID)
	if err != nil || member == nil || member.LeftAt != nil {
		return ErrNotSubscribed
	}
	if !canAdminChannel(member.Role) {
		if post.AuthorID != userID {
			return apperrors.CannotDeleteMessage()
		}
		if !isWithinModerationWindow(post.CreatedAt, time.Now(), MessageDeleteForEveryoneWindow) {
			return apperrors.DeleteWindowExpired()
		}
	}
	if err := s.repo.DeletePost(ctx, postID); err != nil {
		return err
	}
	if err := s.pinnedRepo.UnpinMessage(ctx, postID); err != nil {
		s.logger.Warn("failed to unpin deleted channel post", zap.String("post_id", postID.String()), zap.Error(err))
	}
	subscriberIDs, _ := s.repo.GetChannelSubscriberIDs(ctx, post.ChannelID)
	for _, uid := range subscriberIDs {
		if s.hub.IsUserOnline(uid) {
			s.hub.SendToUser(uid, &ws.Message{
				Type:           "message_deleted",
				ConversationID: &post.ChannelID,
				MessageID:      &postID,
				Content: map[string]interface{}{
					"is_for_everyone": true,
				},
			})
		}
	}
	return nil
}

// PinPost pins/unpins a post (owner/admin only)
func (s *ChannelService) PinPost(ctx context.Context, postID uuid.UUID, userID int64, pinned bool) error {
	post, err := s.repo.GetPost(ctx, postID, userID)
	if err != nil {
		return err
	}
	if err := s.requireAdmin(ctx, post.ChannelID, userID); err != nil {
		return err
	}
	if err := s.repo.PinPost(ctx, postID, pinned); err != nil {
		return err
	}

	if pinned {
		if err := s.pinnedRepo.PinMessage(ctx, post.ChannelID, "channel", postID, userID); err != nil {
			return fmt.Errorf("add to pinned_messages: %w", err)
		}
	} else {
		if err := s.pinnedRepo.UnpinMessage(ctx, postID); err != nil {
			return fmt.Errorf("remove from pinned_messages: %w", err)
		}
	}

	go s.broadcastToSubscribers(post.ChannelID, "channel_post_pinned", map[string]interface{}{
		"channel_id": post.ChannelID.String(),
		"post_id":    postID.String(),
		"is_pinned":  pinned,
	})
	return nil
}

// ─────────────────────────── VIEWS ───────────────────────────

// MarkViewed records that a user viewed a post
func (s *ChannelService) MarkViewed(ctx context.Context, postID uuid.UUID, userID int64) error {
	return s.repo.MarkPostViewed(ctx, postID, userID)
}

// GetPostViewers returns channel post viewers for channel admins.
func (s *ChannelService) GetPostViewers(ctx context.Context, postID uuid.UUID, userID int64) ([]domain.MessageViewer, error) {
	post, err := s.repo.GetPost(ctx, postID, userID)
	if err != nil {
		return nil, ErrChannelNotFound
	}
	if err := s.requireAdmin(ctx, post.ChannelID, userID); err != nil {
		return nil, err
	}
	return s.repo.GetPostViewers(ctx, postID)
}

// ─────────────────────────── REACTIONS ───────────────────────────

// AddReaction adds a reaction to a post
func (s *ChannelService) AddReaction(ctx context.Context, postID uuid.UUID, userID int64, reaction string) ([]domain.ReactionSummary, error) {
	reaction = strings.TrimSpace(reaction)
	if reaction == "" || len([]rune(reaction)) > 32 {
		return nil, ErrInvalidReaction
	}
	if _, err := s.repo.GetPost(ctx, postID, userID); err != nil {
		return nil, ErrChannelNotFound
	}
	if err := s.repo.AddReaction(ctx, postID, userID, reaction); err != nil {
		return nil, err
	}

	reactions, err := s.repo.GetReactions(ctx, postID, userID)
	if err != nil {
		return nil, err
	}

	post, err := s.repo.GetPost(ctx, postID, userID)
	if err == nil {
		s.notifyPostReactionUpdate(ctx, post.ChannelID, postID)
	}

	return reactions, nil
}

// RemoveReaction removes a reaction from a post
func (s *ChannelService) RemoveReaction(ctx context.Context, postID uuid.UUID, userID int64, reaction string) ([]domain.ReactionSummary, error) {
	reaction = strings.TrimSpace(reaction)
	if reaction == "" || len([]rune(reaction)) > 32 {
		return nil, ErrInvalidReaction
	}
	if _, err := s.repo.GetPost(ctx, postID, userID); err != nil {
		return nil, ErrChannelNotFound
	}
	if err := s.repo.RemoveReaction(ctx, postID, userID, reaction); err != nil {
		return nil, err
	}

	reactions, err := s.repo.GetReactions(ctx, postID, userID)
	if err != nil {
		return nil, err
	}

	post, err := s.repo.GetPost(ctx, postID, userID)
	if err == nil {
		s.notifyPostReactionUpdate(ctx, post.ChannelID, postID)
	}

	return reactions, nil
}

func (s *ChannelService) notifyPostReactionUpdate(ctx context.Context, channelID, postID uuid.UUID) {
	// Fetch reactions without specific user context (userID=0)
	reactions, err := s.repo.GetReactions(ctx, postID, 0)
	if err != nil {
		s.logger.Error("Failed to fetch reactions for broadcast", zap.Error(err))
		return
	}

	subscriberIDs, err := s.repo.GetChannelSubscriberIDs(ctx, channelID)
	if err != nil {
		s.logger.Error("Failed to get subscribers for reaction broadcast", zap.Error(err))
		return
	}

	for _, uid := range subscriberIDs {
		if !s.hub.IsUserOnline(uid) {
			continue
		}

		// Customize Reacted flag for each user
		userReactions := make([]domain.ReactionSummary, len(reactions))
		for i, r := range reactions {
			userReactions[i] = r
			userReactions[i].Reacted = false
			for _, u := range r.Users {
				if u.ID == uid {
					userReactions[i].Reacted = true
					break
				}
			}
		}

		msg := &ws.Message{
			Type:           "message_reactions_updated",
			ConversationID: &channelID,
			MessageID:      &postID,
			Content: map[string]interface{}{
				"message_id": postID.String(),
				"reactions":  userReactions,
			},
			Timestamp: time.Now().Unix(),
		}
		s.hub.SendToUser(uid, msg)
	}
}

// ─────────────────────────── HELPERS ───────────────────────────

// canPostToChannel reports whether the given role may publish posts.
// Subscribers are read-only; owner and admin can post.
func canPostToChannel(role domain.ChannelRole) bool {
	return role == domain.ChannelRoleOwner || role == domain.ChannelRoleAdmin
}

// canAdminChannel reports whether the given role has admin-level access.
func canAdminChannel(role domain.ChannelRole) bool {
	return role == domain.ChannelRoleOwner || role == domain.ChannelRoleAdmin
}

func validateChannelRoleChange(actorID, targetID int64, targetRole domain.ChannelRole) error {
	if actorID == targetID || targetRole == domain.ChannelRoleOwner {
		return apperrors.Forbidden("channel owner role cannot be changed")
	}
	return nil
}

func validateChannelMemberRemoval(actorID, targetID int64, actorRole, targetRole domain.ChannelRole) error {
	if actorID == targetID {
		return apperrors.Forbidden("use the unsubscribe endpoint to remove yourself")
	}
	if targetRole == domain.ChannelRoleOwner {
		return apperrors.Forbidden("cannot remove channel owner")
	}
	if actorRole == domain.ChannelRoleAdmin && targetRole == domain.ChannelRoleAdmin {
		return apperrors.Forbidden("only owner can remove another admin")
	}
	return nil
}

func (s *ChannelService) requireOwner(ctx context.Context, channelID uuid.UUID, userID int64) error {
	m, err := s.repo.GetMember(ctx, channelID, userID)
	if err != nil || m == nil || m.LeftAt != nil {
		return ErrNotSubscribed
	}
	if m.Role != domain.ChannelRoleOwner {
		return ErrNotChannelOwner
	}
	return nil
}

func (s *ChannelService) requireAdmin(ctx context.Context, channelID uuid.UUID, userID int64) error {
	m, err := s.repo.GetMember(ctx, channelID, userID)
	if err != nil || m == nil || m.LeftAt != nil {
		return ErrNotSubscribed
	}
	if m.Role == domain.ChannelRoleSubscriber {
		return ErrNotChannelAdmin
	}
	return nil
}

func (s *ChannelService) broadcastToSubscribers(channelID uuid.UUID, eventType string, payload interface{}) {
	ctx := context.Background()
	ids, err := s.repo.GetChannelSubscriberIDs(ctx, channelID)
	if err != nil {
		s.logger.Error("Failed to get subscribers for broadcast", zap.Error(err))
		return
	}

	for _, id := range ids {
		content := map[string]interface{}{
			"channel_id": channelID.String(),
			"payload":    payload,
		}
		if eventType == ws.EventNewChannelPost {
			if view, viewErr := s.repo.GetChannelByID(ctx, channelID, id); viewErr == nil && view != nil {
				content["unread_count"] = view.UnreadCount
				content["channel"] = view
			}
		}

		msg := &ws.Message{
			Type:    eventType,
			To:      id,
			Content: content,
		}
		s.hub.SendToUser(id, msg)
	}
}

func (s *ChannelService) broadcastNewChannelPost(channelID uuid.UUID, post *domain.ChannelPostView, mentioned map[int64]bool) {
	ctx := context.Background()
	ids, err := s.repo.GetChannelSubscriberIDs(ctx, channelID)
	if err != nil {
		s.logger.Error("Failed to get subscribers for channel post broadcast", zap.Error(err))
		return
	}
	for _, userID := range ids {
		content := map[string]interface{}{
			"channel_id": channelID.String(),
			"payload":    post,
		}
		if view, viewErr := s.repo.GetChannelByID(ctx, channelID, userID); viewErr == nil && view != nil {
			content["unread_count"] = view.UnreadCount
			content["channel"] = view
		}
		s.hub.SendToUser(userID, &ws.Message{
			Type:           ws.EventNewChannelPost,
			To:             userID,
			ConversationID: &channelID,
			MessageID:      &post.ID,
			Content:        content,
			IsMention:      mentioned[userID],
			Timestamp:      time.Now().Unix(),
		})
	}
}
