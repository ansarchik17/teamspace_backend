package service

import (
	"context"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

func TestNTS002IntegrationNavigationTagsFavoritesAndSearch(t *testing.T) {
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
	}
	for _, name := range migrations {
		data, err := os.ReadFile("../../db/migrations/" + name)
		require.NoError(t, err)
		_, err = pool.Exec(ctx, strings.Split(string(data), "-- +goose Down")[0])
		require.NoError(t, err)
	}
	const migrationName = "20260929000700_notes_navigation_tags_favorites.sql"
	data, err := os.ReadFile("../../db/migrations/" + migrationName)
	require.NoError(t, err)
	db := stdlib.OpenDB(*pool.Config().ConnConfig)
	t.Cleanup(func() { _ = db.Close() })
	provider, err := goose.NewProvider(goose.DialectPostgres, db, fstest.MapFS{migrationName: &fstest.MapFile{Data: data}})
	require.NoError(t, err)
	_, err = provider.Up(ctx)
	require.NoError(t, err)
	audioMigration, err := os.ReadFile("../../db/migrations/20260930000100_notes_audio_recording.sql")
	require.NoError(t, err)
	_, err = pool.Exec(ctx, strings.Split(string(audioMigration), "-- +goose Down")[0])
	require.NoError(t, err)

	repo := repository.NewNoteRepository(pool, zap.NewNop())
	svc := NewNoteService(repo, nil, zap.NewNop())
	const userID = int64(1)

	// 1. Create Folder
	folder, err := svc.CreateFolder(ctx, userID, &domain.CreateNoteFolderRequest{
		Name:      "Projects",
		SortOrder: 1,
		Color:     "#1687FF",
	})
	require.NoError(t, err)
	require.NotNil(t, folder)

	folders, err := svc.ListFolders(ctx, userID)
	require.NoError(t, err)
	require.Len(t, folders, 1)
	require.Equal(t, 0, folders[0].NoteCount)

	// 2. Create Note 1 in folder with tags and favorite
	doc1 := &domain.ContentDoc{
		Type:    "doc",
		Version: 1,
		Blocks: []domain.ContentBlock{
			{
				ID:   "b1",
				Type: "paragraph",
				Runs: []domain.ContentRun{{Text: "Building the fast navigation feature."}},
			},
		},
	}
	note1, err := svc.CreateNote(ctx, userID, &domain.CreateNoteRequest{
		Title:      "NTS-002 Implementation",
		Type:       "rich",
		FolderID:   &folder.ID,
		Tags:       []string{"#Golang", "Backend", "NTS-002"},
		IsFavorite: true,
		ContentDoc: doc1,
	})
	require.NoError(t, err)
	require.Equal(t, []string{"golang", "backend", "nts-002"}, note1.Tags)
	require.True(t, note1.IsFavorite)

	// Check folder note count incremented
	folders, err = svc.ListFolders(ctx, userID)
	require.NoError(t, err)
	require.Equal(t, 1, folders[0].NoteCount)

	// 3. Create Note 2 without folder
	note2, err := svc.CreateNote(ctx, userID, &domain.CreateNoteRequest{
		Title:      "Personal Learning",
		Type:       "text",
		Content:    "Learning advanced Go concurrency patterns and channels",
		Tags:       []string{"Golang", "personal"},
		IsFavorite: false,
	})
	require.NoError(t, err)
	require.Equal(t, []string{"golang", "personal"}, note2.Tags)
	require.False(t, note2.IsFavorite)

	// 4. Create Note 3 with architecture tags and favorite
	note3, err := svc.CreateNote(ctx, userID, &domain.CreateNoteRequest{
		Title:      "System Architecture Design",
		Type:       "text",
		Content:    "Details about microservices and database sharding",
		Tags:       []string{"architecture", "backend"},
		IsFavorite: true,
	})
	require.NoError(t, err)

	// 5. Check GetUserTags autocomplete
	tagSummaries, err := svc.GetUserTags(ctx, userID)
	require.NoError(t, err)
	require.NotEmpty(t, tagSummaries)

	tagMap := make(map[string]int)
	for _, ts := range tagSummaries {
		tagMap[ts.Tag] = ts.Count
	}
	require.Equal(t, 2, tagMap["golang"])
	require.Equal(t, 2, tagMap["backend"])
	require.Equal(t, 1, tagMap["nts-002"])
	require.Equal(t, 1, tagMap["personal"])
	require.Equal(t, 1, tagMap["architecture"])

	// 6. Test Filtering
	// Filter by tag "golang"
	respTag, err := svc.ListNotes(ctx, domain.NoteListFilter{
		UserID: userID,
		Tag:    "#GoLang",
	})
	require.NoError(t, err)
	require.Len(t, respTag.Items, 2)

	// Filter by favorite
	isFav := true
	respFav, err := svc.ListNotes(ctx, domain.NoteListFilter{
		UserID:     userID,
		IsFavorite: &isFav,
	})
	require.NoError(t, err)
	require.Len(t, respFav.Items, 2)
	for _, n := range respFav.Items {
		require.True(t, n.IsFavorite)
	}

	// Filter by folder
	respFolder, err := svc.ListNotes(ctx, domain.NoteListFilter{
		UserID:   userID,
		FolderID: &folder.ID,
	})
	require.NoError(t, err)
	require.Len(t, respFolder.Items, 1)
	require.Equal(t, note1.ID, respFolder.Items[0].ID)

	// Filter by no-folder (root)
	nilUUID := uuid.Nil
	respRoot, err := svc.ListNotes(ctx, domain.NoteListFilter{
		UserID:   userID,
		FolderID: &nilUUID,
	})
	require.NoError(t, err)
	require.Len(t, respRoot.Items, 2)

	// 7. Full-Text Search
	// Search in content: "microservices"
	respSearchContent, err := svc.SearchNotes(ctx, domain.NoteListFilter{
		UserID: userID,
		Query:  "microservices",
	})
	require.NoError(t, err)
	require.Len(t, respSearchContent.Items, 1)
	require.Equal(t, note3.ID, respSearchContent.Items[0].ID)

	// Search in tags: "nts-002"
	respSearchTag, err := svc.SearchNotes(ctx, domain.NoteListFilter{
		UserID: userID,
		Query:  "nts-002",
	})
	require.NoError(t, err)
	require.Len(t, respSearchTag.Items, 1)
	require.Equal(t, note1.ID, respSearchTag.Items[0].ID)

	// Search in title: "Personal"
	respSearchTitle, err := svc.SearchNotes(ctx, domain.NoteListFilter{
		UserID: userID,
		Query:  "Personal",
	})
	require.NoError(t, err)
	require.Len(t, respSearchTitle.Items, 1)
	require.Equal(t, note2.ID, respSearchTitle.Items[0].ID)

	// 8. Test SetFavorite and Concurrency
	updatedNote2, err := svc.SetFavorite(ctx, userID, note2.ID, note2.Version, true)
	require.NoError(t, err)
	require.True(t, updatedNote2.IsFavorite)
	require.Equal(t, note2.Version+1, updatedNote2.Version)

	// Stale version update should fail
	_, err = svc.SetFavorite(ctx, userID, note2.ID, note2.Version, false)
	require.Error(t, err)

	// 9. Update tags via UpdateNote
	newTags := []string{"concurrency", "expert"}
	updatedNote2, err = svc.UpdateNote(ctx, userID, note2.ID, &domain.UpdateNoteRequest{
		Version: updatedNote2.Version,
		Tags:    &newTags,
	})
	require.NoError(t, err)
	require.Equal(t, []string{"concurrency", "expert"}, updatedNote2.Tags)
	legacySync := domain.SyncNoteUpsertRequest{ID: note2.ID, Version: updatedNote2.Version, Title: updatedNote2.Title, Type: updatedNote2.Type, Content: updatedNote2.Content}
	synced, err := svc.applySyncUpsert(ctx, userID, legacySync)
	require.NoError(t, err)
	require.True(t, synced.IsFavorite)
	require.Equal(t, updatedNote2.Tags, synced.Tags)
	legacySync.Version = synced.Version
	falseFavorite := false
	legacySync.IsFavorite = &falseFavorite
	synced, err = svc.applySyncUpsert(ctx, userID, legacySync)
	require.NoError(t, err)
	require.False(t, synced.IsFavorite)
	_, err = svc.SetFavorite(ctx, 2, note2.ID, synced.Version, true)
	require.Error(t, err)

	// 10. Soft-delete note1 and check tag summary & folder note count
	_, err = svc.SoftDeleteNote(ctx, userID, note1.ID, note1.Version)
	require.NoError(t, err)

	// Note count in folder drops back to 0
	folders, err = svc.ListFolders(ctx, userID)
	require.NoError(t, err)
	require.Equal(t, 0, folders[0].NoteCount)

	// Tags from deleted note should no longer appear in GetUserTags
	tagSummaries, err = svc.GetUserTags(ctx, userID)
	require.NoError(t, err)
	for _, ts := range tagSummaries {
		require.NotEqual(t, "nts-002", ts.Tag, "deleted note tag should not be present")
	}
	_, err = provider.Down(ctx)
	require.NoError(t, err)
	_, err = provider.Up(ctx)
	require.NoError(t, err)
}
