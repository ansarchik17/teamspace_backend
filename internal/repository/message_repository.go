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

type MessageRepository struct {
	db     *pgxpool.Pool
	logger *zap.Logger
}

func NewMessageRepository(db *pgxpool.Pool, logger *zap.Logger) *MessageRepository {
	return &MessageRepository{
		db:     db,
		logger: logger.With(zap.String("repository", "message")),
	}
}

// Create creates a new message
func (r *MessageRepository) Create(ctx context.Context, message *domain.Message) error {
	query := `
       INSERT INTO messages (
          id, conversation_id, sender_id, reply_to_id, content, content_hash, encrypted_keys, type,
          attachment_url, attachment_type, attachment_size, attachment_name, metadata,
          is_edited, is_forwarded, original_sender_id, is_deleted, created_at, updated_at,
          is_ephemeral, ttl_seconds, expires_at
       )
       VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22)
       RETURNING id
    `

	if message.ID == uuid.Nil {
		message.ID = uuid.New()
	}
	if message.CreatedAt.IsZero() {
		message.CreatedAt = time.Now()
	}
	message.UpdatedAt = message.CreatedAt

	if message.EncryptedKeys == nil {
		message.EncryptedKeys = make(map[string]string)
	}

	err := r.db.QueryRow(ctx, query,
		message.ID,
		message.ConversationID,
		message.SenderID,
		message.ReplyToID,
		message.Content,
		message.ContentHash,
		message.EncryptedKeys,
		message.Type,
		message.AttachmentURL,
		message.AttachmentType,
		message.AttachmentSize,
		message.AttachmentName,
		message.Metadata,
		message.IsEdited,
		message.IsForwarded,
		message.OriginalSenderID,
		message.IsDeleted,
		message.CreatedAt,
		message.UpdatedAt,
		message.IsEphemeral,
		message.TTLSeconds,
		message.ExpiresAt,
	).Scan(&message.ID)

	if err != nil {
		r.logger.Error("Failed to create message", zap.Error(err))
		return fmt.Errorf("create message: %w", err)
	}

	r.logger.Info("Message created", zap.String("message_id", message.ID.String()))
	return nil
}

// GetByID gets message by ID
func (r *MessageRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.MessageView, error) {
	query := `
       SELECT 
          m.id, m.conversation_id, m.sender_id, m.reply_to_id, m.content, m.content_hash, m.encrypted_keys, m.type,
          m.attachment_url, m.attachment_type, m.attachment_size, m.attachment_name, m.metadata,
          m.is_edited, m.is_forwarded, m.original_sender_id, m.is_deleted, m.created_at, m.updated_at, m.edited_at,
          m.is_ephemeral, m.ttl_seconds, m.expires_at,
          u.username as sender_username,
          u.first_name || ' ' || u.last_name as sender_full_name,
          u.avatar_url as sender_avatar,
          ou.first_name || ' ' || ou.last_name as original_sender_full_name,
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
          -- Reply sender data
          ru.username as reply_sender_username,
          ru.first_name || ' ' || ru.last_name as reply_sender_full_name,
          ru.avatar_url as reply_sender_avatar
       FROM messages m
       INNER JOIN users u ON m.sender_id = u.id
       LEFT JOIN users ou ON m.original_sender_id = ou.id
       LEFT JOIN messages rm ON m.reply_to_id = rm.id AND rm.is_deleted = false
       LEFT JOIN users ru ON rm.sender_id = ru.id
       WHERE m.id = $1 AND m.is_deleted = false
    `

	msg := &domain.MessageView{}
	var replyID, replyConversationID, replyReplyToID *uuid.UUID
	var replySenderID *int64
	var replyContent, replyType, replyAttachmentURL, replyAttachmentType, replyAttachmentName, replyMetadata *string
	var replyEncryptedKeys map[string]string
	var replyAttachmentSize *int64
	var replyIsEdited, replyIsDeleted *bool
	var replyCreatedAt, replyUpdatedAt, replyEditedAt *time.Time
	var replySenderUsername, replySenderFullName, replySenderAvatar *string

	err := r.db.QueryRow(ctx, query, id).Scan(
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
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("message not found")
		}
		r.logger.Error("Failed to get message", zap.Error(err))
		return nil, fmt.Errorf("get message: %w", err)
	}

	// Если есть reply_to message, собираем его
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
			SenderAvatar:   fileurl.SignLegacyDownloadURL(replySenderAvatar),
		}
	}
	signMessageView(msg)

	return msg, nil
}

// GetByIDs gets multiple messages by their IDs
func (r *MessageRepository) GetByIDs(ctx context.Context, ids []uuid.UUID) ([]*domain.MessageView, error) {
	if len(ids) == 0 {
		return make([]*domain.MessageView, 0), nil
	}

	query := `
       SELECT 
          m.id, m.conversation_id, m.sender_id, m.reply_to_id, m.content, m.content_hash, m.encrypted_keys, m.type,
          m.attachment_url, m.attachment_type, m.attachment_size, m.attachment_name, m.metadata,
          m.is_edited, m.is_forwarded, m.original_sender_id, m.is_deleted, m.created_at, m.updated_at, m.edited_at,
          m.is_ephemeral, m.ttl_seconds, m.expires_at,
          u.username as sender_username,
          u.first_name || ' ' || u.last_name as sender_full_name,
          u.avatar_url as sender_avatar,
          ou.first_name || ' ' || ou.last_name as original_sender_full_name,
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
          -- Reply sender data
          ru.username as reply_sender_username,
          ru.first_name || ' ' || ru.last_name as reply_sender_full_name,
          ru.avatar_url as reply_sender_avatar
       FROM messages m
       INNER JOIN users u ON m.sender_id = u.id
       LEFT JOIN users ou ON m.original_sender_id = ou.id
       LEFT JOIN messages rm ON m.reply_to_id = rm.id AND rm.is_deleted = false
       LEFT JOIN users ru ON rm.sender_id = ru.id
       WHERE m.id = ANY($1) AND m.is_deleted = false
    `

	rows, err := r.db.Query(ctx, query, ids)
	if err != nil {
		r.logger.Error("Failed to get messages by IDs", zap.Error(err))
		return nil, fmt.Errorf("get messages by IDs: %w", err)
	}
	defer rows.Close()

	var messages []*domain.MessageView
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
			r.logger.Error("Failed to scan message", zap.Error(err))
			continue
		}

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
				SenderAvatar:   fileurl.SignLegacyDownloadURL(replySenderAvatar),
			}
		}
		signMessageView(msg)
		messages = append(messages, msg)
	}

	return messages, nil
}

