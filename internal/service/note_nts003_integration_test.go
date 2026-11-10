package service

import (
	"context"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/uuid"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

func TestNTS003IntegrationAutosaveTrashAndRestore(t *testing.T) {
	_, pool := roomIntegrationService(t)
	ctx := context.Background()

	_, err := pool.Exec(ctx, `
		CREATE TABLE files(id UUID PRIMARY KEY,real_name TEXT,mime_type TEXT,size BIGINT,object_name TEXT,deleted_at TIMESTAMPTZ);
		CREATE TABLE note_shares(note_id UUID,user_id BIGINT,is_hidden BOOLEAN DEFAULT false);
		CREATE TABLE messages(is_deleted BOOLEAN,metadata JSONB,conversation_id UUID);
		CREATE TABLE conversation_participants(conversation_id UUID,user_id BIGINT,left_at TIMESTAMPTZ);
		CREATE TABLE group_members(group_id UUID,user_id BIGINT,left_at TIMESTAMPTZ);
	`)
	require.NoError(t, err)

	migrations := []string{
		"20260323000100_create_notes_module.sql",
		"20260325000100_notes_rich_content.sql",
		"20260325000200_fix_notes_type_check.sql",
		"20260921000100_add_color_to_note_folders.sql",
		"20260922000100_note_meeting_result.sql",
		"20260929000600_notes_formatting_color.sql",
		"20260929000700_notes_navigation_tags_favorites.sql",
	}
	for _, name := range migrations {
		data, err := os.ReadFile("../../db/migrations/" + name)
		require.NoError(t, err)
		_, err = pool.Exec(ctx, strings.Split(string(data), "-- +goose Down")[0])
		require.NoError(t, err)
	}

	const migrationName = "20260929000800_notes_reliability_trash_autosave.sql"
	data, err := os.ReadFile("../../db/migrations/" + migrationName)
	require.NoError(t, err)
	db := stdlib.OpenDB(*pool.Config().ConnConfig)
	t.Cleanup(func() { _ = db.Close() })
	provider, err := goose.NewProvider(goose.DialectPostgres, db, fstest.MapFS{migrationName: &fstest.MapFile{Data: data}})
	require.NoError(t, err)
	_, err = provider.Up(ctx)
	require.NoError(t, err)
	cleanupMigration, err := os.ReadFile("../../db/migrations/20260929000900_note_object_cleanup.sql")
	require.NoError(t, err)
	_, err = pool.Exec(ctx, strings.Split(string(cleanupMigration), "-- +goose Down")[0])
	require.NoError(t, err)
	audioMigration, err := os.ReadFile("../../db/migrations/20260930000100_notes_audio_recording.sql")
	require.NoError(t, err)
	_, err = pool.Exec(ctx, strings.Split(string(audioMigration), "-- +goose Down")[0])
	require.NoError(t, err)

	repo := repository.NewNoteRepository(pool, zap.NewNop())
	svc := NewNoteService(repo, nil, zap.NewNop())
	const userID = int64(1)

	// 1. Autosave: Create new draft
	draft, err := svc.AutosaveNote(ctx, userID, &domain.AutosaveNoteRequest{
		Title:   "Draft note via Autosave",
		Content: "Initial draft typing...",
		Type:    "text",
	})
	require.NoError(t, err)
	require.NotNil(t, draft)
	require.Equal(t, 1, draft.Version)
	require.Equal(t, "Draft note via Autosave", draft.Title)
	// Active notes cannot bypass the trash retention/undo workflow.
	require.Error(t, svc.HardDeleteNote(ctx, userID, draft.ID, draft.Version))
	// No-version and cross-user autosave updates must be rejected.
	_, err = svc.AutosaveNote(ctx, userID, &domain.AutosaveNoteRequest{ID: &draft.ID, Content: "stale"})
	require.Error(t, err)
	_, err = svc.AutosaveNote(ctx, 999, &domain.AutosaveNoteRequest{ID: &draft.ID, Version: 1, Content: "foreign"})
	require.Error(t, err)

	// 2. Autosave: Update existing note
	// Both explicit shares and notes sent through chats must remain read-only.
	conversationID, groupID := uuid.New(), uuid.New()
	_, err = pool.Exec(ctx, `INSERT INTO note_shares(note_id,user_id) VALUES($1,2)`, draft.ID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO messages(is_deleted,metadata,conversation_id)
		VALUES(false,jsonb_build_object('kind','note','note_id',$1::text),$2),
		(false,jsonb_build_object('type','note','noteId',$1::text),$3)`, draft.ID.String(), conversationID, groupID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO conversation_participants(conversation_id,user_id) VALUES($1,3)`, conversationID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO group_members(group_id,user_id) VALUES($1,4)`, groupID)
	require.NoError(t, err)
	for _, recipientID := range []int64{2, 3, 4} {
		items, _, listErr := repo.ListNotes(ctx, domain.NoteListFilter{UserID: recipientID, Scope: "shared", Limit: 1})
		require.NoError(t, listErr)
		require.Len(t, items, 1)
		require.Equal(t, draft.ID, items[0].ID)
		owned, _, listErr := repo.ListNotes(ctx, domain.NoteListFilter{UserID: recipientID, Scope: "owned"})
		require.NoError(t, listErr)
		require.Empty(t, owned)
		visible, readErr := repo.GetNoteByID(ctx, recipientID, draft.ID)
		require.NoError(t, readErr)
		require.Equal(t, draft.Content, visible.Content)
		title := "recipient edit"
		mutations := []func() error{
			func() error { return svc.DeleteAttachment(ctx, recipientID, draft.ID, uuid.New()) },
			func() error { return svc.DeletePhotoAttachment(ctx, recipientID, draft.ID, uuid.New()) },
			func() error { return svc.DeleteAudioAttachment(ctx, recipientID, draft.ID, uuid.New()) },
			func() error { _, e := svc.CompletePhotoUpload(ctx, recipientID, draft.ID, uuid.New()); return e },
			func() error { _, e := svc.SoftDeleteNote(ctx, recipientID, draft.ID, 1); return e },
			func() error { return svc.HardDeleteNote(ctx, recipientID, draft.ID, 1) },
			func() error { return svc.HardDeleteNote(ctx, recipientID, draft.ID, 0) },
			func() error { _, e := svc.RestoreNote(ctx, recipientID, draft.ID, 1); return e },
			func() error {
				_, e := svc.UpdateNote(ctx, recipientID, draft.ID, &domain.UpdateNoteRequest{Title: &title, Version: 1})
				return e
			},
			func() error {
				_, e := svc.AutosaveNote(ctx, recipientID, &domain.AutosaveNoteRequest{ID: &draft.ID, Version: 1, Content: title})
				return e
			},
			func() error {
				_, e := svc.MoveNote(ctx, recipientID, draft.ID, &domain.MoveNoteRequest{Version: 1})
				return e
			},
			func() error { _, e := svc.ClearTableCell(ctx, recipientID, draft.ID, "table", 0, 0, 1); return e },
			func() error { _, e := svc.SetPinned(ctx, recipientID, draft.ID, 1, true); return e },
		}
		for _, mutate := range mutations {
			mutationErr := mutate()
			require.Error(t, mutationErr)
			appErr := apperrors.GetAppError(mutationErr)
			require.NotNil(t, appErr)
			require.Equal(t, apperrors.ErrCodeForbidden, appErr.Code)
		}
	}
	sharedWithOwner, _, err := repo.ListNotes(ctx, domain.NoteListFilter{UserID: userID, Scope: "shared"})
	require.NoError(t, err)
	require.Empty(t, sharedWithOwner)
	unchanged, err := repo.GetOwnedNoteByID(ctx, userID, draft.ID)
	require.NoError(t, err)
	require.Equal(t, draft.Title, unchanged.Title)
	require.Equal(t, draft.Content, unchanged.Content)
	require.Equal(t, draft.Version, unchanged.Version)

	updated, err := svc.AutosaveNote(ctx, userID, &domain.AutosaveNoteRequest{
		ID:      &draft.ID,
		Title:   "Updated Draft Title",
		Content: "More typed content...",
		Version: draft.Version,
	})
	require.NoError(t, err)
	require.Equal(t, 2, updated.Version)
	require.Equal(t, "Updated Draft Title", updated.Title)

	// 3. Autosave: Conflict with outdated version
	_, err = svc.AutosaveNote(ctx, userID, &domain.AutosaveNoteRequest{
		ID:      &draft.ID,
		Title:   "Stale Autosave",
		Version: 1, // outdated, current is 2
	})
	require.Error(t, err)

	// 4. Soft delete (move to trash)
	trashed, err := svc.SoftDeleteNote(ctx, userID, draft.ID, updated.Version)
	require.NoError(t, err)
	require.True(t, trashed.IsDeleted)
	require.NotNil(t, trashed.DeletedAt)
	require.NotNil(t, trashed.ExpiresAt)

	// 5. Verify trashed note cannot be edited or moved while in trash
	newTitle := "Illegal edit"
	_, err = svc.UpdateNote(ctx, userID, draft.ID, &domain.UpdateNoteRequest{
		Title:   &newTitle,
		Version: trashed.Version,
	})
	require.Error(t, err)

	_, err = svc.AutosaveNote(ctx, userID, &domain.AutosaveNoteRequest{
		ID:      &draft.ID,
		Title:   newTitle,
		Version: trashed.Version,
	})
	require.Error(t, err)

	// 6. Trash count and listing
	countResp, err := svc.GetTrashCount(ctx, userID)
	require.NoError(t, err)
	require.Equal(t, int64(1), countResp.Count)

	trashList, err := svc.ListTrash(ctx, domain.NoteListFilter{UserID: userID})
	require.NoError(t, err)
	require.Len(t, trashList.Items, 1)
	require.Equal(t, draft.ID, trashList.Items[0].ID)
	require.NotNil(t, trashList.Items[0].ExpiresAt)

	// 7. Restore note from trash (Откат)
	restored, err := svc.RestoreNote(ctx, userID, draft.ID, trashed.Version)
	require.NoError(t, err)
	require.False(t, restored.IsDeleted)
	require.Nil(t, restored.DeletedAt)

	// Trash count is now 0
	countResp, err = svc.GetTrashCount(ctx, userID)
	require.NoError(t, err)
	require.Equal(t, int64(0), countResp.Count)

	// 8. Soft-delete again and empty trash
	trashed2, err := svc.SoftDeleteNote(ctx, userID, draft.ID, restored.Version)
	require.NoError(t, err)
	require.True(t, trashed2.IsDeleted)

	emptyResp, err := svc.EmptyTrash(ctx, userID)
	require.NoError(t, err)
	require.Equal(t, int64(1), emptyResp.PurgedCount)

	// Note is now permanently gone
	_, err = svc.GetNote(ctx, userID, draft.ID)
	require.Error(t, err)

	// Trash pagination must use deletion time even when update time differs.
	var ids []string
	for i := 0; i < 3; i++ {
		n, err := svc.CreateNote(ctx, userID, &domain.CreateNoteRequest{Title: "pagination", Type: "text"})
		require.NoError(t, err)
		_, err = svc.SoftDeleteNote(ctx, userID, n.ID, n.Version)
		require.NoError(t, err)
		_, err = pool.Exec(ctx, `UPDATE notes SET deleted_at=$2, updated_at=$3 WHERE id=$1`, n.ID,
			time.Now().Add(-time.Duration(i+1)*time.Hour), time.Now().Add(time.Duration(i)*time.Hour))
		require.NoError(t, err)
		ids = append(ids, n.ID.String())
	}
	cursor := ""
	for _, id := range ids {
		page, err := svc.ListTrash(ctx, domain.NoteListFilter{UserID: userID, Limit: 1, Cursor: cursor})
		require.NoError(t, err)
		require.Len(t, page.Items, 1)
		require.Equal(t, id, page.Items[0].ID.String())
		cursor = page.NextCursor
	}
	require.Empty(t, cursor)

	// Storage outages retain durable jobs, which can succeed on a later attempt.
	_, err = pool.Exec(ctx, `INSERT INTO note_object_cleanup(object_name) VALUES('test/retry')`)
	require.NoError(t, err)
	require.NoError(t, repo.ProcessNoteObjectCleanup(ctx, func(context.Context, string) error { return context.DeadlineExceeded }))
	var pending int
	require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM note_object_cleanup WHERE object_name='test/retry'`).Scan(&pending))
	require.Equal(t, 1, pending)
	_, err = pool.Exec(ctx, `UPDATE note_object_cleanup SET next_attempt_at=NOW()`)
	require.NoError(t, err)
	require.NoError(t, repo.ProcessNoteObjectCleanup(ctx, func(context.Context, string) error { return nil }))
	require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM note_object_cleanup`).Scan(&pending))
	require.Zero(t, pending)

	fileID, photoID := uuid.New(), uuid.New()
	_, err = pool.Exec(ctx, `INSERT INTO files(id, real_name, mime_type, size, object_name) VALUES($1,'test','text/plain',1,'test/file')`, fileID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO note_attachments(note_id,file_id) VALUES($1,$2)`, ids[0], fileID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO note_photo_attachments(id,note_id,user_id,storage_key,mime_type) VALUES($1,$2,$3,'test/photo','image/png')`, photoID, ids[0], userID)
	require.NoError(t, err)
	cleared, err := svc.EmptyTrash(ctx, userID)
	require.NoError(t, err)
	require.Equal(t, int64(3), cleared.PurgedCount)
	var removed []string
	require.NoError(t, repo.ProcessNoteObjectCleanup(ctx, func(_ context.Context, key string) error { removed = append(removed, key); return nil }))
	require.ElementsMatch(t, []string{"test/file", "test/photo"}, removed)
}
