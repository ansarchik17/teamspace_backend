package service

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

func TestDocumentCreationAndTemplatesIntegration(t *testing.T) {
	ctx, db, s := newDrivePostgresTest(t)
	var mu sync.Mutex
	objects := map[string][]byte{}
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			var reader io.Reader = r.Body
			if strings.Contains(r.Header.Get("Content-Encoding"), "aws-chunked") || strings.HasPrefix(r.Header.Get("X-Amz-Content-Sha256"), "STREAMING-") {
				reader = httputil.NewChunkedReader(r.Body)
			}
			data, err := io.ReadAll(reader)
			if err != nil {
				http.Error(w, "read failed", 500)
				return
			}
			mu.Lock()
			objects[r.URL.Path] = data
			mu.Unlock()
			w.Header().Set("ETag", `"fixture"`)
			w.WriteHeader(200)
			return
		}
		if r.Method == http.MethodDelete {
			mu.Lock()
			delete(objects, r.URL.Path)
			mu.Unlock()
			w.WriteHeader(204)
			return
		}
		mu.Lock()
		data, ok := objects[r.URL.Path]
		mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("ETag", `"fixture"`)
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.Header().Set("Content-Type", "text/plain")
		http.ServeContent(w, r, "file.txt", time.Unix(1, 0), bytes.NewReader(data))
	}))
	defer storage.Close()
	client, err := minio.New(strings.TrimPrefix(storage.URL, "http://"), &minio.Options{Region: "us-east-1"})
	require.NoError(t, err)
	s.fileSvc = &FileService{minioClient: client, bucket: "test", logger: zap.NewNop()}
	_, err = db.Exec(ctx, `CREATE TABLE organizations(id UUID PRIMARY KEY,owner_id BIGINT,status TEXT,updated_at TIMESTAMPTZ DEFAULT NOW());
 ALTER TABLE users ADD COLUMN organization_id UUID, ADD COLUMN role TEXT DEFAULT 'employee';`)
	require.NoError(t, err)
	orgA, orgB := uuid.New(), uuid.New()
	_, err = db.Exec(ctx, `INSERT INTO organizations(id,owner_id,status) VALUES($1,1,'active'),($2,2,'active')`, orgA, orgB)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `UPDATE users SET organization_id=$1 WHERE id IN (1,3,4)`, orgA)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `UPDATE users SET organization_id=$1 WHERE id=2`, orgB)
	require.NoError(t, err)
	migration, err := os.ReadFile("../../db/migrations/20260929120000_document_templates.sql")
	require.NoError(t, err)
	_, err = db.Exec(ctx, strings.Split(string(migration), "-- +goose Down")[0])
	require.NoError(t, err)
	for _, format := range []string{"docx", "xlsx", "pptx"} {
		t.Run(format, func(t *testing.T) {
			file, err := s.CreateDocument(ctx, 1, domain.CreateDocumentRequest{Name: "Чистый лист", Format: format})
			require.NoError(t, err)
			require.Equal(t, "Чистый лист."+format, file.RealName)
			require.Equal(t, 1, file.Version)
			require.Equal(t, "drive", file.Scope)
			require.Equal(t, documentMIME(format), file.MimeType)
			duplicate, err := s.CreateDocument(ctx, 1, domain.CreateDocumentRequest{Name: "Ещё один", Format: format})
			require.NoError(t, err)
			require.NotEqual(t, file.ID, duplicate.ID)
			_, err = s.PublishDocumentTemplate(ctx, 3, domain.PublishDocumentTemplateRequest{Name: "Denied", FileID: file.ID})
			require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
			template, err := s.PublishDocumentTemplate(ctx, 1, domain.PublishDocumentTemplateRequest{Name: "Шаблон", FileID: file.ID})
			require.NoError(t, err)
			_, err = s.PublishDocumentTemplate(ctx, 1, domain.PublishDocumentTemplateRequest{Name: "Duplicate", FileID: file.ID})
			require.Equal(t, 409, apperrors.ToAppError(err).StatusCode)
			items, err := s.ListDocumentTemplates(ctx, 3, 50, 0)
			require.NoError(t, err)
			require.Len(t, items, 1)
			items, err = s.ListDocumentTemplates(ctx, 2, 50, 0)
			require.NoError(t, err)
			require.Empty(t, items)
			_, err = s.CreateDocument(ctx, 2, domain.CreateDocumentRequest{Name: "No access", TemplateID: &template.ID})
			require.Equal(t, 404, apperrors.ToAppError(err).StatusCode)
			folder, err := s.CreateFolder(ctx, 1, nil, "Private-"+format)
			require.NoError(t, err)
			_, err = s.CreateDocument(ctx, 3, domain.CreateDocumentRequest{Name: "No access", FolderID: &folder.ID, TemplateID: &template.ID})
			require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
			_, err = s.canAccessFile(ctx, 3, file.ID, domain.RoleViewer)
			require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
			require.Equal(t, 404, apperrors.ToAppError(s.DeleteDocumentTemplate(ctx, 2, template.ID)).StatusCode)
			copy, err := s.CreateDocument(ctx, 3, domain.CreateDocumentRequest{Name: "Копия", TemplateID: &template.ID})
			require.NoError(t, err)
			require.Equal(t, int64(3), copy.OwnerID)
			require.NotEqual(t, file.ObjectName, copy.ObjectName)
			require.False(t, copy.IsPublic)
			mu.Lock()
			original := append([]byte(nil), objects["/test/"+file.ObjectName]...)
			copied := append([]byte(nil), objects["/test/"+copy.ObjectName]...)
			mu.Unlock()
			require.NotEmpty(t, original)
			require.Equal(t, original, copied)
			versions, err := s.repo.ListFileVersions(ctx, copy.ID, 10, 0)
			require.NoError(t, err)
			require.Len(t, versions, 1)
			_, err = s.CreateDocument(ctx, 3, domain.CreateDocumentRequest{Name: "Ещё копия", TemplateID: &template.ID})
			require.NoError(t, err)
			// Ordinary uploads retain content-based duplicate protection.
			_, err = s.UploadFileToFolder(ctx, 3, nil, "duplicate."+format, int64(len(original)), documentMIME(format), bytes.NewReader(original))
			require.Equal(t, 409, apperrors.ToAppError(err).StatusCode)
			_, err = db.Exec(ctx, `UPDATE files SET owner_id=2 WHERE id=$1`, file.ID)
			require.NoError(t, err)
			items, err = s.ListDocumentTemplates(ctx, 3, 50, 0)
			require.NoError(t, err)
			require.Empty(t, items)
			_, err = s.CreateDocument(ctx, 3, domain.CreateDocumentRequest{Name: "Transferred", TemplateID: &template.ID})
			require.Equal(t, 404, apperrors.ToAppError(err).StatusCode)
			_, err = db.Exec(ctx, `UPDATE files SET owner_id=1 WHERE id=$1`, file.ID)
			require.NoError(t, err)
			// A new source version affects future copies, never previously created files.
			archive, err := zip.NewReader(bytes.NewReader(original), int64(len(original)))
			require.NoError(t, err)
			var changed bytes.Buffer
			writer := zip.NewWriter(&changed)
			require.NoError(t, writer.SetComment("updated company template"))
			for _, part := range archive.File {
				require.NoError(t, writer.Copy(part))
			}
			require.NoError(t, writer.Close())
			_, err = s.UploadFileVersion(ctx, 1, file.ID, file.RealName, int64(changed.Len()), bytes.NewReader(changed.Bytes()), 1)
			require.NoError(t, err)
			latest, err := s.CreateDocument(ctx, 3, domain.CreateDocumentRequest{Name: "Новая версия", TemplateID: &template.ID})
			require.NoError(t, err)
			mu.Lock()
			latestBytes := append([]byte(nil), objects["/test/"+latest.ObjectName]...)
			oldBytes := append([]byte(nil), objects["/test/"+copy.ObjectName]...)
			mu.Unlock()
			require.Equal(t, changed.Bytes(), latestBytes)
			require.Equal(t, original, oldBytes)
			require.NoError(t, s.repo.SoftDeleteFile(ctx, file.ID))
			items, err = s.ListDocumentTemplates(ctx, 3, 50, 0)
			require.NoError(t, err)
			require.Empty(t, items)
			_, err = s.CreateDocument(ctx, 3, domain.CreateDocumentRequest{Name: "Deleted", TemplateID: &template.ID})
			require.Equal(t, 404, apperrors.ToAppError(err).StatusCode)
			require.NoError(t, s.DeleteDocumentTemplate(ctx, 1, template.ID))
			got, err := s.repo.GetFileByID(ctx, copy.ID)
			require.NoError(t, err)
			require.Nil(t, got.DeletedAt)
		})
	}
	_, err = s.ListDocumentTemplates(ctx, 5, 50, 0)
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	setTestDriveQuota(t, ctx, db, 4, 1, 1)
	_, err = s.CreateDocument(ctx, 4, domain.CreateDocumentRequest{Name: "Too big", Format: "docx"})
	require.Error(t, err)
	// Migration rollback is part of the test, after lifecycle checks.
	_, err = db.Exec(ctx, strings.Split(string(migration), "-- +goose Down")[1])
	require.NoError(t, err)
}
