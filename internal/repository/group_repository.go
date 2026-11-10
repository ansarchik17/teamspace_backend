package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/fileurl"
	"go.uber.org/zap"
)

type GroupRepository struct {
	db     *pgxpool.Pool
	logger *zap.Logger
}

func NewGroupRepository(db *pgxpool.Pool, logger *zap.Logger) *GroupRepository {
	return &GroupRepository{
		db:     db,
		logger: logger.With(zap.String("repository", "group")),
	}
}

// CreateGroup creates a new group
func (r *GroupRepository) CreateGroup(ctx context.Context, group *domain.Group) error {
	query := `
		INSERT INTO groups (id, name, description, avatar_url, type, max_members, created_by, is_active, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING id
	`

	if group.ID == uuid.Nil {
		group.ID = uuid.New()
	}
	if group.CreatedAt.IsZero() {
		group.CreatedAt = time.Now()
	}
	group.UpdatedAt = group.CreatedAt

	err := r.db.QueryRow(ctx, query,
		group.ID,
		group.Name,
		group.Description,
		group.AvatarURL,
		group.Type,
		group.MaxMembers,
		group.CreatedBy,
		group.IsActive,
		group.CreatedAt,
		group.UpdatedAt,
	).Scan(&group.ID)

	if err != nil {
		r.logger.Error("Failed to create group", zap.Error(err))
		return fmt.Errorf("create group: %w", err)
	}

	r.logger.Info("Group created", zap.String("group_id", group.ID.String()))
	return nil
}

// GetByID gets group by ID
func (r *GroupRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.Group, error) {
	query := `
		SELECT id, name, description, avatar_url, type, max_members, created_by, is_active, created_at, updated_at
		FROM groups
		WHERE id = $1 AND deleted_at IS NULL
	`

	group := &domain.Group{}
	err := r.db.QueryRow(ctx, query, id).Scan(
		&group.ID,
		&group.Name,
		&group.Description,
		&group.AvatarURL,
		&group.Type,
		&group.MaxMembers,
		&group.CreatedBy,
		&group.IsActive,
		&group.CreatedAt,
		&group.UpdatedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("group not found")
		}
		return nil, fmt.Errorf("get group: %w", err)
	}
	group.AvatarURL = fileurl.SignLegacyDownloadURL(group.AvatarURL)

	return group, nil
}

