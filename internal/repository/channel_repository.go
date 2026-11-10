package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/fileurl"
	"go.uber.org/zap"
)

// ChannelRepository provides access to channel storage
type ChannelRepository struct {
	db     *pgxpool.Pool
	logger *zap.Logger
}

// NewChannelRepository creates a new ChannelRepository
func NewChannelRepository(db *pgxpool.Pool, logger *zap.Logger) *ChannelRepository {
	return &ChannelRepository{
		db:     db,
		logger: logger.With(zap.String("repository", "channel")),
	}
}

// ─────────────────────────── CHANNELS ───────────────────────────

// CreateChannel inserts a new channel into the DB
func (r *ChannelRepository) CreateChannel(ctx context.Context, ch *domain.Channel) error {
	if ch.ID == uuid.Nil {
		ch.ID = uuid.New()
	}
	now := time.Now()
	ch.CreatedAt = now
	ch.UpdatedAt = now

	query := `
		INSERT INTO channels (id, name, description, username, avatar_url, type, created_by, is_active, member_count, created_at, updated_at)
		VALUES              ($1, $2,   $3,          $4,       $5,         $6,   $7,         $8,        0,            $9,         $10)
	`
	_, err := r.db.Exec(ctx, query,
		ch.ID, ch.Name, ch.Description, ch.Username, ch.AvatarURL,
		ch.Type, ch.CreatedBy, ch.IsActive, ch.CreatedAt, ch.UpdatedAt,
	)
	if err != nil {
		r.logger.Error("Failed to create channel", zap.Error(err))
		return fmt.Errorf("create channel: %w", err)
	}
	return nil
}

// GetChannelByID returns a channel enriched with current user's context
func (r *ChannelRepository) GetChannelByID(ctx context.Context, id uuid.UUID, userID int64) (*domain.ChannelView, error) {
	return r.getChannelView(ctx, "c.id = $1", id, userID)
}

// GetChannelByUsername resolves a channel by its @handle (case-insensitive)
func (r *ChannelRepository) GetChannelByUsername(ctx context.Context, username string, userID int64) (*domain.ChannelView, error) {
	return r.getChannelView(ctx, "LOWER(ltrim(c.username, '@')) = LOWER($1)", normalizeChannelUsername(username), userID)
}

func (r *ChannelRepository) getChannelView(ctx context.Context, where string, arg interface{}, userID int64) (*domain.ChannelView, error) {
	query := fmt.Sprintf(`
		SELECT
			c.id, c.name, c.description, c.username, c.avatar_url, c.type,
			c.created_by, c.is_active, c.member_count, c.created_at, c.updated_at, c.deleted_at,
			CASE WHEN cm.left_at IS NULL THEN cm.role END AS role,
			cm.user_id IS NOT NULL AND cm.left_at IS NULL AS is_subscribed,
			COALESCE(cm.left_at IS NULL AND cm.is_muted, FALSE) AS is_muted,
			CASE
				WHEN cm.user_id IS NOT NULL AND cm.left_at IS NULL THEN (
					SELECT COUNT(*)
					FROM channel_posts cp_unread
					WHERE cp_unread.channel_id = c.id
					  AND cp_unread.is_deleted = FALSE
					  AND cp_unread.author_id <> $2
					  AND cp_unread.created_at >= cm.joined_at
					  AND NOT EXISTS (
						SELECT 1
						FROM channel_post_views v
						WHERE v.post_id = cp_unread.id AND v.user_id = $2
					  )
				)
				ELSE 0
			END AS unread_count,
			lp.id, lp.channel_id, lp.author_id, lp.content, lp.attachment_url, lp.attachment_type, lp.created_at
		FROM channels c
		LEFT JOIN channel_members cm ON cm.channel_id = c.id AND cm.user_id = $2
		LEFT JOIN LATERAL (
			SELECT id, channel_id, author_id, content, attachment_url, attachment_type, created_at
			FROM channel_posts
			WHERE channel_id = c.id AND is_deleted = FALSE
			ORDER BY created_at DESC LIMIT 1
		) lp ON TRUE
		WHERE %s AND c.deleted_at IS NULL
	`, where)

	view := &domain.ChannelView{}
	var role *string
	var (
		lpID             *uuid.UUID
		lpChannelID      *uuid.UUID
		lpAuthorID       *int64
		lpContent        *string
		lpAttachmentURL  *string
		lpAttachmentType *string
		lpCreatedAt      *time.Time
	)

	err := r.db.QueryRow(ctx, query, arg, userID).Scan(
		&view.ID, &view.Name, &view.Description, &view.Username, &view.AvatarURL, &view.Type,
		&view.CreatedBy, &view.IsActive, &view.MemberCount, &view.CreatedAt, &view.UpdatedAt, &view.DeletedAt,
		&role, &view.IsSubscribed, &view.IsMuted, &view.UnreadCount,
		&lpID, &lpChannelID, &lpAuthorID, &lpContent, &lpAttachmentURL, &lpAttachmentType, &lpCreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("channel not found")
		}
		return nil, fmt.Errorf("get channel view: %w", err)
	}

	if lpID != nil {
		view.LastPost = &domain.ChannelPost{
			ID:             *lpID,
			ChannelID:      *lpChannelID,
			AuthorID:       *lpAuthorID,
			Content:        lpContent,
			AttachmentURL:  lpAttachmentURL,
			AttachmentType: lpAttachmentType,
			CreatedAt:      *lpCreatedAt,
		}
	}
	view.AvatarURL = fileurl.SignLegacyDownloadURL(view.AvatarURL)
	if view.LastPost != nil {
		view.LastPost.AttachmentURL = fileurl.SignLegacyDownloadURL(view.LastPost.AttachmentURL)
	}

	if role != nil {
		cr := domain.ChannelRole(*role)
		view.MyRole = &cr
	}
	return view, nil
}