// IsVisibleToUser applies the same membership and history boundaries as history.
func (r *MessageRepository) IsVisibleToUser(ctx context.Context, messageID uuid.UUID, userID int64) (bool, error) {
	var visible bool
	err := r.db.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM messages m WHERE m.id = $1 AND NOT m.is_deleted
		AND NOT EXISTS (SELECT 1 FROM deleted_messages dm WHERE dm.message_id = m.id AND dm.user_id = $2)
		AND (
			EXISTS (SELECT 1 FROM conversation_participants cp WHERE cp.conversation_id = m.conversation_id AND cp.user_id = $2 AND cp.left_at IS NULL
				AND m.created_at >= GREATEST(cp.joined_at, COALESCE(cp.last_cleared_at, '-infinity'::timestamptz)))
			OR EXISTS (SELECT 1 FROM group_members gm WHERE gm.group_id = m.conversation_id AND gm.user_id = $2 AND gm.left_at IS NULL
				AND m.created_at >= GREATEST(gm.joined_at, COALESCE(gm.last_cleared_at, '-infinity'::timestamptz)))
		))`, messageID, userID).Scan(&visible)
	return visible, err
}

// GetReplyDescendants returns visible descendants, with one extra row for truncation.
func (r *MessageRepository) GetReplyDescendants(ctx context.Context, rootID uuid.UUID, userID int64, limit int) ([]*domain.MessageView, bool, error) {
	if limit <= 0 {
		return make([]*domain.MessageView, 0), false, nil
	}

	rows, err := r.db.Query(ctx, `
		WITH RECURSIVE visible_messages AS (
			SELECT m.* FROM messages m
			WHERE m.conversation_id = (SELECT conversation_id FROM messages WHERE id = $1)
			AND NOT m.is_deleted
			AND NOT EXISTS (SELECT 1 FROM deleted_messages dm WHERE dm.message_id = m.id AND dm.user_id = $2)
			AND (
				EXISTS (SELECT 1 FROM conversation_participants cp WHERE cp.conversation_id = m.conversation_id AND cp.user_id = $2 AND cp.left_at IS NULL
					AND m.created_at >= GREATEST(cp.joined_at, COALESCE(cp.last_cleared_at, '-infinity'::timestamptz)))
				OR EXISTS (SELECT 1 FROM group_members gm WHERE gm.group_id = m.conversation_id AND gm.user_id = $2 AND gm.left_at IS NULL
					AND m.created_at >= GREATEST(gm.joined_at, COALESCE(gm.last_cleared_at, '-infinity'::timestamptz)))
			)
		), reply_tree AS (
			SELECT m.id, m.reply_to_id, m.created_at, 1 AS depth, ARRAY[m.id] AS path
			FROM visible_messages m
			LEFT JOIN deleted_messages dm ON dm.message_id = m.id AND dm.user_id = $2
			WHERE m.reply_to_id = $1
			  AND m.is_deleted = false
			  AND dm.message_id IS NULL

			UNION ALL

			SELECT child.id, child.reply_to_id, child.created_at, parent.depth + 1, parent.path || child.id
			FROM visible_messages child
			JOIN reply_tree parent ON child.reply_to_id = parent.id
			LEFT JOIN deleted_messages dm ON dm.message_id = child.id AND dm.user_id = $2
			WHERE child.is_deleted = false
			  AND dm.message_id IS NULL
			  AND NOT child.id = ANY(parent.path)
		)
		SELECT id
		FROM reply_tree
		ORDER BY depth ASC, created_at ASC, id ASC
		LIMIT $3
	`, rootID, userID, limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("get reply descendant ids: %w", err)
	}
	defer rows.Close()

	ids := make([]uuid.UUID, 0, limit+1)
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, false, fmt.Errorf("scan reply descendant id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate reply descendant ids: %w", err)
	}

	truncated := len(ids) > limit
	if truncated {
		ids = ids[:limit]
	}
	messages, err := r.GetByIDs(ctx, ids)
	if err != nil {
		return nil, false, err
	}
	return messages, truncated, nil
}

func (r *MessageRepository) IsDeletedForUser(ctx context.Context, messageID uuid.UUID, userID int64) (bool, error) {
	var deleted bool
	err := r.db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM deleted_messages
			WHERE message_id = $1 AND user_id = $2
		)
	`, messageID, userID).Scan(&deleted)
	if err != nil {
		return false, fmt.Errorf("check message deleted for user: %w", err)
	}
	return deleted, nil
}

// GetConversationMessages gets messages for a conversation
func (r *MessageRepository) GetConversationMessages(ctx context.Context, conversationID uuid.UUID, userID int64, limit, offset int) ([]*domain.MessageView, error) {
	query := `
       SELECT 
          m.id, m.conversation_id, m.sender_id, m.reply_to_id, m.content, m.content_hash, m.encrypted_keys, m.type,
          m.attachment_url, m.attachment_type, m.attachment_size, m.attachment_name, m.metadata,
          m.is_edited, m.is_forwarded, m.original_sender_id, m.is_deleted, m.created_at, m.updated_at, m.edited_at,
          m.is_ephemeral, m.ttl_seconds, m.expires_at,
          u.username as sender_username,
          u.first_name || ' ' || u.last_name as sender_full_name,
          u.avatar_url as sender_avatar,
          ou.first_name || ' ' || ou.last_name as original_sender_full_name,
          COALESCE((SELECT COUNT(*) FROM message_status ms WHERE ms.message_id = m.id AND ms.read_at IS NOT NULL), 0) as read_count,
          COALESCE((SELECT COUNT(*) FROM message_status ms WHERE ms.message_id = m.id AND ms.delivered_at IS NOT NULL), 0) as delivered_count,
          EXISTS (SELECT 1 FROM message_status ms WHERE ms.message_id = m.id AND ms.read_at IS NOT NULL) as is_read,
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
          -- Reply sender data
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
       ORDER BY m.created_at DESC
       LIMIT $3 OFFSET $4
    `

	rows, err := r.db.Query(ctx, query, conversationID, userID, limit, offset)
	if err != nil {
		r.logger.Error("Failed to get conversation messages", zap.Error(err))
		return nil, fmt.Errorf("get conversation messages: %w", err)
	}
	defer rows.Close()

	var messages []*domain.MessageView
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
			&msg.ReadByCount,
			&msg.DeliveredCount,
			&msg.IsRead,
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
			r.logger.Error("Failed to scan message", zap.Error(err))
			continue
		}

		// Если есть reply_to message, собираем его
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
				SenderAvatar:   fileurl.SignLegacyDownloadURL(replySenderAvatar),
			}
		}
		signMessageView(msg)

		messages = append(messages, msg)
	}

	return messages, nil
}

// GetConversationSharedMedia returns paginated image/video attachments for a conversation.
func (r *MessageRepository) GetConversationSharedMedia(ctx context.Context, conversationID uuid.UUID, userID int64, limit, offset int, lastClearedAt *time.Time) ([]domain.SharedMediaItem, int, error) {
	countQuery := `
		SELECT COUNT(*)
		FROM messages m
		LEFT JOIN deleted_messages dm ON dm.message_id = m.id AND dm.user_id = $2
		WHERE m.conversation_id = $1
		  AND m.is_deleted = false
		  AND dm.message_id IS NULL
		  AND m.type IN ('image', 'video')
		  AND m.attachment_url IS NOT NULL
		  AND ($3::timestamp IS NULL OR m.created_at > $3)
	`

	var total int
	if err := r.db.QueryRow(ctx, countQuery, conversationID, userID, lastClearedAt).Scan(&total); err != nil {
		r.logger.Error("Failed to count conversation shared media", zap.Error(err))
		return nil, 0, fmt.Errorf("count conversation shared media: %w", err)
	}

	query := `
		SELECT
			m.id,
			m.sender_id,
			COALESCE(NULLIF(TRIM(u.first_name || ' ' || u.last_name), ''), u.username) AS sender_name,
			m.created_at AS sent_at,
			m.type,
			m.attachment_url,
			m.attachment_type,
			m.attachment_size,
			m.encrypted_keys
		FROM messages m
		INNER JOIN users u ON u.id = m.sender_id
		LEFT JOIN deleted_messages dm ON dm.message_id = m.id AND dm.user_id = $2
		WHERE m.conversation_id = $1
		  AND m.is_deleted = false
		  AND dm.message_id IS NULL
		  AND m.type IN ('image', 'video')
		  AND m.attachment_url IS NOT NULL
		  AND ($3::timestamp IS NULL OR m.created_at > $3)
		ORDER BY m.created_at DESC
		LIMIT $4 OFFSET $5
	`

	rows, err := r.db.Query(ctx, query, conversationID, userID, lastClearedAt, limit, offset)
	if err != nil {
		r.logger.Error("Failed to get conversation shared media", zap.Error(err))
		return nil, 0, fmt.Errorf("get conversation shared media: %w", err)
	}
	defer rows.Close()

	items := make([]domain.SharedMediaItem, 0, limit)
	for rows.Next() {
		var item domain.SharedMediaItem
		if err := rows.Scan(
			&item.MessageID,
			&item.SenderID,
			&item.SenderName,
			&item.SentAt,
			&item.Type,
			&item.AttachmentURL,
			&item.AttachmentType,
			&item.AttachmentSize,
			&item.EncryptedKeys,
		); err != nil {
			r.logger.Error("Failed to scan shared media row", zap.Error(err))
			return nil, 0, fmt.Errorf("scan conversation shared media: %w", err)
		}
		if item.EncryptedKeys == nil {
			item.EncryptedKeys = map[string]string{}
		}
		item.AttachmentURL = fileurl.StringValue(fileurl.SignLegacyDownloadURL(&item.AttachmentURL), item.AttachmentURL)
		items = append(items, item)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate conversation shared media: %w", err)
	}

	return items, total, nil
}

