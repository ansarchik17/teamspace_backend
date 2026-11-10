package handler_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func jsonBody(t *testing.T, v any) *bytes.Buffer {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	return bytes.NewBuffer(data)
}

func newTestRouter() *gin.Engine {
	r := gin.New()
	r.Use(func(c *gin.Context) {
		// inject fake user id = 1
		c.Set("user_id", int64(1))
		c.Next()
	})
	return r
}

// ---------------------------------------------------------------------------
// 1. JSON binding – CreateNoteRequest
// ---------------------------------------------------------------------------

func TestCreateNoteRequest_JSON_Binding(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		wantErr bool
	}{
		{
			name: "minimal valid",
			payload: `{
				"title": "My note",
				"type": "text",
				"content": "Hello"
			}`,
			wantErr: false,
		},
		{
			name: "rich type with content_doc",
			payload: `{
				"title": "Rich note",
				"type": "rich",
				"content_doc": {
					"type": "doc",
					"version": 1,
					"blocks": [
						{
							"id": "b1",
							"type": "paragraph",
							"runs": [
								{"text": "Hello ", "marks": ["bold"]},
								{"text": "world"}
							]
						}
					]
				}
			}`,
			wantErr: false,
		},
		{
			name: "checklist type",
			payload: `{
				"type": "checklist",
				"checklist_items": [
					{"id": "` + uuid.New().String() + `", "text": "Buy milk", "is_done": false, "position": 0}
				]
			}`,
			wantErr: false,
		},
		{
			name:    "empty body",
			payload: `{}`,
			wantErr: false, // all fields optional
		},
		{
			name:    "invalid json",
			payload: `{not valid`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req domain.CreateNoteRequest
			err := json.Unmarshal([]byte(tt.payload), &req)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 2. UpdateNoteRequest – version required
// ---------------------------------------------------------------------------

func TestUpdateNoteRequest_VersionRequired(t *testing.T) {
	w := httptest.NewRecorder()
	r := newTestRouter()

	// Handler that just reads the request body and validates
	r.PATCH("/notes/:id", func(c *gin.Context) {
		var req domain.UpdateNoteRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if req.Version == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "version required"})
			return
		}
		c.JSON(http.StatusOK, req)
	})

	// Missing version → should fail
	t.Run("missing version", func(t *testing.T) {
		title := "Updated title"
		body := jsonBody(t, map[string]any{"title": &title})
		req := httptest.NewRequest(http.MethodPatch, "/notes/"+uuid.New().String(), body)
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	// With version → passes binding
	t.Run("with version", func(t *testing.T) {
		title := "Updated title"
		body := jsonBody(t, map[string]any{"title": &title, "version": 3})
		req := httptest.NewRequest(http.MethodPatch, "/notes/"+uuid.New().String(), body)
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	})
}

// ---------------------------------------------------------------------------
// 3. Error response format
// ---------------------------------------------------------------------------

func TestErrorResponse_Format(t *testing.T) {
	errResp := domain.ErrorResponse{
		Error: domain.ErrorPayload{
			Code:    "NOTE_VERSION_CONFLICT",
			Message: "Client version is outdated",
			Details: map[string]any{"server_version": 5},
		},
	}

	data, err := json.Marshal(errResp)
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(data, &parsed))

	// must have top-level "error" key
	errObj, ok := parsed["error"].(map[string]any)
	require.True(t, ok, "error should be object")
	assert.Equal(t, "NOTE_VERSION_CONFLICT", errObj["code"])
	assert.Equal(t, "Client version is outdated", errObj["message"])

	details, ok := errObj["details"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, float64(5), details["server_version"])
}

// ---------------------------------------------------------------------------
// 4. Presign endpoint – request validation
// ---------------------------------------------------------------------------

