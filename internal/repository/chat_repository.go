package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

type ChatRepository struct {
	db          *pgxpool.Pool
	redisClient *redis.Client
	logger      *zap.Logger
}

func NewChatRepository(db *pgxpool.Pool, redisClient *redis.Client, logger *zap.Logger) *ChatRepository {
	return &ChatRepository{
		db:          db,
		redisClient: redisClient,
		logger:      logger.With(zap.String("repository", "chat")),
	}
}

// CreateConversation creates a new conversation
func (r *ChatRepository) CreateConversation(ctx context.Context, createdBy int64) (*domain.Conversation, error) {
	query := `
		INSERT INTO conversations (id, type, created_by, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, type, created_by, created_at, updated_at
	`

	conversation := &domain.Conversation{
		ID:        uuid.New(),
		Type:      "personal",
		CreatedBy: createdBy,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	err := r.db.QueryRow(ctx, query,
		conversation.ID,
		conversation.Type,
		conversation.CreatedBy,
		conversation.CreatedAt,
		conversation.UpdatedAt,
	).Scan(
		&conversation.ID,
		&conversation.Type,
		&conversation.CreatedBy,
		&conversation.CreatedAt,
		&conversation.UpdatedAt,
	)

	if err != nil {
		r.logger.Error("Failed to create conversation", zap.Error(err))
		return nil, fmt.Errorf("create conversation: %w", err)
	}

	r.logger.Info("Conversation created", zap.String("conversation_id", conversation.ID.String()))
	return conversation, nil
}

// AddParticipant adds a participant to conversation
func (r *ChatRepository) AddParticipant(ctx context.Context, conversationID uuid.UUID, userID int64) error {
	query := `
		INSERT INTO conversation_participants (conversation_id, user_id, joined_at, created_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (conversation_id, user_id) DO NOTHING
	`

	_, err := r.db.Exec(ctx, query, conversationID, userID, time.Now(), time.Now())
	if err != nil {
		r.logger.Error("Failed to add participant", zap.Error(err))
		return fmt.Errorf("add participant: %w", err)
	}

	// Cache invalidation/update
	// We simply set the cache key to true
	if r.redisClient != nil {
		cacheKey := fmt.Sprintf("chat:participant:%s:%d", conversationID.String(), userID)
		r.redisClient.Set(ctx, cacheKey, "1", 24*time.Hour)
	}

	return nil
}

// GetConversationByParticipants finds conversation between two users
func (r *ChatRepository) GetConversationByParticipants(ctx context.Context, user1ID, user2ID int64) (*domain.Conversation, error) {
	query := `
		SELECT c.id, c.type, c.created_by, c.created_at, c.updated_at
		FROM conversations c
		WHERE c.type = 'personal'
		  AND c.deleted_at IS NULL
		  AND EXISTS (
			  SELECT 1 FROM conversation_participants cp
			  WHERE cp.conversation_id = c.id
			    AND cp.user_id = $1
			    AND cp.left_at IS NULL
		  )
		  AND EXISTS (
			  SELECT 1 FROM conversation_participants cp
			  WHERE cp.conversation_id = c.id
			    AND cp.user_id = $2
			    AND cp.left_at IS NULL
		  )
		  AND (
			  SELECT COUNT(*) 
			  FROM conversation_participants cp
			  WHERE cp.conversation_id = c.id
			    AND cp.left_at IS NULL
		  ) = 2
		LIMIT 1
	`

	conversation := &domain.Conversation{}
	err := r.db.QueryRow(ctx, query, user1ID, user2ID).Scan(
		&conversation.ID,
		&conversation.Type,
		&conversation.CreatedBy,
		&conversation.CreatedAt,
		&conversation.UpdatedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get conversation by participants: %w", err)
	}

	return conversation, nil
}

func (r *ChatRepository) GetUserConversations(ctx context.Context, userID int64, limit, offset int) ([]*domain.ConversationView, error) {
	supportID := domain.SupportUserID

	query := `
		SELECT 
			c.id,
			CASE 
				WHEN stats.active_cnt = 2 AND stats.has_support AND $1 <> $2 THEN 'support'
				ELSE c.type
			END AS view_type,

			peer.id as peer_id,
			peer.username as peer_username,
			peer.full_name as peer_full_name,
			peer.avatar_url as peer_avatar,
			peer.last_seen_at as peer_last_seen_at,

			lm.conversation_id,
			lm.sender_id,
			lm.content,
			lm.content_hash,
			lm.encrypted_keys,
			lm.type,
			lm.is_edited,
			lm.is_deleted,
			lm.created_at as lm_created_at,
			lm.updated_at as lm_updated_at,
			lm.sender_username,
			lm.sender_full_name,
			lm.sender_avatar,
			lm.is_read,
			cp.is_muted,
			badges.unread_count,
			badges.mention_count,
			lm.id,
			c.created_at,
			c.updated_at
		FROM conversations c

		JOIN conversation_participants cp
		  ON cp.conversation_id = c.id
		 AND cp.user_id = $1
		 AND cp.left_at IS NULL
		 AND cp.hidden_at IS NULL

		-- Count unread messages once; mentions are a subset of the same visible unread rows.
		LEFT JOIN LATERAL (
			SELECT COUNT(*) AS unread_count,
			       COUNT(mm.message_id) AS mention_count
			FROM messages m
			LEFT JOIN message_mentions mm ON mm.message_id = m.id AND mm.user_id = $1
			WHERE m.conversation_id = c.id
			  AND m.sender_id != $1
			  AND m.is_deleted = false
			  AND m.created_at >= cp.joined_at
			  AND m.created_at > COALESCE(cp.last_cleared_at, '-infinity'::timestamptz)
			  AND NOT EXISTS (SELECT 1 FROM deleted_messages dm WHERE dm.message_id = m.id AND dm.user_id = $1)
			  AND NOT EXISTS (SELECT 1 FROM message_status ms WHERE ms.message_id = m.id AND ms.user_id = $1 AND ms.read_at IS NOT NULL)
		) badges ON true

		-- считаем активных участников и наличие саппорта
		JOIN LATERAL (
			SELECT 
				COUNT(*)::int as active_cnt,
				BOOL_OR(user_id = $2) as has_support
			FROM conversation_participants x
			WHERE x.conversation_id = c.id
			  AND x.left_at IS NULL
		) stats ON true

		-- ВАЖНО: личные чаты = только 2 активных участника
		-- иначе это уже "псевдо-группа" и в personal не должна попадать
		LEFT JOIN LATERAL (
			SELECT m.id, m.conversation_id, m.sender_id, m.content, m.content_hash, m.encrypted_keys, m.type, m.is_edited, m.is_deleted, m.created_at, m.updated_at,
				   u.username as sender_username,
				   u.first_name || ' ' || u.last_name as sender_full_name,
				   u.avatar_url as sender_avatar,
				   CASE 
					   WHEN m.sender_id = $1 THEN
						   -- Исходящее: прочитано ли собеседником (кем-то кроме меня)
						   EXISTS (
							   SELECT 1 FROM message_status ms 
							   WHERE ms.message_id = m.id 
								 AND ms.user_id != $1 
								 AND ms.read_at IS NOT NULL
						   )
					   ELSE
						   -- Входящее: прочитано ли мной
						   EXISTS (
							   SELECT 1 FROM message_status ms 
							   WHERE ms.message_id = m.id 
								 AND ms.user_id = $1 
								 AND ms.read_at IS NOT NULL
						   )
				   END as is_read
			FROM messages m
			JOIN users u ON m.sender_id = u.id
			LEFT JOIN deleted_messages dm ON m.id = dm.message_id AND dm.user_id = $1
			WHERE m.conversation_id = c.id 
			  AND m.is_deleted = false
			  AND dm.message_id IS NULL
			  AND m.created_at >= cp.joined_at
			  AND m.created_at > COALESCE(cp.last_cleared_at, '1970-01-01'::timestamp)
			ORDER BY m.created_at DESC
			LIMIT 1
		) lm ON true

		JOIN LATERAL (
			SELECT
				u.id,
				u.username,
				(u.first_name || ' ' || u.last_name) AS full_name,
				u.avatar_url,
				u.last_seen_at
			FROM conversation_participants cp2
			JOIN users u ON u.id = cp2.user_id
			WHERE cp2.conversation_id = c.id
			  AND cp2.left_at IS NULL
			  AND cp2.user_id <> $1
			ORDER BY
				-- поддержка как peer ТОЛЬКО если это реальный support чат (2 участника: user + support)
				CASE 
					WHEN stats.active_cnt = 2 AND stats.has_support AND $1 <> $2 AND cp2.user_id = $2 THEN 0
					ELSE 1
				END,
				-- иначе peer по последнему сообщению
				CASE 
					WHEN lm.sender_id IS NOT NULL AND cp2.user_id = lm.sender_id AND lm.sender_id <> $1 THEN 0
					ELSE 1
				END,
				cp2.joined_at DESC
			LIMIT 1
		) peer ON true

		WHERE c.type = 'personal'
		  AND c.deleted_at IS NULL
		  AND stats.active_cnt = 2

		ORDER BY COALESCE(lm.created_at, c.updated_at) DESC
	`
	args := []any{userID, supportID}
	if limit > 0 {
		query += "\n\t\tLIMIT $3 OFFSET $4"
		args = append(args, limit, offset)
	}

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		r.logger.Error("Failed to get user conversations", zap.Error(err))
		return nil, fmt.Errorf("get user conversations: %w", err)
	}
	defer rows.Close()

	var conversations []*domain.ConversationView
	for rows.Next() {
		conv := &domain.ConversationView{}

		var (
			lmID             *uuid.UUID
			lmConversationID *uuid.UUID
			lmSenderID       *int64
			lmContent        *string
			lmContentHash    *string
			lmEncryptedKeys  map[string]string
			lmType           *string
			lmIsEdited       *bool
			lmIsDeleted      *bool
			lmCreatedAt      *time.Time
			lmUpdatedAt      *time.Time
			lmSenderUsername *string
			lmSenderFullName *string
			lmSenderAvatar   *string
			lmIsRead         *bool
		)

		err := rows.Scan(
			&conv.ID,
			&conv.Type,

			&conv.PeerID,
			&conv.PeerUsername,
			&conv.PeerFullName,
			&conv.PeerAvatar,
			&conv.PeerLastSeenAt,

			&lmConversationID,
			&lmSenderID,
			&lmContent,
			&lmContentHash,
			&lmEncryptedKeys,
			&lmType,
			&lmIsEdited,
			&lmIsDeleted,
			&lmCreatedAt,
			&lmUpdatedAt,
			&lmSenderUsername,
			&lmSenderFullName,
			&lmSenderAvatar,
			&lmIsRead,
			&conv.IsMuted,
			&conv.UnreadCount,
			&conv.MentionCount,
			&lmID,
			&conv.CreatedAt,
			&conv.UpdatedAt,
		)
		if err != nil {
			r.logger.Error("Failed to scan conversation", zap.Error(err))
			continue
		}

		if lmID != nil {
			var isRead bool
			if lmIsRead != nil {
				isRead = *lmIsRead
			}

			conv.LastMessage = &domain.MessageView{
				Message: domain.Message{
					ID:             *lmID,
					ConversationID: *lmConversationID,
					SenderID:       *lmSenderID,
					Content:        lmContent,
					ContentHash:    lmContentHash,
					EncryptedKeys:  lmEncryptedKeys,
					Type:           *lmType,
					IsEdited:       *lmIsEdited,
					IsDeleted:      *lmIsDeleted,
					CreatedAt:      *lmCreatedAt,
					UpdatedAt:      *lmUpdatedAt,
				},
				SenderUsername: *lmSenderUsername,
				SenderFullName: *lmSenderFullName,
				SenderAvatar:   lmSenderAvatar,
				IsRead:         isRead,
			}
		}

		conversations = append(conversations, conv)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate user conversations: %w", err)
	}
	return conversations, nil
}

