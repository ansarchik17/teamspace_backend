package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/domain"
)

var ErrMessageSearchAccessDenied = errors.New("message search access denied")

const searchableMessagesCTE = `
	WITH accessible_message_chats AS (
		SELECT
			cp.conversation_id AS chat_id,
			'personal'::text AS chat_type,
			COALESCE((
				SELECT TRIM(COALESCE(u.first_name, '') || ' ' || COALESCE(u.last_name, ''))
				FROM conversation_participants peer
				JOIN users u ON u.id = peer.user_id
				WHERE peer.conversation_id = cp.conversation_id
				  AND peer.user_id <> cp.user_id AND peer.left_at IS NULL
				ORDER BY peer.joined_at DESC LIMIT 1
			), 'Личный чат') AS chat_name,
			GREATEST(cp.joined_at, COALESCE(cp.last_cleared_at, '-infinity'::timestamptz)) AS visible_after
		FROM conversation_participants cp
		JOIN conversations c ON c.id = cp.conversation_id AND c.deleted_at IS NULL
		WHERE cp.user_id = $1 AND cp.left_at IS NULL

		UNION ALL

		SELECT gm.group_id, 'group'::text, g.name,
			GREATEST(gm.joined_at, COALESCE(gm.last_cleared_at, '-infinity'::timestamptz))
		FROM group_members gm
		JOIN groups g ON g.id = gm.group_id AND g.deleted_at IS NULL AND g.is_active = TRUE
		WHERE gm.user_id = $1 AND gm.left_at IS NULL
	), searchable AS (
		SELECT
			m.id, m.conversation_id, m.sender_id, m.reply_to_id, m.content, m.content_hash,
			m.encrypted_keys, m.type, m.attachment_url, m.attachment_type, m.attachment_size,
			m.attachment_name, m.metadata, m.is_edited, m.is_forwarded, m.original_sender_id,
			m.is_deleted, m.created_at, m.updated_at, m.edited_at, m.is_ephemeral,
			m.ttl_seconds, m.expires_at, COALESCE(u.username, '') AS sender_username,
			TRIM(COALESCE(u.first_name, '') || ' ' || COALESCE(u.last_name, '')) AS sender_full_name,
			u.avatar_url AS sender_avatar,
			ac.chat_type, ac.chat_name, m.search_vector,
			COALESCE((SELECT COUNT(*) FROM message_status ms WHERE ms.message_id=m.id AND ms.read_at IS NOT NULL), 0)::int AS read_count,
			COALESCE((SELECT COUNT(*) FROM message_status ms WHERE ms.message_id=m.id AND ms.delivered_at IS NOT NULL), 0)::int AS delivered_count,
			EXISTS(SELECT 1 FROM message_status ms WHERE ms.message_id=m.id AND ms.read_at IS NOT NULL) AS is_read
		FROM messages m
		JOIN accessible_message_chats ac ON ac.chat_id = m.conversation_id
		JOIN users u ON u.id = m.sender_id
		LEFT JOIN deleted_messages dm ON dm.message_id = m.id AND dm.user_id = $1
		WHERE m.is_deleted = FALSE AND dm.message_id IS NULL AND m.created_at >= ac.visible_after

		UNION ALL

		SELECT
			cp.id, cp.channel_id, cp.author_id, cp.reply_to_id, cp.content, NULL::text,
			'{}'::jsonb,
			CASE
				WHEN cp.attachment_url IS NULL THEN 'text'
				WHEN lower(COALESCE(cp.attachment_type,''))='voice' THEN 'voice'
				WHEN lower(COALESCE(cp.attachment_type,''))='video_message' THEN 'video_message'
				WHEN lower(COALESCE(cp.attachment_type,'')) LIKE 'image%%' THEN 'image'
				WHEN lower(COALESCE(cp.attachment_type,'')) LIKE 'video%%' THEN 'video'
				WHEN lower(COALESCE(cp.attachment_type,'')) LIKE 'audio%%' THEN 'audio'
				ELSE 'file'
			END,
			cp.attachment_url, cp.attachment_type, cp.attachment_size, cp.attachment_name,
			cp.metadata, cp.is_edited, cp.is_forwarded, NULL::bigint, cp.is_deleted,
			cp.created_at, cp.updated_at, cp.edited_at, FALSE, NULL::int, NULL::timestamptz,
			COALESCE(u.username, ''),
			TRIM(COALESCE(u.first_name, '') || ' ' || COALESCE(u.last_name, '')), u.avatar_url,
			'channel'::text, c.name, cp.search_vector, 0, 0, FALSE
		FROM channel_posts cp
		JOIN channels c ON c.id = cp.channel_id AND c.deleted_at IS NULL AND c.is_active = TRUE
		JOIN channel_members cm ON cm.channel_id = c.id AND cm.user_id = $1 AND cm.left_at IS NULL
		JOIN users u ON u.id = cp.author_id
		WHERE cp.is_deleted = FALSE AND cp.created_at >= cm.joined_at
	)
`