func TestPresignRequest_JSON(t *testing.T) {
	tests := []struct {
		name     string
		payload  string
		wantMIME string
		wantSize int64
		wantErr  bool
	}{
		{
			name:     "jpeg",
			payload:  `{"mime_type":"image/jpeg","size_bytes":512000}`,
			wantMIME: "image/jpeg",
			wantSize: 512000,
		},
		{
			name:     "png",
			payload:  `{"mime_type":"image/png","size_bytes":1048576}`,
			wantMIME: "image/png",
			wantSize: 1048576,
		},
		{
			name:    "empty body",
			payload: `{}`,
			wantErr: true, // mime_type and size_bytes are required
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req domain.PresignRequest
			err := json.Unmarshal([]byte(tt.payload), &req)
			require.NoError(t, err) // json.Unmarshal never fails for valid JSON

			if tt.wantErr {
				// binding:required enforced by Gin, check empty
				assert.Empty(t, req.MimeType)
			} else {
				assert.Equal(t, tt.wantMIME, req.MimeType)
				assert.Equal(t, tt.wantSize, req.SizeBytes)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 5. Presign response serialization
// ---------------------------------------------------------------------------

func TestPresignResponse_Serialization(t *testing.T) {
	attID := uuid.New()
	_ = time.Now() // ensure time import is used

	resp := domain.PresignResponse{
		AttachmentID: attID.String(),
		UploadURL:    "https://minio.local/bucket/note-photos/x/y?sig=abc",
		StorageKey:   "note-photos/note-id/att-id",
	}

	data, err := json.Marshal(resp)
	require.NoError(t, err)

	var out map[string]any
	require.NoError(t, json.Unmarshal(data, &out))

	assert.Equal(t, attID.String(), out["attachment_id"])
	assert.Contains(t, out["upload_url"].(string), "https://")
	assert.Contains(t, out["storage_key"].(string), "note-photos/")
}

// ---------------------------------------------------------------------------
// 6. Route parameter validation – UUID parsing
// ---------------------------------------------------------------------------

func TestRouteParam_InvalidUUID(t *testing.T) {
	r := newTestRouter()
	r.GET("/notes/:id", func(c *gin.Context) {
		if _, err := uuid.Parse(c.Param("id")); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid note id"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	tests := []struct {
		name     string
		id       string
		wantCode int
	}{
		{"valid uuid", uuid.New().String(), http.StatusOK},
		{"invalid uuid", "not-a-uuid", http.StatusBadRequest},
		{"empty-like", "00000000-0000-0000-0000-000000000000", http.StatusOK}, // nil uuid is still valid format
		{"short id", "12345", http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/notes/"+tt.id, nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			assert.Equal(t, tt.wantCode, w.Code)
		})
	}
}

// ---------------------------------------------------------------------------
// 7. SyncNotes – since parameter validation
// ---------------------------------------------------------------------------

func TestSyncNotes_SinceParam(t *testing.T) {
	r := newTestRouter()
	r.GET("/notes/sync", func(c *gin.Context) {
		since := c.Query("since")
		if _, err := time.Parse(time.RFC3339, since); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid since"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"since": since})
	})

	tests := []struct {
		name     string
		since    string
		wantCode int
	}{
		{"valid RFC3339", "2026-01-01T00:00:00Z", http.StatusOK},
		{"valid with offset", "2026-03-25T12:00:00%2B05:00", http.StatusOK},
		{"invalid format", "2026-01-01", http.StatusBadRequest},
		{"empty", "", http.StatusBadRequest},
		{"not a date", "yesterday", http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := "/notes/sync"
			if tt.since != "" {
				url += "?since=" + tt.since
			}
			req := httptest.NewRequest(http.MethodGet, url, nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			assert.Equal(t, tt.wantCode, w.Code, "since=%q", tt.since)
		})
	}
}

// ---------------------------------------------------------------------------
// 8. content_doc full round-trip through HTTP JSON
// ---------------------------------------------------------------------------

func TestContentDoc_FullHTTPRoundTrip(t *testing.T) {
	r := newTestRouter()
	r.POST("/notes", func(c *gin.Context) {
		var req domain.CreateNoteRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if req.ContentDoc != nil {
			if err := req.ContentDoc.Validate(); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
		}
		// Echo the note back
		c.JSON(http.StatusCreated, domain.Note{
			ID:         uuid.New(),
			Title:      req.Title,
			ContentDoc: req.ContentDoc,
			Type:       req.Type,
			Version:    1,
		})
	})

	payload := map[string]any{
		"title": "Rich Note",
		"type":  "rich",
		"content_doc": map[string]any{
			"type":    "doc",
			"version": 1,
			"blocks": []map[string]any{
				{
					"id":   "b1",
					"type": "paragraph",
					"runs": []map[string]any{
						{"text": "Hello ", "marks": []string{"bold"}},
						{"text": "World"},
					},
				},
				{
					"id":   "b2",
					"type": "checklist",
					"items": []map[string]any{
						{"id": uuid.New().String(), "text": "Do laundry", "checked": false},
						{"id": uuid.New().String(), "text": "Buy groceries", "checked": true},
					},
				},
				{
					"id":   "b3",
					"type": "table",
					"rows": [][]map[string]any{
						{{"text": "Name"}, {"text": "Value"}},
						{{"text": "Foo"}, {"text": "Bar"}},
					},
				},
				{
					"id":           "b4",
					"type":         "image",
					"attachmentId": uuid.New().String(),
					"alt":          "Screenshot",
				},
			},
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/notes", jsonBody(t, payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	var respNote domain.Note
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &respNote))

	assert.Equal(t, "Rich Note", respNote.Title)
	require.NotNil(t, respNote.ContentDoc)
	require.Len(t, respNote.ContentDoc.Blocks, 4)
	assert.Equal(t, domain.NoteBlockTypeParagraph, respNote.ContentDoc.Blocks[0].Type)
	assert.Equal(t, domain.NoteMarkBold, respNote.ContentDoc.Blocks[0].Runs[0].Marks[0])
	assert.Equal(t, domain.NoteBlockTypeChecklist, respNote.ContentDoc.Blocks[1].Type)
	assert.Len(t, respNote.ContentDoc.Blocks[1].Items, 2)
	assert.Equal(t, domain.NoteBlockTypeTable, respNote.ContentDoc.Blocks[2].Type)
	assert.Len(t, respNote.ContentDoc.Blocks[2].Rows, 2)
	assert.Equal(t, domain.NoteBlockTypeImage, respNote.ContentDoc.Blocks[3].Type)
}

// ---------------------------------------------------------------------------
// 9. 409 Version Conflict response
// ---------------------------------------------------------------------------

func TestVersionConflict_HTTPResponse(t *testing.T) {
	r := newTestRouter()
	r.PATCH("/notes/:id", func(c *gin.Context) {
		// simulate version conflict
		c.JSON(http.StatusConflict, domain.ErrorResponse{
			Error: domain.ErrorPayload{
				Code:    "NOTE_VERSION_CONFLICT",
				Message: "Note version conflict. Please sync state from server.",
				Details: map[string]any{
					"server_note": map[string]any{
						"id":      "11111111-1111-1111-1111-111111111111",
						"version": 2,
					},
				},
			},
		})
	})

	req := httptest.NewRequest(http.MethodPatch, "/notes/"+uuid.New().String(),
		jsonBody(t, map[string]any{"title": "x", "version": 1}))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)

	var errResp domain.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	assert.Equal(t, "NOTE_VERSION_CONFLICT", errResp.Error.Code)
	assert.Equal(t, "Note version conflict. Please sync state from server.", errResp.Error.Message)
	serverNote, ok := errResp.Error.Details["server_note"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "11111111-1111-1111-1111-111111111111", serverNote["id"])
	assert.Equal(t, float64(2), serverNote["version"])
}

// ---------------------------------------------------------------------------
// 10. Invalid content_doc → 400
// ---------------------------------------------------------------------------

func TestCreateNote_InvalidContentDoc_Returns400(t *testing.T) {
	r := newTestRouter()
	r.POST("/notes", func(c *gin.Context) {
		var req domain.CreateNoteRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, domain.ErrorResponse{
				Error: domain.ErrorPayload{Code: "VALIDATION_ERROR", Message: err.Error()},
			})
			return
		}
		if req.ContentDoc != nil {
			if err := req.ContentDoc.Validate(); err != nil {
				c.JSON(http.StatusBadRequest, domain.ErrorResponse{
					Error: domain.ErrorPayload{Code: "INVALID_CONTENT_SCHEMA", Message: "invalid content_doc: " + err.Error()},
				})
				return
			}
		}
		c.JSON(http.StatusCreated, gin.H{"ok": true})
	})

	invalidPayloads := []struct {
		name    string
		payload map[string]any
	}{
		{
			name: "unknown block type",
			payload: map[string]any{
				"type": "rich",
				"content_doc": map[string]any{
					"blocks": []map[string]any{
						{"id": "b1", "type": "video"},
					},
				},
			},
		},
		{
			name: "unknown mark",
			payload: map[string]any{
				"type": "rich",
				"content_doc": map[string]any{
					"blocks": []map[string]any{
						{"id": "b1", "type": "paragraph", "runs": []map[string]any{
							{"text": "x", "marks": []string{"underline"}},
						}},
					},
				},
			},
		},
		{
			name: "image missing attachmentId",
			payload: map[string]any{
				"type": "rich",
				"content_doc": map[string]any{
					"blocks": []map[string]any{
						{"id": "b1", "type": "image"},
					},
				},
			},
		},
	}

	for _, tt := range invalidPayloads {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/notes", jsonBody(t, tt.payload))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			assert.Equal(t, http.StatusBadRequest, w.Code, "payload: %v", tt.payload)

			var errResp domain.ErrorResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
			assert.Equal(t, "INVALID_CONTENT_SCHEMA", errResp.Error.Code)
		})
	}
}