// IncrementUnread increments unread count for a participant
func (r *ChatRepository) IncrementUnread(ctx context.Context, conversationID uuid.UUID, userID int64) error {
	query := `
		UPDATE conversation_participants
		SET unread_count = unread_count + 1
		WHERE conversation_id = $1 AND user_id = $2
	`
	_, err := r.db.Exec(ctx, query, conversationID, userID)
	return err
}

// ResetUnread resets unread count for a participant
func (r *ChatRepository) ResetUnread(ctx context.Context, conversationID uuid.UUID, userID int64) error {
	query := `
		UPDATE conversation_participants
		SET unread_count = 0
		WHERE conversation_id = $1 AND user_id = $2
	`
	_, err := r.db.Exec(ctx, query, conversationID, userID)
	return err
}

// SetUnreadCount synchronizes the denormalized personal-chat badge with the
// authoritative message_status rows.
func (r *ChatRepository) SetUnreadCount(ctx context.Context, conversationID uuid.UUID, userID int64, count int) error {
	if count < 0 {
		count = 0
	}
	_, err := r.db.Exec(ctx, `
		UPDATE conversation_participants
		SET unread_count = $3
		WHERE conversation_id = $1 AND user_id = $2 AND left_at IS NULL
	`, conversationID, userID, count)
	return err
}

