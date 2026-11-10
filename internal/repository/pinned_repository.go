package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

type PinnedRepository struct {
	db     *pgxpool.Pool
	logger *zap.Logger
}

func NewPinnedRepository(db *pgxpool.Pool, logger *zap.Logger) *PinnedRepository {
	return &PinnedRepository{
		db:     db,
		logger: logger.With(zap.String("repository", "pinned")),
	}
}

// MessageBelongsToChat prevents pin operations from associating a message or
// channel post with an unrelated chat supplied by the client.
func (r *PinnedRepository) MessageBelongsToChat(ctx context.Context, chatID uuid.UUID, chatType string, messageID uuid.UUID) (bool, error) {
	query := `
		SELECT EXISTS (
			SELECT 1 FROM messages
			WHERE id = $1 AND conversation_id = $2 AND is_deleted = false
		)
	`
	if chatType == "channel" {
		query = `
			SELECT EXISTS (
				SELECT 1 FROM channel_posts
				WHERE id = $1 AND channel_id = $2 AND is_deleted = false
			)
		`
	}

	var belongs bool
	if err := r.db.QueryRow(ctx, query, messageID, chatID).Scan(&belongs); err != nil {
		return false, fmt.Errorf("check pinned message chat: %w", err)
	}
	return belongs, nil
}

// PinChat pins a chat for user
func (r *PinnedRepository) PinChat(ctx context.Context, userID int64, chatID uuid.UUID, chatType string) error {
	query := `
		INSERT INTO pinned_chats (id, user_id, chat_id, chat_type, position, created_at, updated_at)
		SELECT $1, $2, $3, $4, COALESCE(MAX(position) + 1, 1), $5, $6
		FROM pinned_chats
		WHERE user_id = $2
		ON CONFLICT (user_id, chat_id, chat_type) DO UPDATE SET updated_at = $6
	`

	_, err := r.db.Exec(ctx, query,
		uuid.New(),
		userID,
		chatID,
		chatType,
		time.Now(),
		time.Now(),
	)

	if err != nil {
		r.logger.Error("Failed to pin chat", zap.Error(err))
		return fmt.Errorf("pin chat: %w", err)
	}

	return nil
}

// UnpinChat unpins a chat for user
func (r *PinnedRepository) UnpinChat(ctx context.Context, userID int64, chatID uuid.UUID, chatType string) error {
	query := `
		DELETE FROM pinned_chats
		WHERE user_id = $1 AND chat_id = $2 AND chat_type = $3
	`

	_, err := r.db.Exec(ctx, query, userID, chatID, chatType)
	if err != nil {
		r.logger.Error("Failed to unpin chat", zap.Error(err))
		return fmt.Errorf("unpin chat: %w", err)
	}

	return nil
}

// RemoveAllPinsForChat removes all pins for a chat (for all users)
// Used when a group or chat is permanently deleted
func (r *PinnedRepository) RemoveAllPinsForChat(ctx context.Context, chatID uuid.UUID, chatType string) error {
	query := `
		DELETE FROM pinned_chats
		WHERE chat_id = $1 AND chat_type = $2
	`

	_, err := r.db.Exec(ctx, query, chatID, chatType)
	if err != nil {
		r.logger.Error("Failed to remove all pins for chat", zap.Error(err))
		return fmt.Errorf("remove all pins for chat: %w", err)
	}

	return nil
}

// GetPinnedChats gets all pinned chats for user
func (r *PinnedRepository) GetPinnedChats(ctx context.Context, userID int64) ([]*domain.PinnedChat, error) {
	query := `
		SELECT id, user_id, chat_id, chat_type, position, created_at, updated_at
		FROM pinned_chats
		WHERE user_id = $1
		ORDER BY position ASC
	`

	rows, err := r.db.Query(ctx, query, userID)
	if err != nil {
		r.logger.Error("Failed to get pinned chats", zap.Error(err))
		return nil, fmt.Errorf("get pinned chats: %w", err)
	}
	defer rows.Close()

	var pinnedChats []*domain.PinnedChat
	for rows.Next() {
		pc := &domain.PinnedChat{}
		err := rows.Scan(
			&pc.ID,
			&pc.UserID,
			&pc.ChatID,
			&pc.ChatType,
			&pc.Position,
			&pc.CreatedAt,
			&pc.UpdatedAt,
		)
		if err != nil {
			r.logger.Error("Failed to scan pinned chat", zap.Error(err))
			continue
		}
		pinnedChats = append(pinnedChats, pc)
	}

	return pinnedChats, nil
}

// IsChatPinned checks if chat is pinned
func (r *PinnedRepository) IsChatPinned(ctx context.Context, userID int64, chatID uuid.UUID, chatType string) (bool, error) {
	query := `
		SELECT EXISTS(
			SELECT 1 FROM pinned_chats
			WHERE user_id = $1 AND chat_id = $2 AND chat_type = $3
		)
	`

	var isPinned bool
	err := r.db.QueryRow(ctx, query, userID, chatID, chatType).Scan(&isPinned)
	if err != nil {
		return false, fmt.Errorf("check pinned: %w", err)
	}

	return isPinned, nil
}

