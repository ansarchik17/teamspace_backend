package domain_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestNTS003TrashRetentionAndExpiration(t *testing.T) {
	assert.Equal(t, 30*24*time.Hour, domain.NoteTrashRetention)

	t.Run("nil deletedAt has nil expiresAt and is not expired", func(t *testing.T) {
		assert.Nil(t, domain.CalculateTrashExpiresAt(nil))
		assert.False(t, domain.IsTrashExpired(nil, time.Now()))
	})

	t.Run("valid deletedAt calculates exact retention time", func(t *testing.T) {
		deletedAt := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
		expiresAt := domain.CalculateTrashExpiresAt(&deletedAt)
		require.NotNil(t, expiresAt)
		expected := deletedAt.Add(30 * 24 * time.Hour)
		assert.Equal(t, expected, *expiresAt)

		// Before retention limit
		assert.False(t, domain.IsTrashExpired(&deletedAt, deletedAt.Add(29*24*time.Hour)))
		// Exactly at limit or after
		assert.True(t, domain.IsTrashExpired(&deletedAt, deletedAt.Add(30*24*time.Hour+time.Second)))
	})
}

func TestNTS003AutosaveNoteRequest_JSONSerialization(t *testing.T) {
	id := uuid.New()
	folderID := uuid.New()
	color := "#1687FF"
	fav := true
	tags := []string{"work", "draft"}

	req := domain.AutosaveNoteRequest{
		ID:         &id,
		FolderID:   &folderID,
		Title:      "Autosaved Title",
		Content:    "Autosaved Content",
		Type:       "text",
		Color:      &color,
		Tags:       &tags,
		IsFavorite: &fav,
		Version:    3,
	}

	data, err := json.Marshal(req)
	require.NoError(t, err)

	var decoded domain.AutosaveNoteRequest
	require.NoError(t, json.Unmarshal(data, &decoded))

	assert.Equal(t, id, *decoded.ID)
	assert.Equal(t, folderID, *decoded.FolderID)
	assert.Equal(t, "Autosaved Title", decoded.Title)
	assert.Equal(t, 3, decoded.Version)
	assert.Equal(t, color, *decoded.Color)
	assert.Equal(t, fav, *decoded.IsFavorite)
}

func TestNTS003Note_ExpiresAt_JSONSerialization(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	deletedAt := now
	expiresAt := domain.CalculateTrashExpiresAt(&deletedAt)

	note := domain.Note{
		ID:        uuid.New(),
		UserID:    123,
		Title:     "Trashed Note",
		IsDeleted: true,
		DeletedAt: &deletedAt,
		ExpiresAt: expiresAt,
		CreatedAt: now,
		UpdatedAt: now,
		Version:   2,
	}

	data, err := json.Marshal(note)
	require.NoError(t, err)

	var decoded domain.Note
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.NotNil(t, decoded.ExpiresAt)
	assert.Equal(t, expiresAt.Unix(), decoded.ExpiresAt.Unix())
}