// Update updates group
func (r *GroupRepository) Update(ctx context.Context, group *domain.Group) error {
	query := `
		UPDATE groups
		SET name = $1, description = $2, avatar_url = $3, type = $4, max_members = $5, is_active = $6, updated_at = $7
		WHERE id = $8 AND deleted_at IS NULL
	`

	group.UpdatedAt = time.Now()

	result, err := r.db.Exec(ctx, query,
		group.Name,
		group.Description,
		group.AvatarURL,
		group.Type,
		group.MaxMembers,
		group.IsActive,
		group.UpdatedAt,
		group.ID,
	)

	if err != nil {
		r.logger.Error("Failed to update group", zap.Error(err))
		return fmt.Errorf("update group: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("group not found")
	}

	return nil
}

// DeleteGroup soft deletes a group
func (r *GroupRepository) DeleteGroup(ctx context.Context, groupID uuid.UUID) error {
	query := `
		UPDATE groups
		SET deleted_at = $1, is_active = false
		WHERE id = $2 AND deleted_at IS NULL
	`

	result, err := r.db.Exec(ctx, query, time.Now(), groupID)
	if err != nil {
		r.logger.Error("Failed to delete group", zap.Error(err))
		return fmt.Errorf("delete group: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("group not found")
	}

	return nil
}

// AddMember adds a member to group
func (r *GroupRepository) AddMember(ctx context.Context, groupID uuid.UUID, userID int64, role string) error {
	query := `
		INSERT INTO group_members (group_id, user_id, role, joined_at, created_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (group_id, user_id) DO UPDATE SET left_at = NULL, role = $3
	`

	_, err := r.db.Exec(ctx, query, groupID, userID, role, time.Now(), time.Now())
	if err != nil {
		r.logger.Error("Failed to add member", zap.Error(err))
		return fmt.Errorf("add member: %w", err)
	}

	return nil
}

// RemoveMember removes a member from group
func (r *GroupRepository) RemoveMember(ctx context.Context, groupID uuid.UUID, userID int64) error {
	query := `
		UPDATE group_members
		SET left_at = $1
		WHERE group_id = $2 AND user_id = $3 AND left_at IS NULL
	`

	result, err := r.db.Exec(ctx, query, time.Now(), groupID, userID)
	if err != nil {
		r.logger.Error("Failed to remove member", zap.Error(err))
		return fmt.Errorf("remove member: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("member not found")
	}

	return nil
}

// UpdateMemberRole updates member role
func (r *GroupRepository) UpdateMemberRole(ctx context.Context, groupID uuid.UUID, userID int64, role string) error {
	query := `
		UPDATE group_members
		SET role = $1
		WHERE group_id = $2 AND user_id = $3 AND left_at IS NULL
	`

	result, err := r.db.Exec(ctx, query, role, groupID, userID)
	if err != nil {
		return fmt.Errorf("update member role: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("member not found")
	}

	return nil
}

// IsMember checks if user is member of group
func (r *GroupRepository) IsMember(ctx context.Context, groupID uuid.UUID, userID int64) (bool, error) {
	query := `
		SELECT EXISTS(
			SELECT 1 FROM group_members
			WHERE group_id = $1 AND user_id = $2 AND left_at IS NULL
		)
	`

	var exists bool
	err := r.db.QueryRow(ctx, query, groupID, userID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check member: %w", err)
	}

	return exists, nil
}

// GetMemberRole gets member role in group
func (r *GroupRepository) GetMemberRole(ctx context.Context, groupID uuid.UUID, userID int64) (string, error) {
	query := `
		SELECT role
		FROM group_members
		WHERE group_id = $1 AND user_id = $2 AND left_at IS NULL
	`

	var role string
	err := r.db.QueryRow(ctx, query, groupID, userID).Scan(&role)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("member not found")
		}
		return "", fmt.Errorf("get member role: %w", err)
	}

	return role, nil
}

// SetMute toggles push notifications for a group member.
func (r *GroupRepository) SetMute(ctx context.Context, groupID uuid.UUID, userID int64, mute bool) error {
	query := `
		UPDATE group_members
		SET is_muted = $3
		WHERE group_id = $1 AND user_id = $2 AND left_at IS NULL
	`
	result, err := r.db.Exec(ctx, query, groupID, userID, mute)
	if err != nil {
		return fmt.Errorf("set group mute: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("group member not found")
	}
	return nil
}

func (r *GroupRepository) IsMuted(ctx context.Context, groupID uuid.UUID, userID int64) (bool, error) {
	query := `
		SELECT is_muted
		FROM group_members
		WHERE group_id = $1 AND user_id = $2 AND left_at IS NULL
	`

	var muted bool
	err := r.db.QueryRow(ctx, query, groupID, userID).Scan(&muted)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("get group mute: %w", err)
	}
	return muted, nil
}

func (r *GroupRepository) GetMemberMuteMap(ctx context.Context, groupID uuid.UUID) (map[int64]bool, error) {
	query := `
		SELECT user_id, is_muted
		FROM group_members
		WHERE group_id = $1 AND left_at IS NULL
	`
	rows, err := r.db.Query(ctx, query, groupID)
	if err != nil {
		return nil, fmt.Errorf("get group mute map: %w", err)
	}
	defer rows.Close()

	result := make(map[int64]bool)
	for rows.Next() {
		var userID int64
		var muted bool
		if err := rows.Scan(&userID, &muted); err != nil {
			return nil, err
		}
		result[userID] = muted
	}
	return result, rows.Err()
}

// GetUserGroups gets all groups for user
func (r *GroupRepository) GetUserGroups(ctx context.Context, userID int64, limit, offset int) ([]*domain.GroupView, error) {
	query := `
		SELECT 
			g.id,
			g.name,
			g.description,
			g.avatar_url,
			g.type,
			g.max_members,
			g.created_by,
			g.is_active,
			g.created_at,
			g.updated_at,
			gm.role as member_role,
			(SELECT COUNT(*) FROM group_members WHERE group_id = g.id AND left_at IS NULL) as member_count,
			gm.is_muted,
			badges.unread_count,
			badges.mention_count,
			lm.id,
			lm.conversation_id,
			lm.sender_id,
			lm.content,
			lm.type,
			lm.is_edited,
			lm.is_deleted,
			lm.created_at,
			lm.sender_username,
			lm.sender_full_name,
			lm.sender_avatar,
			lm.is_read
		FROM groups g
		INNER JOIN group_members gm ON g.id = gm.group_id
		LEFT JOIN LATERAL (
			SELECT COUNT(*) AS unread_count,
			       COUNT(mm.message_id) AS mention_count
			FROM messages m
			LEFT JOIN message_mentions mm ON mm.message_id = m.id AND mm.user_id = $1
			WHERE m.conversation_id = g.id
			  AND m.sender_id != $1
			  AND m.is_deleted = false
			  AND m.created_at >= gm.joined_at
			  AND m.created_at > COALESCE(gm.last_cleared_at, '-infinity'::timestamptz)
			  AND NOT EXISTS (SELECT 1 FROM deleted_messages dm WHERE dm.message_id = m.id AND dm.user_id = $1)
			  AND NOT EXISTS (SELECT 1 FROM message_status ms WHERE ms.message_id = m.id AND ms.user_id = $1 AND ms.read_at IS NOT NULL)
		) badges ON true
		LEFT JOIN LATERAL (
			SELECT m.id, m.conversation_id, m.sender_id, m.content, m.type, m.is_edited, m.is_deleted, m.created_at,
				   u.username as sender_username,
				   u.first_name || ' ' || u.last_name as sender_full_name,
				   u.avatar_url as sender_avatar,
				   CASE 
					  WHEN m.sender_id = $1 THEN 
						 EXISTS(SELECT 1 FROM message_status ms WHERE ms.message_id = m.id AND ms.user_id != $1 AND ms.read_at IS NOT NULL)
					  ELSE 
						 EXISTS(SELECT 1 FROM message_status ms WHERE ms.message_id = m.id AND ms.user_id = $1 AND ms.read_at IS NOT NULL)
				   END as is_read
			FROM messages m 
			JOIN users u ON m.sender_id = u.id
			LEFT JOIN deleted_messages dm ON m.id = dm.message_id AND dm.user_id = $1
			WHERE m.conversation_id = g.id 
			  AND m.is_deleted = false 
			  AND dm.message_id IS NULL
			  AND m.created_at >= gm.joined_at
			  AND m.created_at > COALESCE(gm.last_cleared_at, '1970-01-01'::timestamp)
			ORDER BY m.created_at DESC LIMIT 1
		) lm ON true
		WHERE gm.user_id = $1 
		  AND gm.left_at IS NULL
		  AND gm.hidden_at IS NULL
		  AND g.is_active = true
		  AND g.deleted_at IS NULL
		ORDER BY g.updated_at DESC
	`
	args := []any{userID}
	if limit > 0 {
		query += "\n\t\tLIMIT $2 OFFSET $3"
		args = append(args, limit, offset)
	}

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		r.logger.Error("Failed to get user groups", zap.Error(err))
		return nil, fmt.Errorf("get user groups: %w", err)
	}
	defer rows.Close()

	var groups []*domain.GroupView
	for rows.Next() {
		view := &domain.GroupView{}
		var (
			lmID             *uuid.UUID
			lmConversationID *uuid.UUID
			lmSenderID       *int64
			lmContent        *string
			lmType           *string
			lmIsEdited       *bool
			lmIsDeleted      *bool
			lmCreatedAt      *time.Time
			lmSenderUsername *string
			lmSenderFullName *string
			lmSenderAvatar   *string
			lmIsRead         *bool
		)

		err := rows.Scan(
			&view.ID,
			&view.Name,
			&view.Description,
			&view.AvatarURL,
			&view.Type,
			&view.MaxMembers,
			&view.CreatedBy,
			&view.IsActive,
			&view.CreatedAt,
			&view.UpdatedAt,
			&view.MemberRole,
			&view.MemberCount,
			&view.IsMuted,
			&view.UnreadCount,
			&view.MentionCount,
			&lmID,
			&lmConversationID,
			&lmSenderID,
			&lmContent,
			&lmType,
			&lmIsEdited,
			&lmIsDeleted,
			&lmCreatedAt,
			&lmSenderUsername,
			&lmSenderFullName,
			&lmSenderAvatar,
			&lmIsRead,
		)
		if err != nil {
			r.logger.Error("Failed to scan group", zap.Error(err))
			continue
		}

		if lmID != nil {
			isRead := false
			if lmIsRead != nil {
				isRead = *lmIsRead
			}
			view.LastMessage = &domain.MessageView{
				Message: domain.Message{
					ID:             *lmID,
					ConversationID: *lmConversationID,
					SenderID:       *lmSenderID,
					Content:        lmContent,
					Type:           *lmType,
					IsEdited:       *lmIsEdited,
					IsDeleted:      *lmIsDeleted,
					CreatedAt:      *lmCreatedAt,
				},
				SenderUsername: *lmSenderUsername,
				SenderFullName: *lmSenderFullName,
				SenderAvatar:   fileurl.SignLegacyDownloadURL(lmSenderAvatar),
				IsRead:         isRead,
			}
		}
		view.AvatarURL = fileurl.SignLegacyDownloadURL(view.AvatarURL)

		groups = append(groups, view)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate user groups: %w", err)
	}
	return groups, nil
}

// GetMemberCount gets number of members in group
func (r *GroupRepository) GetMemberCount(ctx context.Context, groupID uuid.UUID) (int, error) {
	query := `
		SELECT COUNT(*)
		FROM group_members
		WHERE group_id = $1 AND left_at IS NULL
	`

	var count int
	err := r.db.QueryRow(ctx, query, groupID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("get member count: %w", err)
	}

	return count, nil
}

// GetGroupMembers gets all member IDs of a group
func (r *GroupRepository) GetGroupMembers(ctx context.Context, groupID uuid.UUID) ([]int64, error) {
	query := `
		SELECT user_id FROM group_members
		WHERE group_id = $1 AND left_at IS NULL
	`

	rows, err := r.db.Query(ctx, query, groupID)
	if err != nil {
		return nil, fmt.Errorf("get group members: %w", err)
	}
	defer rows.Close()

	var userIDs []int64
	for rows.Next() {
		var userID int64
		if err := rows.Scan(&userID); err != nil {
			return nil, err
		}
		userIDs = append(userIDs, userID)
	}

	return userIDs, nil
}

// GetGroupView gets a single group view by ID
func (r *GroupRepository) GetGroupView(ctx context.Context, groupID uuid.UUID, userID int64) (*domain.GroupView, error) {
	query := `
		SELECT 
			g.id,
			g.name,
			g.description,
			g.avatar_url,
			g.type,
			g.max_members,
			g.created_by,
			g.is_active,
			g.created_at,
			g.updated_at,
			gm.role as member_role,
			(SELECT COUNT(*) FROM group_members WHERE group_id = g.id AND left_at IS NULL) as member_count,
			gm.is_muted,
			(SELECT COUNT(*) 
			 FROM messages m 
			 WHERE m.conversation_id = g.id 
			   AND m.sender_id != $2
			   AND m.is_deleted = false
			   AND m.created_at >= gm.joined_at
			   AND m.created_at > COALESCE(gm.last_cleared_at, '-infinity'::timestamptz)
			   AND NOT EXISTS (SELECT 1 FROM deleted_messages dm WHERE dm.message_id = m.id AND dm.user_id = $2)
			   AND NOT EXISTS (
				   SELECT 1 FROM message_status ms 
				   WHERE ms.message_id = m.id 
					 AND ms.user_id = $2
					 AND ms.read_at IS NOT NULL
			   )
			) as unread_count,
			(SELECT COUNT(*) 
			 FROM message_mentions mm
			 JOIN messages m ON mm.message_id = m.id
			 WHERE m.conversation_id = g.id 
			   AND mm.user_id = $2
			   AND m.sender_id != $2
			   AND m.is_deleted = false
			   AND m.created_at >= gm.joined_at
			   AND m.created_at > COALESCE(gm.last_cleared_at, '-infinity'::timestamptz)
			   AND NOT EXISTS (SELECT 1 FROM deleted_messages dm WHERE dm.message_id = m.id AND dm.user_id = $2)
			   AND NOT EXISTS (
				   SELECT 1 FROM message_status ms 
				   WHERE ms.message_id = m.id 
					 AND ms.user_id = $2
					 AND ms.read_at IS NOT NULL
			   )
			) as mention_count,
			lm.id,
			lm.conversation_id,
			lm.sender_id,
			lm.content,
			lm.type,
			lm.is_edited,
			lm.is_deleted,
			lm.created_at,
			lm.sender_username,
			lm.sender_full_name,
			lm.sender_avatar
		FROM groups g
		INNER JOIN group_members gm ON g.id = gm.group_id
		LEFT JOIN LATERAL (
			SELECT m.id, m.conversation_id, m.sender_id, m.content, m.type, m.is_edited, m.is_deleted, m.created_at,
				   u.username as sender_username,
				   u.first_name || ' ' || u.last_name as sender_full_name,
				   u.avatar_url as sender_avatar
			FROM messages m 
			JOIN users u ON m.sender_id = u.id
			LEFT JOIN deleted_messages dm ON m.id = dm.message_id AND dm.user_id = $2
			WHERE m.conversation_id = g.id 
			  AND m.is_deleted = false 
			  AND dm.message_id IS NULL
			  AND m.created_at >= gm.joined_at
			  AND m.created_at > COALESCE(gm.last_cleared_at, '1970-01-01'::timestamp)
			ORDER BY m.created_at DESC LIMIT 1
		) lm ON true
		WHERE g.id = $1
		  AND gm.user_id = $2
		  AND gm.left_at IS NULL
		  AND gm.hidden_at IS NULL
		  AND g.is_active = true
		  AND g.deleted_at IS NULL
	`

	view := &domain.GroupView{}
	var (
		lmID             *uuid.UUID
		lmConversationID *uuid.UUID
		lmSenderID       *int64
		lmContent        *string
		lmType           *string
		lmIsEdited       *bool
		lmIsDeleted      *bool
		lmCreatedAt      *time.Time
		lmSenderUsername *string
		lmSenderFullName *string
		lmSenderAvatar   *string
	)

	err := r.db.QueryRow(ctx, query, groupID, userID).Scan(
		&view.ID,
		&view.Name,
		&view.Description,
		&view.AvatarURL,
		&view.Type,
		&view.MaxMembers,
		&view.CreatedBy,
		&view.IsActive,
		&view.CreatedAt,
		&view.UpdatedAt,
		&view.MemberRole,
		&view.MemberCount,
		&view.IsMuted,
		&view.UnreadCount,
		&view.MentionCount,
		&lmID,
		&lmConversationID,
		&lmSenderID,
		&lmContent,
		&lmType,
		&lmIsEdited,
		&lmIsDeleted,
		&lmCreatedAt,
		&lmSenderUsername,
		&lmSenderFullName,
		&lmSenderAvatar,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("group not found")
		}
		r.logger.Error("Failed to get group view", zap.Error(err))
		return nil, fmt.Errorf("get group view: %w", err)
	}

	if lmID != nil {
		view.LastMessage = &domain.MessageView{
			Message: domain.Message{
				ID:             *lmID,
				ConversationID: *lmConversationID,
				SenderID:       *lmSenderID,
				Content:        lmContent,
				Type:           *lmType,
				IsEdited:       *lmIsEdited,
				IsDeleted:      *lmIsDeleted,
				CreatedAt:      *lmCreatedAt,
			},
			SenderUsername: *lmSenderUsername,
			SenderFullName: *lmSenderFullName,
			SenderAvatar:   fileurl.SignLegacyDownloadURL(lmSenderAvatar),
		}
	}
	view.AvatarURL = fileurl.SignLegacyDownloadURL(view.AvatarURL)

	return view, nil
}

