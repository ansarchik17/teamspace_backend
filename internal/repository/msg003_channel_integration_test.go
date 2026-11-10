package repository

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

// Uses a disposable schema in PostgreSQL. The test is skipped unless an
// explicit test DSN is provided, so production databases are never selected by
// accident.
func TestMSG003ChannelInteractionsIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_MESSENGER_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_MESSENGER_DATABASE_URL to a disposable PostgreSQL database")
	}

	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	defer admin.Close()

	schema := "msg003_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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
		CREATE TABLE users (
			id BIGINT PRIMARY KEY,
			username TEXT NOT NULL,
			first_name TEXT,
			last_name TEXT,
			avatar_url TEXT
		);
		CREATE TABLE channels (
			id UUID PRIMARY KEY,
			name TEXT NOT NULL,
			avatar_url TEXT,
			type TEXT NOT NULL,
			deleted_at TIMESTAMPTZ
		);
		CREATE TABLE channel_members (
			channel_id UUID NOT NULL,
			user_id BIGINT NOT NULL,
			is_muted BOOLEAN NOT NULL DEFAULT FALSE,
			left_at TIMESTAMPTZ,
			PRIMARY KEY(channel_id, user_id)
		);
		CREATE TABLE channel_posts (
			id UUID PRIMARY KEY,
			channel_id UUID NOT NULL REFERENCES channels(id),
			author_id BIGINT NOT NULL REFERENCES users(id),
			content TEXT,
			attachment_url TEXT,
			attachment_type TEXT,
			attachment_size BIGINT,
			attachment_name TEXT,
			is_pinned BOOLEAN NOT NULL DEFAULT FALSE,
			is_deleted BOOLEAN NOT NULL DEFAULT FALSE,
			is_edited BOOLEAN NOT NULL DEFAULT FALSE,
			is_forwarded BOOLEAN NOT NULL DEFAULT FALSE,
			original_channel_id UUID,
			original_post_id UUID,
			views_count INT NOT NULL DEFAULT 0,
			comments_count INT NOT NULL DEFAULT 0,
			linked_group_id UUID,
			metadata JSONB,
			edited_at TIMESTAMPTZ,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			deleted_at TIMESTAMPTZ
		);
		CREATE TABLE channel_post_views (
			post_id UUID NOT NULL,
			user_id BIGINT NOT NULL,
			viewed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY(post_id, user_id)
		);
		CREATE TABLE channel_post_reactions (
			post_id UUID NOT NULL REFERENCES channel_posts(id) ON DELETE CASCADE,
			user_id BIGINT NOT NULL REFERENCES users(id),
			reaction VARCHAR(32) NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY(post_id, user_id, reaction)
		);
		CREATE TABLE messages (
			id UUID PRIMARY KEY,
			content TEXT,
			type TEXT NOT NULL DEFAULT 'text',
			is_deleted BOOLEAN NOT NULL DEFAULT FALSE
		);
		CREATE TABLE pinned_messages (
			id UUID PRIMARY KEY,
			chat_id UUID NOT NULL,
			chat_type TEXT NOT NULL,
			message_id UUID NOT NULL UNIQUE,
			pinned_by BIGINT NOT NULL,
			position INT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL
		);
	`)
	require.NoError(t, err)

	migration, err := os.ReadFile("../../db/migrations/20260923000100_add_channel_post_replies.sql")
	require.NoError(t, err)
	_, err = pool.Exec(ctx, strings.Split(string(migration), "-- +goose Down")[0])
	require.NoError(t, err)
	mentionMigration, err := os.ReadFile("../../db/migrations/20260923000200_create_channel_post_mentions.sql")
	require.NoError(t, err)
	_, err = pool.Exec(ctx, strings.Split(string(mentionMigration), "-- +goose Down")[0])
	require.NoError(t, err)

	publicChannelID := uuid.New()
	privateChannelID := uuid.New()
	_, err = pool.Exec(ctx, `INSERT INTO users(id, username, first_name, last_name) VALUES
		(1, 'owner', 'Owner', 'One'), (2, 'reader', 'Reader', 'Two')`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO channels(id, name, type) VALUES
		($1, 'Public', 'public'), ($2, 'Private', 'private')`, publicChannelID, privateChannelID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO channel_members(channel_id, user_id, is_muted) VALUES
		($1, 1, FALSE), ($1, 2, TRUE), ($2, 1, FALSE)`, publicChannelID, privateChannelID)
	require.NoError(t, err)

	channelRepo := NewChannelRepository(pool, zap.NewNop())
	pinnedRepo := NewPinnedRepository(pool, zap.NewNop())
	content := "root"
	root := &domain.ChannelPost{ChannelID: publicChannelID, AuthorID: 1, Content: &content}
	require.NoError(t, channelRepo.CreatePost(ctx, root))
	replyContent := "reply"
	reply := &domain.ChannelPost{ChannelID: publicChannelID, AuthorID: 1, ReplyToID: &root.ID, Content: &replyContent}
	require.NoError(t, channelRepo.CreatePost(ctx, reply))
	nestedContent := "nested"
	nested := &domain.ChannelPost{ChannelID: publicChannelID, AuthorID: 1, ReplyToID: &reply.ID, Content: &nestedContent}
	require.NoError(t, channelRepo.CreatePost(ctx, nested))

	descendants, truncated, err := channelRepo.GetPostReplyDescendants(ctx, root.ID, 1, 200)
	require.NoError(t, err)
	require.False(t, truncated)
	require.Len(t, descendants, 2)
	byID := map[uuid.UUID]domain.ChannelPostView{}
	for _, post := range descendants {
		byID[post.ID] = post
	}
	require.Equal(t, root.ID, *byID[reply.ID].ReplyToID)
	require.Equal(t, reply.ID, *byID[nested.ID].ReplyToID)

	privateRoot := &domain.ChannelPost{ChannelID: privateChannelID, AuthorID: 1, Content: &content}
	require.NoError(t, channelRepo.CreatePost(ctx, privateRoot))
	_, err = channelRepo.GetPost(ctx, privateRoot.ID, 2)
	require.Error(t, err, "non-member must not read a private channel thread root")

	require.NoError(t, channelRepo.AddReaction(ctx, root.ID, 1, "👍"))
	reactions, err := channelRepo.GetReactions(ctx, root.ID, 1)
	require.NoError(t, err)
	require.Len(t, reactions, 1)
	require.Equal(t, 1, reactions[0].Count)
	require.True(t, reactions[0].Reacted)

	muteMap, err := channelRepo.GetActiveMemberMuteMap(ctx, publicChannelID)
	require.NoError(t, err)
	require.True(t, muteMap[2], "mentions must still target muted subscribers")
	require.NoError(t, channelRepo.CreatePostMentions(ctx, root.ID, []int64{2}))
	var mentionCount int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM channel_post_mentions WHERE post_id=$1 AND user_id=2`, root.ID).Scan(&mentionCount))
	require.Equal(t, 1, mentionCount)

	belongs, err := pinnedRepo.MessageBelongsToChat(ctx, publicChannelID, "channel", root.ID)
	require.NoError(t, err)
	require.True(t, belongs)
	belongs, err = pinnedRepo.MessageBelongsToChat(ctx, privateChannelID, "channel", root.ID)
	require.NoError(t, err)
	require.False(t, belongs)

	require.NoError(t, pinnedRepo.PinMessage(ctx, publicChannelID, "channel", root.ID, 1))
	pinned, err := pinnedRepo.GetPinnedMessages(ctx, publicChannelID, "channel")
	require.NoError(t, err)
	require.Len(t, pinned, 1)
	require.Equal(t, root.ID, pinned[0].MessageID)
	require.NoError(t, pinnedRepo.UnpinMessage(ctx, root.ID))
	pinned, err = pinnedRepo.GetPinnedMessages(ctx, publicChannelID, "channel")
	require.NoError(t, err)
	require.Empty(t, pinned)
}
