package repository

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestMSG006SearchRespectsAccessAndVisibilityIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_MESSENGER_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_MESSENGER_DATABASE_URL to a disposable PostgreSQL database")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	defer admin.Close()
	schema := "msg006_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	defer func() { _, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE") }()
	cfg, err := pgxpool.ParseConfig(dsn)
	require.NoError(t, err)
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	defer pool.Close()

	_, err = pool.Exec(ctx, `
		CREATE TABLE users (id BIGINT PRIMARY KEY, username TEXT, first_name TEXT, last_name TEXT, avatar_url TEXT);
		CREATE TABLE conversations (id UUID PRIMARY KEY, deleted_at TIMESTAMPTZ);
		CREATE TABLE conversation_participants (conversation_id UUID, user_id BIGINT, joined_at TIMESTAMPTZ, left_at TIMESTAMPTZ, last_cleared_at TIMESTAMPTZ);
		CREATE TABLE groups (id UUID PRIMARY KEY, name TEXT, is_active BOOLEAN, deleted_at TIMESTAMPTZ);
		CREATE TABLE group_members (group_id UUID, user_id BIGINT, joined_at TIMESTAMPTZ, left_at TIMESTAMPTZ, last_cleared_at TIMESTAMPTZ);
		CREATE TABLE messages (
			id UUID PRIMARY KEY, conversation_id UUID, sender_id BIGINT, reply_to_id UUID, content TEXT, content_hash TEXT,
			encrypted_keys JSONB DEFAULT '{}'::jsonb, type TEXT DEFAULT 'text', attachment_url TEXT, attachment_type TEXT,
			attachment_size BIGINT, attachment_name TEXT, metadata TEXT, is_edited BOOLEAN DEFAULT FALSE,
			is_forwarded BOOLEAN DEFAULT FALSE, original_sender_id BIGINT, is_deleted BOOLEAN DEFAULT FALSE,
			created_at TIMESTAMPTZ, updated_at TIMESTAMPTZ, edited_at TIMESTAMPTZ, is_ephemeral BOOLEAN DEFAULT FALSE,
			ttl_seconds INT, expires_at TIMESTAMPTZ, search_vector TSVECTOR
		);
		CREATE TABLE message_status (message_id UUID, user_id BIGINT, delivered_at TIMESTAMPTZ, read_at TIMESTAMPTZ);
		CREATE TABLE deleted_messages (message_id UUID, user_id BIGINT, PRIMARY KEY(message_id,user_id));
		CREATE TABLE channels (id UUID PRIMARY KEY, name TEXT, is_active BOOLEAN, deleted_at TIMESTAMPTZ);
		CREATE TABLE channel_members (channel_id UUID, user_id BIGINT, joined_at TIMESTAMPTZ, left_at TIMESTAMPTZ);
		CREATE TABLE channel_posts (
			id UUID PRIMARY KEY, channel_id UUID, author_id BIGINT, reply_to_id UUID, content TEXT,
			attachment_url TEXT, attachment_type TEXT, attachment_size BIGINT, attachment_name TEXT, metadata TEXT,
			is_edited BOOLEAN DEFAULT FALSE, is_forwarded BOOLEAN DEFAULT FALSE, is_deleted BOOLEAN DEFAULT FALSE,
			created_at TIMESTAMPTZ, updated_at TIMESTAMPTZ, edited_at TIMESTAMPTZ, search_vector TSVECTOR
		);
	`)
	require.NoError(t, err)

	userID, authorID := int64(1), int64(2)
	_, err = pool.Exec(ctx, `INSERT INTO users VALUES (1,'reader','Read','User',NULL),(2,'author','Message','Author',NULL)`)
	require.NoError(t, err)
	now := time.Now().UTC()
	visibleChat, privateChat, channelID := uuid.New(), uuid.New(), uuid.New()
	_, err = pool.Exec(ctx, `INSERT INTO conversations(id) VALUES ($1),($2)`, visibleChat, privateChat)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO conversation_participants(conversation_id,user_id,joined_at) VALUES ($1,$2,$3)`, visibleChat, userID, now.Add(-time.Hour))
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO channels(id,name,is_active) VALUES ($1,'Новости',TRUE)`, channelID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO channel_members(channel_id,user_id,joined_at) VALUES ($1,$2,$3)`, channelID, userID, now.Add(-time.Hour))
	require.NoError(t, err)

	visibleMessage, hiddenMessage, oldMessage, deletedForUser, channelPost := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	_, err = pool.Exec(ctx, `
		INSERT INTO messages(id,conversation_id,sender_id,content,attachment_url,attachment_type,attachment_size,attachment_name,created_at,updated_at,search_vector) VALUES
		($1,$5,$6,'секрет доступный','https://file','file',123,'секретный отчет.pdf',$7,$7,to_tsvector('russian','секрет доступный секретный отчет')),
		($2,$8,$6,'секрет чужой',NULL,NULL,NULL,NULL,$7,$7,to_tsvector('russian','секрет чужой')),
		($3,$5,$6,'секрет до вступления',NULL,NULL,NULL,NULL,$9,$9,to_tsvector('russian','секрет до вступления')),
		($4,$5,$6,'секрет удаленный',NULL,NULL,NULL,NULL,$7,$7,to_tsvector('russian','секрет удаленный'))
	`, visibleMessage, hiddenMessage, oldMessage, deletedForUser, visibleChat, authorID, now, privateChat, now.Add(-2*time.Hour))
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO deleted_messages(message_id,user_id) VALUES ($1,$2)`, deletedForUser, userID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO channel_posts(id,channel_id,author_id,content,created_at,updated_at,search_vector)
		VALUES ($1,$2,$3,'секрет канала',$4,$4,to_tsvector('russian','секрет канала'))`, channelPost, channelID, authorID, now)
	require.NoError(t, err)

	repo := NewMessageRepository(pool, zap.NewNop())
	t.Run("thread respects history visibility", func(t *testing.T) {
		for _, id := range []uuid.UUID{oldMessage, hiddenMessage, deletedForUser} {
			visible, err := repo.IsVisibleToUser(ctx, id, userID)
			require.NoError(t, err)
			require.False(t, visible)
		}
		visible, err := repo.IsVisibleToUser(ctx, visibleMessage, userID)
		require.NoError(t, err)
		require.True(t, visible)
		_, err = pool.Exec(ctx, `UPDATE messages SET reply_to_id = $1 WHERE id = ANY($2::uuid[])`, visibleMessage, []uuid.UUID{oldMessage, hiddenMessage, deletedForUser})
		require.NoError(t, err)
		replies, truncated, err := repo.GetReplyDescendants(ctx, visibleMessage, userID, 20)
		require.NoError(t, err)
		require.Empty(t, replies)
		require.False(t, truncated)
		visibleReply := uuid.New()
		_, err = pool.Exec(ctx, `INSERT INTO messages(id,conversation_id,sender_id,reply_to_id,content,created_at,updated_at)
			VALUES ($1,$2,$3,$4,'видимый ответ',$5,$5)`, visibleReply, visibleChat, authorID, visibleMessage, now.Add(time.Minute))
		require.NoError(t, err)
		replies, truncated, err = repo.GetReplyDescendants(ctx, visibleMessage, userID, 20)
		require.NoError(t, err)
		require.Len(t, replies, 1)
		require.Equal(t, visibleReply, replies[0].ID)
		require.False(t, truncated)
		_, err = pool.Exec(ctx, `DELETE FROM messages WHERE id = $1`, visibleReply)
		require.NoError(t, err)
		_, err = pool.Exec(ctx, `UPDATE conversation_participants SET last_cleared_at = $1 WHERE conversation_id = $2 AND user_id = $3`, now.Add(time.Second), visibleChat, userID)
		require.NoError(t, err)
		visible, err = repo.IsVisibleToUser(ctx, visibleMessage, userID)
		require.NoError(t, err)
		require.False(t, visible)
		// The group branch must enforce the same join/clear boundaries.
		_, err = pool.Exec(ctx, `UPDATE conversation_participants SET left_at = $1 WHERE conversation_id = $2 AND user_id = $3`, now, visibleChat, userID)
		require.NoError(t, err)
		_, err = pool.Exec(ctx, `INSERT INTO group_members(group_id,user_id,joined_at,last_cleared_at) VALUES ($1,$2,$3,$4)`, visibleChat, userID, now.Add(-time.Hour), now.Add(time.Second))
		require.NoError(t, err)
		visible, err = repo.IsVisibleToUser(ctx, visibleMessage, userID)
		require.NoError(t, err)
		require.False(t, visible)
		_, err = pool.Exec(ctx, `UPDATE group_members SET last_cleared_at = NULL WHERE group_id = $1`, visibleChat)
		require.NoError(t, err)
		visible, err = repo.IsVisibleToUser(ctx, visibleMessage, userID)
		require.NoError(t, err)
		require.True(t, visible)
		visible, err = repo.IsVisibleToUser(ctx, oldMessage, userID)
		require.NoError(t, err)
		require.False(t, visible)
		_, err = pool.Exec(ctx, `DELETE FROM group_members WHERE group_id = $1`, visibleChat)
		require.NoError(t, err)
		_, err = pool.Exec(ctx, `UPDATE conversation_participants SET left_at = NULL WHERE conversation_id = $1`, visibleChat)
		require.NoError(t, err)
		_, err = pool.Exec(ctx, `UPDATE conversation_participants SET last_cleared_at = NULL WHERE conversation_id = $1 AND user_id = $2`, visibleChat, userID)
		require.NoError(t, err)
		_, err = pool.Exec(ctx, `UPDATE messages SET reply_to_id = NULL`)
		require.NoError(t, err)
	})
	results, total, err := repo.SearchMessagesGlobal(ctx, userID, "секрет", map[string]interface{}{}, 20, 0)
	require.NoError(t, err)
	require.Equal(t, 2, total)
	require.Len(t, results, 2)
	ids := map[uuid.UUID]bool{}
	for _, result := range results {
		ids[result.ID] = true
	}
	require.True(t, ids[visibleMessage])
	require.True(t, ids[channelPost])
	require.False(t, ids[hiddenMessage])
	require.False(t, ids[oldMessage])
	require.False(t, ids[deletedForUser])
	emptyPage, emptyPageTotal, err := repo.SearchMessagesGlobal(ctx, userID, "секрет", map[string]interface{}{}, 20, 10)
	require.NoError(t, err)
	require.Empty(t, emptyPage)
	require.Equal(t, 2, emptyPageTotal, "total_count must remain correct beyond the last page")
	channelResults, channelTotal, err := repo.SearchMessagesGlobal(ctx, userID, "секрет", map[string]interface{}{"chat_type": "channel"}, 20, 0)
	require.NoError(t, err)
	require.Equal(t, 1, channelTotal)
	require.Len(t, channelResults, 1)
	require.Equal(t, channelPost, channelResults[0].ID)
	require.Equal(t, "channel", channelResults[0].SearchChatType)

	attachments, attachmentTotal, err := repo.SearchAttachments(ctx, userID, map[string]interface{}{"query": "отчет"}, 20, 0)
	require.NoError(t, err)
	require.Equal(t, 1, attachmentTotal)
	require.Len(t, attachments, 1)
	require.Equal(t, visibleMessage, attachments[0].ID)
	wildcardAttachments, wildcardTotal, err := repo.SearchAttachments(ctx, userID, map[string]interface{}{"query": "%"}, 20, 0)
	require.NoError(t, err)
	require.Zero(t, wildcardTotal, "LIKE wildcard characters in user input must be escaped")
	require.Empty(t, wildcardAttachments)

	stats, totalSize, err := repo.GetAttachmentStats(ctx, visibleChat, userID)
	require.NoError(t, err)
	require.Equal(t, 1, stats["file"])
	require.Greater(t, totalSize, int64(0))
	_, _, err = repo.GetAttachmentStats(ctx, privateChat, userID)
	require.ErrorIs(t, err, ErrMessageSearchAccessDenied)
}
