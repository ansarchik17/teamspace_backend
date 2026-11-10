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

func TestNTS004IsAllowedAudioMimeType(t *testing.T) {
	validCases := []string{
		"audio/mpeg",
		"audio/mp3",
		"audio/mp4",
		"audio/m4a",
		"audio/x-m4a",
		"audio/wav",
		"audio/wave",
		"audio/x-wav",
		"audio/vnd.wave",
		"audio/webm",
		"audio/webm;codecs=opus",
		"audio/webm; codecs=opus",
		"video/webm",
		"audio/ogg",
		"application/ogg",
		"audio/aac",
		"audio/x-aac",
		"audio/flac",
		"audio/x-flac",
		"AUDIO/MPEG",
		"  audio/wav  ",
	}

	for _, tc := range validCases {
		t.Run("valid_"+tc, func(t *testing.T) {
			assert.True(t, domain.IsAllowedAudioMimeType(tc))
		})
	}

	invalidCases := []string{
		"",
		"   ",
		"image/png",
		"image/jpeg",
		"video/mp4",
		"text/plain",
		"application/pdf",
		"application/octet-stream",
	}

	for _, tc := range invalidCases {
		t.Run("invalid_"+tc, func(t *testing.T) {
			assert.False(t, domain.IsAllowedAudioMimeType(tc))
		})
	}
}

func TestNTS004IsAllowedAudioExtension(t *testing.T) {
	validFiles := []string{
		"recording.mp3",
		"voice.m4a",
		"meeting.wav",
		"record.webm",
		"track.ogg",
		"audio.aac",
		"lossless.flac",
		"UPPERCASE.MP3",
		"MIXED.WaV",
		"/path/to/nested/audio.m4a",
	}

	for _, tc := range validFiles {
		t.Run("valid_"+tc, func(t *testing.T) {
			assert.True(t, domain.IsAllowedAudioExtension(tc))
		})
	}

	invalidFiles := []string{
		"document.pdf",
		"image.png",
		"video.mp4",
		"script.sh",
		"noextension",
		"",
		".",
	}

	for _, tc := range invalidFiles {
		t.Run("invalid_"+tc, func(t *testing.T) {
			assert.False(t, domain.IsAllowedAudioExtension(tc))
		})
	}
}

func TestNTS004ContentBlockAudioValidation(t *testing.T) {
	dur := 125.5
	wf := []float64{0.1, 0.4, 0.9, 0.3}

	t.Run("valid audio block with url and attachmentId", func(t *testing.T) {
		doc := domain.ContentDoc{
			Type:    "doc",
			Version: 1,
			Blocks: []domain.ContentBlock{
				{
					ID:           "b1",
					Type:         domain.NoteBlockTypeAudio,
					URL:          "https://storage.local/audio/rec1.mp3",
					AttachmentID: uuid.New().String(),
					Caption:      "Meeting recording",
					Duration:     &dur,
					Waveform:     wf,
					IsRecording:  true,
				},
			},
		}
		require.NoError(t, doc.Validate())
		assert.Contains(t, doc.ExtractPlainText(), "Meeting recording")
	})

	t.Run("valid audio block with only url", func(t *testing.T) {
		doc := domain.ContentDoc{
			Type:    "doc",
			Version: 1,
			Blocks: []domain.ContentBlock{
				{
					ID:   "b1",
					Type: domain.NoteBlockTypeAudio,
					URL:  "https://cdn.example.com/audio.wav",
				},
			},
		}
		require.NoError(t, doc.Validate())
		assert.Equal(t, "", doc.ExtractPlainText())
	})

	t.Run("valid audio block with only attachmentId", func(t *testing.T) {
		doc := domain.ContentDoc{
			Type:    "doc",
			Version: 1,
			Blocks: []domain.ContentBlock{
				{
					ID:           "b1",
					Type:         domain.NoteBlockTypeAudio,
					AttachmentID: uuid.New().String(),
				},
			},
		}
		require.NoError(t, doc.Validate())
		assert.Equal(t, "", doc.ExtractPlainText())
	})

	t.Run("invalid audio block missing both url and attachmentId", func(t *testing.T) {
		doc := domain.ContentDoc{
			Type:    "doc",
			Version: 1,
			Blocks: []domain.ContentBlock{
				{
					ID:   "b1",
					Type: domain.NoteBlockTypeAudio,
				},
			},
		}
		err := doc.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "audio block missing attachmentId or url")
	})
}