// SetMute toggles push notifications for a conversation participant.
func (r *ChatRepository) SetMute(ctx context.Context, conversationID uuid.UUID, userID int64, mute bool) error {
	query := `
		UPDATE conversation_participants
		SET is_muted = $3
		WHERE conversation_id = $1 AND user_id = $2 AND left_at IS NULL
	`
	result, err := r.db.Exec(ctx, query, conversationID, userID, mute)
	if err != nil {
		return fmt.Errorf("set conversation mute: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("conversation participant not found")
	}
	return nil
}

func (r *ChatRepository) IsMuted(ctx context.Context, conversationID uuid.UUID, userID int64) (bool, error) {
	query := `
		SELECT is_muted
		FROM conversation_participants
		WHERE conversation_id = $1 AND user_id = $2 AND left_at IS NULL
	`

	var muted bool
	err := r.db.QueryRow(ctx, query, conversationID, userID).Scan(&muted)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("get conversation mute: %w", err)
	}
	return muted, nil
}

// IsParticipant checks if user is participant of conversation
func (r *ChatRepository) IsParticipant(ctx context.Context, conversationID uuid.UUID, userID int64) (bool, error) {
	// 1. Try Cache
	if r.redisClient != nil {
		cacheKey := fmt.Sprintf("chat:participant:%s:%d", conversationID.String(), userID)
		val, err := r.redisClient.Get(ctx, cacheKey).Result()
		if err == nil {
			return val == "1", nil
		}
	}

	// 2. Fallback to DB
	query := `
		SELECT EXISTS(
			SELECT 1 FROM conversation_participants
			WHERE conversation_id = $1 AND user_id = $2 AND left_at IS NULL
		)
	`

	var exists bool
	err := r.db.QueryRow(ctx, query, conversationID, userID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check participant: %w", err)
	}

	// 3. Update Cache
	if r.redisClient != nil {
		cacheKey := fmt.Sprintf("chat:participant:%s:%d", conversationID.String(), userID)
		val := "0"
		if exists {
			val = "1"
		}
		// Cache for 1 hour, or until Add/Remove participant
		// False negatives (not participant) should expire faster or rely on consistency
		r.redisClient.Set(ctx, cacheKey, val, 1*time.Hour)
	}

	return exists, nil
}