// UpdateChannel updates mutable channel fields
func (r *ChannelRepository) UpdateChannel(ctx context.Context, ch *domain.Channel) error {
	ch.UpdatedAt = time.Now()
	query := `
		UPDATE channels
		SET name=$1, description=$2, username=$3, avatar_url=$4, type=$5, is_active=$6, updated_at=$7
		WHERE id=$8 AND deleted_at IS NULL
	`
	res, err := r.db.Exec(ctx, query,
		ch.Name, ch.Description, ch.Username, ch.AvatarURL, ch.Type, ch.IsActive, ch.UpdatedAt, ch.ID,
	)
	if err != nil {
		return fmt.Errorf("update channel: %w", err)
	}
	if res.RowsAffected() == 0 {
		return fmt.Errorf("channel not found")
	}
	return nil
}

// DeleteChannel soft-deletes a channel
func (r *ChannelRepository) DeleteChannel(ctx context.Context, id uuid.UUID) error {
	query := `UPDATE channels SET deleted_at=$1, is_active=FALSE WHERE id=$2 AND deleted_at IS NULL`
	res, err := r.db.Exec(ctx, query, time.Now(), id)
	if err != nil {
		return fmt.Errorf("delete channel: %w", err)
	}
	if res.RowsAffected() == 0 {
		return fmt.Errorf("channel not found")
	}
	return nil
}

// GetUserChannels returns all channels the user is subscribed to
func (r *ChannelRepository) GetUserChannels(ctx context.Context, userID int64) ([]domain.ChannelView, error) {
	query := `
		SELECT
			c.id, c.name, c.description, c.username, c.avatar_url, c.type,
			c.created_by, c.is_active, c.member_count, c.created_at, c.updated_at, c.deleted_at,
			cm.role,
			TRUE                 AS is_subscribed,
			cm.is_muted          AS is_muted,
			(
				SELECT COUNT(*)
				FROM channel_posts cp_unread
				WHERE cp_unread.channel_id = c.id
				  AND cp_unread.is_deleted = FALSE
				  AND cp_unread.author_id <> $1
				  AND cp_unread.created_at >= cm.joined_at
				  AND NOT EXISTS (
					SELECT 1
					FROM channel_post_views v
					WHERE v.post_id = cp_unread.id AND v.user_id = $1
				  )
			)                    AS unread_count,
			lp.id, lp.channel_id, lp.author_id, lp.content, lp.attachment_url, lp.attachment_type, lp.created_at
		FROM channels c
		JOIN channel_members cm ON cm.channel_id = c.id AND cm.user_id = $1 AND cm.left_at IS NULL
		LEFT JOIN LATERAL (
			SELECT id, channel_id, author_id, content, attachment_url, attachment_type, created_at
			FROM channel_posts
			WHERE channel_id = c.id AND is_deleted = FALSE
			ORDER BY created_at DESC LIMIT 1
		) lp ON TRUE
		WHERE c.deleted_at IS NULL AND c.is_active = TRUE
		ORDER BY c.updated_at DESC
	`
	return r.scanChannelViews(ctx, query, userID)
}

