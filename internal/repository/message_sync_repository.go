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

// MessageSyncRepository handles message synchronization operations
type MessageSyncRepository struct {
	db     *pgxpool.Pool
	logger *zap.Logger
}

func NewMessageSyncRepository(db *pgxpool.Pool, logger *zap.Logger) *MessageSyncRepository {
	return &MessageSyncRepository{
		db:     db,
		logger: logger.With(zap.String("repository", "message_sync")),
	}
}

// SyncCursor represents the last synchronized position for a device
type SyncCursor struct {
	ID                 uuid.UUID  `json:"id"`
	UserID             int64      `json:"user_id"`
	DeviceID           string     `json:"device_id"`
	ConversationID     *uuid.UUID `json:"conversation_id,omitempty"`
	LastSyncTimestamp  time.Time  `json:"last_sync_timestamp"`
	LastSequenceNumber int64      `json:"last_sequence_number"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

// GetOrCreateSyncCursor gets or creates a sync cursor for a user device
func (r *MessageSyncRepository) GetOrCreateSyncCursor(ctx context.Context, userID int64, deviceID string, conversationID *uuid.UUID) (*SyncCursor, error) {
	query := `INSERT INTO message_sync_cursors (user_id, device_id, conversation_id, last_sync_timestamp)
		VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING`
	if _, err := r.db.Exec(ctx, query, userID, deviceID, conversationID, time.Now()); err != nil {
		return nil, fmt.Errorf("create sync cursor: %w", err)
	}

	cursor := &SyncCursor{}
	err := r.db.QueryRow(ctx, `
		SELECT id, user_id, device_id, conversation_id,
		       last_sync_timestamp, last_sequence_number, created_at, updated_at
		FROM message_sync_cursors
		WHERE user_id=$1 AND device_id=$2
		  AND (conversation_id=$3 OR (conversation_id IS NULL AND $3 IS NULL))`, userID, deviceID, conversationID).Scan(
		&cursor.ID,
		&cursor.UserID,
		&cursor.DeviceID,
		&cursor.ConversationID,
		&cursor.LastSyncTimestamp,
		&cursor.LastSequenceNumber,
		&cursor.CreatedAt,
		&cursor.UpdatedAt,
	)

	if err != nil {
		r.logger.Error("Failed to get or create sync cursor", zap.Error(err))
		return nil, fmt.Errorf("get or create sync cursor: %w", err)
	}

	return cursor, nil
}

// UpdateSyncCursor acknowledges messages successfully applied by the client.
// GREATEST makes retries and out-of-order acknowledgements idempotent.
func (r *MessageSyncRepository) UpdateSyncCursor(ctx context.Context, userID int64, deviceID string, conversationID *uuid.UUID, sequence int64) error {
	query := `
		UPDATE message_sync_cursors
		SET last_sequence_number = GREATEST(last_sequence_number, $1), last_sync_timestamp = $2
		WHERE user_id = $3 AND device_id = $4 AND (conversation_id = $5 OR (conversation_id IS NULL AND $5 IS NULL))
	`

	result, err := r.db.Exec(ctx, query, sequence, time.Now(), userID, deviceID, conversationID)
	if err != nil {
		r.logger.Error("Failed to update sync cursor", zap.Error(err))
		return fmt.Errorf("update sync cursor: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("sync cursor not found")
	}

	return nil
}

// GetMissedMessages retrieves messages that were missed since the last sync
func (r *MessageSyncRepository) GetMissedMessages(ctx context.Context, userID int64, conversationID *uuid.UUID, afterSequence int64, limit int) ([]*domain.MessageView, error) {
	// Build query based on whether we're syncing a specific conversation or all conversations
	var query string
	var args []interface{}

	if conversationID != nil {
		// Conversation-specific sync
		query = r.buildConversationSyncQuery()
		args = []interface{}{*conversationID, userID}

		args = append(args, afterSequence)
		args = append(args, limit)
	} else {
		// Global sync - all conversations the user has access to
		query = r.buildGlobalSyncQuery()
		args = []interface{}{userID}

		args = append(args, afterSequence)
		args = append(args, limit)
	}

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		r.logger.Error("Failed to query missed messages", zap.Error(err))
		return nil, fmt.Errorf("query missed messages: %w", err)
	}
	defer rows.Close()

	messages := make([]*domain.MessageView, 0)
	for rows.Next() {
		msg := &domain.MessageView{}
		var replyID, replyConversationID, replyReplyToID *uuid.UUID
		var replySenderID *int64
		var replyContent, replyType, replyAttachmentURL, replyAttachmentType, replyAttachmentName, replyMetadata *string
		var replyEncryptedKeys map[string]string
		var replyAttachmentSize *int64
		var replyIsEdited, replyIsDeleted *bool
		var replyCreatedAt, replyUpdatedAt, replyEditedAt *time.Time
		var replySenderUsername, replySenderFullName, replySenderAvatar *string
		var sequenceNumber int64

		err := rows.Scan(
			&msg.ID,
			&msg.ConversationID,
			&msg.SenderID,
			&msg.ReplyToID,
			&msg.Content,
			&msg.ContentHash,
			&msg.EncryptedKeys,
			&msg.Type,
			&msg.AttachmentURL,
			&msg.AttachmentType,
			&msg.AttachmentSize,
			&msg.AttachmentName,
			&msg.Metadata,
			&msg.IsEdited,
			&msg.IsForwarded,
			&msg.OriginalSenderID,
			&msg.IsDeleted,
			&msg.CreatedAt,
			&msg.UpdatedAt,
			&msg.EditedAt,
			&msg.IsEphemeral,
			&msg.TTLSeconds,
			&msg.ExpiresAt,
			&msg.SenderUsername,
			&msg.SenderFullName,
			&msg.SenderAvatar,
			&msg.OriginalSenderFullName,
			&sequenceNumber,
			// Reply message
			&replyID,
			&replyConversationID,
			&replySenderID,
			&replyReplyToID,
			&replyContent,
			&replyEncryptedKeys,
			&replyType,
			&replyAttachmentURL,
			&replyAttachmentType,
			&replyAttachmentSize,
			&replyAttachmentName,
			&replyMetadata,
			&replyIsEdited,
			&replyIsDeleted,
			&replyCreatedAt,
			&replyUpdatedAt,
			&replyEditedAt,
			&replySenderUsername,
			&replySenderFullName,
			&replySenderAvatar,
		)

		if err != nil {
			r.logger.Error("Failed to scan missed message", zap.Error(err))
			continue
		}

		msg.SequenceNumber = sequenceNumber

		// Build reply_to message if exists
		if replyID != nil {
			msg.ReplyTo = &domain.MessageView{
				Message: domain.Message{
					ID:             *replyID,
					ConversationID: *replyConversationID,
					SenderID:       *replySenderID,
					ReplyToID:      replyReplyToID,
					Content:        replyContent,
					EncryptedKeys:  replyEncryptedKeys,
					Type:           *replyType,
					AttachmentURL:  replyAttachmentURL,
					AttachmentType: replyAttachmentType,
					AttachmentSize: replyAttachmentSize,
					AttachmentName: replyAttachmentName,
					Metadata:       replyMetadata,
					IsEdited:       *replyIsEdited,
					IsDeleted:      *replyIsDeleted,
					CreatedAt:      *replyCreatedAt,
					UpdatedAt:      *replyUpdatedAt,
					EditedAt:       replyEditedAt,
				},
				SenderUsername: *replySenderUsername,
				SenderFullName: *replySenderFullName,
				SenderAvatar:   replySenderAvatar,
			}
		}

		messages = append(messages, msg)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate missed messages: %w", err)
	}

	r.logger.Info("Retrieved missed messages",
		zap.Int64("user_id", userID),
		zap.Int64("after_sequence", afterSequence),
		zap.Int("count", len(messages)),
	)

	return messages, nil
}

func (r *MessageSyncRepository) buildConversationSyncQuery() string {
	return `
		SELECT
			m.id, m.conversation_id, m.sender_id, m.reply_to_id, m.content, m.content_hash, m.encrypted_keys, m.type,
			m.attachment_url, m.attachment_type, m.attachment_size, m.attachment_name, m.metadata,
			m.is_edited, m.is_forwarded, m.original_sender_id, m.is_deleted, m.created_at, m.updated_at, m.edited_at,
			m.is_ephemeral, m.ttl_seconds, m.expires_at,
			u.username as sender_username,
			u.first_name || ' ' || u.last_name as sender_full_name,
			u.avatar_url as sender_avatar,
			ou.first_name || ' ' || ou.last_name as original_sender_full_name,
			m.sequence_number,
			-- Reply message data
			rm.id as reply_id,
			rm.conversation_id as reply_conversation_id,
			rm.sender_id as reply_sender_id,
			rm.reply_to_id as reply_reply_to_id,
			rm.content as reply_content,
			rm.encrypted_keys as reply_encrypted_keys,
			rm.type as reply_type,
			rm.attachment_url as reply_attachment_url,
			rm.attachment_type as reply_attachment_type,
			rm.attachment_size as reply_attachment_size,
			rm.attachment_name as reply_attachment_name,
			rm.metadata as reply_metadata,
			rm.is_edited as reply_is_edited,
			rm.is_deleted as reply_is_deleted,
			rm.created_at as reply_created_at,
			rm.updated_at as reply_updated_at,
			rm.edited_at as reply_edited_at,
			ru.username as reply_sender_username,
			ru.first_name || ' ' || ru.last_name as reply_sender_full_name,
			ru.avatar_url as reply_sender_avatar
		FROM messages m
		INNER JOIN users u ON m.sender_id = u.id
		LEFT JOIN users ou ON m.original_sender_id = ou.id
		LEFT JOIN messages rm ON m.reply_to_id = rm.id AND rm.is_deleted = false
		LEFT JOIN users ru ON rm.sender_id = ru.id
		LEFT JOIN deleted_messages dm ON m.id = dm.message_id AND dm.user_id = $2
		WHERE m.conversation_id = $1
			AND m.is_deleted = false
			AND dm.message_id IS NULL
			AND m.sequence_number > $3
			AND (
				EXISTS (
					SELECT 1 FROM conversation_participants cp
					WHERE cp.conversation_id = m.conversation_id
						AND cp.user_id = $2
						AND cp.left_at IS NULL
						AND m.created_at >= cp.joined_at
				)
				OR EXISTS (
					SELECT 1 FROM group_members gm
					WHERE gm.group_id = m.conversation_id
						AND gm.user_id = $2
						AND gm.left_at IS NULL
						AND m.created_at >= gm.joined_at
				)
			)
		ORDER BY m.sequence_number ASC
		LIMIT $4
	`
}

func (r *MessageSyncRepository) buildGlobalSyncQuery() string {
	return `
		SELECT
			m.id, m.conversation_id, m.sender_id, m.reply_to_id, m.content, m.content_hash, m.encrypted_keys, m.type,
			m.attachment_url, m.attachment_type, m.attachment_size, m.attachment_name, m.metadata,
			m.is_edited, m.is_forwarded, m.original_sender_id, m.is_deleted, m.created_at, m.updated_at, m.edited_at,
			m.is_ephemeral, m.ttl_seconds, m.expires_at,
			u.username as sender_username,
			u.first_name || ' ' || u.last_name as sender_full_name,
			u.avatar_url as sender_avatar,
			ou.first_name || ' ' || ou.last_name as original_sender_full_name,
			m.sequence_number,
			-- Reply message data
			rm.id as reply_id,
			rm.conversation_id as reply_conversation_id,
			rm.sender_id as reply_sender_id,
			rm.reply_to_id as reply_reply_to_id,
			rm.content as reply_content,
			rm.encrypted_keys as reply_encrypted_keys,
			rm.type as reply_type,
			rm.attachment_url as reply_attachment_url,
			rm.attachment_type as reply_attachment_type,
			rm.attachment_size as reply_attachment_size,
			rm.attachment_name as reply_attachment_name,
			rm.metadata as reply_metadata,
			rm.is_edited as reply_is_edited,
			rm.is_deleted as reply_is_deleted,
			rm.created_at as reply_created_at,
			rm.updated_at as reply_updated_at,
			rm.edited_at as reply_edited_at,
			ru.username as reply_sender_username,
			ru.first_name || ' ' || ru.last_name as reply_sender_full_name,
			ru.avatar_url as reply_sender_avatar
		FROM messages m
		INNER JOIN users u ON m.sender_id = u.id
		LEFT JOIN users ou ON m.original_sender_id = ou.id
		LEFT JOIN messages rm ON m.reply_to_id = rm.id AND rm.is_deleted = false
		LEFT JOIN users ru ON rm.sender_id = ru.id
		LEFT JOIN deleted_messages dm ON m.id = dm.message_id AND dm.user_id = $1
		WHERE m.is_deleted = false
			AND dm.message_id IS NULL
			AND m.sequence_number > $2
			AND (
				EXISTS (
					SELECT 1 FROM conversation_participants cp
					WHERE cp.conversation_id = m.conversation_id
						AND cp.user_id = $1
						AND cp.left_at IS NULL
						AND m.created_at >= cp.joined_at
				)
				OR EXISTS (
					SELECT 1 FROM group_members gm
					WHERE gm.group_id = m.conversation_id
						AND gm.user_id = $1
						AND gm.left_at IS NULL
						AND m.created_at >= gm.joined_at
				)
			)
		ORDER BY m.sequence_number ASC
		LIMIT $3
	`
}

// CleanupOldCursors removes sync cursors that haven't been updated in a while
func (r *MessageSyncRepository) CleanupOldCursors(ctx context.Context, olderThan time.Duration) (int64, error) {
	query := `
		DELETE FROM message_sync_cursors
		WHERE updated_at < $1
	`

	result, err := r.db.Exec(ctx, query, time.Now().Add(-olderThan))
	if err != nil {
		r.logger.Error("Failed to cleanup old cursors", zap.Error(err))
		return 0, fmt.Errorf("cleanup old cursors: %w", err)
	}

	deleted := result.RowsAffected()
	if deleted > 0 {
		r.logger.Info("Cleaned up old sync cursors", zap.Int64("count", deleted))
	}

	return deleted, nil
}