// GetPinnedCount gets number of pinned chats for user
func (r *PinnedRepository) GetPinnedCount(ctx context.Context, userID int64) (int, error) {
	query := `
		SELECT COUNT(*)
		FROM pinned_chats
		WHERE user_id = $1
	`

	var count int
	err := r.db.QueryRow(ctx, query, userID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("get pinned count: %w", err)
	}

	return count, nil
}

// PinMessage pins a message in chat
func (r *PinnedRepository) PinMessage(ctx context.Context, chatID uuid.UUID, chatType string, messageID uuid.UUID, pinnedBy int64) error {
	r.logger.Info("Pinning message",
		zap.String("chat_id", chatID.String()),
		zap.String("chat_type", chatType),
		zap.String("message_id", messageID.String()),
		zap.Int64("pinned_by", pinnedBy),
	)

	query := `
		INSERT INTO pinned_messages (id, chat_id, chat_type, message_id, pinned_by, position, created_at)
		VALUES ($1, $2, LOWER(TRIM($3)), $4, $5, 
			(SELECT COALESCE(MAX(position), 0) + 1 FROM pinned_messages WHERE chat_id = $2 AND LOWER(chat_type) = LOWER(TRIM($3))), 
			$6)
		ON CONFLICT (message_id) DO UPDATE SET
			chat_id = EXCLUDED.chat_id,
			chat_type = EXCLUDED.chat_type,
			pinned_by = EXCLUDED.pinned_by,
			created_at = EXCLUDED.created_at
	`

	_, err := r.db.Exec(ctx, query,
		uuid.New(),
		chatID,
		chatType,
		messageID,
		pinnedBy,
		time.Now(),
	)
	if err != nil {
		r.logger.Error("Failed to pin message", zap.Error(err), zap.String("message_id", messageID.String()))
		return fmt.Errorf("pin message: %w", err)
	}

	return nil
}

// UnpinMessage unpins a message
func (r *PinnedRepository) UnpinMessage(ctx context.Context, messageID uuid.UUID) error {
	query := `
		DELETE FROM pinned_messages
		WHERE message_id = $1
	`

	_, err := r.db.Exec(ctx, query, messageID)
	if err != nil {
		r.logger.Error("Failed to unpin message", zap.Error(err))
		return fmt.Errorf("unpin message: %w", err)
	}

	return nil
}

// GetPinnedMessages gets all pinned messages for chat
func (r *PinnedRepository) GetPinnedMessages(ctx context.Context, chatID uuid.UUID, chatType string) ([]*domain.PinnedMessage, error) {
	query := `
		SELECT 
			pm.id, pm.chat_id, pm.chat_type, pm.message_id, pm.pinned_by, pm.position, pm.created_at,
			COALESCE(m.content, cp.content, '') as content,
			COALESCE(m.type, 'text') as type
		FROM pinned_messages pm
		LEFT JOIN messages m ON pm.message_id = m.id AND LOWER(TRIM(pm.chat_type)) != 'channel'
		LEFT JOIN channel_posts cp ON pm.message_id = cp.id AND LOWER(TRIM(pm.chat_type)) = 'channel'
		WHERE pm.chat_id = $1 AND LOWER(TRIM(pm.chat_type)) = LOWER(TRIM($2))
		AND (
			(LOWER(TRIM(pm.chat_type)) != 'channel' AND m.id IS NOT NULL AND m.is_deleted = false) OR
			(LOWER(TRIM(pm.chat_type)) = 'channel' AND cp.id IS NOT NULL AND cp.is_deleted = false)
		)
		ORDER BY pm.position ASC
	`

	rows, err := r.db.Query(ctx, query, chatID, chatType)
	if err != nil {
		r.logger.Error("Failed to get pinned messages", zap.Error(err))
		return nil, fmt.Errorf("get pinned messages: %w", err)
	}
	defer rows.Close()

	var pinnedMessages []*domain.PinnedMessage
	for rows.Next() {
		pm := &domain.PinnedMessage{}
		err := rows.Scan(
			&pm.ID,
			&pm.ChatID,
			&pm.ChatType,
			&pm.MessageID,
			&pm.PinnedBy,
			&pm.Position,
			&pm.CreatedAt,
			&pm.Content,
			&pm.Type,
		)
		if err != nil {
			r.logger.Error("Failed to scan pinned message", zap.Error(err))
			continue
		}
		pinnedMessages = append(pinnedMessages, pm)
	}

	return pinnedMessages, nil
}

// IsMessagePinned checks if message is pinned
func (r *PinnedRepository) IsMessagePinned(ctx context.Context, messageID uuid.UUID) (bool, error) {
	query := `
		SELECT EXISTS(
			SELECT 1 FROM pinned_messages
			WHERE message_id = $1
		)
	`

	var isPinned bool
	err := r.db.QueryRow(ctx, query, messageID).Scan(&isPinned)
	if err != nil {
		return false, fmt.Errorf("check message pinned: %w", err)
	}

	return isPinned, nil
}