// SearchChannels finds channels by name or username
func (r *ChannelRepository) SearchChannels(ctx context.Context, q string, userID int64) ([]domain.ChannelView, error) {
	query := `
		SELECT
			c.id, c.name, c.description, c.username, c.avatar_url, c.type,
			c.created_by, c.is_active, c.member_count, c.created_at, c.updated_at, c.deleted_at,
			CASE WHEN cm.left_at IS NULL THEN cm.role END AS role,
			cm.user_id IS NOT NULL AND cm.left_at IS NULL AS is_subscribed,
			COALESCE(cm.left_at IS NULL AND cm.is_muted, FALSE) AS is_muted,
			CASE
				WHEN cm.user_id IS NOT NULL AND cm.left_at IS NULL THEN (
					SELECT COUNT(*)
					FROM channel_posts cp_unread
					WHERE cp_unread.channel_id = c.id
					  AND cp_unread.is_deleted = FALSE
					  AND cp_unread.author_id <> $2
					  AND cp_unread.created_at >= cm.joined_at
					  AND NOT EXISTS (
						SELECT 1
						FROM channel_post_views v
						WHERE v.post_id = cp_unread.id AND v.user_id = $2
					  )
				)
				ELSE 0
			END AS unread_count,
			lp.id, lp.channel_id, lp.author_id, lp.content, lp.attachment_url, lp.attachment_type, lp.created_at
		FROM channels c
		LEFT JOIN channel_members cm ON cm.channel_id = c.id AND cm.user_id = $2
		LEFT JOIN LATERAL (
			SELECT id, channel_id, author_id, content, attachment_url, attachment_type, created_at
			FROM channel_posts
			WHERE channel_id = c.id AND is_deleted = FALSE
			ORDER BY created_at DESC LIMIT 1
		) lp ON TRUE
		WHERE c.deleted_at IS NULL
		  AND c.is_active = TRUE
		  AND c.type = 'public'
		  AND (c.name ILIKE '%' || $1 || '%' OR c.username ILIKE '%' || $1 || '%')
		ORDER BY c.member_count DESC
		LIMIT 50
	`
	return r.scanChannelViews(ctx, query, q, userID)
}

func normalizeChannelUsername(username string) string {
	username = strings.TrimSpace(username)
	username = strings.Trim(username, "/")
	username = strings.TrimPrefix(username, "@")
	return username
}

func (r *ChannelRepository) scanChannelViews(ctx context.Context, query string, args ...interface{}) ([]domain.ChannelView, error) {
	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query channels: %w", err)
	}
	defer rows.Close()

	var result []domain.ChannelView
	for rows.Next() {
		view := domain.ChannelView{}
		var role *string
		var (
			lpID             *uuid.UUID
			lpChannelID      *uuid.UUID
			lpAuthorID       *int64
			lpContent        *string
			lpAttachmentURL  *string
			lpAttachmentType *string
			lpCreatedAt      *time.Time
		)
		if err := rows.Scan(
			&view.ID, &view.Name, &view.Description, &view.Username, &view.AvatarURL, &view.Type,
			&view.CreatedBy, &view.IsActive, &view.MemberCount, &view.CreatedAt, &view.UpdatedAt, &view.DeletedAt,
			&role, &view.IsSubscribed, &view.IsMuted, &view.UnreadCount,
			&lpID, &lpChannelID, &lpAuthorID, &lpContent, &lpAttachmentURL, &lpAttachmentType, &lpCreatedAt,
		); err != nil {
			r.logger.Error("Failed to scan channel view", zap.Error(err))
			continue
		}

		if lpID != nil {
			view.LastPost = &domain.ChannelPost{
				ID:             *lpID,
				ChannelID:      *lpChannelID,
				AuthorID:       *lpAuthorID,
				Content:        lpContent,
				AttachmentURL:  lpAttachmentURL,
				AttachmentType: lpAttachmentType,
				CreatedAt:      *lpCreatedAt,
			}
		}
		view.AvatarURL = fileurl.SignLegacyDownloadURL(view.AvatarURL)
		if view.LastPost != nil {
			view.LastPost.AttachmentURL = fileurl.SignLegacyDownloadURL(view.LastPost.AttachmentURL)
		}

		if role != nil {
			cr := domain.ChannelRole(*role)
			view.MyRole = &cr
		}
		result = append(result, view)
	}
	return result, nil
}

// ─────────────────────────── MEMBERS ───────────────────────────

// Subscribe adds a user to a channel (or re-activates a previous subscription)
func (r *ChannelRepository) Subscribe(ctx context.Context, channelID uuid.UUID, userID int64) error {
	query := `
		INSERT INTO channel_members (channel_id, user_id, role, joined_at, created_at)
		VALUES ($1, $2, 'subscriber', NOW(), NOW())
		ON CONFLICT (channel_id, user_id)
		DO UPDATE SET left_at = NULL, joined_at = NOW()
	`
	_, err := r.db.Exec(ctx, query, channelID, userID)
	if err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}
	return nil
}

