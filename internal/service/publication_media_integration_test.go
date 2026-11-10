package service

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"testing"
	"testing/fstest"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/minio/minio-go/v7"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

// Uses real PostgreSQL migrations and MinIO, with a disposable schema and bucket.
func TestNWS003MediaIntegration(t *testing.T) {
	endpoint := os.Getenv("TEST_NWS_MINIO_ENDPOINT")
	if endpoint == "" {
		t.Skip("set TEST_NWS_MINIO_ENDPOINT and TEST_MESSENGER_DATABASE_URL")
	}
	_, pool := roomIntegrationService(t)
	ctx := context.Background()
	sqlDB := stdlib.OpenDB(*pool.Config().ConnConfig)
	t.Cleanup(func() { _ = sqlDB.Close() })
	migrations := fstest.MapFS{}
	for _, name := range []string{
		"20260630000100_create_publications.sql",
		"20260701000200_create_publication_views.sql",
		"20260929001000_publications_news_cover_status_rich.sql",
		"20260929001100_publications_media_gallery_pdf_video.sql",
		"20260929001200_publication_object_cleanup.sql",
		"20260929001300_publications_reactions_threads_moderation.sql",
		"20260930001400_publications_pinned_banners_broadcasts.sql",
		"20260930001500_publication_audience_scope.sql",
		"20261001000100_publications_drafts_scheduled_archive.sql",
		"20261001000200_publications_target_audience_departments.sql",
	} {
		data, err := os.ReadFile("../../db/migrations/" + name)
		require.NoError(t, err)
		migrations[name] = &fstest.MapFile{Data: data}
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations)
	require.NoError(t, err)
	applied, err := provider.Up(ctx)
	require.NoError(t, err)
	require.Len(t, applied, 10)
	applied, err = provider.Up(ctx)
	require.NoError(t, err)
	require.Empty(t, applied, "restarting must not reapply migrations")

	bucket := "nws003-test-" + uuid.NewString()
	files, err := NewFileService(config.MinIOConfig{
		Endpoint: endpoint, PublicBaseURL: "http://" + endpoint,
		AccessKey: os.Getenv("TEST_NWS_MINIO_ACCESS_KEY"), SecretKey: os.Getenv("TEST_NWS_MINIO_SECRET_KEY"),
		Bucket: bucket,
	}, "https://api.test", nil, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() {
		for object := range files.minioClient.ListObjects(context.Background(), bucket, minio.ListObjectsOptions{Recursive: true}) {
			if object.Err == nil {
				require.NoError(t, files.minioClient.RemoveObject(context.Background(), bucket, object.Key, minio.RemoveObjectOptions{}))
			}
		}
		require.NoError(t, files.minioClient.RemoveBucket(context.Background(), bucket))
	})
	repo := repository.NewPublicationRepository(pool, zap.NewNop())
	svc := NewPublicationService(repo, repository.NewUserRepository(pool, zap.NewNop()), zap.NewNop())
	svc.SetFileService(files)
	pubID := uuid.New()
	_, err = pool.Exec(ctx, `INSERT INTO publications(id,author_id,title,status) VALUES($1,1,'Media test','draft')`, pubID)
	require.NoError(t, err)
	var picture bytes.Buffer
	require.NoError(t, png.Encode(&picture, image.NewRGBA(image.Rect(0, 0, 3, 3))))
	imageHeaders := nws003Multipart(t, "photo.png", picture.Bytes())

	_, err = svc.UploadGallery(ctx, pubID, 2, imageHeaders, nil)
	require.ErrorIs(t, err, ErrForbiddenAction)
	gallery, err := svc.UploadGallery(ctx, pubID, 1, imageHeaders, []string{"Caption"})
	require.NoError(t, err)
	require.Len(t, gallery, 1)
	nws003Download(t, files, gallery[0].FileURL, picture.Bytes(), "image/png")
	pdf := []byte("%PDF-1.4\n1 0 obj\n<< /Type /Catalog >>\nendobj\n%%EOF")
	attachments, err := svc.UploadAttachments(ctx, pubID, 1, nws003Multipart(t, "document.pdf", pdf), nil)
	require.NoError(t, err)
	nws003Download(t, files, attachments[0].FileURL, pdf, "application/pdf")
	video, err := svc.AddVideoEmbed(ctx, pubID, 1, domain.AddVideoEmbedRequest{VideoURL: "https://youtu.be/dQw4w9WgXcQ"})
	require.NoError(t, err)
	require.Equal(t, "https://www.youtube.com/embed/dQw4w9WgXcQ", *video.VideoEmbedURL)
	_, err = svc.AddVideoEmbed(ctx, pubID, 1, domain.AddVideoEmbedRequest{VideoURL: "https://youtube.com.evil.test/watch?v=abc"})
	require.Error(t, err)

	pub, err := repo.GetByID(ctx, pubID, 1)
	require.NoError(t, err)
	pub.Title = "Edited without attachments"
	require.NoError(t, repo.Update(ctx, pub, nil))
	after, err := repo.GetAttachmentsByPubID(ctx, pubID)
	require.NoError(t, err)
	require.Len(t, after, 3)
	require.Equal(t, gallery[0].ID, after[0].ID)
	previousID := after[0].ID
	after[0].ID = uuid.Nil
	require.NoError(t, repo.Update(ctx, pub, after))
	after, err = repo.GetAttachmentsByPubID(ctx, pubID)
	require.NoError(t, err)
	require.Equal(t, previousID, after[0].ID)
	_, err = svc.ReorderAttachments(ctx, pubID, 1, []uuid.UUID{previousID, previousID, video.ID})
	require.Error(t, err)
	_, err = svc.ReorderAttachments(ctx, pubID, 1, []uuid.UUID{video.ID, attachments[0].ID, previousID})
	require.NoError(t, err)
	// A failure after storage upload must roll back all attachment inserts and enqueue the object.
	_, err = pool.Exec(ctx, `CREATE FUNCTION reject_media_test() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test insert failure'; END $$;
	CREATE TRIGGER reject_media_test BEFORE INSERT ON publication_attachments FOR EACH ROW EXECUTE FUNCTION reject_media_test()`)
	require.NoError(t, err)
	_, err = svc.UploadGallery(ctx, pubID, 1, imageHeaders, nil)
	require.Error(t, err)
	var failedKey string
	require.NoError(t, pool.QueryRow(ctx, `SELECT object_name FROM publication_object_cleanup`).Scan(&failedKey))
	_, err = pool.Exec(ctx, `DROP TRIGGER reject_media_test ON publication_attachments`)
	require.NoError(t, err)
	require.NoError(t, repo.ProcessPublicationObjectCleanup(ctx, func(context.Context, string) error { return fmt.Errorf("storage offline") }))
	var attempts int
	require.NoError(t, pool.QueryRow(ctx, `SELECT attempts FROM publication_object_cleanup WHERE object_name=$1`, failedKey).Scan(&attempts))
	require.Equal(t, 1, attempts)
	_, err = pool.Exec(ctx, `UPDATE publication_object_cleanup SET next_attempt_at=NOW()`)
	require.NoError(t, err)
	require.NoError(t, repo.ProcessPublicationObjectCleanup(ctx, files.DeleteFile))
	_, err = files.minioClient.StatObject(ctx, bucket, failedKey, minio.StatObjectOptions{})
	require.Error(t, err)

	// Shared references keep objects alive; removed attachments are eventually deleted.
	stored, err := repo.GetAttachmentByID(ctx, pubID, previousID)
	require.NoError(t, err)
	require.NoError(t, repo.QueuePublicationObjectCleanup(ctx, []string{stored.FileURL}))
	require.NoError(t, repo.ProcessPublicationObjectCleanup(ctx, files.DeleteFile))
	_, err = files.minioClient.StatObject(ctx, bucket, stored.FileURL, minio.StatObjectOptions{})
	require.NoError(t, err)
	require.NoError(t, svc.DeleteAttachment(ctx, pubID, previousID, 1))
	require.NoError(t, repo.ProcessPublicationObjectCleanup(ctx, files.DeleteFile))
	_, err = files.minioClient.StatObject(ctx, bucket, stored.FileURL, minio.StatObjectOptions{})
	require.Error(t, err)
	require.NoError(t, repo.Update(ctx, pub, []domain.PublicationAttachment{}))
	require.NoError(t, repo.ProcessPublicationObjectCleanup(ctx, files.DeleteFile))
	after, err = repo.GetAttachmentsByPubID(ctx, pubID)
	require.NoError(t, err)
	require.Empty(t, after)
}

func nws003Multipart(t *testing.T, name string, data []byte) []*multipart.FileHeader {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("files", name)
	require.NoError(t, err)
	_, err = part.Write(data)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	r := httptest.NewRequest("POST", "/", &body)
	r.Header.Set("Content-Type", w.FormDataContentType())
	require.NoError(t, r.ParseMultipartForm(1<<20))
	t.Cleanup(func() { _ = r.MultipartForm.RemoveAll() })
	return r.MultipartForm.File["files"]
}

func nws003Download(t *testing.T, files *FileService, rawURL string, want []byte, mime string) {
	t.Helper()
	u, err := url.Parse(rawURL)
	require.NoError(t, err)
	key := u.Query().Get("object_name")
	expires, err := strconv.ParseInt(u.Query().Get("expires"), 10, 64)
	require.NoError(t, err)
	require.True(t, files.ValidateSignedURL(key, expires, u.Query().Get("signature")))
	reader, contentType, size, err := files.DownloadFile(context.Background(), key)
	require.NoError(t, err)
	defer reader.Close()
	data, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Equal(t, want, data)
	require.Equal(t, mime, contentType)
	require.Equal(t, int64(len(want)), size)
}
