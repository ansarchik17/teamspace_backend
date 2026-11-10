package service

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestNoteListScopeValidation(t *testing.T) {
	deleted := true
	for _, filter := range []domain.NoteListFilter{
		{Scope: "invalid"},
		{Scope: "shared", FolderID: &uuid.Nil},
		{Scope: "shared", IsDeleted: &deleted},
	} {
		_, err := (&NoteService{}).ListNotes(context.Background(), filter)
		require.Error(t, err)
		appErr := apperrors.GetAppError(err)
		require.NotNil(t, appErr)
		require.Equal(t, apperrors.ErrCodeValidation, appErr.Code)
	}
}

func TestNoteListCacheSeparatesAccessScopes(t *testing.T) {
	keys := make(map[string]bool)
	for _, scope := range []string{"", "owned", "shared"} {
		key := noteListCacheKey(domain.NoteListFilter{UserID: 1, Scope: scope}, 1)
		require.False(t, keys[key], "scope %q reused another scope's cached list", scope)
		keys[key] = true
	}
}
