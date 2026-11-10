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

// Exercise the actual list queries, not a simplified copy of the badge SQL.
func TestUnifiedListBadgesAndHistoryIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_MESSENGER_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_MESSENGER_DATABASE_URL to a disposable PostgreSQL database")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	defer admin.Close()
	schema := "unified_list_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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
		CREATE TABLE users(id BIGINT PRIMARY KEY, username TEXT NOT NULL, first_name TEXT NOT NULL, last_name TEXT NOT NULL, avatar_url TEXT, last_seen_at TIMESTAMPTZ);
		CREATE TABLE conversations(id UUID PRIMARY KEY, type TEXT, created_at TIMESTAMPTZ DEFAULT now(), updated_at TIMESTAMPTZ DEFAULT now(), deleted_at TIMESTAMPTZ);
		CREATE TABLE conversation_participants(conversation_id UUID, user_id BIGINT, joined_at TIMESTAMPTZ, left_at TIMESTAMPTZ, hidden_at TIMESTAMPTZ, last_cleared_at TIMESTAMPTZ, is_muted BOOLEAN DEFAULT false, PRIMARY KEY(conversation_id,user_id));
		CREATE TABLE groups(id UUID PRIMARY KEY, name TEXT, description TEXT, avatar_url TEXT, type TEXT, max_members INTEGER, created_by BIGINT, is_active BOOLEAN DEFAULT true, created_at TIMESTAMPTZ DEFAULT now(), updated_at TIMESTAMPTZ DEFAULT now(), deleted_at TIMESTAMPTZ);
		CREATE TABLE group_members(group_id UUID, user_id BIGINT, role TEXT, joined_at TIMESTAMPTZ, left_at TIMESTAMPTZ, hidden_at TIMESTAMPTZ, last_cleared_at TIMESTAMPTZ, is_muted BOOLEAN DEFAULT false, PRIMARY KEY(group_id,user_id));
		CREATE TABLE messages(id UUID PRIMARY KEY, conversation_id UUID, sender_id BIGINT, content TEXT, content_hash TEXT, encrypted_keys JSONB, type TEXT DEFAULT 'text', is_edited BOOLEAN DEFAULT false, is_deleted BOOLEAN DEFAULT false, created_at TIMESTAMPTZ, updated_at TIMESTAMPTZ DEFAULT now());
		CREATE TABLE message_status(message_id UUID, user_id BIGINT, read_at TIMESTAMPTZ, delivered_at TIMESTAMPTZ, PRIMARY KEY(message_id,user_id));
		CREATE TABLE deleted_messages(message_id UUID, user_id BIGINT, PRIMARY KEY(message_id,user_id));
		CREATE TABLE message_mentions(message_id UUID, user_id BIGINT, PRIMARY KEY(message_id,user_id));
		INSERT INTO users(id,username,first_name,last_name) VALUES (1,'viewer','View','User'), (2,'peer','Peer','User');
	`)
	require.NoError(t, err)
	chatRepo := NewChatRepository(pool, nil, zap.NewNop())
	groupRepo := NewGroupRepository(pool, zap.NewNop())
	for _, kind := range []string{"personal", "group"} {
		t.Run(kind, func(t *testing.T) {
			id := uuid.New()
			joined := time.Now().UTC().Truncate(time.Second).Add(-time.Hour)
			var membership, key string
			if kind == "personal" {
				membership, key = "conversation_participants", "conversation_id"
				_, err = pool.Exec(ctx, `INSERT INTO conversations(id,type) VALUES($1,'personal');`, id)
				require.NoError(t, err)
				_, err = pool.Exec(ctx, `INSERT INTO conversation_participants(conversation_id,user_id,joined_at) VALUES($1,1,$2),($1,2,$2)`, id, joined)
			} else {
				membership, key = "group_members", "group_id"
				_, err = pool.Exec(ctx, `INSERT INTO groups(id,name,type,max_members,created_by) VALUES($1,'Group','private',100,2)`, id)
				require.NoError(t, err)
				_, err = pool.Exec(ctx, `INSERT INTO group_members(group_id,user_id,role,joined_at) VALUES($1,1,'member',$2),($1,2,'owner',$2)`, id, joined)
			}
			require.NoError(t, err)
			var latest uuid.UUID
			for i, label := range []string{"before_join", "at_join", "clear_boundary", "read", "deleted_all", "deleted_me", "own", "unread_mention", "missing_status", "delivered_mention", "latest_read"} {
				messageID := uuid.New()
				at := joined.Add(time.Duration(i-1) * time.Minute)
				sender := int64(2)
				if label == "own" {
					sender = 1
				}
				_, err = pool.Exec(ctx, `INSERT INTO messages(id,conversation_id,sender_id,content,created_at,is_deleted) VALUES($1,$2,$3,$4,$5,$6)`, messageID, id, sender, label, at, label == "deleted_all")
				require.NoError(t, err)
				if label == "read" || label == "latest_read" {
					_, err = pool.Exec(ctx, `INSERT INTO message_status(message_id,user_id,read_at) VALUES($1,1,now())`, messageID)
				} else if label != "missing_status" {
					_, err = pool.Exec(ctx, `INSERT INTO message_status(message_id,user_id,delivered_at) VALUES($1,1,now())`, messageID)
				}
				require.NoError(t, err)
				if label == "deleted_me" {
					_, err = pool.Exec(ctx, `INSERT INTO deleted_messages VALUES($1,1)`, messageID)
					require.NoError(t, err)
				}
				// Include excluded mentions and another user's mention to catch over-counting.
				mentionUser := int64(1)
				if label == "missing_status" {
					mentionUser = 2
				}
				_, err = pool.Exec(ctx, `INSERT INTO message_mentions VALUES($1,$2)`, messageID, mentionUser)
				require.NoError(t, err)
				latest = messageID
			}
			check := func(unread, mentions int, preview bool, present bool) {
				t.Helper()
				// The single-view path is also used for support / missing pinned chats.
				if present {
					if kind == "personal" {
						item, e := chatRepo.GetConversationView(ctx, id, 1)
						require.NoError(t, e)
						require.NotNil(t, item)
						require.Equal(t, unread, item.UnreadCount)
						require.Equal(t, mentions, item.MentionCount)
						if preview {
							require.NotNil(t, item.LastMessage)
							require.Equal(t, latest, item.LastMessage.ID)
						} else {
							require.Nil(t, item.LastMessage)
						}
					} else {
						item, e := groupRepo.GetGroupView(ctx, id, 1)
						require.NoError(t, e)
						require.NotNil(t, item)
						require.Equal(t, unread, item.UnreadCount)
						require.Equal(t, mentions, item.MentionCount)
						if preview {
							require.NotNil(t, item.LastMessage)
							require.Equal(t, latest, item.LastMessage.ID)
						} else {
							require.Nil(t, item.LastMessage)
						}
					}
				}
				if kind == "personal" {
					items, e := chatRepo.GetUserConversations(ctx, 1, 0, 0)
					require.NoError(t, e)
					for _, item := range items {
						if item.ID == id {
							require.True(t, present)
							require.Equal(t, unread, item.UnreadCount)
							require.Equal(t, mentions, item.MentionCount)
							if preview {
								require.NotNil(t, item.LastMessage)
								require.Equal(t, latest, item.LastMessage.ID)
							} else {
								require.Nil(t, item.LastMessage)
							}
							return
						}
					}
				} else {
					items, e := groupRepo.GetUserGroups(ctx, 1, 0, 0)
					require.NoError(t, e)
					for _, item := range items {
						if item.ID == id {
							require.True(t, present)
							require.Equal(t, unread, item.UnreadCount)
							require.Equal(t, mentions, item.MentionCount)
							if preview {
								require.NotNil(t, item.LastMessage)
								require.Equal(t, latest, item.LastMessage.ID)
							} else {
								require.Nil(t, item.LastMessage)
							}
							return
						}
					}
				}
				require.False(t, present, "chat missing from list")
			}
			check(5, 4, true, true)
			_, err = pool.Exec(ctx, "UPDATE "+membership+" SET last_cleared_at=$2 WHERE "+key+"=$1 AND user_id=1", id, joined.Add(time.Minute))
			require.NoError(t, err)
			check(3, 2, true, true)
			_, err = pool.Exec(ctx, `UPDATE message_status SET read_at=now() WHERE user_id=1 AND message_id IN (SELECT id FROM messages WHERE conversation_id=$1)`, id)
			require.NoError(t, err)
			check(1, 0, true, true) // missing receipt still means unread.
			_, err = pool.Exec(ctx, "UPDATE "+membership+" SET last_cleared_at=$2 WHERE "+key+"=$1 AND user_id=1", id, joined.Add(time.Hour))
			require.NoError(t, err)
			check(0, 0, false, true)
			_, err = pool.Exec(ctx, "UPDATE "+membership+" SET joined_at=$2,last_cleared_at=NULL WHERE "+key+"=$1 AND user_id=1", id, joined.Add(time.Hour))
			require.NoError(t, err)
			check(0, 0, false, true) // old last-message preview must not leak after rejoining.
			for _, column := range []string{"hidden_at", "left_at"} {
				_, err = pool.Exec(ctx, "UPDATE "+membership+" SET "+column+"=now() WHERE "+key+"=$1 AND user_id=1", id)
				if err != nil {
					t.Fatal(err)
				}
				check(0, 0, false, false)
				_, err = pool.Exec(ctx, "UPDATE "+membership+" SET "+column+"=NULL WHERE "+key+"=$1 AND user_id=1", id)
				require.NoError(t, err)
			}
		})
	}
}
