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

func TestMSG007MembershipMutationsIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_MESSENGER_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_MESSENGER_DATABASE_URL to a disposable PostgreSQL database")
	}

	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	defer admin.Close()

	schema := "msg007_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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
		CREATE TABLE group_members (
			group_id UUID NOT NULL,
			user_id BIGINT NOT NULL,
			role TEXT NOT NULL,
			is_muted BOOLEAN NOT NULL DEFAULT FALSE,
			joined_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			left_at TIMESTAMPTZ,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY(group_id,user_id)
		);
		CREATE TABLE channel_members (
			channel_id UUID NOT NULL,
			user_id BIGINT NOT NULL,
			role TEXT NOT NULL,
			is_muted BOOLEAN NOT NULL DEFAULT FALSE,
			joined_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			left_at TIMESTAMPTZ,
			PRIMARY KEY(channel_id,user_id)
		);
	`)
	require.NoError(t, err)

	groupID, channelID := uuid.New(), uuid.New()
	_, err = pool.Exec(ctx, `INSERT INTO group_members(group_id,user_id,role) VALUES ($1,1,'member')`, groupID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO channel_members(channel_id,user_id,role) VALUES ($1,1,'subscriber')`, channelID)
	require.NoError(t, err)

	groupRepo := NewGroupRepository(pool, zap.NewNop())
	channelRepo := NewChannelRepository(pool, zap.NewNop())

	require.NoError(t, groupRepo.SetMute(ctx, groupID, 1, true))
	muted, err := groupRepo.IsMuted(ctx, groupID, 1)
	require.NoError(t, err)
	require.True(t, muted)
	require.Error(t, groupRepo.SetMute(ctx, groupID, 999, true))

	require.NoError(t, channelRepo.MuteChannel(ctx, channelID, 1, true))
	require.Error(t, channelRepo.MuteChannel(ctx, channelID, 999, true))
	require.Error(t, channelRepo.UpdateMemberRole(ctx, channelID, 999, domain.ChannelRoleAdmin))
	require.Error(t, channelRepo.KickMember(ctx, channelID, 999))

	require.NoError(t, channelRepo.UpdateMemberRole(ctx, channelID, 1, domain.ChannelRoleAdmin))
	require.NoError(t, channelRepo.KickMember(ctx, channelID, 1))
	require.Error(t, channelRepo.MuteChannel(ctx, channelID, 1, false), "inactive members cannot change mute")

	require.NoError(t, groupRepo.RemoveMember(ctx, groupID, 1))
	require.Error(t, groupRepo.RemoveMember(ctx, groupID, 1), "repeated removal must not report success")
}
