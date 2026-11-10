package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestNTS002NormalizeTags(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		expected []string
	}{
		{
			name:     "nil input",
			input:    nil,
			expected: []string{},
		},
		{
			name:     "empty input",
			input:    []string{},
			expected: []string{},
		},
		{
			name:     "clean tags with hash prefix and casing",
			input:    []string{"#Golang", "  BACKEND  ", "#docker", "golang", "GoLang"},
			expected: []string{"golang", "backend", "docker"},
		},
		{
			name:     "whitespace and empty strings",
			input:    []string{"", "   ", "#", "##", "tag"},
			expected: []string{"tag"},
		},
		{
			name: "truncate over 50 chars",
			input: []string{
				strings.Repeat("a", 60),
			},
			expected: []string{
				strings.Repeat("a", 50),
			},
		},
		{
			name: "limit to max 50 tags",
			input: func() []string {
				arr := make([]string, 70)
				for i := 0; i < 70; i++ {
					arr[i] = "tag_" + string(rune('0'+i/10)) + string(rune('0'+i%10))
				}
				return arr
			}(),
			expected: func() []string {
				arr := make([]string, 50)
				for i := 0; i < 50; i++ {
					arr[i] = "tag_" + string(rune('0'+i/10)) + string(rune('0'+i%10))
				}
				return arr
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := domain.NormalizeTags(tt.input)
			assert.Equal(t, tt.expected, res)
		})
	}
}

func TestNTS002CreateAndUpdateNoteRequest_TagsAndFavorites_Binding(t *testing.T) {
	r := newTestRouter()
	r.POST("/notes", func(c *gin.Context) {
		var req domain.CreateNoteRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusCreated, req)
	})

	r.PATCH("/notes/:id", func(c *gin.Context) {
		var req domain.UpdateNoteRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, req)
	})

	t.Run("CreateNoteRequest with tags and is_favorite", func(t *testing.T) {
		body := map[string]any{
			"title":       "Testing tags",
			"tags":        []string{"golang", "nts-002"},
			"is_favorite": true,
		}
		req := httptest.NewRequest(http.MethodPost, "/notes", jsonBody(t, body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusCreated, w.Code)
		var resp domain.CreateNoteRequest
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, []string{"golang", "nts-002"}, resp.Tags)
		assert.True(t, resp.IsFavorite)
	})

	t.Run("UpdateNoteRequest with tags and is_favorite", func(t *testing.T) {
		body := map[string]any{
			"version":     3,
			"tags":        []string{"updated"},
			"is_favorite": false,
		}
		noteID := uuid.New().String()
		req := httptest.NewRequest(http.MethodPatch, "/notes/"+noteID, jsonBody(t, body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var resp domain.UpdateNoteRequest
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, 3, resp.Version)
		require.NotNil(t, resp.Tags)
		assert.Equal(t, []string{"updated"}, *resp.Tags)
		require.NotNil(t, resp.IsFavorite)
		assert.False(t, *resp.IsFavorite)
	})
}

func TestNTS002FavoriteNoteRequest_Binding(t *testing.T) {
	r := newTestRouter()
	r.PATCH("/notes/:id/favorite", func(c *gin.Context) {
		var req domain.FavoriteNoteRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, req)
	})

	t.Run("valid favorite request", func(t *testing.T) {
		body := map[string]any{
			"version":     2,
			"is_favorite": true,
		}
		noteID := uuid.New().String()
		req := httptest.NewRequest(http.MethodPatch, "/notes/"+noteID+"/favorite", jsonBody(t, body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var resp domain.FavoriteNoteRequest
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, 2, resp.Version)
		assert.True(t, resp.IsFavorite)
	})

	t.Run("missing version returns 400", func(t *testing.T) {
		body := map[string]any{
			"is_favorite": true,
		}
		noteID := uuid.New().String()
		req := httptest.NewRequest(http.MethodPatch, "/notes/"+noteID+"/favorite", jsonBody(t, body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}

func TestNTS002NoteTagSummary_JSON(t *testing.T) {
	tagSummary := domain.NoteTagSummary{
		Tag:   "architecture",
		Count: 5,
	}
	bytes, err := json.Marshal(tagSummary)
	require.NoError(t, err)

	var decoded domain.NoteTagSummary
	require.NoError(t, json.Unmarshal(bytes, &decoded))
	assert.Equal(t, "architecture", decoded.Tag)
	assert.Equal(t, 5, decoded.Count)
}

func TestNTS002NoteFolder_NoteCount_JSON(t *testing.T) {
	folder := domain.NoteFolder{
		ID:        uuid.New(),
		UserID:    100,
		Name:      "Work",
		Color:     "#1687FF",
		NoteCount: 12,
	}
	bytes, err := json.Marshal(folder)
	require.NoError(t, err)

	var decoded domain.NoteFolder
	require.NoError(t, json.Unmarshal(bytes, &decoded))
	assert.Equal(t, "Work", decoded.Name)
	assert.Equal(t, 12, decoded.NoteCount)
}
