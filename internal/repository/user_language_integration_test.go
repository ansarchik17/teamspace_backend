package repository

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestUserLanguagePersistence(t *testing.T) {
	dsn := os.Getenv("TEST_MESSENGER_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_MESSENGER_DATABASE_URL to a disposable PostgreSQL database")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	defer admin.Close()
	schema := "language_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	defer func() { _, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE") }()
	cfg, err := pgxpool.ParseConfig(dsn)
	require.NoError(t, err)
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	defer pool.Close()
	_, err = pool.Exec(ctx, `CREATE TABLE users (
 id BIGINT PRIMARY KEY, keycloak_id TEXT, iin TEXT, email TEXT DEFAULT '', username TEXT DEFAULT '',
 password_hash TEXT, first_name TEXT DEFAULT '', last_name TEXT DEFAULT '', avatar_url TEXT, bio TEXT, position TEXT,
 is_active BOOLEAN DEFAULT TRUE, is_first_login BOOLEAN DEFAULT TRUE, last_login_at TIMESTAMPTZ, last_seen_at TIMESTAMPTZ,
 created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ DEFAULT NOW(), deleted_at TIMESTAMPTZ,
 role TEXT DEFAULT 'user', availability_status TEXT, full_name_local TEXT,
 can_view_full_drive_activity BOOLEAN DEFAULT FALSE, organization_id UUID, team_id UUID, manager_id BIGINT,
 ministry_id BIGINT, department_id BIGINT);
 INSERT INTO users (id) VALUES (1), (2);`)
	require.NoError(t, err)
	migration, err := os.ReadFile("../../db/migrations/20261009000100_user_language.sql")
	require.NoError(t, err)
	parts := strings.Split(string(migration), "-- +goose Down")
	_, err = pool.Exec(ctx, parts[0])
	require.NoError(t, err)
	repo := NewUserRepository(pool, zap.NewNop())
	user, err := repo.GetByID(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, "ru", user.Language)
	for _, language := range []string{"kk", "ru", "kk"} {
		require.NoError(t, repo.UpdateLanguage(ctx, 1, language))
		// A separate connection pool represents a subsequent session/device.
		other, err := pgxpool.NewWithConfig(ctx, cfg.Copy())
		require.NoError(t, err)
		details, err := NewUserRepository(other, zap.NewNop()).GetFullDetailsByID(ctx, 1)
		other.Close()
		require.NoError(t, err)
		require.Equal(t, language, details.Language)
	}
	for _, invalid := range []string{"", "en", "RU", "kk-KZ"} {
		require.Error(t, repo.UpdateLanguage(ctx, 1, invalid))
	}
	user, err = repo.GetByID(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, "kk", user.ToResponse().Language)
	user, err = repo.GetByID(ctx, 2)
	require.NoError(t, err)
	require.Equal(t, "ru", user.Language)
	_, err = pool.Exec(ctx, "INSERT INTO users (id) VALUES (3)")
	require.NoError(t, err)
	user, err = repo.GetByID(ctx, 3)
	require.NoError(t, err)
	require.Equal(t, "ru", user.Language)
	_, err = pool.Exec(ctx, "UPDATE users SET language = 'en' WHERE id = 1")
	require.Error(t, err)
	require.ErrorIs(t, repo.UpdateLanguage(ctx, 999, "kk"), pgx.ErrNoRows)
	_, err = pool.Exec(ctx, "UPDATE users SET deleted_at = NOW() WHERE id = 2")
	require.NoError(t, err)
	require.ErrorIs(t, repo.UpdateLanguage(ctx, 2, "kk"), pgx.ErrNoRows)
	_, err = pool.Exec(ctx, parts[1])
	require.NoError(t, err)
}