// GetGroupMemberDetails gets detailed member info for a group
func (r *GroupRepository) GetGroupMemberDetails(ctx context.Context, groupID uuid.UUID) ([]domain.GroupMemberDetail, error) {
	query := `
		SELECT
			u.id, u.email, u.username, u.first_name, u.last_name,
			u.avatar_url, u.bio, u.is_active, u.last_login_at, u.created_at,
			gm.role
		FROM users u
		JOIN group_members gm ON u.id = gm.user_id
		WHERE gm.group_id = $1 AND gm.left_at IS NULL
		ORDER BY gm.joined_at ASC
	`

	rows, err := r.db.Query(ctx, query, groupID)
	if err != nil {
		return nil, fmt.Errorf("get group member details: %w", err)
	}
	defer rows.Close()

	var members []domain.GroupMemberDetail
	for rows.Next() {
		var m domain.GroupMemberDetail
		var (
			id          int64
			email       string
			username    string
			firstName   string
			lastName    string
			avatarURL   *string
			bio         *string
			isActive    bool
			lastLoginAt *time.Time
			createdAt   time.Time
			role        string
		)

		if err := rows.Scan(
			&id, &email, &username, &firstName, &lastName,
			&avatarURL, &bio, &isActive, &lastLoginAt, &createdAt,
			&role,
		); err != nil {
			return nil, err
		}

		m.ID = id
		m.Email = email
		m.Username = username
		m.FirstName = firstName
		m.LastName = lastName
		m.FullName = firstName + " " + lastName
		m.AvatarURL = fileurl.SignLegacyDownloadURL(avatarURL)
		m.Bio = bio
		m.IsActive = isActive
		m.LastLoginAt = lastLoginAt
		m.CreatedAt = createdAt
		m.Role = role

		members = append(members, m)
	}

	return members, nil
}

