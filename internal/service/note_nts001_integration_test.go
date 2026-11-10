package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
	"os"
	"strings"
	"testing"
	"time"
)

func TestNTS001IntegrationCreateEditColorVersionAndOwnership(t *testing.T) {
	_, pool := roomIntegrationService(t)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `CREATE TABLE files(id UUID PRIMARY KEY,real_name TEXT,mime_type TEXT,size BIGINT,object_name TEXT,deleted_at TIMESTAMPTZ);
 CREATE TABLE note_shares(note_id UUID,user_id BIGINT,is_hidden BOOLEAN DEFAULT false);
 CREATE TABLE messages(is_deleted BOOLEAN,metadata JSONB,conversation_id UUID);
 CREATE TABLE conversation_participants(conversation_id UUID,user_id BIGINT,left_at TIMESTAMPTZ);
 CREATE TABLE group_members(group_id UUID,user_id BIGINT,left_at TIMESTAMPTZ);`)
	require.NoError(t, err)
	for _, name := range []string{"20260323000100_create_notes_module.sql", "20260325000100_notes_rich_content.sql", "20260325000200_fix_notes_type_check.sql", "20260922000100_note_meeting_result.sql", "20260929000600_notes_formatting_color.sql", "20260929000700_notes_navigation_tags_favorites.sql", "20260930000100_notes_audio_recording.sql"} {
		data, err := os.ReadFile("../../db/migrations/" + name)
		require.NoError(t, err)
		_, err = pool.Exec(ctx, strings.Split(string(data), "-- +goose Down")[0])
		require.NoError(t, err)
	}
	repo := repository.NewNoteRepository(pool, zap.NewNop())
	svc := NewNoteService(repo, nil, zap.NewNop())
	doc := &domain.ContentDoc{Type: "doc", Version: 1, Blocks: []domain.ContentBlock{{ID: "b", Type: "paragraph", ListType: "ordered", Runs: []domain.ContentRun{{Text: "Title", Marks: []string{"heading2", "bold"}}}}}}
	note, err := svc.CreateNote(ctx, 1, &domain.CreateNoteRequest{Title: "NTS001", Type: "rich", Color: "#1687FF", ContentDoc: doc})
	require.NoError(t, err)
	got, err := svc.GetNote(ctx, 1, note.ID)
	require.NoError(t, err)
	require.Equal(t, "#1687FF", got.Color)
	require.Equal(t, doc, got.ContentDoc)
	doc.Page = &domain.NotePageMetadata{Icon: "💡", Description: "Сохранено на сервере"}
	color := "#19B96B"
	updated, err := svc.UpdateNote(ctx, 1, note.ID, &domain.UpdateNoteRequest{Version: note.Version, Color: &color, ContentDoc: doc})
	require.NoError(t, err)
	require.Equal(t, 2, updated.Version)
	require.Equal(t, color, updated.Color)
	// A fresh service/read must restore metadata without any browser cache.
	freshService := NewNoteService(repository.NewNoteRepository(pool, zap.NewNop()), nil, zap.NewNop())
	fresh, err := freshService.GetNote(ctx, 1, note.ID)
	require.NoError(t, err)
	require.Equal(t, doc.Page, fresh.ContentDoc.Page)
	_, err = svc.UpdateNote(ctx, 1, note.ID, &domain.UpdateNoteRequest{Version: 1, Color: &color})
	require.Error(t, err)
	_, err = svc.UpdateNote(ctx, 2, note.ID, &domain.UpdateNoteRequest{Version: 2, Color: &color})
	require.Error(t, err)
	_, err = pool.Exec(ctx, "INSERT INTO note_shares(note_id,user_id) VALUES($1,2)", note.ID)
	require.NoError(t, err)
	_, err = svc.applySyncUpsert(ctx, 2, domain.SyncNoteUpsertRequest{ID: note.ID, Version: 2, Type: "rich", Color: &color})
	require.Error(t, err)
	own, err := repo.GetOwnedNoteByID(ctx, 1, note.ID)
	require.NoError(t, err)
	require.Equal(t, 2, own.Version)
	reset := ""
	cleared, err := svc.UpdateNote(ctx, 1, note.ID, &domain.UpdateNoteRequest{Version: 2, Color: &reset})
	require.NoError(t, err)
	require.Empty(t, cleared.Color)
	list, _, err := repo.ListNotes(ctx, domain.NoteListFilter{UserID: 1})
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Empty(t, list[0].Color)
	require.Equal(t, doc.Page, list[0].ContentDoc.Page)
	synced, _, err := repo.ListSyncNotes(ctx, domain.NoteSyncFilter{UserID: 1, Since: time.Time{}})
	require.NoError(t, err)
	require.Len(t, synced, 1)
	_, err = pool.Exec(ctx, "UPDATE notes SET color='invalid' WHERE id=$1", note.ID)
	require.Error(t, err)
}
