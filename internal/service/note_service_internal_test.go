package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestMergePhotoAttachments_AppendsPhotoEntriesWithDownloadURL(t *testing.T) {
	svc := &NoteService{
		fileService: &FileService{
			publicURL: "https://api.example.com",
		},
	}

	merged, err := svc.mergePhotoAttachments(context.Background(),
		[]domain.NoteAttachment{{FileID: uuid.New(), DownloadURL: "https://api.example.com/api/v1/files/download?object_name=legacy"}},
		[]domain.NotePhotoAttachment{{
			ID:         uuid.MustParse("11111111-1111-1111-1111-111111111111"),
			StorageKey: "note-photos/note-1/att-1",
			MimeType:   "image/jpeg",
			SizeBytes:  12345,
			CreatedAt:  time.Date(2026, 3, 25, 12, 0, 0, 0, time.UTC),
		}},
	)
	require.NoError(t, err)
	require.Len(t, merged, 2)
	assert.Equal(t, uuid.MustParse("11111111-1111-1111-1111-111111111111"), merged[1].FileID)
	assert.Equal(t, "image/jpeg", merged[1].MimeType)
	assert.Equal(t, int64(12345), merged[1].Size)
	assert.Contains(t, merged[1].DownloadURL, "https://api.example.com/api/v1/files/download?object_name=note-photos%2Fnote-1%2Fatt-1")
	assert.Contains(t, merged[1].DownloadURL, "&signature=")
}

func TestMergePhotoAttachments_FileServiceMissing(t *testing.T) {
	svc := &NoteService{}
	_, err := svc.mergePhotoAttachments(context.Background(), nil, []domain.NotePhotoAttachment{{ID: uuid.New(), StorageKey: "note-photos/x"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "file service is unavailable")
}
