package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/config"
	"go.uber.org/zap"
)

func TestFileDeletionRejectsUnprovenOwnership(t *testing.T) {
	leaf := "1794736800_209fab7c-4174-45ac-a258-d9b80e1f2900.txt"
	for _, tc := range []struct {
		name, key string
		user      int64
		status    int
	}{
		{"anonymous", "file/42/" + leaf, 0, 401},
		{"invalid actor", "file/42/" + leaf, -1, 401},
		{"other owner", "file/42/" + leaf, 7, 403},
		{"similar owner prefix", "file/420/" + leaf, 42, 403},
		{"anonymous public upload", "image/0/" + leaf, 42, 403},
		{"public namespace", "public/42/" + leaf, 42, 403},
		{"unowned avatar", "avatars/42/" + leaf, 42, 403},
		{"managed namespace", "note-photos/42/" + leaf, 42, 403},
		{"drive namespace", "drive/42/" + leaf, 42, 403},
		{"leading slash", "/file/42/" + leaf, 42, 403},
		{"traversal", "file/42/../7/" + leaf, 42, 403},
		{"encoded traversal", "file/42/%2e%2e%2f7%2f" + leaf, 42, 403},
		{"forged public upload type", "file/42/0/" + leaf, 42, 403},
		{"noncanonical owner", "file/042/" + leaf, 42, 403},
		{"arbitrary object", "file/42/private.txt", 42, 403},
		{"unknown storage key", "unknown", 42, 403},
		{"missing ownership database", "file/42/" + leaf, 42, 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// No storage client exists: a denied operation must never touch S3.
			err := (&FileService{}).DeleteFileForUser(context.Background(), tc.user, tc.key)
			require.Error(t, err)
			require.Equal(t, tc.status, apperrors.ToAppError(err).StatusCode)
		})
	}
	for _, kind := range []string{"file", "image", "video", "document", "pdf", "audio", "voice", "ai-uploads"} {
		require.True(t, ownsStandaloneUpload(42, kind+"/42/"+leaf))
	}
	s := &FileService{}
	require.True(t, ownsStandaloneUpload(42, s.generateObjectName(42, "file", "")))
	require.True(t, ownsStandaloneUpload(42, s.generateObjectName(42, "document", ".PDF")))
}

func TestFileDeletionObjectStorageIntegration(t *testing.T) {
	endpoint := os.Getenv("TEST_NWS_MINIO_ENDPOINT")
	if endpoint == "" {
		t.Skip("set TEST_NWS_MINIO_ENDPOINT and disposable TEST_MESSENGER_DATABASE_URL")
	}
	ctx, db, drive := newDrivePostgresTest(t)
	_, err := db.Exec(ctx, `CREATE TABLE note_audio_attachments(storage_key TEXT)`)
	require.NoError(t, err)
	bucket := "file-delete-test-" + uuid.NewString()
	s, err := NewFileService(config.MinIOConfig{
		Endpoint: endpoint, PublicBaseURL: "http://" + endpoint, Bucket: bucket, Region: "us-east-1",
		AccessKey: os.Getenv("TEST_NWS_MINIO_ACCESS_KEY"), SecretKey: os.Getenv("TEST_NWS_MINIO_SECRET_KEY"),
	}, "http://localhost", drive.repo, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() {
		for object := range s.minioClient.ListObjects(context.Background(), bucket, minio.ListObjectsOptions{Recursive: true}) {
			require.NoError(t, object.Err)
			require.NoError(t, s.DeleteFile(context.Background(), object.Key))
		}
		require.NoError(t, s.minioClient.RemoveBucket(context.Background(), bucket))
	})
	const payload = "only my upload may be deleted"
	key, err := s.UploadFile(ctx, 1, "own.txt", "file", int64(len(payload)), strings.NewReader(payload))
	require.NoError(t, err)
	for _, actor := range []int64{0, 2} {
		require.Error(t, s.DeleteFileForUser(ctx, actor, key))
		reader, _, _, err := s.DownloadFile(ctx, key)
		require.NoError(t, err)
		data, err := io.ReadAll(reader)
		require.NoError(t, err)
		require.NoError(t, reader.Close())
		require.Equal(t, payload, string(data), "a denied request must leave the actual stored bytes intact")
	}
	require.NoError(t, s.DeleteFileForUser(ctx, 1, key))
	_, err = s.minioClient.StatObject(ctx, bucket, key, minio.StatObjectOptions{})
	require.Equal(t, "NoSuchKey", minio.ToErrorResponse(err).Code)
	for _, kind := range []string{"file", "image/1"} {
		key, err := s.UploadFile(ctx, 0, "anonymous.txt", kind, int64(len(payload)), strings.NewReader(payload))
		require.NoError(t, err)
		err = s.DeleteFileForUser(ctx, 1, key)
		require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
		_, err = s.minioClient.StatObject(ctx, bucket, key, minio.StatObjectOptions{})
		require.NoError(t, err)
	}
}