func signMessageView(msg *domain.MessageView) {
	if msg == nil {
		return
	}
	msg.SenderAvatar = fileurl.SignLegacyDownloadURL(msg.SenderAvatar)
	msg.AttachmentURL = fileurl.SignLegacyDownloadURL(msg.AttachmentURL)
	if msg.ReplyTo != nil {
		msg.ReplyTo.SenderAvatar = fileurl.SignLegacyDownloadURL(msg.ReplyTo.SenderAvatar)
		msg.ReplyTo.AttachmentURL = fileurl.SignLegacyDownloadURL(msg.ReplyTo.AttachmentURL)
	}
}

func (r *MessageRepository) GetEquivalentDeviceIDs(ctx context.Context, userID int64, currentDeviceID string) ([]string, error) {
	rows, err := r.db.Query(ctx, `
		SELECT alias.device_id
		FROM user_public_keys current_key
		INNER JOIN user_public_keys alias
		        ON alias.user_id = current_key.user_id
		       AND alias.public_key = current_key.public_key
		WHERE current_key.user_id = $1
		  AND current_key.device_id = $2
		  AND alias.device_id <> $2
		ORDER BY alias.created_at ASC, alias.device_id ASC
	`, userID, currentDeviceID)
	if err != nil {
		return nil, fmt.Errorf("get equivalent device ids: %w", err)
	}
	defer rows.Close()

	aliases := make([]string, 0)
	for rows.Next() {
		var deviceID string
		if scanErr := rows.Scan(&deviceID); scanErr != nil {
			return nil, fmt.Errorf("scan equivalent device id: %w", scanErr)
		}
		aliases = append(aliases, deviceID)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate equivalent device ids: %w", err)
	}

	return aliases, nil
}

