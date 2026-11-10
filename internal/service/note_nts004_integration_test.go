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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

func TestNTS004IntegrationAudioNotesAndCleanup(t *testing.T) {
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

	const migrationTrash = "20260929000800_notes_reliability_trash_autosave.sql"
	data, err := os.ReadFile("../../db/migrations/" + migrationTrash)
	require.NoError(t, err)
	db := stdlib.OpenDB(*pool.Config().ConnConfig)
	t.Cleanup(func() { _ = db.Close() })
	provider, err := goose.NewProvider(goose.DialectPostgres, db, fstest.MapFS{migrationTrash: &fstest.MapFile{Data: data}})
	require.NoError(t, err)
	_, err = provider.Up(ctx)
	require.NoError(t, err)

	cleanupMigration, err := os.ReadFile("../../db/migrations/20260929000900_note_object_cleanup.sql")
	require.NoError(t, err)
	_, err = pool.Exec(ctx, strings.Split(string(cleanupMigration), "-- +goose Down")[0])
	require.NoError(t, err)

	// Apply NTS-004 audio recording migration
	audioMigration, err := os.ReadFile("../../db/migrations/20260930000100_notes_audio_recording.sql")
	require.NoError(t, err)
	_, err = pool.Exec(ctx, strings.Split(string(audioMigration), "-- +goose Down")[0])
	require.NoError(t, err)

	repo := repository.NewNoteRepository(pool, zap.NewNop())
	svc := NewNoteService(repo, nil, zap.NewNop())
	const userID = int64(42)
	_, err = pool.Exec(ctx, `INSERT INTO users(id,username,email) VALUES($1,'audio-owner','audio-owner@example.com')`, userID)
	require.NoError(t, err)

	// 1. Create note with type "audio"
	dur := 18.5
	wf := []float64{0.1, 0.5, 0.8, 0.3}
	audioNote, err := svc.CreateNote(ctx, userID, &domain.CreateNoteRequest{
		Title: "Voice Memo 1",
		Type:  domain.NoteTypeAudio,
	})
	require.NoError(t, err)
	require.NotNil(t, audioNote)
	assert.Equal(t, domain.NoteTypeAudio, audioNote.Type)

	// 2. Insert audio attachment directly into repo
	audioAtt := &domain.NoteAudioAttachment{
		ID:          uuid.New(),
		NoteID:      audioNote.ID,
		UserID:      userID,
		StorageKey:  "notes/audio/42/voice_rec_1.webm",
		FileName:    "recording.webm",
		MimeType:    "audio/webm",
		SizeBytes:   128000,
		Duration:    &dur,
		Waveform:    wf,
		IsRecording: true,
		Status:      domain.NoteAudioStatusPending,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	err = repo.CreateAudioAttachment(ctx, audioAtt)
	require.NoError(t, err)

	// 3. Mark audio attachment uploaded
	err = repo.MarkAudioAttachmentUploaded(ctx, audioAtt.ID, audioAtt.Duration, audioAtt.Waveform, &audioAtt.FileName)
	require.NoError(t, err)

	// 4. Retrieve audio attachment
	fetchedAtt, err := repo.GetAudioAttachment(ctx, userID, audioNote.ID, audioAtt.ID)
	require.NoError(t, err)
	assert.Equal(t, audioAtt.ID, fetchedAtt.ID)
	assert.Equal(t, domain.NoteAudioStatusUploaded, fetchedAtt.Status)
	assert.True(t, fetchedAtt.IsRecording)
	assert.Equal(t, "audio/webm", fetchedAtt.MimeType)

	// 5. List audio attachments
	list, err := repo.ListAudioAttachments(ctx, userID, audioNote.ID)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, audioAtt.ID, list[0].ID)

	// 6. Soft delete note and hard delete note -> check cleanup queue
	trashed, err := svc.SoftDeleteNote(ctx, userID, audioNote.ID, audioNote.Version)
	require.NoError(t, err)

	err = svc.HardDeleteNote(ctx, userID, audioNote.ID, trashed.Version)
	require.NoError(t, err)

	// Verify audio storage key was enqueued to note_object_cleanup
	var queuedKeys []string
	err = repo.ProcessNoteObjectCleanup(ctx, func(_ context.Context, key string) error {
		queuedKeys = append(queuedKeys, key)
		return nil
	})
	require.NoError(t, err)
	assert.Contains(t, queuedKeys, "notes/audio/42/voice_rec_1.webm")
}