func TestFileDeletionOwnershipIntegration(t *testing.T) {
	ctx, db, drive := newDrivePostgresTest(t)
	_, err := db.Exec(ctx, `CREATE TABLE note_audio_attachments(storage_key TEXT)`)
	require.NoError(t, err)
	var mu sync.Mutex
	var deleted []string
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("unexpected storage request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		mu.Lock()
		deleted = append(deleted, strings.TrimPrefix(r.URL.Path, "/test-bucket/"))
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(storage.Close)
	client, err := minio.New(strings.TrimPrefix(storage.URL, "http://"), &minio.Options{
		Creds: credentials.NewStaticV4("test-access", "test-secret", ""), Region: "us-east-1",
	})
	require.NoError(t, err)
	s := &FileService{minioClient: client, driveRepo: drive.repo, bucket: "test-bucket", logger: zap.NewNop()}
	count := func() int { mu.Lock(); defer mu.Unlock(); return len(deleted) }

	key := s.generateObjectName(1, "file", ".txt")
	err = s.DeleteFileForUser(ctx, 2, key)
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	require.Zero(t, count())
	require.NoError(t, s.DeleteFileForUser(ctx, 1, key))
	require.Equal(t, 1, count())
	mu.Lock()
	require.Equal(t, key, deleted[0], "delete the exact authorized object")
	mu.Unlock()

	for _, state := range []string{"active", "trashed", "historical version", "note audio"} {
		t.Run(state, func(t *testing.T) {
			key := s.generateObjectName(1, "audio", ".mp3")
			if state == "note audio" {
				_, err = db.Exec(ctx, `INSERT INTO note_audio_attachments(storage_key) VALUES($1)`, key)
				require.NoError(t, err)
			} else {
				file := quotaTestFile(2, 1) // A moved/shared file may have a different resource owner.
				file.ObjectName = key
				if state == "historical version" {
					file.ObjectName = "new-current-object-" + file.ID.String()
				}
				require.NoError(t, drive.repo.CreateFileWithInitialVersion(ctx, file, 2))
				if state == "trashed" {
					require.NoError(t, drive.repo.SoftDeleteFile(ctx, file.ID))
				}
				if state == "historical version" {
					_, err = drive.repo.AppendFileVersion(ctx, file.ID, key, 1, 2, 1)
					require.NoError(t, err)
					_, err = db.Exec(ctx, `UPDATE files SET object_name='latest-object' WHERE id=$1`, file.ID)
					require.NoError(t, err)
				}
			}
			before := count()
			err := s.DeleteFileForUser(ctx, 1, key)
			require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
			require.Equal(t, before, count(), "managed objects must never be physically deleted by the generic endpoint")
		})
	}
	// Ownership lookup failure must not degrade into permission to delete.
	_, err = db.Exec(ctx, `DROP TABLE note_audio_attachments`)
	require.NoError(t, err)
	before := count()
	err = s.DeleteFileForUser(ctx, 1, s.generateObjectName(1, "file", ".txt"))
	require.Equal(t, 500, apperrors.ToAppError(err).StatusCode)
	require.Equal(t, before, count())
	// Dedicated Drive deletion still uses its own authorized workflow.
	_, err = db.Exec(ctx, `CREATE TABLE note_audio_attachments(storage_key TEXT)`)
	require.NoError(t, err)
	file := quotaTestFile(1, 1)
	require.NoError(t, drive.repo.CreateFileWithInitialVersion(ctx, file, 1))
	_, err = drive.DeleteFile(ctx, 1, file.ID)
	require.NoError(t, err)
	var deletedAt *time.Time
	require.NoError(t, db.QueryRow(ctx, `SELECT deleted_at FROM files WHERE id=$1`, file.ID).Scan(&deletedAt))
	require.NotNil(t, deletedAt)
}