// Unsubscribe marks the member as having left the channel
func (r *ChannelRepository) Unsubscribe(ctx context.Context, channelID uuid.UUID, userID int64) error {
	query := `UPDATE channel_members SET left_at = NOW() WHERE channel_id=$1 AND user_id=$2 AND left_at IS NULL`
	_, err := r.db.Exec(ctx, query, channelID, userID)
	if err != nil {
		return fmt.Errorf("unsubscribe: %w", err)
	}
	return nil
}

// GetMembers returns paginated list of channel members (enriched with user details)
func (r *ChannelRepository) GetMembers(ctx context.Context, channelID uuid.UUID, role *domain.ChannelRole, limit, offset int) ([]domain.ChannelMember, error) {
	query := `
		SELECT 
			cm.channel_id, cm.user_id, cm.role, cm.is_muted, cm.joined_at, cm.left_at, cm.created_at,
			u.first_name || ' ' || u.last_name AS user_full_name,
			u.username                        AS user_username,
			u.avatar_url                       AS user_avatar
		FROM channel_members cm
		JOIN users u ON u.id = cm.user_id
		WHERE cm.channel_id = $1 AND cm.left_at IS NULL
	`
	args := []interface{}{channelID}
	limitArg := 2

	if role != nil {
		query += fmt.Sprintf(" AND cm.role = $%d", limitArg)
		args = append(args, *role)
		limitArg++
	}

	query += fmt.Sprintf(" ORDER BY cm.joined_at DESC LIMIT $%d OFFSET $%d", limitArg, limitArg+1)
	args = append(args, limit, offset)

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("get members: %w", err)
	}
	defer rows.Close()

	var result []domain.ChannelMember
	for rows.Next() {
		m := domain.ChannelMember{}
		if err := rows.Scan(
			&m.ChannelID, &m.UserID, &m.Role, &m.IsMuted, &m.JoinedAt, &m.LeftAt, &m.CreatedAt,
			&m.UserFullName, &m.UserUsername, &m.UserAvatar,
		); err != nil {
			continue
		}
		m.UserAvatar = fileurl.SignLegacyDownloadURL(m.UserAvatar)
		result = append(result, m)
	}
	return result, nil
}

// GetMember returns membership record or nil if not a member
func (r *ChannelRepository) GetMember(ctx context.Context, channelID uuid.UUID, userID int64) (*domain.ChannelMember, error) {
	query := `
		SELECT channel_id, user_id, role, is_muted, joined_at, left_at, created_at
		FROM channel_members
		WHERE channel_id=$1 AND user_id=$2
	`
	m := &domain.ChannelMember{}
	err := r.db.QueryRow(ctx, query, channelID, userID).Scan(
		&m.ChannelID, &m.UserID, &m.Role, &m.IsMuted, &m.JoinedAt, &m.LeftAt, &m.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get member: %w", err)
	}
	return m, nil
}

// IsSubscribed returns true if user is currently subscribed to channel
func (r *ChannelRepository) IsSubscribed(ctx context.Context, channelID uuid.UUID, userID int64) (bool, error) {
	query := `SELECT EXISTS(SELECT 1 FROM channel_members WHERE channel_id=$1 AND user_id=$2 AND left_at IS NULL)`
	var exists bool
	err := r.db.QueryRow(ctx, query, channelID, userID).Scan(&exists)
	return exists, err
}

// GetMemberRole returns user's role in channel or error if not a member
func (r *ChannelRepository) GetMemberRole(ctx context.Context, channelID uuid.UUID, userID int64) (string, error) {
	query := `SELECT role FROM channel_members WHERE channel_id=$1 AND user_id=$2 AND left_at IS NULL`
	var role string
	err := r.db.QueryRow(ctx, query, channelID, userID).Scan(&role)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("not a member")
		}
		return "", err
	}
	return role, nil
}