// GetConversationParticipants gets all participant IDs for a conversation
func (r *ChatRepository) GetConversationParticipants(ctx context.Context, conversationID uuid.UUID) ([]int64, error) {
	query := `
		SELECT user_id FROM conversation_participants
		WHERE conversation_id = $1 AND left_at IS NULL
	`
	rows, err := r.db.Query(ctx, query, conversationID)
	if err != nil {
		return nil, fmt.Errorf("get participants: %w", err)
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

// HideConversation скрывает conversation для конкретного пользователя
// Каждый участник может скрыть чат у себя независимо
// При скрытии также сохраняется last_cleared_at - время "очистки" истории
func (r *ChatRepository) HideConversation(ctx context.Context, conversationID uuid.UUID, userID int64) error {
	query := `
		UPDATE conversation_participants
		SET hidden_at = NOW(),
		    last_cleared_at = NOW()
		WHERE conversation_id = $1
		  AND user_id = $2
		  AND hidden_at IS NULL
	`

	result, err := r.db.Exec(ctx, query, conversationID, userID)
	if err != nil {
		r.logger.Error("Failed to hide conversation", zap.Error(err))
		return fmt.Errorf("hide conversation: %w", err)
	}

	rowsAffected := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("conversation not found or already hidden")
	}

	r.logger.Info("Conversation hidden for user",
		zap.String("conversation_id", conversationID.String()),
		zap.Int64("user_id", userID),
	)
	return nil
}

// UnhideConversation восстанавливает скрытый conversation для пользователя
// ВАЖНО: last_cleared_at НЕ очищается - старые сообщения остаются скрытыми
func (r *ChatRepository) UnhideConversation(ctx context.Context, conversationID uuid.UUID, userID int64) error {
	query := `
		UPDATE conversation_participants
		SET hidden_at = NULL
		WHERE conversation_id = $1
		  AND user_id = $2
		  AND hidden_at IS NOT NULL
	`

	result, err := r.db.Exec(ctx, query, conversationID, userID)
	if err != nil {
		r.logger.Error("Failed to unhide conversation", zap.Error(err))
		return fmt.Errorf("unhide conversation: %w", err)
	}

	rowsAffected := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("conversation not found or not hidden")
	}

	r.logger.Info("Conversation unhidden for user",
		zap.String("conversation_id", conversationID.String()),
		zap.Int64("user_id", userID),
	)
	return nil
}

