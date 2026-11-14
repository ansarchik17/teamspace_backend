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
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

func TestNoteAccessWithoutMessengerTables(t *testing.T) {
	dsn := os.Getenv("TEST_MESSENGER_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_MESSENGER_DATABASE_URL to a disposable PostgreSQL database")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	defer admin.Close()
	schema := "note_access_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	defer func() { _, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE") }()
	cfg, err := pgxpool.ParseConfig(dsn)
	require.NoError(t, err)
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	defer pool.Close()
	_, err = pool.Exec(ctx, `CREATE TABLE notes (
 id UUID PRIMARY KEY, user_id BIGINT, folder_id UUID, title TEXT DEFAULT '', content TEXT DEFAULT '',
 content_doc JSONB, content_text TEXT DEFAULT '', type TEXT DEFAULT 'text', checklist_items JSONB DEFAULT '[]',
 is_pinned BOOLEAN DEFAULT false, is_archived BOOLEAN DEFAULT false, is_deleted BOOLEAN DEFAULT false,
 deleted_at TIMESTAMPTZ, created_at TIMESTAMPTZ DEFAULT now(), updated_at TIMESTAMPTZ DEFAULT now(),
 version BIGINT DEFAULT 1, color TEXT DEFAULT '', tags TEXT[] DEFAULT '{}', is_favorite BOOLEAN DEFAULT false);
 CREATE TABLE note_shares (note_id UUID, user_id BIGINT, is_hidden BOOLEAN DEFAULT false, PRIMARY KEY(note_id,user_id));`)
	require.NoError(t, err)
	id := uuid.New()
	_, err = pool.Exec(ctx, `INSERT INTO notes(id,user_id,title) VALUES($1,1,'Shared note');`, id)
	require.NoError(t, err)
	repo := NewNoteRepository(pool, zap.NewNop())
	_, err = repo.GetNoteByID(ctx, 1, id)
	require.NoError(t, err, "owner access must not require chat tables")
	_, err = repo.GetNoteByID(ctx, 2, id)
	require.Error(t, err, "an unshared note must remain private")
	_, err = pool.Exec(ctx, `INSERT INTO note_shares(note_id,user_id) VALUES($1,2)`, id)
	require.NoError(t, err)
	_, err = repo.GetNoteByID(ctx, 2, id)
	require.NoError(t, err)
	notes, _, err := repo.ListNotes(ctx, domain.NoteListFilter{UserID: 2, Scope: "shared", Limit: 20})
	require.NoError(t, err)
	require.Len(t, notes, 1)
	notes, _, err = repo.ListSyncNotes(ctx, domain.NoteSyncFilter{UserID: 2, Since: time.Unix(0, 0), Limit: 20})
	require.NoError(t, err)
	require.Len(t, notes, 1)
	_, err = pool.Exec(ctx, `UPDATE note_shares SET is_hidden=true WHERE note_id=$1`, id)
	require.NoError(t, err)
	_, err = repo.GetNoteByID(ctx, 2, id)
	require.Error(t, err)
	notes, _, err = repo.ListNotes(ctx, domain.NoteListFilter{UserID: 2, Limit: 20})
	require.NoError(t, err)
	require.Empty(t, notes)
	notes, _, err = repo.ListSyncNotes(ctx, domain.NoteSyncFilter{UserID: 2, Since: time.Unix(0, 0), Limit: 20})
	require.NoError(t, err)
	require.Empty(t, notes)
}