// GetAdmins returns all admins and the owner of a channel
func (r *ChannelRepository) GetAdmins(ctx context.Context, channelID uuid.UUID) ([]domain.ChannelMember, error) {
	query := `
		SELECT 
			cm.channel_id, cm.user_id, cm.role, cm.is_muted, cm.joined_at, cm.left_at, cm.created_at,
			u.first_name || ' ' || u.last_name AS user_full_name,
			u.username                        AS user_username,
			u.avatar_url                       AS user_avatar
		FROM channel_members cm
		JOIN users u ON u.id = cm.user_id
		WHERE cm.channel_id = $1 AND cm.role IN ('owner','admin') AND cm.left_at IS NULL
		ORDER BY CASE WHEN cm.role = 'owner' THEN 1 ELSE 2 END, cm.joined_at
	`
	rows, err := r.db.Query(ctx, query, channelID)
	if err != nil {
		return nil, fmt.Errorf("get admins: %w", err)
	}
	defer rows.Close()

	var result []domain.ChannelMember
	for rows.Next() {
		m := domain.ChannelMember{}
		if err := rows.Scan(
			&m.ChannelID, &m.UserID, &m.Role, &m.IsMuted, &m.JoinedAt, &m.LeftAt, &m.CreatedAt,
			&m.UserFullName, &m.UserUsername, &m.UserAvatar,
		); err != nil {
			continue
		}
		m.UserAvatar = fileurl.SignLegacyDownloadURL(m.UserAvatar)
		result = append(result, m)
	}
	return result, nil
}