// ---------------------------------------------------------------------------
// 11. ListNotes – query param parsing
// ---------------------------------------------------------------------------

func TestListNotes_QueryParams(t *testing.T) {
	r := newTestRouter()
	r.GET("/notes", func(c *gin.Context) {
		filter := struct {
			Q      string `form:"q"`
			Limit  int    `form:"limit"`
			Cursor string `form:"cursor"`
		}{}
		_ = c.ShouldBindQuery(&filter)
		c.JSON(http.StatusOK, gin.H{
			"q":      filter.Q,
			"limit":  filter.Limit,
			"cursor": filter.Cursor,
		})
	})

	tests := []struct {
		url       string
		wantQ     string
		wantLimit int
	}{
		{"/notes?q=hello&limit=10", "hello", 10},
		{"/notes?limit=100", "", 100},
		{"/notes", "", 0},
	}

	for _, tt := range tests {
		req := httptest.NewRequest(http.MethodGet, tt.url, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)

		var body map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		assert.Equal(t, tt.wantQ, body["q"])
		assert.Equal(t, float64(tt.wantLimit), body["limit"])
	}
}

// ---------------------------------------------------------------------------
// 12. MoveNoteRequest
// ---------------------------------------------------------------------------

func TestMoveNoteRequest_JSON(t *testing.T) {
	t.Run("with folder", func(t *testing.T) {
		folderID := uuid.New()
		req := domain.MoveNoteRequest{
			FolderID: &folderID,
			Version:  2,
		}
		data, err := json.Marshal(req)
		require.NoError(t, err)

		var out domain.MoveNoteRequest
		require.NoError(t, json.Unmarshal(data, &out))
		require.NotNil(t, out.FolderID)
		assert.Equal(t, folderID, *out.FolderID)
		assert.Equal(t, 2, out.Version)
	})

	t.Run("remove from folder (null)", func(t *testing.T) {
		payload := `{"folder_id": null, "version": 3}`
		var req domain.MoveNoteRequest
		require.NoError(t, json.Unmarshal([]byte(payload), &req))
		assert.Nil(t, req.FolderID)
		assert.Equal(t, 3, req.Version)
	})
}