func TestNTS004AudioRequest_JSONSerialization(t *testing.T) {
	dur := 42.0
	wf := []float64{0.12, 0.34, 0.56}

	t.Run("PresignAudioRequest", func(t *testing.T) {
		req := domain.PresignAudioRequest{
			FileName:    "dictaphone.webm",
			MimeType:    "audio/webm",
			SizeBytes:   204800,
			Duration:    &dur,
			IsRecording: true,
		}
		data, err := json.Marshal(req)
		require.NoError(t, err)

		var decoded domain.PresignAudioRequest
		require.NoError(t, json.Unmarshal(data, &decoded))
		assert.Equal(t, req.FileName, decoded.FileName)
		assert.Equal(t, req.MimeType, decoded.MimeType)
		assert.Equal(t, req.SizeBytes, decoded.SizeBytes)
		assert.Equal(t, *req.Duration, *decoded.Duration)
		assert.True(t, decoded.IsRecording)
	})

	t.Run("CompleteAudioRequest", func(t *testing.T) {
		fileName := "renamed.m4a"
		req := domain.CompleteAudioRequest{
			FileName: &fileName,
			Duration: &dur,
			Waveform: wf,
		}
		data, err := json.Marshal(req)
		require.NoError(t, err)

		var decoded domain.CompleteAudioRequest
		require.NoError(t, json.Unmarshal(data, &decoded))
		assert.Equal(t, fileName, *decoded.FileName)
		assert.Equal(t, *req.Duration, *decoded.Duration)
		assert.Equal(t, req.Waveform, decoded.Waveform)
	})

	t.Run("Note with AudioAttachments", func(t *testing.T) {
		noteID := uuid.New()
		attID := uuid.New()
		now := time.Now().UTC().Truncate(time.Second)

		note := domain.Note{
			ID:     noteID,
			UserID: 55,
			Title:  "Audio Note",
			Type:   domain.NoteTypeAudio,
			AudioAttachments: []domain.NoteAudioAttachment{
				{
					ID:          attID,
					NoteID:      noteID,
					UserID:      55,
					StorageKey:  "notes/audio/55/rec.mp3",
					FileName:    "rec.mp3",
					MimeType:    "audio/mpeg",
					SizeBytes:   102400,
					Duration:    &dur,
					Waveform:    wf,
					IsRecording: true,
					Status:      domain.NoteAudioStatusUploaded,
					DownloadURL: "https://minio.local/notes/audio/55/rec.mp3",
					CreatedAt:   now,
					UpdatedAt:   now,
				},
			},
			CreatedAt: now,
			UpdatedAt: now,
			Version:   1,
		}

		data, err := json.Marshal(note)
		require.NoError(t, err)

		var decoded domain.Note
		require.NoError(t, json.Unmarshal(data, &decoded))
		assert.Equal(t, domain.NoteTypeAudio, decoded.Type)
		require.Len(t, decoded.AudioAttachments, 1)
		assert.Equal(t, attID, decoded.AudioAttachments[0].ID)
		assert.Equal(t, "rec.mp3", decoded.AudioAttachments[0].FileName)
		assert.Equal(t, true, decoded.AudioAttachments[0].IsRecording)
		assert.Equal(t, domain.NoteAudioStatusUploaded, decoded.AudioAttachments[0].Status)
	})
}