// Update updates a message
func (r *MessageRepository) Update(ctx context.Context, message *domain.Message) error {
	query := `
       UPDATE messages
       SET content = $1, is_edited = $2, updated_at = $3, edited_at = $4
       WHERE id = $5 AND is_deleted = false
    `

	now := time.Now()
	message.UpdatedAt = now
	editedAt := now
	message.EditedAt = &editedAt
	message.IsEdited = true

	result, err := r.db.Exec(ctx, query,
		message.Content,
		message.IsEdited,
		message.UpdatedAt,
		message.EditedAt,
		message.ID,
	)

	if err != nil {
		r.logger.Error("Failed to update message", zap.Error(err))
		return fmt.Errorf("update message: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("message not found")
	}

	return nil
}

// Delete soft deletes a message
func (r *MessageRepository) Delete(ctx context.Context, messageID uuid.UUID) error {
	query := `
       UPDATE messages
       SET is_deleted = true, deleted_at = $1
       WHERE id = $2
    `

	_, err := r.db.Exec(ctx, query, time.Now(), messageID)
	if err != nil {
		r.logger.Error("Failed to delete message", zap.Error(err))
		return fmt.Errorf("delete message: %w", err)
	}

	return nil
}

// DeleteMessages soft deletes multiple messages
func (r *MessageRepository) DeleteMessages(ctx context.Context, messageIDs []uuid.UUID) error {
	query := `
       UPDATE messages
       SET is_deleted = true, deleted_at = $1
       WHERE id = ANY($2)
    `

	result, err := r.db.Exec(ctx, query, time.Now(), messageIDs)
	if err != nil {
		r.logger.Error("Failed to delete messages", zap.Error(err))
		return fmt.Errorf("delete messages: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("no messages found to delete")
	}

	return nil
}

// DeleteMessagesForUser deletes messages for a specific user (hidden from them)
func (r *MessageRepository) DeleteMessagesForUser(ctx context.Context, userID int64, messageIDs []uuid.UUID) error {
	if len(messageIDs) == 0 {
		return nil
	}

	query := `
       INSERT INTO deleted_messages (user_id, message_id)
       SELECT $1, unnest($2::uuid[])
       ON CONFLICT (user_id, message_id) DO NOTHING
    `

	_, err := r.db.Exec(ctx, query, userID, messageIDs)
	if err != nil {
		r.logger.Error("Failed to delete messages for user", zap.Error(err))
		return fmt.Errorf("delete messages for user: %w", err)
	}

	return nil
}

// DeleteExpiredMessages soft deletes ephemeral messages whose expires_at time has passed
func (r *MessageRepository) DeleteExpiredMessages(ctx context.Context) ([]struct {
	ID             uuid.UUID
	ConversationID uuid.UUID
}, error) {
	query := `
		WITH expired AS (
			SELECT id 
			FROM messages
			WHERE is_ephemeral = true 
			  AND expires_at IS NOT NULL 
			  AND expires_at <= NOW()
			  AND is_deleted = false
			ORDER BY expires_at ASC
			LIMIT 1000
			FOR UPDATE SKIP LOCKED
		)
		DELETE FROM messages m
		USING expired e
		WHERE m.id = e.id
		RETURNING m.id, m.conversation_id
	`
	rows, err := r.db.Query(ctx, query)
	if err != nil {
		r.logger.Error("Failed to delete expired messages", zap.Error(err))
		return nil, fmt.Errorf("delete expired messages: %w", err)
	}
	defer rows.Close()

	var deleted []struct {
		ID             uuid.UUID
		ConversationID uuid.UUID
	}
	for rows.Next() {
		var item struct {
			ID             uuid.UUID
			ConversationID uuid.UUID
		}
		if err := rows.Scan(&item.ID, &item.ConversationID); err == nil {
			deleted = append(deleted, item)
		}
	}

	return deleted, nil
}

// CreateMessageStatus creates message status for a user
func (r *MessageRepository) CreateMessageStatus(ctx context.Context, messageID uuid.UUID, userID int64) error {
	query := `
       INSERT INTO message_status (id, message_id, user_id, created_at)
       VALUES ($1, $2, $3, $4)
       ON CONFLICT (message_id, user_id) DO NOTHING
    `

	_, err := r.db.Exec(ctx, query, uuid.New(), messageID, userID, time.Now())
	if err != nil {
		r.logger.Error("Failed to create message status", zap.Error(err))
		return fmt.Errorf("create message status: %w", err)
	}

	return nil
}

// BulkCreateMessageStatus creates message_status rows for all recipients of a message.
func (r *MessageRepository) BulkCreateMessageStatus(ctx context.Context, messageID uuid.UUID, recipientIDs []int64) error {
	if len(recipientIDs) == 0 {
		return nil
	}
	_, err := r.db.Exec(ctx, `
		INSERT INTO message_status (id, message_id, user_id, created_at)
		SELECT gen_random_uuid(), $1, recipient_id, $3
		FROM unnest($2::bigint[]) AS recipient_id
		ON CONFLICT (message_id, user_id) DO NOTHING
	`, messageID, recipientIDs, time.Now())
	if err != nil {
		return fmt.Errorf("bulk create message status: %w", err)
	}
	return nil
}

// MarkAsDelivered marks message as delivered for user
func (r *MessageRepository) MarkAsDelivered(ctx context.Context, messageID uuid.UUID, userID int64) (bool, error) {
	var changed bool
	err := r.db.QueryRow(ctx, `
		INSERT INTO message_status (id, message_id, user_id, delivered_at, created_at)
		VALUES ($1, $2, $3, $4, $4)
		ON CONFLICT (message_id, user_id) DO UPDATE
		SET delivered_at = EXCLUDED.delivered_at
		WHERE message_status.delivered_at IS NULL
		RETURNING TRUE
	`, uuid.New(), messageID, userID, time.Now()).Scan(&changed)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		r.logger.Error("Failed to mark as delivered", zap.Error(err))
		return false, fmt.Errorf("mark as delivered: %w", err)
	}
	return changed, nil
}

type ReadStatusUpdate struct {
	MessageID uuid.UUID
	SenderID  int64
}

// MarkAsRead marks message as read for user
func (r *MessageRepository) MarkAsRead(ctx context.Context, messageID uuid.UUID, userID int64) error {
	query := `
       UPDATE message_status
       SET read_at = $1, delivered_at = COALESCE(delivered_at, $1)
       WHERE message_id = $2 AND user_id = $3
    `

	_, err := r.db.Exec(ctx, query, time.Now(), messageID, userID)
	if err != nil {
		r.logger.Error("Failed to mark as read", zap.Error(err))
		return fmt.Errorf("mark as read: %w", err)
	}

	// Trigger ephemeral countdown
	ephemeralQuery := `
		UPDATE messages
		SET expires_at = $1::timestamptz + (ttl_seconds * INTERVAL '1 second')
		WHERE id = $2 AND is_ephemeral = true AND expires_at IS NULL AND sender_id != $3
	`
	_, err = r.db.Exec(ctx, ephemeralQuery, time.Now(), messageID, userID)
	if err != nil {
		r.logger.Warn("Failed to trigger ephemeral countdown", zap.Error(err))
	}

	return nil
}

// MarkMessagesAsRead marks all messages as read in a conversation up to a specific message
func (r *MessageRepository) MarkMessagesAsRead(ctx context.Context, conversationID uuid.UUID, userID int64, lastMessageID uuid.UUID) ([]ReadStatusUpdate, error) {
	var lastMessageCreatedAt time.Time
	err := r.db.QueryRow(ctx, "SELECT created_at FROM messages WHERE id = $1", lastMessageID).Scan(&lastMessageCreatedAt)
	if err != nil {
		return nil, fmt.Errorf("get last message time: %w", err)
	}

	query := `
	   WITH marked AS (
	      INSERT INTO message_status (id, message_id, user_id, read_at, delivered_at, created_at)
	      SELECT gen_random_uuid(), m.id, $2, $4, $4, $4
	      FROM messages m
	      WHERE m.conversation_id = $1
	        AND m.created_at <= $3
	        AND m.sender_id != $2
	        AND m.is_deleted = FALSE
	      ON CONFLICT (message_id, user_id)
	      DO UPDATE SET
	         read_at = EXCLUDED.read_at,
	         delivered_at = COALESCE(message_status.delivered_at, EXCLUDED.delivered_at)
	      WHERE message_status.read_at IS NULL
	      RETURNING message_id
	   )
	   SELECT m.id, m.sender_id
	   FROM marked
	   JOIN messages m ON m.id = marked.message_id
	   ORDER BY m.created_at
    `

	now := time.Now()
	rows, err := r.db.Query(ctx, query, conversationID, userID, lastMessageCreatedAt, now)
	if err != nil {
		r.logger.Error("Failed to mark messages as read", zap.Error(err))
		return nil, fmt.Errorf("mark messages as read: %w", err)
	}
	defer rows.Close()
	updates := make([]ReadStatusUpdate, 0)
	for rows.Next() {
		var update ReadStatusUpdate
		if err := rows.Scan(&update.MessageID, &update.SenderID); err != nil {
			return nil, fmt.Errorf("scan read status update: %w", err)
		}
		updates = append(updates, update)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate read status updates: %w", err)
	}
	rows.Close()

	// Trigger ephemeral countdown for all marked messages
	ephemeralQuery := `
		UPDATE messages
		SET expires_at = $4::timestamptz + (ttl_seconds * INTERVAL '1 second')
		WHERE conversation_id = $1 AND is_ephemeral = true AND expires_at IS NULL AND sender_id != $2 AND created_at <= $3
	`
	_, err = r.db.Exec(ctx, ephemeralQuery, conversationID, userID, lastMessageCreatedAt, now)
	if err != nil {
		r.logger.Warn("Failed to trigger ephemeral countdown in bulk", zap.Error(err))
	}

	return updates, nil
}

func (r *MessageRepository) GetStatusCounts(ctx context.Context, messageID uuid.UUID) (int, int, error) {
	var deliveredCount, readCount int
	err := r.db.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE delivered_at IS NOT NULL),
			COUNT(*) FILTER (WHERE read_at IS NOT NULL)
		FROM message_status
		WHERE message_id = $1
	`, messageID).Scan(&deliveredCount, &readCount)
	if err != nil {
		return 0, 0, fmt.Errorf("get message status counts: %w", err)
	}
	return deliveredCount, readCount, nil
}

func (r *MessageRepository) CountUnreadMessages(ctx context.Context, conversationID uuid.UUID, userID int64) (int, error) {
	var count int
	err := r.db.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM messages m
		WHERE m.conversation_id = $1
		  AND m.sender_id <> $2
		  AND m.is_deleted = FALSE
		  AND NOT EXISTS (
			SELECT 1 FROM deleted_messages dm
			WHERE dm.message_id = m.id AND dm.user_id = $2
		  )
		  AND m.created_at >= COALESCE(
			(SELECT cp.joined_at FROM conversation_participants cp
			 WHERE cp.conversation_id = $1 AND cp.user_id = $2 AND cp.left_at IS NULL),
			(SELECT gm.joined_at FROM group_members gm
			 WHERE gm.group_id = $1 AND gm.user_id = $2 AND gm.left_at IS NULL),
			'-infinity'::timestamptz
		  )
		  AND m.created_at > COALESCE(
			(SELECT cp.last_cleared_at FROM conversation_participants cp
			 WHERE cp.conversation_id = $1 AND cp.user_id = $2 AND cp.left_at IS NULL),
			(SELECT gm.last_cleared_at FROM group_members gm
			 WHERE gm.group_id = $1 AND gm.user_id = $2 AND gm.left_at IS NULL),
			'-infinity'::timestamptz
		  )
		  AND NOT EXISTS (
			SELECT 1 FROM message_status ms
			WHERE ms.message_id = m.id AND ms.user_id = $2 AND ms.read_at IS NOT NULL
		  )
	`, conversationID, userID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count unread messages: %w", err)
	}
	return count, nil
}

// GetMessageViewers returns a list of users who viewed (read) the message
func (r *MessageRepository) GetMessageViewers(ctx context.Context, messageID uuid.UUID) ([]domain.MessageViewer, error) {
	query := `
		SELECT 
			ms.user_id,
			u.first_name || ' ' || u.last_name as full_name,
			u.avatar_url,
			ms.read_at
		FROM message_status ms
		JOIN users u ON ms.user_id = u.id
		WHERE ms.message_id = $1 AND ms.read_at IS NOT NULL
		ORDER BY ms.read_at DESC
	`

	rows, err := r.db.Query(ctx, query, messageID)
	if err != nil {
		r.logger.Error("Failed to get message viewers", zap.Error(err))
		return nil, fmt.Errorf("get message viewers: %w", err)
	}
	defer rows.Close()

	viewers := make([]domain.MessageViewer, 0)
	for rows.Next() {
		var v domain.MessageViewer
		if err := rows.Scan(&v.UserID, &v.FullName, &v.AvatarURL, &v.ReadAt); err != nil {
			r.logger.Error("Failed to scan viewer", zap.Error(err))
			continue
		}
		v.AvatarURL = fileurl.SignLegacyDownloadURL(v.AvatarURL)
		viewers = append(viewers, v)
	}

	return viewers, nil
}