// ---------------------------------------------------------------------------
// 13. NoteListResponse cursor pagination
// ---------------------------------------------------------------------------

func TestNoteListResponse_Cursor(t *testing.T) {
	t.Run("no next page", func(t *testing.T) {
		resp := domain.NoteListResponse{
			Items:      []domain.Note{{ID: uuid.New(), Title: "Note 1"}},
			NextCursor: "",
		}
		data, err := json.Marshal(resp)
		require.NoError(t, err)
		// next_cursor should be omitempty
		assert.NotContains(t, string(data), `"next_cursor"`)
	})

	t.Run("with next page", func(t *testing.T) {
		resp := domain.NoteListResponse{
			Items:      []domain.Note{{ID: uuid.New()}},
			NextCursor: "base64encodedcursor==",
		}
		data, err := json.Marshal(resp)
		require.NoError(t, err)
		assert.Contains(t, string(data), `"next_cursor"`)
		assert.Contains(t, string(data), "base64encodedcursor==")
	})
}

// ---------------------------------------------------------------------------
// 14. VersionedNoteActionRequest binding
// ---------------------------------------------------------------------------

func TestVersionedNoteActionRequest_Binding(t *testing.T) {
	r := newTestRouter()
	r.POST("/notes/:id/pin", func(c *gin.Context) {
		var req domain.VersionedNoteActionRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if req.Version == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "version required"})
			return
		}
		c.JSON(http.StatusOK, req)
	})

	t.Run("missing version", func(t *testing.T) {
		body := jsonBody(t, map[string]any{})
		req := httptest.NewRequest(http.MethodPost, "/notes/"+uuid.New().String()+"/pin", body)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("valid version", func(t *testing.T) {
		body := jsonBody(t, map[string]any{"version": 5})
		req := httptest.NewRequest(http.MethodPost, "/notes/"+uuid.New().String()+"/pin", body)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	})
}

func TestDeleteTableColumnRouteParams(t *testing.T) {
	r := newTestRouter()
	r.DELETE("/notes/:id/tables/:blockId/columns/:colIndex", func(c *gin.Context) {
		if _, err := uuid.Parse(c.Param("id")); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid note id"})
			return
		}
		if _, err := strconv.Atoi(c.Param("colIndex")); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid colIndex"})
			return
		}
		var req domain.VersionedNoteActionRequest
		if err := c.ShouldBindJSON(&req); err != nil || req.Version == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "version required"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	req := httptest.NewRequest(http.MethodDelete, "/notes/"+uuid.New().String()+"/tables/block-1/columns/1", jsonBody(t, map[string]any{"version": 12}))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	req = httptest.NewRequest(http.MethodDelete, "/notes/"+uuid.New().String()+"/tables/block-1/columns/not-int", jsonBody(t, map[string]any{"version": 12}))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestClearTableCellRouteParams(t *testing.T) {
	r := newTestRouter()
	r.DELETE("/notes/:id/tables/:blockId/cells/:rowIndex/:colIndex", func(c *gin.Context) {
		if _, err := uuid.Parse(c.Param("id")); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid note id"})
			return
		}
		if _, err := strconv.Atoi(c.Param("rowIndex")); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid rowIndex"})
			return
		}
		if _, err := strconv.Atoi(c.Param("colIndex")); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid colIndex"})
			return
		}
		var req domain.VersionedNoteActionRequest
		if err := c.ShouldBindJSON(&req); err != nil || req.Version == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "version required"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	req := httptest.NewRequest(http.MethodDelete, "/notes/"+uuid.New().String()+"/tables/block-1/cells/0/1", jsonBody(t, map[string]any{"version": 12}))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	req = httptest.NewRequest(http.MethodDelete, "/notes/"+uuid.New().String()+"/tables/block-1/cells/x/1", jsonBody(t, map[string]any{"version": 12}))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ---------------------------------------------------------------------------
// 15. Note attachment content-type detection
// ---------------------------------------------------------------------------

func TestDetectAttachmentType(t *testing.T) {
	// Test the detection logic directly (mirrors note_handler.go detectAttachmentType)
	detect := func(contentType, filename string) string {
		switch {
		case len(contentType) >= 6 && contentType[:6] == "image/":
			return "image"
		case len(contentType) >= 6 && contentType[:6] == "video/":
			return "video"
		case len(contentType) >= 12 && contentType[:12] == "application/":
			return "document"
		default:
			if strings.HasSuffix(strings.ToLower(filename), ".jpg") {
				return "image"
			}
			return "file"
		}
	}

	assert.Equal(t, "image", detect("image/jpeg", "photo.jpg"))
	assert.Equal(t, "image", detect("image/png", "screenshot.png"))
	assert.Equal(t, "video", detect("video/mp4", "clip.mp4"))
	assert.Equal(t, "document", detect("application/pdf", "report.pdf"))
	assert.Equal(t, "image", detect("", "photo.jpg"))
	assert.Equal(t, "file", detect("", "data.bin"))
}