// GetLastClearedAt получает время последнего скрытия чата пользователем
// Используется для фильтрации старых сообщений
func (r *ChatRepository) GetLastClearedAt(ctx context.Context, conversationID uuid.UUID, userID int64) (*time.Time, error) {
	query := `
		SELECT last_cleared_at
		FROM conversation_participants
		WHERE conversation_id = $1 AND user_id = $2
	`

	var lastClearedAt *time.Time
	err := r.db.QueryRow(ctx, query, conversationID, userID).Scan(&lastClearedAt)
	if err != nil {
		r.logger.Error("Failed to get last_cleared_at", zap.Error(err))
		return nil, fmt.Errorf("get last_cleared_at: %w", err)
	}

	return lastClearedAt, nil
}

// GetConversationView gets a single conversation view by ID
func (r *ChatRepository) GetConversationView(ctx context.Context, conversationID uuid.UUID, userID int64) (*domain.ConversationView, error) {
	supportID := domain.SupportUserID

	query := `
		SELECT 
			c.id,
			CASE 
				WHEN stats.active_cnt = 2 AND stats.has_support AND $1 <> $3 THEN 'support'
				ELSE c.type
			END AS view_type,

			peer.id as peer_id,
			peer.username as peer_username,
			peer.full_name as peer_full_name,
			peer.avatar_url as peer_avatar,
			peer.last_seen_at as peer_last_seen_at,

			lm.conversation_id,
			lm.sender_id,
			lm.content,
			lm.content_hash,
			lm.encrypted_keys,
			lm.type,
			lm.is_edited,
			lm.is_deleted,
			lm.created_at as lm_created_at,
			lm.updated_at as lm_updated_at,
			lm.sender_username,
			lm.sender_full_name,
			lm.sender_avatar,
			cp.is_muted,
			(SELECT COUNT(*) 
			 FROM messages m 
			 WHERE m.conversation_id = c.id 
			   AND m.sender_id != $1
			   AND m.is_deleted = false
			   AND m.created_at >= cp.joined_at
			   AND m.created_at > COALESCE(cp.last_cleared_at, '-infinity'::timestamptz)
			   AND NOT EXISTS (SELECT 1 FROM deleted_messages dm WHERE dm.message_id = m.id AND dm.user_id = $1)
			   AND NOT EXISTS (
				   SELECT 1 FROM message_status ms 
				   WHERE ms.message_id = m.id 
					 AND ms.user_id = $1 
					 AND ms.read_at IS NOT NULL
			   )
			) as unread_count,
			(SELECT COUNT(*) 
			 FROM message_mentions mm
			 JOIN messages m ON mm.message_id = m.id
			 WHERE m.conversation_id = c.id 
			   AND mm.user_id = $1
			   AND m.sender_id != $1
			   AND m.is_deleted = false
			   AND m.created_at >= cp.joined_at
			   AND m.created_at > COALESCE(cp.last_cleared_at, '-infinity'::timestamptz)
			   AND NOT EXISTS (SELECT 1 FROM deleted_messages dm WHERE dm.message_id = m.id AND dm.user_id = $1)
			   AND NOT EXISTS (
				   SELECT 1 FROM message_status ms 
				   WHERE ms.message_id = m.id 
					 AND ms.user_id = $1 
					 AND ms.read_at IS NOT NULL
			   )
			) as mention_count,
			lm.id,
			c.created_at,
			c.updated_at
		FROM conversations c
		JOIN conversation_participants cp
		  ON cp.conversation_id = c.id
		 AND cp.user_id = $1
		 AND cp.left_at IS NULL
		 AND cp.hidden_at IS NULL

		JOIN LATERAL (
			SELECT 
				COUNT(*)::int as active_cnt,
				BOOL_OR(user_id = $3) as has_support
			FROM conversation_participants x
			WHERE x.conversation_id = c.id
			  AND x.left_at IS NULL
		) stats ON true

		LEFT JOIN LATERAL (
			SELECT m.id, m.conversation_id, m.sender_id, m.content, m.content_hash, m.encrypted_keys, m.type, m.is_edited, m.is_deleted, m.created_at, m.updated_at,
				   u.username as sender_username,
				   u.first_name || ' ' || u.last_name as sender_full_name,
				   u.avatar_url as sender_avatar
			FROM messages m
			JOIN users u ON m.sender_id = u.id
			LEFT JOIN deleted_messages dm ON m.id = dm.message_id AND dm.user_id = $1
			WHERE m.conversation_id = c.id 
			  AND m.is_deleted = false
			  AND dm.message_id IS NULL
			  AND m.created_at >= cp.joined_at
			  AND m.created_at > COALESCE(cp.last_cleared_at, '1970-01-01'::timestamp)
			ORDER BY m.created_at DESC
			LIMIT 1
		) lm ON true

		JOIN LATERAL (
			SELECT
				u.id,
				u.username,
				(u.first_name || ' ' || u.last_name) AS full_name,
				u.avatar_url,
				u.last_seen_at
			FROM conversation_participants cp2
			JOIN users u ON u.id = cp2.user_id
			WHERE cp2.conversation_id = c.id
			  AND cp2.left_at IS NULL
			  AND cp2.user_id <> $1
			ORDER BY
				CASE 
					WHEN stats.active_cnt = 2 AND stats.has_support AND $1 <> $3 AND cp2.user_id = $3 THEN 0
					ELSE 1
				END,
				CASE 
					WHEN lm.sender_id IS NOT NULL AND cp2.user_id = lm.sender_id AND lm.sender_id <> $1 THEN 0
					ELSE 1
				END,
				cp2.joined_at DESC
			LIMIT 1
		) peer ON true

		WHERE c.id = $2
		  AND c.deleted_at IS NULL
	`

	conv := &domain.ConversationView{}

	var (
		lmID             *uuid.UUID
		lmConversationID *uuid.UUID
		lmSenderID       *int64
		lmContent        *string
		lmContentHash    *string
		lmEncryptedKeys  map[string]string
		lmType           *string
		lmIsEdited       *bool
		lmIsDeleted      *bool
		lmCreatedAt      *time.Time
		lmUpdatedAt      *time.Time
		lmSenderUsername *string
		lmSenderFullName *string
		lmSenderAvatar   *string
	)

	err := r.db.QueryRow(ctx, query, userID, conversationID, supportID).Scan(
		&conv.ID,
		&conv.Type,

		&conv.PeerID,
		&conv.PeerUsername,
		&conv.PeerFullName,
		&conv.PeerAvatar,
		&conv.PeerLastSeenAt,

		&lmConversationID,
		&lmSenderID,
		&lmContent,
		&lmContentHash,
		&lmEncryptedKeys,
		&lmType,
		&lmIsEdited,
		&lmIsDeleted,
		&lmCreatedAt,
		&lmUpdatedAt,
		&lmSenderUsername,
		&lmSenderFullName,
		&lmSenderAvatar,
		&conv.IsMuted,
		&conv.UnreadCount,
		&conv.MentionCount,
		&lmID,
		&conv.CreatedAt,
		&conv.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("conversation not found")
		}
		r.logger.Error("Failed to get conversation view", zap.Error(err))
		return nil, fmt.Errorf("get conversation view: %w", err)
	}

	if lmID != nil {
		conv.LastMessage = &domain.MessageView{
			Message: domain.Message{
				ID:             *lmID,
				ConversationID: *lmConversationID,
				SenderID:       *lmSenderID,
				Content:        lmContent,
				ContentHash:    lmContentHash,
				EncryptedKeys:  lmEncryptedKeys,
				Type:           *lmType,
				IsEdited:       *lmIsEdited,
				IsDeleted:      *lmIsDeleted,
				CreatedAt:      *lmCreatedAt,
				UpdatedAt:      *lmUpdatedAt,
			},
			SenderUsername: *lmSenderUsername,
			SenderFullName: *lmSenderFullName,
			SenderAvatar:   lmSenderAvatar,
		}
	}

	return conv, nil
}