// SearchMessages выполняет полнотекстовый поиск сообщений в чате (как в Telegram/WhatsApp)
func (r *MessageRepository) SearchMessages(ctx context.Context, conversationID uuid.UUID, userID int64, query string, limit int) ([]*domain.MessageView, error) {
	var isChannel bool
	if err := r.db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM channels
			WHERE id = $1 AND deleted_at IS NULL
		)
	`, conversationID).Scan(&isChannel); err != nil {
		return nil, fmt.Errorf("detect channel search: %w", err)
	}
	if isChannel {
		return r.searchChannelPosts(ctx, conversationID, userID, query, limit)
	}

	// Подготовка query для полнотекстового поиска
	tsQuery := query // PostgreSQL сам преобразует в tsquery

	sqlQuery := `
		SELECT 
			m.id, m.conversation_id, m.sender_id, m.reply_to_id, m.content, m.content_hash, m.encrypted_keys, m.type,
			m.attachment_url, m.attachment_type, m.attachment_size, m.attachment_name, m.metadata,
			m.is_edited, m.is_forwarded, m.original_sender_id, m.is_deleted, m.created_at, m.updated_at, m.edited_at,
			m.is_ephemeral, m.ttl_seconds, m.expires_at,
			u.username as sender_username,
			u.first_name || ' ' || u.last_name as sender_full_name,
			u.avatar_url as sender_avatar,
			ou.first_name || ' ' || ou.last_name as original_sender_full_name,
			COALESCE((SELECT COUNT(*) FROM message_status ms WHERE ms.message_id = m.id AND ms.read_at IS NOT NULL), 0) as read_count,
			COALESCE((SELECT COUNT(*) FROM message_status ms WHERE ms.message_id = m.id AND ms.delivered_at IS NOT NULL), 0) as delivered_count,
			EXISTS (SELECT 1 FROM message_status ms WHERE ms.message_id = m.id AND ms.read_at IS NOT NULL) as is_read,
			-- Релевантность поиска (чем выше, тем релевантнее)
			ts_rank(m.search_vector, plainto_tsquery('russian', $3)) as rank,
			-- Подсветка найденных слов (контекст)
			ts_headline('russian', COALESCE(m.content, ''), plainto_tsquery('russian', $3), 
				'StartSel=<mark>, StopSel=</mark>, MaxWords=50, MinWords=25, ShortWord=3, HighlightAll=false') as highlight,
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
			-- Reply sender data
			ru.username as reply_sender_username,
			ru.first_name || ' ' || ru.last_name as reply_sender_full_name,
			ru.avatar_url as reply_sender_avatar
		FROM messages m
		INNER JOIN users u ON m.sender_id = u.id
		LEFT JOIN users ou ON m.original_sender_id = ou.id
		LEFT JOIN messages rm ON m.reply_to_id = rm.id AND rm.is_deleted = false
			AND NOT EXISTS (SELECT 1 FROM deleted_messages rdm WHERE rdm.message_id = rm.id AND rdm.user_id = $2)
		LEFT JOIN users ru ON rm.sender_id = ru.id
		LEFT JOIN deleted_messages dm ON m.id = dm.message_id AND dm.user_id = $2
		WHERE m.conversation_id = $1 
			AND m.is_deleted = false
			AND dm.message_id IS NULL
			AND (
				EXISTS (
					SELECT 1 FROM conversation_participants cp
					WHERE cp.conversation_id = m.conversation_id
					  AND cp.user_id = $2
					  AND cp.left_at IS NULL
					  AND m.created_at >= GREATEST(cp.joined_at, COALESCE(cp.last_cleared_at, '-infinity'::timestamptz))
				)
				OR EXISTS (
					SELECT 1 FROM group_members gm
					WHERE gm.group_id = m.conversation_id
					  AND gm.user_id = $2
					  AND gm.left_at IS NULL
					  AND m.created_at >= GREATEST(gm.joined_at, COALESCE(gm.last_cleared_at, '-infinity'::timestamptz))
				)
			)
			-- Russian FTS drops stop words such as "что". The substring fallback
			-- keeps those queries useful without treating user input as a LIKE pattern.
			AND (
				m.search_vector @@ plainto_tsquery('russian', $3)
				OR strpos(lower(COALESCE(m.content, '')), lower($3)) > 0
			)
		ORDER BY rank DESC, m.created_at DESC
		LIMIT $4
	`

	rows, err := r.db.Query(ctx, sqlQuery, conversationID, userID, tsQuery, limit)
	if err != nil {
		r.logger.Error("Failed to search messages", zap.Error(err))
		return nil, fmt.Errorf("search messages: %w", err)
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
		var rank float64
		var highlight string

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
			&msg.ReadByCount,
			&msg.DeliveredCount,
			&msg.IsRead,
			&rank,      // Релевантность
			&highlight, // Подсвеченный текст
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
			r.logger.Error("Failed to scan search result", zap.Error(err))
			continue
		}

		// Если есть reply_to message, собираем его
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
				SenderAvatar:   fileurl.SignLegacyDownloadURL(replySenderAvatar),
			}
		}
		signMessageView(msg)

		// Сохраняем highlight в metadata для отображения на клиенте
		if msg.Metadata == nil {
			msg.Metadata = &highlight
		}

		messages = append(messages, msg)
	}

	r.logger.Info("Messages searched",
		zap.String("conversation_id", conversationID.String()),
		zap.Int("query_length", len([]rune(query))),
		zap.Int("results", len(messages)),
	)

	return messages, nil
}

// searchChannelPosts maps channel posts to the message-search response expected by
// existing clients. This keeps /search/messages compatible for conversations,
// groups, and channels without requiring a separate frontend request.
func (r *MessageRepository) searchChannelPosts(ctx context.Context, channelID uuid.UUID, userID int64, query string, limit int) ([]*domain.MessageView, error) {
	rows, err := r.db.Query(ctx, `
		SELECT
			cp.id,
			cp.channel_id,
			cp.author_id,
			cp.content,
			CASE
				WHEN cp.attachment_url IS NULL THEN 'text'
				WHEN lower(COALESCE(cp.attachment_type, '')) = 'video_message' THEN 'video_message'
				WHEN lower(COALESCE(cp.attachment_type, '')) = 'voice' THEN 'voice'
				WHEN lower(COALESCE(cp.attachment_type, '')) LIKE 'image%' THEN 'image'
				WHEN lower(COALESCE(cp.attachment_type, '')) LIKE 'video%' THEN 'video'
				WHEN lower(COALESCE(cp.attachment_type, '')) LIKE 'audio%' THEN 'audio'
				ELSE 'file'
			END AS message_type,
			cp.attachment_url,
			cp.attachment_type,
			cp.attachment_size,
			cp.attachment_name,
			cp.metadata,
			cp.is_edited,
			cp.is_forwarded,
			cp.is_deleted,
			cp.created_at,
			cp.updated_at,
			cp.edited_at,
			COALESCE(u.username, ''),
			TRIM(COALESCE(u.first_name, '') || ' ' || COALESCE(u.last_name, '')),
			u.avatar_url
		FROM channel_posts cp
		JOIN channels c ON c.id = cp.channel_id
		JOIN users u ON u.id = cp.author_id
		WHERE cp.channel_id = $1
		  AND cp.is_deleted = false
		  AND c.deleted_at IS NULL
		  AND (
			c.type = 'public'
			OR EXISTS (
				SELECT 1
				FROM channel_members cm
				WHERE cm.channel_id = c.id
				  AND cm.user_id = $2
				  AND cm.left_at IS NULL
			)
		  )
		  AND strpos(lower(COALESCE(cp.content, '')), lower($3)) > 0
		ORDER BY cp.created_at DESC
		LIMIT $4
	`, channelID, userID, query, limit)
	if err != nil {
		r.logger.Error("Failed to search channel posts", zap.Error(err))
		return nil, fmt.Errorf("search channel posts: %w", err)
	}
	defer rows.Close()

	messages := make([]*domain.MessageView, 0)
	for rows.Next() {
		msg := &domain.MessageView{}
		if err := rows.Scan(
			&msg.ID,
			&msg.ConversationID,
			&msg.SenderID,
			&msg.Content,
			&msg.Type,
			&msg.AttachmentURL,
			&msg.AttachmentType,
			&msg.AttachmentSize,
			&msg.AttachmentName,
			&msg.Metadata,
			&msg.IsEdited,
			&msg.IsForwarded,
			&msg.IsDeleted,
			&msg.CreatedAt,
			&msg.UpdatedAt,
			&msg.EditedAt,
			&msg.SenderUsername,
			&msg.SenderFullName,
			&msg.SenderAvatar,
		); err != nil {
			return nil, fmt.Errorf("scan channel post search result: %w", err)
		}

		msg.EncryptedKeys = map[string]string{}
		msg.Reactions = make([]domain.ReactionSummary, 0)
		signMessageView(msg)
		messages = append(messages, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate channel post search results: %w", err)
	}

	r.logger.Info("Channel posts searched",
		zap.String("channel_id", channelID.String()),
		zap.Int("query_length", len([]rune(query))),
		zap.Int("results", len(messages)),
	)

	return messages, nil
}

func (r *MessageRepository) SearchGlobal(ctx context.Context, userID int64, query string, limit int) ([]domain.MessageSearchResult, error) {
	if limit <= 0 {
		limit = 20
	}

	sqlQuery := `
		SELECT
			m.id::text,
			m.conversation_id::text,
			m.sender_id,
			COALESCE(u.username, '') AS sender_username,
			TRIM(COALESCE(u.first_name, '') || ' ' || COALESCE(u.last_name, '')) AS sender_full_name,
			COALESCE(m.content, '') AS content,
			m.type,
			m.created_at,
			m.updated_at
		FROM messages m
		JOIN users u ON u.id = m.sender_id
		LEFT JOIN deleted_messages dm ON dm.message_id = m.id AND dm.user_id = $1
		WHERE m.is_deleted = false
		  AND dm.message_id IS NULL
		  AND (
			m.search_vector @@ plainto_tsquery('russian', $2)
			OR COALESCE(m.content, '') ILIKE '%' || $2 || '%'
		  )
		  AND (
			EXISTS (
				SELECT 1
				FROM conversation_participants cp
				WHERE cp.conversation_id = m.conversation_id
				  AND cp.user_id = $1
				  AND cp.left_at IS NULL
			)
			OR EXISTS (
				SELECT 1
				FROM group_members gm
				WHERE gm.group_id = m.conversation_id
				  AND gm.user_id = $1
				  AND gm.left_at IS NULL
			)
		  )
		ORDER BY ts_rank(m.search_vector, plainto_tsquery('russian', $2)) DESC, m.created_at DESC
		LIMIT $3
	`

	rows, err := r.db.Query(ctx, sqlQuery, userID, query, limit)
	if err != nil {
		r.logger.Error("Failed to search messages globally", zap.Error(err))
		return nil, fmt.Errorf("search global messages: %w", err)
	}
	defer rows.Close()

	results := make([]domain.MessageSearchResult, 0, limit)
	for rows.Next() {
		var item domain.MessageSearchResult
		if err := rows.Scan(
			&item.ID,
			&item.ConversationID,
			&item.SenderID,
			&item.SenderUsername,
			&item.SenderFullName,
			&item.Content,
			&item.Type,
			&item.CreatedAt,
			&item.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan global message search result: %w", err)
		}
		results = append(results, item)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate global message search results: %w", err)
	}

	return results, nil
}

// GetByContentHash gets messages by content hash
func (r *MessageRepository) GetByContentHash(ctx context.Context, hash string, userID int64) ([]*domain.MessageView, error) {
	query := `
       SELECT 
          m.id, m.conversation_id, m.sender_id, m.reply_to_id, m.content, m.content_hash, m.encrypted_keys, m.type,
          m.attachment_url, m.attachment_type, m.attachment_size, m.attachment_name, m.metadata,
          m.is_edited, m.is_forwarded, m.original_sender_id, m.is_deleted, m.created_at, m.updated_at, m.edited_at,
          m.is_ephemeral, m.ttl_seconds, m.expires_at,
          u.username as sender_username,
          u.first_name || ' ' || u.last_name as sender_full_name,
          u.avatar_url as sender_avatar,
          ou.first_name || ' ' || ou.last_name as original_sender_full_name,
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
          -- Reply sender data
          ru.username as reply_sender_username,
          ru.first_name || ' ' || ru.last_name as reply_sender_full_name,
          ru.avatar_url as reply_sender_avatar
       FROM messages m
       INNER JOIN users u ON m.sender_id = u.id
       LEFT JOIN users ou ON m.original_sender_id = ou.id
		LEFT JOIN messages rm ON m.reply_to_id = rm.id AND rm.is_deleted = false
			AND NOT EXISTS (SELECT 1 FROM deleted_messages rdm WHERE rdm.message_id=rm.id AND rdm.user_id=$2)
       LEFT JOIN users ru ON rm.sender_id = ru.id
		LEFT JOIN deleted_messages dm ON dm.message_id=m.id AND dm.user_id=$2
		WHERE m.content_hash = $1 AND m.is_deleted = false AND dm.message_id IS NULL
		  AND (
			EXISTS (
				SELECT 1 FROM conversation_participants cp
				WHERE cp.conversation_id=m.conversation_id AND cp.user_id=$2 AND cp.left_at IS NULL
				  AND m.created_at >= GREATEST(cp.joined_at, COALESCE(cp.last_cleared_at, '-infinity'::timestamptz))
			)
			OR EXISTS (
				SELECT 1 FROM group_members gm
				WHERE gm.group_id=m.conversation_id AND gm.user_id=$2 AND gm.left_at IS NULL
				  AND m.created_at >= GREATEST(gm.joined_at, COALESCE(gm.last_cleared_at, '-infinity'::timestamptz))
			)
		  )
    `

	rows, err := r.db.Query(ctx, query, hash, userID)
	if err != nil {
		r.logger.Error("Failed to get messages by hash", zap.Error(err))
		return nil, fmt.Errorf("get messages by hash: %w", err)
	}
	defer rows.Close()

	var messages []*domain.MessageView
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
			r.logger.Error("Failed to scan message", zap.Error(err))
			continue
		}

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

	return messages, nil
}

// CreateMentions saves user mentions for a message
func (r *MessageRepository) CreateMentions(ctx context.Context, messageID uuid.UUID, userIDs []int64) error {
	if len(userIDs) == 0 {
		return nil
	}

	query := `INSERT INTO message_mentions (message_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`

	batch := &pgx.Batch{}
	for _, userID := range userIDs {
		batch.Queue(query, messageID, userID)
	}

	br := r.db.SendBatch(ctx, batch)
	defer br.Close()

	for i := 0; i < len(userIDs); i++ {
		_, err := br.Exec()
		if err != nil {
			r.logger.Error("Failed to save mention", zap.Error(err))
		}
	}

	return nil
}

// SearchMessagesGlobal выполняет глобальный поиск по всем доступным пользователю чатам
func (r *MessageRepository) searchMessagesGlobalLegacy(ctx context.Context, userID int64, query string, filters map[string]interface{}, limit, offset int) ([]*domain.MessageView, int, error) {
	// Базовый SQL запрос
	baseQuery := `
		WITH accessible_chats AS (
			-- Личные чаты
			SELECT DISTINCT cp.conversation_id as chat_id, 'personal' as chat_type
			FROM conversation_participants cp
			WHERE cp.user_id = $1 AND cp.left_at IS NULL

			UNION

			-- Группы
			SELECT DISTINCT gm.group_id as chat_id, 'group' as chat_type
			FROM group_members gm
			WHERE gm.user_id = $1 AND gm.left_at IS NULL

			UNION

			-- Каналы
			SELECT DISTINCT cs.channel_id as chat_id, 'channel' as chat_type
			FROM channel_subscribers cs
			WHERE cs.user_id = $1 AND cs.left_at IS NULL
		)
		SELECT
			m.id, m.conversation_id, m.sender_id, m.reply_to_id, m.content, m.content_hash, m.encrypted_keys, m.type,
			m.attachment_url, m.attachment_type, m.attachment_size, m.attachment_name, m.metadata,
			m.is_edited, m.is_forwarded, m.original_sender_id, m.is_deleted, m.created_at, m.updated_at, m.edited_at,
			m.is_ephemeral, m.ttl_seconds, m.expires_at,
			u.username as sender_username,
			u.first_name || ' ' || u.last_name as sender_full_name,
			u.avatar_url as sender_avatar,
			ou.first_name || ' ' || ou.last_name as original_sender_full_name,
			COALESCE((SELECT COUNT(*) FROM message_status ms WHERE ms.message_id = m.id AND ms.read_at IS NOT NULL), 0) as read_count,
			COALESCE((SELECT COUNT(*) FROM message_status ms WHERE ms.message_id = m.id AND ms.delivered_at IS NOT NULL), 0) as delivered_count,
			EXISTS (SELECT 1 FROM message_status ms WHERE ms.message_id = m.id AND ms.read_at IS NOT NULL) as is_read,
			ts_rank(m.search_vector, plainto_tsquery('russian', $2)) as rank,
			ts_headline('russian', COALESCE(m.content, ''), plainto_tsquery('russian', $2),
				'StartSel=<mark>, StopSel=</mark>, MaxWords=50, MinWords=25, ShortWord=3, HighlightAll=false') as highlight,
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
		INNER JOIN accessible_chats ac ON m.conversation_id = ac.chat_id
		INNER JOIN users u ON m.sender_id = u.id
		LEFT JOIN users ou ON m.original_sender_id = ou.id
		LEFT JOIN messages rm ON m.reply_to_id = rm.id AND rm.is_deleted = false
		LEFT JOIN users ru ON rm.sender_id = ru.id
		LEFT JOIN deleted_messages dm ON m.id = dm.message_id AND dm.user_id = $1
		WHERE m.is_deleted = false
			AND dm.message_id IS NULL
			AND m.search_vector @@ plainto_tsquery('russian', $2)
	`

	// Динамически добавляем фильтры
	args := []interface{}{userID, query}
	argPos := 3

	// Фильтр по типу чата
	if chatType, ok := filters["chat_type"].(string); ok && chatType != "" {
		baseQuery += fmt.Sprintf(" AND ac.chat_type = $%d", argPos)
		args = append(args, chatType)
		argPos++
	}

	// Фильтр по конкретному чату
	if chatID, ok := filters["chat_id"].(uuid.UUID); ok {
		baseQuery += fmt.Sprintf(" AND m.conversation_id = $%d", argPos)
		args = append(args, chatID)
		argPos++
	}

	// Фильтр по типу вложения
	if attachmentType, ok := filters["attachment_type"].(string); ok && attachmentType != "" {
		baseQuery += fmt.Sprintf(" AND m.attachment_type = $%d", argPos)
		args = append(args, attachmentType)
		argPos++
	}

	// Фильтр по отправителю
	if senderID, ok := filters["sender_id"].(int64); ok && senderID > 0 {
		baseQuery += fmt.Sprintf(" AND m.sender_id = $%d", argPos)
		args = append(args, senderID)
		argPos++
	}

	// Фильтр по дате "от"
	if fromDate, ok := filters["from_date"].(time.Time); ok && !fromDate.IsZero() {
		baseQuery += fmt.Sprintf(" AND m.created_at >= $%d", argPos)
		args = append(args, fromDate)
		argPos++
	}

	// Фильтр по дате "до"
	if toDate, ok := filters["to_date"].(time.Time); ok && !toDate.IsZero() {
		baseQuery += fmt.Sprintf(" AND m.created_at <= $%d", argPos)
		args = append(args, toDate)
		argPos++
	}

	// Сортировка по релевантности и дате
	baseQuery += " ORDER BY rank DESC, m.created_at DESC"

	// Подсчет общего количества
	countQuery := `
		WITH accessible_chats AS (
			SELECT DISTINCT cp.conversation_id as chat_id FROM conversation_participants cp WHERE cp.user_id = $1 AND cp.left_at IS NULL
			UNION
			SELECT DISTINCT gm.group_id as chat_id FROM group_members gm WHERE gm.user_id = $1 AND gm.left_at IS NULL
			UNION
			SELECT DISTINCT cs.channel_id as chat_id FROM channel_subscribers cs WHERE cs.user_id = $1 AND cs.left_at IS NULL
		)
		SELECT COUNT(*) FROM messages m
		INNER JOIN accessible_chats ac ON m.conversation_id = ac.chat_id
		LEFT JOIN deleted_messages dm ON m.id = dm.message_id AND dm.user_id = $1
		WHERE m.is_deleted = false AND dm.message_id IS NULL
			AND m.search_vector @@ plainto_tsquery('russian', $2)
	`

	var totalCount int
	if err := r.db.QueryRow(ctx, countQuery, userID, query).Scan(&totalCount); err != nil {
		return nil, 0, fmt.Errorf("count messages: %w", err)
	}

	// Добавляем пагинацию
	baseQuery += fmt.Sprintf(" LIMIT $%d OFFSET $%d", argPos, argPos+1)
	args = append(args, limit, offset)

	// Выполняем запрос
	rows, err := r.db.Query(ctx, baseQuery, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("query messages: %w", err)
	}
	defer rows.Close()

	var messages []*domain.MessageView
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
		var rank float64
		var highlight string

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
			&msg.ReadByCount,
			&msg.DeliveredCount,
			&msg.IsRead,
			&rank,
			&highlight,
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
			r.logger.Warn("Failed to scan message", zap.Error(err))
			continue
		}

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
				SenderAvatar:   fileurl.SignLegacyDownloadURL(replySenderAvatar),
			}
		}
		signMessageView(msg)
		messages = append(messages, msg)
	}

	return messages, totalCount, nil
}

// SearchAttachments ищет вложения в доступных пользователю чатах
func (r *MessageRepository) searchAttachmentsLegacy(ctx context.Context, userID int64, filters map[string]interface{}, limit, offset int) ([]*domain.MessageView, int, error) {
	baseQuery := `
		WITH accessible_chats AS (
			SELECT DISTINCT cp.conversation_id as chat_id, 'personal' as chat_type FROM conversation_participants cp WHERE cp.user_id = $1 AND cp.left_at IS NULL
			UNION
			SELECT DISTINCT gm.group_id as chat_id, 'group' as chat_type FROM group_members gm WHERE gm.user_id = $1 AND gm.left_at IS NULL
			UNION
			SELECT DISTINCT cs.channel_id as chat_id, 'channel' as chat_type FROM channel_subscribers cs WHERE cs.user_id = $1 AND cs.left_at IS NULL
		)
		SELECT
			m.id, m.conversation_id, m.sender_id, m.reply_to_id, m.content, m.content_hash, m.encrypted_keys, m.type,
			m.attachment_url, m.attachment_type, m.attachment_size, m.attachment_name, m.metadata,
			m.is_edited, m.is_forwarded, m.original_sender_id, m.is_deleted, m.created_at, m.updated_at, m.edited_at,
			m.is_ephemeral, m.ttl_seconds, m.expires_at,
			u.username as sender_username,
			u.first_name || ' ' || u.last_name as sender_full_name,
			u.avatar_url as sender_avatar,
			ou.first_name || ' ' || ou.last_name as original_sender_full_name,
			COALESCE((SELECT COUNT(*) FROM message_status ms WHERE ms.message_id = m.id AND ms.read_at IS NOT NULL), 0) as read_count,
			COALESCE((SELECT COUNT(*) FROM message_status ms WHERE ms.message_id = m.id AND ms.delivered_at IS NOT NULL), 0) as delivered_count,
			EXISTS (SELECT 1 FROM message_status ms WHERE ms.message_id = m.id AND ms.read_at IS NOT NULL) as is_read,
			0.0 as rank,
			'' as highlight,
			NULL::uuid as reply_id, NULL::uuid as reply_conversation_id, NULL::bigint as reply_sender_id, NULL::uuid as reply_reply_to_id,
			NULL::text as reply_content, NULL::jsonb as reply_encrypted_keys, NULL::text as reply_type,
			NULL::text as reply_attachment_url, NULL::text as reply_attachment_type, NULL::bigint as reply_attachment_size, NULL::text as reply_attachment_name,
			NULL::text as reply_metadata, NULL::boolean as reply_is_edited, NULL::boolean as reply_is_deleted,
			NULL::timestamp as reply_created_at, NULL::timestamp as reply_updated_at, NULL::timestamp as reply_edited_at,
			NULL::text as reply_sender_username, NULL::text as reply_sender_full_name, NULL::text as reply_sender_avatar
		FROM messages m
		INNER JOIN accessible_chats ac ON m.conversation_id = ac.chat_id
		INNER JOIN users u ON m.sender_id = u.id
		LEFT JOIN users ou ON m.original_sender_id = ou.id
		LEFT JOIN deleted_messages dm ON m.id = dm.message_id AND dm.user_id = $1
		WHERE m.is_deleted = false
			AND dm.message_id IS NULL
			AND m.attachment_url IS NOT NULL
	`

	args := []interface{}{userID}
	argPos := 2

	// Фильтр по типу вложения
	if attachmentType, ok := filters["attachment_type"].(string); ok && attachmentType != "" && attachmentType != "all" {
		baseQuery += fmt.Sprintf(" AND m.attachment_type = $%d", argPos)
		args = append(args, attachmentType)
		argPos++
	}

	// Поиск по имени файла
	if query, ok := filters["query"].(string); ok && query != "" {
		baseQuery += fmt.Sprintf(" AND m.attachment_name ILIKE $%d", argPos)
		args = append(args, "%"+query+"%")
		argPos++
	}

	// Фильтр по конкретному чату
	if chatID, ok := filters["chat_id"].(uuid.UUID); ok {
		baseQuery += fmt.Sprintf(" AND m.conversation_id = $%d", argPos)
		args = append(args, chatID)
		argPos++
	}

	// Фильтр по дате "от"
	if fromDate, ok := filters["from_date"].(time.Time); ok && !fromDate.IsZero() {
		baseQuery += fmt.Sprintf(" AND m.created_at >= $%d", argPos)
		args = append(args, fromDate)
		argPos++
	}

	// Фильтр по дате "до"
	if toDate, ok := filters["to_date"].(time.Time); ok && !toDate.IsZero() {
		baseQuery += fmt.Sprintf(" AND m.created_at <= $%d", argPos)
		args = append(args, toDate)
		argPos++
	}

	// Сортировка по дате
	baseQuery += " ORDER BY m.created_at DESC"

	// Подсчет общего количества
	countQuery := `
		WITH accessible_chats AS (
			SELECT DISTINCT cp.conversation_id as chat_id FROM conversation_participants cp WHERE cp.user_id = $1 AND cp.left_at IS NULL
			UNION
			SELECT DISTINCT gm.group_id as chat_id FROM group_members gm WHERE gm.user_id = $1 AND gm.left_at IS NULL
			UNION
			SELECT DISTINCT cs.channel_id as chat_id FROM channel_subscribers cs WHERE cs.user_id = $1 AND cs.left_at IS NULL
		)
		SELECT COUNT(*) FROM messages m
		INNER JOIN accessible_chats ac ON m.conversation_id = ac.chat_id
		LEFT JOIN deleted_messages dm ON m.id = dm.message_id AND dm.user_id = $1
		WHERE m.is_deleted = false AND dm.message_id IS NULL AND m.attachment_url IS NOT NULL
	`

	var totalCount int
	if err := r.db.QueryRow(ctx, countQuery, userID).Scan(&totalCount); err != nil {
		return nil, 0, fmt.Errorf("count attachments: %w", err)
	}

	// Добавляем пагинацию
	baseQuery += fmt.Sprintf(" LIMIT $%d OFFSET $%d", argPos, argPos+1)
	args = append(args, limit, offset)

	// Выполняем запрос
	rows, err := r.db.Query(ctx, baseQuery, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("query attachments: %w", err)
	}
	defer rows.Close()

	var messages []*domain.MessageView
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
		var rank float64
		var highlight string

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
			&msg.ReadByCount,
			&msg.DeliveredCount,
			&msg.IsRead,
			&rank,
			&highlight,
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
			r.logger.Warn("Failed to scan attachment message", zap.Error(err))
			continue
		}

		signMessageView(msg)
		messages = append(messages, msg)
	}

	return messages, totalCount, nil
}

// GetAttachmentStats возвращает статистику по вложениям в чате
func (r *MessageRepository) getAttachmentStatsLegacy(ctx context.Context, chatID uuid.UUID, userID int64) (map[string]int, int64, error) {
	// Проверяем доступ
	var hasAccess bool
	accessQuery := `
		SELECT EXISTS (
			SELECT 1 FROM conversation_participants WHERE conversation_id = $1 AND user_id = $2 AND left_at IS NULL
			UNION
			SELECT 1 FROM group_members WHERE group_id = $1 AND user_id = $2 AND left_at IS NULL
			UNION
			SELECT 1 FROM channel_subscribers WHERE channel_id = $1 AND user_id = $2 AND left_at IS NULL
		)
	`
	if err := r.db.QueryRow(ctx, accessQuery, chatID, userID).Scan(&hasAccess); err != nil {
		return nil, 0, fmt.Errorf("check access: %w", err)
	}
	if !hasAccess {
		return nil, 0, errors.New("access denied")
	}

	// Получаем статистику
	statsQuery := `
		SELECT
			COALESCE(attachment_type, 'unknown') as type,
			COUNT(*) as count,
			COALESCE(SUM(attachment_size), 0) as total_size
		FROM messages
		WHERE conversation_id = $1
			AND is_deleted = false
			AND attachment_url IS NOT NULL
		GROUP BY attachment_type
	`

	rows, err := r.db.Query(ctx, statsQuery, chatID)
	if err != nil {
		return nil, 0, fmt.Errorf("query stats: %w", err)
	}
	defer rows.Close()

	stats := make(map[string]int)
	var totalSize int64

	for rows.Next() {
		var attachType string
		var count int
		var size int64
		if err := rows.Scan(&attachType, &count, &size); err != nil {
			continue
		}
		stats[attachType] = count
		totalSize += size
	}

	return stats, totalSize, nil
}
