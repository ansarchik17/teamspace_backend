package service

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestNTS002UnicodeTagsAndFilterNormalization(t *testing.T) {
	for _, tag := range []string{strings.Repeat("a", 49) + "я", strings.Repeat("я", 60), strings.Repeat("😊", 60)} {
		tags := domain.NormalizeTags([]string{tag})
		require.True(t, utf8.ValidString(tags[0]))
		require.LessOrEqual(t, utf8.RuneCountInString(tags[0]), 50)
	}
	require.Equal(t, "golang", domain.NormalizeNoteTag(" #GoLang "))
	require.Equal(t, []string{"golang"}, domain.NormalizeTags([]string{" #GoLang ", "golang"}))
}

func TestNTS002CacheKeysAreUnambiguous(t *testing.T) {
	a := noteListCacheKey(domain.NoteListFilter{UserID: 1, Tag: "a|q=b", Query: "c"}, 1)
	b := noteListCacheKey(domain.NoteListFilter{UserID: 1, Tag: "a", Query: "b|q=c"}, 1)
	require.NotEqual(t, a, b)
	require.Equal(t, noteListCacheKey(domain.NoteListFilter{UserID: 1, Tag: "#GoLang"}, 1), noteListCacheKey(domain.NoteListFilter{UserID: 1, Tag: "golang"}, 1))
}

func TestNTS002SyncFavoritePresence(t *testing.T) {
	var req domain.SyncNoteUpsertRequest
	require.NoError(t, json.Unmarshal([]byte(`{"title":"legacy"}`), &req))
	require.Nil(t, req.IsFavorite)
	require.NoError(t, json.Unmarshal([]byte(`{"is_favorite":false}`), &req))
	require.NotNil(t, req.IsFavorite)
	require.False(t, *req.IsFavorite)
}