func appendMessageSearchFilters(sql string, args []interface{}, filters map[string]interface{}) (string, []interface{}) {
	argPos := len(args) + 1
	if value, ok := filters["chat_type"].(string); ok && value != "" {
		sql += fmt.Sprintf(" AND s.chat_type = $%d", argPos)
		args = append(args, value)
		argPos++
	}
	if value, ok := filters["chat_id"].(uuid.UUID); ok {
		sql += fmt.Sprintf(" AND s.conversation_id = $%d", argPos)
		args = append(args, value)
		argPos++
	}
	if value, ok := filters["attachment_type"].(string); ok && value != "" && value != "all" {
		sql += fmt.Sprintf(" AND (s.type = $%d OR s.attachment_type = $%d)", argPos, argPos)
		args = append(args, value)
		argPos++
	}
	if value, ok := filters["sender_id"].(int64); ok && value > 0 {
		sql += fmt.Sprintf(" AND s.sender_id = $%d", argPos)
		args = append(args, value)
		argPos++
	}
	if value, ok := filters["from_date"].(time.Time); ok && !value.IsZero() {
		sql += fmt.Sprintf(" AND s.created_at >= $%d", argPos)
		args = append(args, value)
		argPos++
	}
	if value, ok := filters["to_date"].(time.Time); ok && !value.IsZero() {
		sql += fmt.Sprintf(" AND s.created_at <= $%d", argPos)
		args = append(args, value)
	}
	return sql, args
}

func escapeLikeContains(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + replacer.Replace(strings.ToLower(value)) + "%"
}