// HideGroup скрывает группу для конкретного пользователя
// Каждый участник может скрыть группу у себя независимо
func (r *GroupRepository) HideGroup(ctx context.Context, groupID uuid.UUID, userID int64) error {
	query := `
		UPDATE group_members
		SET hidden_at = NOW(),
		    last_cleared_at = NOW()
		WHERE group_id = $1
		  AND user_id = $2
		  AND hidden_at IS NULL
	`

	result, err := r.db.Exec(ctx, query, groupID, userID)
	if err != nil {
		r.logger.Error("Failed to hide group", zap.Error(err))
		return fmt.Errorf("hide group: %w", err)
	}

	rowsAffected := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("group not found or already hidden")
	}

	r.logger.Info("Group hidden for user",
		zap.String("group_id", groupID.String()),
		zap.Int64("user_id", userID),
	)
	return nil
}

// UnhideGroup восстанавливает скрытую группу для пользователя
// ВАЖНО: last_cleared_at НЕ очищается - старые сообщения остаются скрытыми
func (r *GroupRepository) UnhideGroup(ctx context.Context, groupID uuid.UUID, userID int64) error {
	query := `
		UPDATE group_members
		SET hidden_at = NULL
		WHERE group_id = $1
		  AND user_id = $2
		  AND hidden_at IS NOT NULL
	`

	result, err := r.db.Exec(ctx, query, groupID, userID)
	if err != nil {
		r.logger.Error("Failed to unhide group", zap.Error(err))
		return fmt.Errorf("unhide group: %w", err)
	}

	rowsAffected := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("group not found or not hidden")
	}

	r.logger.Info("Group unhidden for user",
		zap.String("group_id", groupID.String()),
		zap.Int64("user_id", userID),
	)
	return nil
}

// GetLastClearedAt получает время последнего скрытия группы пользователем
// Используется для фильтрации старых сообщений
func (r *GroupRepository) GetLastClearedAt(ctx context.Context, groupID uuid.UUID, userID int64) (*time.Time, error) {
	query := `
		SELECT last_cleared_at
		FROM group_members
		WHERE group_id = $1 AND user_id = $2
	`

	var lastClearedAt *time.Time
	err := r.db.QueryRow(ctx, query, groupID, userID).Scan(&lastClearedAt)
	if err != nil {
		r.logger.Error("Failed to get last_cleared_at", zap.Error(err))
		return nil, fmt.Errorf("get last_cleared_at: %w", err)
	}

	return lastClearedAt, nil
}
