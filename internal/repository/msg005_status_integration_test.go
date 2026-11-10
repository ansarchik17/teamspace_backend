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

func TestMSG005StatusLifecycleIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_MESSENGER_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_MESSENGER_DATABASE_URL to a disposable PostgreSQL database")
	}

	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	defer admin.Close()

	schema := "msg005_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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
		CREATE TABLE users (id BIGINT PRIMARY KEY);
		CREATE TABLE messages (
			id UUID PRIMARY KEY,
			conversation_id UUID NOT NULL,
			sender_id BIGINT NOT NULL REFERENCES users(id),
			is_deleted BOOLEAN NOT NULL DEFAULT FALSE,
			is_ephemeral BOOLEAN NOT NULL DEFAULT FALSE,
			ttl_seconds INTEGER,
			expires_at TIMESTAMPTZ,
			created_at TIMESTAMPTZ NOT NULL
		);
		CREATE TABLE message_status (
			id UUID PRIMARY KEY,
			message_id UUID NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
			user_id BIGINT NOT NULL REFERENCES users(id),
			delivered_at TIMESTAMPTZ,
			read_at TIMESTAMPTZ,
			created_at TIMESTAMPTZ NOT NULL,
			UNIQUE(message_id, user_id)
		);
		CREATE TABLE deleted_messages (
			user_id BIGINT NOT NULL,
			message_id UUID NOT NULL,
			PRIMARY KEY(user_id, message_id)
		);
		CREATE TABLE conversation_participants (
			conversation_id UUID NOT NULL,
			user_id BIGINT NOT NULL,
			joined_at TIMESTAMPTZ NOT NULL,
			left_at TIMESTAMPTZ,
			last_cleared_at TIMESTAMPTZ
		);
		CREATE TABLE group_members (
			group_id UUID NOT NULL,
			user_id BIGINT NOT NULL,
			joined_at TIMESTAMPTZ NOT NULL,
			left_at TIMESTAMPTZ,
			last_cleared_at TIMESTAMPTZ
		);
	`)
	require.NoError(t, err)

	conversationID := uuid.New()
	senderID, recipientID := int64(101), int64(202)
	_, err = pool.Exec(ctx, `INSERT INTO users(id) VALUES ($1), ($2)`, senderID, recipientID)
	require.NoError(t, err)
	joinedAt := time.Now().Add(-time.Hour)
	_, err = pool.Exec(ctx, `INSERT INTO conversation_participants(conversation_id,user_id,joined_at) VALUES ($1,$2,$3),($1,$4,$3)`, conversationID, senderID, joinedAt, recipientID)
	require.NoError(t, err)

	firstID, secondID := uuid.New(), uuid.New()
	firstAt := time.Now().Add(-2 * time.Minute)
	secondAt := firstAt.Add(time.Minute)
	_, err = pool.Exec(ctx, `
		INSERT INTO messages(id,conversation_id,sender_id,created_at)
		VALUES ($1,$3,$4,$6), ($2,$3,$4,$5)
	`, firstID, secondID, conversationID, senderID, secondAt, firstAt)
	require.NoError(t, err)

	repo := NewMessageRepository(pool, zap.NewNop())
	require.NoError(t, repo.BulkCreateMessageStatus(ctx, firstID, []int64{recipientID}))
	require.NoError(t, repo.BulkCreateMessageStatus(ctx, secondID, []int64{recipientID}))

	changed, err := repo.MarkAsDelivered(ctx, firstID, recipientID)
	require.NoError(t, err)
	require.True(t, changed)
	changed, err = repo.MarkAsDelivered(ctx, firstID, recipientID)
	require.NoError(t, err)
	require.False(t, changed, "repeated delivery acknowledgement must be idempotent")

	updates, err := repo.MarkMessagesAsRead(ctx, conversationID, recipientID, firstID)
	require.NoError(t, err)
	require.Len(t, updates, 1)
	require.Equal(t, firstID, updates[0].MessageID)

	unreadCount, err := repo.CountUnreadMessages(ctx, conversationID, recipientID)
	require.NoError(t, err)
	require.Equal(t, 1, unreadCount, "reading an older message must keep newer messages unread")

	updates, err = repo.MarkMessagesAsRead(ctx, conversationID, recipientID, firstID)
	require.NoError(t, err)
	require.Empty(t, updates, "repeated read acknowledgement must not emit another receipt")

	_, err = pool.Exec(ctx, `INSERT INTO deleted_messages(user_id,message_id) VALUES ($1,$2)`, recipientID, secondID)
	require.NoError(t, err)
	unreadCount, err = repo.CountUnreadMessages(ctx, conversationID, recipientID)
	require.NoError(t, err)
	require.Zero(t, unreadCount, "messages deleted for the user must not contribute to their badge")
}
