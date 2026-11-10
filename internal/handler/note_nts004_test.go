package handler_test

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/handler"
	"go.uber.org/zap"
)

func TestNTS004AudioEndpoints_InvalidUUIDRejection(t *testing.T) {
	h := handler.NewNoteHandler(nil, zap.NewNop())
	r := newTestRouter()

	r.POST("/notes/:id/audio", h.UploadAudio)
	r.POST("/notes/:id/audio/presign", h.PresignAudioUpload)
	r.POST("/notes/:id/audio/:attachmentId/complete", h.CompleteAudioUpload)
	r.GET("/notes/:id/audio", h.ListAudioAttachments)
	r.GET("/notes/:id/audio/:attachmentId", h.GetAudioAttachment)
	r.GET("/notes/:id/audio/:attachmentId/stream", h.StreamAudioAttachment)
	r.DELETE("/notes/:id/audio/:attachmentId", h.DeleteAudioAttachment)

	t.Run("upload audio with invalid note id returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/notes/invalid-uuid/audio", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), `"code":"BAD_REQUEST"`)
	})

	t.Run("presign audio with invalid note id returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/notes/not-a-uuid/audio/presign", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), `"code":"BAD_REQUEST"`)
	})

	t.Run("complete audio with invalid note id returns 400", func(t *testing.T) {
		attID := uuid.New().String()
		req := httptest.NewRequest(http.MethodPost, "/notes/bad-uuid/audio/"+attID+"/complete", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("complete audio with invalid attachment id returns 400", func(t *testing.T) {
		noteID := uuid.New().String()
		req := httptest.NewRequest(http.MethodPost, "/notes/"+noteID+"/audio/bad-att-id/complete", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("list audio with invalid note id returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/notes/bad-note/audio", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("get audio with invalid attachment id returns 400", func(t *testing.T) {
		noteID := uuid.New().String()
		req := httptest.NewRequest(http.MethodGet, "/notes/"+noteID+"/audio/bad-att", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("stream audio with invalid attachment id returns 400", func(t *testing.T) {
		noteID := uuid.New().String()
		req := httptest.NewRequest(http.MethodGet, "/notes/"+noteID+"/audio/bad-att/stream", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("delete audio with invalid attachment id returns 400", func(t *testing.T) {
		noteID := uuid.New().String()
		req := httptest.NewRequest(http.MethodDelete, "/notes/"+noteID+"/audio/bad-att", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}

func TestNTS004AudioUpload_MissingFileRejection(t *testing.T) {
	h := handler.NewNoteHandler(nil, zap.NewNop())
	r := newTestRouter()
	r.POST("/notes/:id/audio", h.UploadAudio)
	r.POST("/notes/audio", h.CreateAudioNote)

	noteID := uuid.New().String()

	t.Run("UploadAudio without file returns 400", func(t *testing.T) {
		var b bytes.Buffer
		w := multipart.NewWriter(&b)
		_ = w.WriteField("duration", "45.2")
		w.Close()

		req := httptest.NewRequest(http.MethodPost, "/notes/"+noteID+"/audio", &b)
		req.Header.Set("Content-Type", w.FormDataContentType())
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), `"code":"BAD_REQUEST"`)
	})

	t.Run("CreateAudioNote without file returns 400", func(t *testing.T) {
		var b bytes.Buffer
		w := multipart.NewWriter(&b)
		_ = w.WriteField("title", "Voice Note")
		w.Close()

		req := httptest.NewRequest(http.MethodPost, "/notes/audio", &b)
		req.Header.Set("Content-Type", w.FormDataContentType())
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), `"code":"BAD_REQUEST"`)
	})
}

func TestNTS004PresignAudioRequest_Binding(t *testing.T) {
	r := newTestRouter()
	r.POST("/notes/:id/audio/presign", func(c *gin.Context) {
		var req domain.PresignAudioRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusCreated, req)
	})

	t.Run("valid presign payload binds correctly", func(t *testing.T) {
		dur := 65.5
		payload := map[string]any{
			"file_name":    "voice-memo.webm",
			"mime_type":    "audio/webm; codecs=opus",
			"size_bytes":   524288,
			"duration":     dur,
			"is_recording": true,
		}

		req := httptest.NewRequest(http.MethodPost, "/notes/"+uuid.New().String()+"/audio/presign", jsonBody(t, payload))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusCreated, w.Code)
		var resp domain.PresignAudioRequest
		require.NoError(t, jsonBodyDecode(w.Body.Bytes(), &resp))
		assert.Equal(t, "voice-memo.webm", resp.FileName)
		assert.Equal(t, "audio/webm; codecs=opus", resp.MimeType)
		assert.Equal(t, int64(524288), resp.SizeBytes)
		require.NotNil(t, resp.Duration)
		assert.Equal(t, dur, *resp.Duration)
		assert.True(t, resp.IsRecording)
	})

	t.Run("missing required mime_type or size_bytes fails binding", func(t *testing.T) {
		payload := map[string]any{
			"file_name": "voice.mp3",
		}
		req := httptest.NewRequest(http.MethodPost, "/notes/"+uuid.New().String()+"/audio/presign", jsonBody(t, payload))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}

func jsonBodyDecode(data []byte, v any) error {
	return json.Unmarshal(data, v)
}
