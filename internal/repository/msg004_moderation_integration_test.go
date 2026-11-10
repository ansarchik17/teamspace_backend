package repository

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestMSG004DeleteScopesIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_MESSENGER_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_MESSENGER_DATABASE_URL to a disposable PostgreSQL database")
	}

	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	defer admin.Close()

	schema := "msg004_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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
		CREATE TABLE messages (
			id UUID PRIMARY KEY,
			is_deleted BOOLEAN NOT NULL DEFAULT FALSE,
			deleted_at TIMESTAMPTZ
		);
		CREATE TABLE deleted_messages (
			user_id BIGINT NOT NULL,
			message_id UUID NOT NULL REFERENCES messages(id),
			PRIMARY KEY (user_id, message_id)
		);
	`)
	require.NoError(t, err)

	repo := NewMessageRepository(pool, zap.NewNop())
	messageID := uuid.New()
	_, err = pool.Exec(ctx, `INSERT INTO messages (id) VALUES ($1)`, messageID)
	require.NoError(t, err)

	require.NoError(t, repo.DeleteMessagesForUser(ctx, 101, []uuid.UUID{messageID}))

	var globallyDeleted bool
	require.NoError(t, pool.QueryRow(ctx, `SELECT is_deleted FROM messages WHERE id=$1`, messageID).Scan(&globallyDeleted))
	require.False(t, globallyDeleted, "delete-for-me must not hide the message from other users")

	var personalDeleteCount int
	require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM deleted_messages WHERE user_id=$1 AND message_id=$2`, 101, messageID).Scan(&personalDeleteCount))
	require.Equal(t, 1, personalDeleteCount)

	require.NoError(t, repo.Delete(ctx, messageID))
	require.NoError(t, pool.QueryRow(ctx, `SELECT is_deleted FROM messages WHERE id=$1`, messageID).Scan(&globallyDeleted))
	require.True(t, globallyDeleted, "delete-for-everyone must soft-delete the shared message")
}
