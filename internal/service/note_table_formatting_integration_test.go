package service

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

func TestNoteTableFormattingPersistenceIntegration(t *testing.T) {
	_, db := roomIntegrationService(t)
	ctx := context.Background()
	_, err := db.Exec(ctx, `CREATE TABLE files(id UUID PRIMARY KEY,real_name TEXT,mime_type TEXT,size BIGINT,object_name TEXT,deleted_at TIMESTAMPTZ);
CREATE TABLE note_shares(note_id UUID,user_id BIGINT,is_hidden BOOLEAN DEFAULT false);
CREATE TABLE messages(is_deleted BOOLEAN,metadata JSONB,conversation_id UUID);
CREATE TABLE conversation_participants(conversation_id UUID,user_id BIGINT,left_at TIMESTAMPTZ);
CREATE TABLE group_members(group_id UUID,user_id BIGINT,left_at TIMESTAMPTZ);`)
	require.NoError(t, err)
	for _, name := range []string{
		"20260323000100_create_notes_module.sql", "20260325000100_notes_rich_content.sql", "20260325000200_fix_notes_type_check.sql",
		"20260922000100_note_meeting_result.sql", "20260929000600_notes_formatting_color.sql", "20260929000700_notes_navigation_tags_favorites.sql",
		"20260929000800_notes_reliability_trash_autosave.sql", "20260929000900_note_object_cleanup.sql", "20260930000100_notes_audio_recording.sql",
	} {
		data, err := os.ReadFile("../../db/migrations/" + name)
		require.NoError(t, err)
		_, err = db.Exec(ctx, strings.Split(string(data), "-- +goose Down")[0])
		require.NoError(t, err, name)
	}
	s := NewNoteService(repository.NewNoteRepository(db, zap.NewNop()), nil, zap.NewNop())
	var request domain.CreateNoteRequest
	require.NoError(t, json.Unmarshal([]byte(`{"title":"Table formatting","type":"rich","content_doc":{"type":"doc","version":1,"blocks":[{"id":"table","type":"table","rows":[[{"text":"Форматирование","html":"<b>Форматирование</b>"},{"text":"Курсив","html":"<i>Курсив</i>"}],[{"text":"Legacy text"},{"text":"Цвет","html":"<span style=\"color: #2458d3; font-weight: bold\">Цвет</span>"}]]}]}}`), &request))
	created, err := s.CreateNote(ctx, 1, &request)
	require.NoError(t, err)
	loaded, err := s.GetNote(ctx, 1, created.ID)
	require.NoError(t, err)
	require.Equal(t, request.ContentDoc, loaded.ContentDoc)
	require.Contains(t, loaded.ContentText, "Форматирование")
	require.NotContains(t, loaded.ContentText, "<b>")
	var storedHTML string
	require.NoError(t, db.QueryRow(ctx, `SELECT content_doc->'blocks'->0->'rows'->0->0->>'html' FROM notes WHERE id=$1`, created.ID).Scan(&storedHTML))
	require.Equal(t, "<b>Форматирование</b>", storedHTML, "formatting must be present in PostgreSQL, not only the response")
	saved, err := s.AutosaveNote(ctx, 1, &domain.AutosaveNoteRequest{ID: &loaded.ID, Version: loaded.Version, Title: loaded.Title, Type: "rich", ContentDoc: loaded.ContentDoc})
	require.NoError(t, err)
	reopened, err := s.GetNote(ctx, 1, saved.ID)
	require.NoError(t, err)
	require.Equal(t, request.ContentDoc, reopened.ContentDoc)
	cleared, err := s.ClearTableCell(ctx, 1, reopened.ID, "table", 0, 0, reopened.Version)
	require.NoError(t, err)
	reopened, err = s.GetNote(ctx, 1, cleared.ID)
	require.NoError(t, err)
	require.Equal(t, domain.TableCell{}, reopened.ContentDoc.Blocks[0].Rows[0][0], "clearing text must also clear formatting HTML")
	require.Equal(t, "<i>Курсив</i>", reopened.ContentDoc.Blocks[0].Rows[0][1].HTML)
	trimmed, err := s.DeleteTableColumn(ctx, 1, reopened.ID, "table", 0, reopened.Version)
	require.NoError(t, err)
	require.Equal(t, "<i>Курсив</i>", trimmed.ContentDoc.Blocks[0].Rows[0][0].HTML)
	trashed, err := s.SoftDeleteNote(ctx, 1, trimmed.ID, trimmed.Version)
	require.NoError(t, err)
	restored, err := s.RestoreNote(ctx, 1, trashed.ID, trashed.Version)
	require.NoError(t, err)
	require.Equal(t, trimmed.ContentDoc, restored.ContentDoc)
}