func (r *MessageRepository) SearchMessagesGlobal(ctx context.Context, userID int64, query string, filters map[string]interface{}, limit, offset int) ([]*domain.MessageView, int, error) {
	args := []interface{}{userID, query, escapeLikeContains(query)}
	where := ` WHERE (
		s.search_vector @@ plainto_tsquery('russian', $2)
		OR lower(COALESCE(s.content,'')) LIKE $3 ESCAPE '\'
		OR lower(COALESCE(s.attachment_name,'')) LIKE $3 ESCAPE '\'
	)`
	where, args = appendMessageSearchFilters(where, args, filters)
	limitPos := len(args) + 1
	dataQuery := searchableMessagesCTE + `
		SELECT
			s.id, s.conversation_id, s.sender_id, s.reply_to_id, s.content, s.content_hash,
			s.encrypted_keys, s.type, s.attachment_url, s.attachment_type, s.attachment_size,
			s.attachment_name, s.metadata, s.is_edited, s.is_forwarded, s.original_sender_id,
			s.is_deleted, s.created_at, s.updated_at, s.edited_at, s.is_ephemeral,
			s.ttl_seconds, s.expires_at, s.sender_username, s.sender_full_name, s.sender_avatar,
			s.read_count, s.delivered_count, s.is_read, s.chat_type, s.chat_name,
			ts_rank(s.search_vector, plainto_tsquery('russian', $2)) AS rank,
			ts_headline('russian', COALESCE(s.content,''), plainto_tsquery('russian', $2),
				'StartSel=⟦, StopSel=⟧, MaxWords=50, MinWords=10') AS highlight,
			COUNT(*) OVER() AS total_count
		FROM searchable s` + where + fmt.Sprintf(`
		ORDER BY rank DESC, s.created_at DESC, s.id DESC
		LIMIT $%d OFFSET $%d`, limitPos, limitPos+1)
	args = append(args, limit, offset)

	rows, err := r.db.Query(ctx, dataQuery, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("search accessible messages: %w", err)
	}
	defer rows.Close()
	results := make([]*domain.MessageView, 0, limit)
	total := 0
	for rows.Next() {
		msg := &domain.MessageView{}
		var rank float64
		if err := rows.Scan(
			&msg.ID, &msg.ConversationID, &msg.SenderID, &msg.ReplyToID, &msg.Content, &msg.ContentHash,
			&msg.EncryptedKeys, &msg.Type, &msg.AttachmentURL, &msg.AttachmentType, &msg.AttachmentSize,
			&msg.AttachmentName, &msg.Metadata, &msg.IsEdited, &msg.IsForwarded, &msg.OriginalSenderID,
			&msg.IsDeleted, &msg.CreatedAt, &msg.UpdatedAt, &msg.EditedAt, &msg.IsEphemeral,
			&msg.TTLSeconds, &msg.ExpiresAt, &msg.SenderUsername, &msg.SenderFullName, &msg.SenderAvatar,
			&msg.ReadByCount, &msg.DeliveredCount, &msg.IsRead, &msg.SearchChatType, &msg.SearchChatName,
			&rank, &msg.SearchHighlight, &total,
		); err != nil {
			return nil, 0, fmt.Errorf("scan accessible message search result: %w", err)
		}
		msg.SearchRelevance = rank
		msg.Reactions = make([]domain.ReactionSummary, 0)
		signMessageView(msg)
		results = append(results, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate accessible message search results: %w", err)
	}
	if total == 0 && offset > 0 {
		countArgs := args[:len(args)-2]
		if err := r.db.QueryRow(ctx, searchableMessagesCTE+`SELECT COUNT(*) FROM searchable s`+where, countArgs...).Scan(&total); err != nil {
			return nil, 0, fmt.Errorf("count accessible message search results: %w", err)
		}
	}
	return results, total, nil
}

func (r *MessageRepository) SearchAttachments(ctx context.Context, userID int64, filters map[string]interface{}, limit, offset int) ([]*domain.MessageView, int, error) {
	args := []interface{}{userID}
	where := " WHERE s.attachment_url IS NOT NULL"
	if query, ok := filters["query"].(string); ok && strings.TrimSpace(query) != "" {
		args = append(args, escapeLikeContains(strings.TrimSpace(query)))
		where += fmt.Sprintf(" AND lower(COALESCE(s.attachment_name,'')) LIKE $%d ESCAPE '\\'", len(args))
	}
	where, args = appendMessageSearchFilters(where, args, filters)
	limitPos := len(args) + 1
	dataQuery := searchableMessagesCTE + `
		SELECT
			s.id, s.conversation_id, s.sender_id, s.reply_to_id, s.content, s.content_hash,
			s.encrypted_keys, s.type, s.attachment_url, s.attachment_type, s.attachment_size,
			s.attachment_name, s.metadata, s.is_edited, s.is_forwarded, s.original_sender_id,
			s.is_deleted, s.created_at, s.updated_at, s.edited_at, s.is_ephemeral,
			s.ttl_seconds, s.expires_at, s.sender_username, s.sender_full_name, s.sender_avatar,
			s.read_count, s.delivered_count, s.is_read, s.chat_type, s.chat_name,
			COUNT(*) OVER() AS total_count
		FROM searchable s` + where + fmt.Sprintf(`
		ORDER BY s.created_at DESC, s.id DESC LIMIT $%d OFFSET $%d`, limitPos, limitPos+1)
	args = append(args, limit, offset)
	rows, err := r.db.Query(ctx, dataQuery, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("search accessible attachments: %w", err)
	}
	defer rows.Close()
	results := make([]*domain.MessageView, 0, limit)
	total := 0
	for rows.Next() {
		msg := &domain.MessageView{}
		if err := rows.Scan(
			&msg.ID, &msg.ConversationID, &msg.SenderID, &msg.ReplyToID, &msg.Content, &msg.ContentHash,
			&msg.EncryptedKeys, &msg.Type, &msg.AttachmentURL, &msg.AttachmentType, &msg.AttachmentSize,
			&msg.AttachmentName, &msg.Metadata, &msg.IsEdited, &msg.IsForwarded, &msg.OriginalSenderID,
			&msg.IsDeleted, &msg.CreatedAt, &msg.UpdatedAt, &msg.EditedAt, &msg.IsEphemeral,
			&msg.TTLSeconds, &msg.ExpiresAt, &msg.SenderUsername, &msg.SenderFullName, &msg.SenderAvatar,
			&msg.ReadByCount, &msg.DeliveredCount, &msg.IsRead, &msg.SearchChatType, &msg.SearchChatName, &total,
		); err != nil {
			return nil, 0, fmt.Errorf("scan accessible attachment result: %w", err)
		}
		msg.Reactions = make([]domain.ReactionSummary, 0)
		signMessageView(msg)
		results = append(results, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate accessible attachments: %w", err)
	}
	if total == 0 && offset > 0 {
		countArgs := args[:len(args)-2]
		if err := r.db.QueryRow(ctx, searchableMessagesCTE+`SELECT COUNT(*) FROM searchable s`+where, countArgs...).Scan(&total); err != nil {
			return nil, 0, fmt.Errorf("count accessible attachments: %w", err)
		}
	}
	return results, total, nil
}

func (r *MessageRepository) GetAttachmentStats(ctx context.Context, chatID uuid.UUID, userID int64) (map[string]int, int64, error) {
	var chatType string
	var visibleAfter time.Time
	err := r.db.QueryRow(ctx, `
		SELECT chat_type, visible_after FROM (
			SELECT 'personal'::text AS chat_type,
				GREATEST(joined_at, COALESCE(last_cleared_at, '-infinity'::timestamptz)) AS visible_after
			FROM conversation_participants
			WHERE conversation_id=$1 AND user_id=$2 AND left_at IS NULL
			UNION ALL
			SELECT 'group', GREATEST(joined_at, COALESCE(last_cleared_at, '-infinity'::timestamptz))
			FROM group_members WHERE group_id=$1 AND user_id=$2 AND left_at IS NULL
			UNION ALL
			SELECT 'channel', joined_at FROM channel_members WHERE channel_id=$1 AND user_id=$2 AND left_at IS NULL
		) access LIMIT 1
	`, chatID, userID).Scan(&chatType, &visibleAfter)
	if err != nil {
		return nil, 0, ErrMessageSearchAccessDenied
	}

	query := `
		SELECT COALESCE(attachment_type,'unknown'), COUNT(*)::int, COALESCE(SUM(attachment_size),0)::bigint
		FROM messages m
		LEFT JOIN deleted_messages dm ON dm.message_id=m.id AND dm.user_id=$2
		WHERE m.conversation_id=$1 AND m.is_deleted=FALSE AND dm.message_id IS NULL
		  AND m.attachment_url IS NOT NULL AND m.created_at >= $3
		GROUP BY attachment_type`
	if chatType == "channel" {
		query = `SELECT COALESCE(attachment_type,'unknown'), COUNT(*)::int, COALESCE(SUM(attachment_size),0)::bigint
			FROM channel_posts WHERE channel_id=$1 AND is_deleted=FALSE AND attachment_url IS NOT NULL AND created_at >= $2
			GROUP BY attachment_type`
	}
	queryArgs := []interface{}{chatID, userID, visibleAfter}
	if chatType == "channel" {
		queryArgs = []interface{}{chatID, visibleAfter}
	}
	rows, err := r.db.Query(ctx, query, queryArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("query attachment stats: %w", err)
	}
	defer rows.Close()
	stats := make(map[string]int)
	var totalSize int64
	for rows.Next() {
		var kind string
		var count int
		var size int64
		if err := rows.Scan(&kind, &count, &size); err != nil {
			return nil, 0, fmt.Errorf("scan attachment stats: %w", err)
		}
		stats[kind] = count
		totalSize += size
	}
	return stats, totalSize, rows.Err()
}
