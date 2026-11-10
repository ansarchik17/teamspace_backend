package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

func TestNTS003AutosaveNilValidation(t *testing.T) {
	svc := NewNoteService(nil, nil, zap.NewNop())
	ctx := context.Background()

	_, err := svc.AutosaveNote(ctx, 1, nil)
	require.Error(t, err)
	appErr, ok := err.(*apperrors.AppError)
	require.True(t, ok)
	assert.Equal(t, apperrors.ErrCodeValidation, appErr.Code)
}

func TestNTS003AutosaveRejectsUnsafePayloadBeforeRepository(t *testing.T) {
	svc := NewNoteService(nil, nil, zap.NewNop())
	id := uuid.New()
	badColor := "not-a-color"
	for _, req := range []domain.AutosaveNoteRequest{
		{ID: &id}, {ID: &id, Version: -1},
		{Color: &badColor}, {Title: strings.Repeat("x", 4097)},
		{Content: strings.Repeat("x", (4<<20)+1)},
	} {
		_, err := svc.AutosaveNote(context.Background(), 1, &req)
		require.Error(t, err)
	}
}

func TestNTS003TrashExpirationHelper(t *testing.T) {
	now := time.Now()
	deletedAt := now.Add(-31 * 24 * time.Hour) // 31 days ago
	assert.True(t, domain.IsTrashExpired(&deletedAt, now))

	recentDeletedAt := now.Add(-5 * 24 * time.Hour) // 5 days ago
	assert.False(t, domain.IsTrashExpired(&recentDeletedAt, now))
	expiresAt := domain.CalculateTrashExpiresAt(&recentDeletedAt)
	require.NotNil(t, expiresAt)
	assert.Equal(t, recentDeletedAt.Add(30*24*time.Hour), *expiresAt)
}

func TestNTS003PrepareNoteForViewer_SetsExpiresAt(t *testing.T) {
	svc := NewNoteService(nil, nil, zap.NewNop())
	deletedAt := time.Now()

	note := &domain.Note{
		ID:        uuid.New(),
		UserID:    10,
		IsDeleted: true,
		DeletedAt: &deletedAt,
	}

	// Owner viewer
	svc.prepareNoteForViewer(note, 10)
	require.NotNil(t, note.ExpiresAt)
	assert.Equal(t, deletedAt.Add(domain.NoteTrashRetention).Unix(), note.ExpiresAt.Unix())

	// Non-owner viewer
	note.FolderID = func() *uuid.UUID { id := uuid.New(); return &id }()
	note.IsPinned = true
	svc.prepareNoteForViewer(note, 99)
	assert.Nil(t, note.FolderID)
	assert.False(t, note.IsPinned)
	require.NotNil(t, note.ExpiresAt)
}
