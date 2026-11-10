package service

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

func TestDriveStructureIntegration(t *testing.T) {
	dsn, endpoint := os.Getenv("TEST_MESSENGER_DATABASE_URL"), os.Getenv("TEST_DRIVE_MINIO_ENDPOINT")
	if dsn == "" || endpoint == "" {
		t.Skip("set disposable PostgreSQL and TEST_DRIVE_MINIO_ENDPOINT/ACCESS_KEY/SECRET_KEY")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	defer admin.Close()
	schema := "drive_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	defer admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
	cfg, err := pgxpool.ParseConfig(dsn)
	require.NoError(t, err)
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Exec(ctx, `CREATE TABLE organizations(id UUID PRIMARY KEY); CREATE TABLE users(id BIGINT PRIMARY KEY,organization_id UUID,username TEXT,first_name TEXT,last_name TEXT,email TEXT,avatar_url TEXT,deleted_at TIMESTAMPTZ); INSERT INTO users(id) VALUES(1),(2),(3),(4),(5),(6);`)
	require.NoError(t, err)
	for _, name := range []string{"014_create_cloud_disk.sql", "024_alter_table_files.sql", "20260504143300_add_is_public_to_drive.sql", "20260513000200_add_scope_to_files.sql", "20260624110000_add_content_hash_to_drive_files.sql", "20260728193000_add_external_source_to_drive_files.sql", "20260123001000_create_drive_links.sql", "20260626000100_create_drive_activity_logs.sql", "20260927120000_drive_version_history_index.sql", "20260927140000_drive_storage_quotas.sql", "20261012000400_company_storage_quotas.sql", "20261013000100_company_shared_storage.sql"} {
		data, err := os.ReadFile("../../db/migrations/" + name)
		require.NoError(t, err)
		_, err = db.Exec(ctx, strings.Split(string(data), "-- +goose Down")[0])
		require.NoError(t, err, name)
	}
	_, err = db.Exec(ctx, `CREATE TABLE user_recent_items(user_id BIGINT,resource_type TEXT,resource_id UUID,last_accessed_at TIMESTAMPTZ,UNIQUE(user_id,resource_type,resource_id))`)
	require.NoError(t, err)
	client, err := minio.New(endpoint, &minio.Options{Creds: credentials.NewStaticV4(os.Getenv("TEST_DRIVE_MINIO_ACCESS_KEY"), os.Getenv("TEST_DRIVE_MINIO_SECRET_KEY"), ""), Region: "us-east-1"})
	require.NoError(t, err)
	bucket := "drv-test-" + uuid.NewString()
	require.NoError(t, client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}))
	defer func() {
		for obj := range client.ListObjects(ctx, bucket, minio.ListObjectsOptions{Recursive: true}) {
			require.NoError(t, client.RemoveObject(ctx, bucket, obj.Key, minio.RemoveObjectOptions{}))
		}
		require.NoError(t, client.RemoveBucket(ctx, bucket))
	}()
	repo := repository.NewDriveRepository(db, zap.NewNop())
	fs := &FileService{minioClient: client, bucket: bucket, logger: zap.NewNop()}
	s := NewDriveService(repo, repository.NewRecentRepository(db, zap.NewNop()), fs, "http://localhost", nil, nil, zap.NewNop())
	root, err := s.CreateFolder(ctx, 1, nil, "Documents")
	require.NoError(t, err)
	_, err = s.CreateFolder(ctx, 2, nil, "Documents")
	require.NoError(t, err, "personal root names must be independent")
	_, err = s.CreateFolder(ctx, 1, nil, " documents ")
	require.Equal(t, 409, apperrors.ToAppError(err).StatusCode)
	for user, role := range map[int64]domain.PermissionRole{2: domain.RoleEditor, 3: domain.RoleViewer} {
		require.NoError(t, repo.UpsertPermission(ctx, &domain.Permission{ResourceType: domain.ResourceFolder, ResourceID: root.ID, UserID: user, Role: role, CreatedAt: time.Now()}))
	}
	nested, err := s.CreateFolder(ctx, 2, &root.ID, "Shared child")
	require.NoError(t, err)
	_, err = s.CreateFolder(ctx, 1, &root.ID, "Shared child")
	require.Equal(t, 409, apperrors.ToAppError(err).StatusCode)
	_, err = s.CreateFolder(ctx, 3, &root.ID, "Denied")
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	payload := []byte("DRV-001 exact bytes\nПривет\x00\xff")
	file, err := s.UploadFileToFolder(ctx, 2, &nested.ID, "report.txt", int64(len(payload)), "text/plain", bytes.NewReader(payload))
	require.NoError(t, err)
	for _, user := range []int64{1, 2, 3} {
		r, _, size, _, err := s.DownloadFileByID(ctx, user, file.ID)
		require.NoError(t, err)
		data, err := io.ReadAll(r)
		r.Close()
		require.NoError(t, err)
		require.Equal(t, int64(len(payload)), size)
		require.Equal(t, payload, data)
	}
	_, _, _, _, err = s.DownloadFileByID(ctx, 4, file.ID)
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	_, err = s.UploadFileToFolder(ctx, 3, &root.ID, "denied.txt", 1, "text/plain", strings.NewReader("x"))
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	countObjects := func() int {
		n := 0
		for obj := range client.ListObjects(ctx, bucket, minio.ListObjectsOptions{Recursive: true}) {
			require.NoError(t, obj.Err)
			n++
		}
		return n
	}
	empty, err := s.UploadFileToFolder(ctx, 1, nil, "empty.txt", 0, "text/plain", strings.NewReader(""))
	require.NoError(t, err)
	emptyReader, _, emptySize, _, err := s.DownloadFileByID(ctx, 1, empty.ID)
	require.NoError(t, err)
	emptyData, err := io.ReadAll(emptyReader)
	emptyReader.Close()
	require.NoError(t, err)
	require.Zero(t, emptySize)
	require.Empty(t, emptyData)
	_, err = s.UploadFileToFolder(ctx, 1, nil, "large.txt", MaxFileSize+1, "text/plain", strings.NewReader(""))
	require.Error(t, err)
	testDrivePreview(t, ctx, db, s)
	testDrivePublicLinks(t, ctx, db, s)
	testDriveACL(t, ctx, s, root, nested, file)
	testDriveOperations(t, ctx, db, s, root, nested, file, payload)
	before := countObjects()
	_, err = s.UploadFileToFolder(ctx, 2, &nested.ID, "duplicate.txt", int64(len(payload)), "text/plain", bytes.NewReader(payload))
	require.Equal(t, 409, apperrors.ToAppError(err).StatusCode)
	require.Equal(t, before, countObjects())
	link, err := s.CreatePublicLink(ctx, 2, "file", file.ID, "viewer", time.Hour)
	require.NoError(t, err)
	r, _, _, _, err := s.DownloadPublicFile(ctx, link.Token)
	require.NoError(t, err)
	data, err := io.ReadAll(r)
	r.Close()
	require.NoError(t, err)
	require.Equal(t, payload, data)
	_, _, _, _, err = s.DownloadPublicFile(ctx, uuid.New())
	require.Equal(t, 404, apperrors.ToAppError(err).StatusCode)
	_, err = db.Exec(ctx, `UPDATE drive_shared_links SET expires_at=NOW()-INTERVAL '1 second' WHERE token=$1`, link.Token)
	require.NoError(t, err)
	_, _, _, _, err = s.DownloadPublicFile(ctx, link.Token)
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	_, err = db.Exec(ctx, `UPDATE drive_shared_links SET expires_at=NULL WHERE token=$1;`, link.Token)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `UPDATE files SET deleted_at=NOW() WHERE id=$1`, file.ID)
	require.NoError(t, err)
	_, _, _, _, err = s.DownloadPublicFile(ctx, link.Token)
	require.Equal(t, 404, apperrors.ToAppError(err).StatusCode)
	_, err = db.Exec(ctx, `UPDATE files SET deleted_at=NULL WHERE id=$1`, file.ID)
	require.NoError(t, err)
	// A missing bucket produces an upload error without a metadata row.
	missingFS := *fs
	missingFS.bucket = "missing-" + uuid.NewString()
	failedService := NewDriveService(repo, repository.NewRecentRepository(db, zap.NewNop()), &missingFS, "", nil, nil, zap.NewNop())
	var countBefore, countAfter int
	require.NoError(t, db.QueryRow(ctx, `SELECT COUNT(*) FROM files`).Scan(&countBefore))
	_, err = failedService.UploadFileToFolder(ctx, 1, nil, "storage-failure.txt", 1, "text/plain", strings.NewReader("x"))
	require.Error(t, err)
	require.NoError(t, db.QueryRow(ctx, `SELECT COUNT(*) FROM files`).Scan(&countAfter))
	require.Equal(t, countBefore, countAfter)
	_, err = db.Exec(ctx, `CREATE FUNCTION reject_file() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'metadata failure'; END $$; CREATE TRIGGER reject_file BEFORE INSERT ON files FOR EACH ROW EXECUTE FUNCTION reject_file()`)
	require.NoError(t, err)
	_, err = s.UploadFileToFolder(ctx, 1, nil, "rollback.txt", 8, "text/plain", strings.NewReader("rollback"))
	require.Error(t, err)
	require.Equal(t, before, countObjects(), "failed metadata must not leave an object")
	// Cancellation after the upload must not cancel compensation.
	object, err := fs.UploadFile(ctx, 1, "cancelled.txt", "file", 1, strings.NewReader("x"))
	require.NoError(t, err)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	require.NoError(t, s.rollbackDriveUpload(cancelled, object))
	require.Equal(t, before, countObjects())
}