// GetChannelSubscriberIDs returns user IDs of all active subscribers
func (r *ChannelRepository) GetChannelSubscriberIDs(ctx context.Context, channelID uuid.UUID) ([]int64, error) {
	query := `SELECT user_id FROM channel_members WHERE channel_id=$1 AND left_at IS NULL`
	rows, err := r.db.Query(ctx, query, channelID)
	if err != nil {
		return nil, fmt.Errorf("get subscriber ids: %w", err)
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			continue
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// GetActiveMemberMuteMap returns active channel subscribers and their mute state.
func (r *ChannelRepository) GetActiveMemberMuteMap(ctx context.Context, channelID uuid.UUID) (map[int64]bool, error) {
	rows, err := r.db.Query(ctx, `
		SELECT user_id, is_muted
		FROM channel_members
		WHERE channel_id = $1 AND left_at IS NULL
	`, channelID)
	if err != nil {
		return nil, fmt.Errorf("get channel member mute map: %w", err)
	}
	defer rows.Close()

	result := make(map[int64]bool)
	for rows.Next() {
		var userID int64
		var muted bool
		if err := rows.Scan(&userID, &muted); err != nil {
			return nil, fmt.Errorf("scan channel member mute state: %w", err)
		}
		result[userID] = muted
	}
	return result, rows.Err()
}

// UpdateMemberRole changes a member's role
func (r *ChannelRepository) UpdateMemberRole(ctx context.Context, channelID uuid.UUID, userID int64, role domain.ChannelRole) error {
	query := `UPDATE channel_members SET role=$1 WHERE channel_id=$2 AND user_id=$3 AND left_at IS NULL`
	result, err := r.db.Exec(ctx, query, role, channelID, userID)
	if err != nil {
		return fmt.Errorf("update member role: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("channel member not found")
	}
	return nil
}

// MuteChannel toggles mute for a subscriber
func (r *ChannelRepository) MuteChannel(ctx context.Context, channelID uuid.UUID, userID int64, mute bool) error {
	query := `UPDATE channel_members SET is_muted=$1 WHERE channel_id=$2 AND user_id=$3 AND left_at IS NULL`
	result, err := r.db.Exec(ctx, query, mute, channelID, userID)
	if err != nil {
		return fmt.Errorf("mute channel: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("channel member not found")
	}
	return nil
}

// KickMember forces a member to leave (sets left_at)
func (r *ChannelRepository) KickMember(ctx context.Context, channelID uuid.UUID, userID int64) error {
	query := `UPDATE channel_members SET left_at=NOW() WHERE channel_id=$1 AND user_id=$2 AND left_at IS NULL`
	result, err := r.db.Exec(ctx, query, channelID, userID)
	if err != nil {
		return fmt.Errorf("kick member: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("channel member not found")
	}
	return nil
}

// ─────────────────────────── POSTS ───────────────────────────

// CreatePost inserts a new channel post
func (r *ChannelRepository) CreatePost(ctx context.Context, post *domain.ChannelPost) error {
	if post.ID == uuid.Nil {
		post.ID = uuid.New()
	}
	now := time.Now()
	post.CreatedAt = now
	post.UpdatedAt = now

	query := `
		INSERT INTO channel_posts
		  (id, channel_id, author_id, reply_to_id, content, attachment_url, attachment_type, attachment_size, attachment_name,
		   is_pinned, is_deleted, is_edited, is_forwarded, original_channel_id, original_post_id,
		   views_count, comments_count, linked_group_id, metadata, created_at, updated_at)
		VALUES
		  ($1,$2,$3,$4,$5,$6,$7,$8,$9, FALSE,FALSE,FALSE,FALSE,$10,$11, 0,0,$12,$13,$14,$15)
	`
	_, err := r.db.Exec(ctx, query,
		post.ID, post.ChannelID, post.AuthorID, post.ReplyToID, post.Content,
		post.AttachmentURL, post.AttachmentType, post.AttachmentSize, post.AttachmentName,
		post.OriginalChannelID, post.OriginalPostID,
		post.LinkedGroupID, post.Metadata,
		post.CreatedAt, post.UpdatedAt,
	)
	if err != nil {
		r.logger.Error("Failed to create post", zap.Error(err))
		return fmt.Errorf("create post: %w", err)
	}
	if _, err := r.db.Exec(ctx, `UPDATE channels SET updated_at = $1 WHERE id = $2`, post.CreatedAt, post.ChannelID); err != nil {
		r.logger.Warn("Failed to touch channel after post creation", zap.Error(err), zap.String("channel_id", post.ChannelID.String()))
	}
	return nil
}

// GetPost returns a single enriched post view
func (r *ChannelRepository) GetPost(ctx context.Context, postID uuid.UUID, userID int64) (*domain.ChannelPostView, error) {
	views, err := r.getPosts(ctx, "cp.id = $1", postID, userID, 1, 0)
	if err != nil {
		return nil, err
	}
	if len(views) == 0 {
		return nil, fmt.Errorf("post not found")
	}
	return &views[0], nil
}

// GetPosts returns paginated posts for a channel
func (r *ChannelRepository) GetPosts(ctx context.Context, channelID uuid.UUID, userID int64, limit, offset int) ([]domain.ChannelPostView, error) {
	return r.getPosts(ctx, "cp.channel_id = $1", channelID, userID, limit, offset)
}

// GetPinnedPosts returns pinned posts for a channel
func (r *ChannelRepository) GetPinnedPosts(ctx context.Context, channelID uuid.UUID, userID int64) ([]domain.ChannelPostView, error) {
	return r.getPosts(ctx, "cp.channel_id = $1 AND cp.is_pinned = TRUE", channelID, userID, 100, 0)
}

func (r *ChannelRepository) getPosts(ctx context.Context, where string, filterArg interface{}, userID int64, limit, offset int) ([]domain.ChannelPostView, error) {
	query := fmt.Sprintf(`
		SELECT
			cp.id, cp.channel_id, cp.author_id, cp.reply_to_id, cp.content,
			cp.attachment_url, cp.attachment_type, cp.attachment_size, cp.attachment_name,
			cp.is_pinned, cp.is_deleted, cp.is_edited, cp.is_forwarded,
			cp.original_channel_id, cp.original_post_id,
			cp.views_count, cp.comments_count, cp.linked_group_id,
			cp.metadata, cp.edited_at, cp.created_at, cp.updated_at, cp.deleted_at,
			TRIM(COALESCE(u.first_name, '') || ' ' || COALESCE(u.last_name, '')) AS author_name,
			u.avatar_url                        AS author_avatar,
			c.name                              AS channel_name,
			c.avatar_url                        AS channel_avatar,
			EXISTS(
				SELECT 1 FROM channel_post_views v
				WHERE v.post_id = cp.id AND v.user_id = $2
			) AS is_viewed
		FROM channel_posts cp
		JOIN users    u ON u.id = cp.author_id
		JOIN channels c ON c.id = cp.channel_id
		WHERE %s
		  AND cp.is_deleted = FALSE
		  AND (
			c.type = 'public'
			OR EXISTS (
				SELECT 1
				FROM channel_members cm
				WHERE cm.channel_id = cp.channel_id
				  AND cm.user_id = $2
				  AND cm.left_at IS NULL
			)
		  )
		ORDER BY cp.created_at DESC
		LIMIT $3 OFFSET $4
	`, where)

	rows, err := r.db.Query(ctx, query, filterArg, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("get posts: %w", err)
	}
	defer rows.Close()

	var result []domain.ChannelPostView
	for rows.Next() {
		v := domain.ChannelPostView{}
		if err := rows.Scan(
			&v.ID, &v.ChannelID, &v.AuthorID, &v.ReplyToID, &v.Content,
			&v.AttachmentURL, &v.AttachmentType, &v.AttachmentSize, &v.AttachmentName,
			&v.IsPinned, &v.IsDeleted, &v.IsEdited, &v.IsForwarded,
			&v.OriginalChannelID, &v.OriginalPostID,
			&v.ViewsCount, &v.CommentsCount, &v.LinkedGroupID,
			&v.Metadata, &v.EditedAt, &v.CreatedAt, &v.UpdatedAt, &v.DeletedAt,
			&v.AuthorName, &v.AuthorAvatar,
			&v.ChannelName, &v.ChannelAvatar,
			&v.IsViewed,
		); err != nil {
			r.logger.Error("Failed to scan post view", zap.Error(err))
			continue
		}
		v.AttachmentURL = fileurl.SignLegacyDownloadURL(v.AttachmentURL)
		v.AuthorAvatar = fileurl.SignLegacyDownloadURL(v.AuthorAvatar)
		v.ChannelAvatar = fileurl.SignLegacyDownloadURL(v.ChannelAvatar)

		// fetch reactions inline
		v.Reactions, _ = r.GetReactions(ctx, v.ID, userID)
		for i := range v.Reactions {
			for j := range v.Reactions[i].Users {
				v.Reactions[i].Users[j].Avatar = fileurl.SignLegacyDownloadURL(v.Reactions[i].Users[j].Avatar)
			}
		}
		result = append(result, v)
	}
	return result, nil
}

// GetPostReplyDescendants returns all visible nested replies below rootID.
func (r *ChannelRepository) GetPostReplyDescendants(ctx context.Context, rootID uuid.UUID, userID int64, limit int) ([]domain.ChannelPostView, bool, error) {
	if limit <= 0 {
		return make([]domain.ChannelPostView, 0), false, nil
	}
	rows, err := r.db.Query(ctx, `
		WITH RECURSIVE reply_tree AS (
			SELECT cp.id, cp.channel_id, cp.reply_to_id, cp.created_at, 1 AS depth, ARRAY[cp.id] AS path
			FROM channel_posts cp
			WHERE cp.reply_to_id = $1 AND cp.is_deleted = false

			UNION ALL

			SELECT child.id, child.channel_id, child.reply_to_id, child.created_at, parent.depth + 1, parent.path || child.id
			FROM channel_posts child
			JOIN reply_tree parent ON child.reply_to_id = parent.id AND child.channel_id = parent.channel_id
			WHERE child.is_deleted = false
			  AND NOT child.id = ANY(parent.path)
		)
		SELECT id
		FROM reply_tree
		ORDER BY depth ASC, created_at ASC, id ASC
		LIMIT $2
	`, rootID, limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("get channel reply ids: %w", err)
	}
	defer rows.Close()

	ids := make([]uuid.UUID, 0, limit+1)
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, false, fmt.Errorf("scan channel reply id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate channel reply ids: %w", err)
	}

	truncated := len(ids) > limit
	if truncated {
		ids = ids[:limit]
	}
	if len(ids) == 0 {
		return make([]domain.ChannelPostView, 0), truncated, nil
	}
	posts, err := r.getPosts(ctx, "cp.id = ANY($1)", ids, userID, len(ids), 0)
	if err != nil {
		return nil, false, err
	}
	return posts, truncated, nil
}

func (r *ChannelRepository) CreatePostMentions(ctx context.Context, postID uuid.UUID, userIDs []int64) error {
	if len(userIDs) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, userID := range userIDs {
		batch.Queue(`
			INSERT INTO channel_post_mentions (post_id, user_id)
			VALUES ($1, $2)
			ON CONFLICT DO NOTHING
		`, postID, userID)
	}
	results := r.db.SendBatch(ctx, batch)
	defer results.Close()
	for range userIDs {
		if _, err := results.Exec(); err != nil {
			return fmt.Errorf("create channel post mention: %w", err)
		}
	}
	return nil
}

// UpdatePost updates mutable post fields
func (r *ChannelRepository) UpdatePost(ctx context.Context, post *domain.ChannelPost) error {
	now := time.Now()
	post.UpdatedAt = now
	post.EditedAt = &now
	post.IsEdited = true

	query := `
		UPDATE channel_posts
		SET content=$1, attachment_url=$2, attachment_type=$3, attachment_size=$4,
		    is_edited=TRUE, edited_at=$5, updated_at=$6
		WHERE id=$7 AND is_deleted=FALSE
	`
	res, err := r.db.Exec(ctx, query,
		post.Content, post.AttachmentURL, post.AttachmentType, post.AttachmentSize,
		post.EditedAt, post.UpdatedAt, post.ID,
	)
	if err != nil {
		return fmt.Errorf("update post: %w", err)
	}
	if res.RowsAffected() == 0 {
		return fmt.Errorf("post not found")
	}
	return nil
}

// DeletePost soft-deletes a post
func (r *ChannelRepository) DeletePost(ctx context.Context, postID uuid.UUID) error {
	query := `UPDATE channel_posts SET is_deleted=TRUE, deleted_at=NOW(), updated_at=NOW() WHERE id=$1`
	_, err := r.db.Exec(ctx, query, postID)
	return err
}

// PinPost sets/unsets pin flag for a post
func (r *ChannelRepository) PinPost(ctx context.Context, postID uuid.UUID, pinned bool) error {
	query := `UPDATE channel_posts SET is_pinned=$1, updated_at=NOW() WHERE id=$2`
	_, err := r.db.Exec(ctx, query, pinned, postID)
	return err
}

// ─────────────────────────── VIEWS ───────────────────────────

// MarkPostViewed atomically records a view and increments the counter once per user
func (r *ChannelRepository) MarkPostViewed(ctx context.Context, postID uuid.UUID, userID int64) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// idempotent insert
	insertViewed := `
		INSERT INTO channel_post_views (post_id, user_id)
		VALUES ($1, $2)
		ON CONFLICT DO NOTHING
	`
	tag, err := tx.Exec(ctx, insertViewed, postID, userID)
	if err != nil {
		return fmt.Errorf("mark viewed insert: %w", err)
	}

	// only count new view
	if tag.RowsAffected() > 0 {
		if _, err := tx.Exec(ctx,
			`UPDATE channel_posts SET views_count = views_count + 1 WHERE id = $1`,
			postID,
		); err != nil {
			return fmt.Errorf("mark viewed update: %w", err)
		}
	}

	return tx.Commit(ctx)
}

// GetPostViewers returns users who viewed a channel post.
func (r *ChannelRepository) GetPostViewers(ctx context.Context, postID uuid.UUID) ([]domain.MessageViewer, error) {
	query := `
		SELECT
			v.user_id,
			TRIM(COALESCE(u.first_name, '') || ' ' || COALESCE(u.last_name, '')) AS full_name,
			u.avatar_url,
			v.viewed_at
		FROM channel_post_views v
		JOIN users u ON u.id = v.user_id
		WHERE v.post_id = $1
		ORDER BY v.viewed_at DESC
	`

	rows, err := r.db.Query(ctx, query, postID)
	if err != nil {
		return nil, fmt.Errorf("get post viewers: %w", err)
	}
	defer rows.Close()

	viewers := make([]domain.MessageViewer, 0)
	for rows.Next() {
		var v domain.MessageViewer
		if err := rows.Scan(&v.UserID, &v.FullName, &v.AvatarURL, &v.ReadAt); err != nil {
			r.logger.Error("Failed to scan channel post viewer", zap.Error(err))
			continue
		}
		v.AvatarURL = fileurl.SignLegacyDownloadURL(v.AvatarURL)
		viewers = append(viewers, v)
	}

	return viewers, nil
}

// ─────────────────────────── REACTIONS ───────────────────────────

// AddReaction inserts a reaction (idempotent)
func (r *ChannelRepository) AddReaction(ctx context.Context, postID uuid.UUID, userID int64, reaction string) error {
	query := `
		INSERT INTO channel_post_reactions (post_id, user_id, reaction)
		VALUES ($1, $2, $3)
		ON CONFLICT DO NOTHING
	`
	_, err := r.db.Exec(ctx, query, postID, userID, reaction)
	if err != nil {
		return fmt.Errorf("add reaction: %w", err)
	}
	return nil
}

// RemoveReaction deletes a specific reaction
func (r *ChannelRepository) RemoveReaction(ctx context.Context, postID uuid.UUID, userID int64, reaction string) error {
	query := `DELETE FROM channel_post_reactions WHERE post_id=$1 AND user_id=$2 AND reaction=$3`
	_, err := r.db.Exec(ctx, query, postID, userID, reaction)
	return err
}

// GetReactions returns aggregated reactions for a post
func (r *ChannelRepository) GetReactions(ctx context.Context, postID uuid.UUID, userID int64) ([]domain.ReactionSummary, error) {
	query := `
		SELECT
			cpr.reaction,
			COUNT(*)::int                                  AS count,
			BOOL_OR(cpr.user_id = $2)                      AS reacted,
			COALESCE(
				json_agg(
					json_build_object(
						'id', u.id,
						'full_name', CONCAT(u.first_name, ' ', u.last_name),
						'avatar', u.avatar_url
					)
				) FILTER (WHERE u.id IS NOT NULL),
				'[]'
			) AS users
		FROM channel_post_reactions cpr
		JOIN users u ON cpr.user_id = u.id
		WHERE cpr.post_id = $1
		GROUP BY cpr.reaction
		ORDER BY count DESC
	`
	rows, err := r.db.Query(ctx, query, postID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []domain.ReactionSummary
	for rows.Next() {
		s := domain.ReactionSummary{}
		if err := rows.Scan(&s.Reaction, &s.Count, &s.Reacted, &s.Users); err != nil {
			r.logger.Error("Failed to scan reaction summary", zap.Error(err))
			continue
		}
		result = append(result, s)
	}
	return result, nil
}
