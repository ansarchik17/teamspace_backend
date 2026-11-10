package service

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

func TestNotePhotoUploadIntegration(t *testing.T) {
	endpoint := os.Getenv("TEST_NWS_MINIO_ENDPOINT")
	if endpoint == "" {
		t.Skip("set TEST_NWS_MINIO_ENDPOINT and TEST_MESSENGER_DATABASE_URL")
	}
	_, pool := roomIntegrationService(t)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `
	CREATE TABLE files(id UUID PRIMARY KEY,owner_id BIGINT,folder_id UUID,real_name TEXT,object_name TEXT,size BIGINT,mime_type TEXT,version INT,scope TEXT,
	onlyoffice_key TEXT,source_system_code TEXT,source_system_name TEXT,external_message_id TEXT,created_at TIMESTAMPTZ,updated_at TIMESTAMPTZ,deleted_at TIMESTAMPTZ);
	CREATE TABLE file_versions(id UUID PRIMARY KEY,file_id UUID,version INT,object_name TEXT,size BIGINT,created_by BIGINT,created_at TIMESTAMPTZ);
	CREATE TABLE note_shares(note_id UUID,user_id BIGINT,is_hidden BOOLEAN DEFAULT false);
	CREATE TABLE messages(is_deleted BOOLEAN,metadata JSONB,conversation_id UUID);
	CREATE TABLE conversation_participants(conversation_id UUID,user_id BIGINT,left_at TIMESTAMPTZ);
	CREATE TABLE group_members(group_id UUID,user_id BIGINT,left_at TIMESTAMPTZ);`)
	require.NoError(t, err)
	for _, name := range []string{"20260323000100_create_notes_module.sql", "20260325000100_notes_rich_content.sql", "20260325000200_fix_notes_type_check.sql", "20260922000100_note_meeting_result.sql", "20260929000600_notes_formatting_color.sql", "20260929000700_notes_navigation_tags_favorites.sql", "20260930000100_notes_audio_recording.sql"} {
		data, err := os.ReadFile("../../db/migrations/" + name)
		require.NoError(t, err)
		_, err = pool.Exec(ctx, strings.Split(string(data), "-- +goose Down")[0])
		require.NoError(t, err)
	}
	bucket := "note-photo-test-" + uuid.NewString()
	files, err := NewFileService(config.MinIOConfig{Endpoint: endpoint, PublicBaseURL: "http://" + endpoint, Bucket: bucket,
		AccessKey: os.Getenv("TEST_NWS_MINIO_ACCESS_KEY"), SecretKey: os.Getenv("TEST_NWS_MINIO_SECRET_KEY")}, "https://teamspace.kz", nil, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() {
		for object := range files.minioClient.ListObjects(context.Background(), bucket, minio.ListObjectsOptions{Recursive: true}) {
			require.NoError(t, object.Err)
			require.NoError(t, files.minioClient.RemoveObject(context.Background(), bucket, object.Key, minio.RemoveObjectOptions{}))
		}
		require.NoError(t, files.minioClient.RemoveBucket(context.Background(), bucket))
	})
	svc := NewNoteService(repository.NewNoteRepository(pool, zap.NewNop()), files, zap.NewNop())
	note, err := svc.CreateNote(ctx, 1, &domain.CreateNoteRequest{Title: "Photo upload", Type: "rich"})
	require.NoError(t, err)
	var picture bytes.Buffer
	require.NoError(t, png.Encode(&picture, image.NewRGBA(image.Rect(0, 0, 3, 3))))
	_, _, err = svc.UploadPhotoWithID(ctx, 2, note.ID, "photo.png", int64(picture.Len()), bytes.NewReader(picture.Bytes()))
	require.Error(t, err)
	uploaded, attachmentID, err := svc.UploadPhotoWithID(ctx, 1, note.ID, "photo.png", int64(picture.Len()), bytes.NewReader(picture.Bytes()))
	require.NoError(t, err)
	require.Len(t, uploaded.Attachments, 1)
	require.Equal(t, attachmentID, uploaded.Attachments[0].FileID)
	downloadURL, err := url.Parse(uploaded.Attachments[0].DownloadURL)
	require.NoError(t, err)
	require.Equal(t, "teamspace.kz", downloadURL.Host)
	key := downloadURL.Query().Get("object_name")
	expires, err := strconv.ParseInt(downloadURL.Query().Get("expires"), 10, 64)
	require.NoError(t, err)
	require.True(t, files.ValidateSignedURL(key, expires, downloadURL.Query().Get("signature")))
	reader, mime, size, err := files.DownloadFile(ctx, key)
	require.NoError(t, err)
	data, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	require.Equal(t, picture.Bytes(), data)
	require.Equal(t, "image/png", mime)
	require.Equal(t, int64(picture.Len()), size)
	doc := &domain.ContentDoc{Type: "doc", Version: 1, Blocks: []domain.ContentBlock{{ID: "image-1", Type: "image", AttachmentID: attachmentID.String()}}}
	_, err = svc.UpdateNote(ctx, 1, note.ID, &domain.UpdateNoteRequest{Version: uploaded.Version, ContentDoc: doc})
	require.NoError(t, err)
	reopened, err := svc.GetNote(ctx, 1, note.ID)
	require.NoError(t, err)
	require.Equal(t, doc, reopened.ContentDoc)
	require.Len(t, reopened.Attachments, 1)
	require.Equal(t, attachmentID, reopened.Attachments[0].FileID)
}
