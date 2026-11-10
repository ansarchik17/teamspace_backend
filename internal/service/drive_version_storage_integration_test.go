package service

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
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

// Exercise actual FileService/S3 client streaming with a local storage fixture.
func TestDriveVersionStorageIntegration(t *testing.T) {
	ctx, _, s := newDrivePostgresTest(t)
	first := []byte("first revision\nПривет\x00\xff")
	second := []byte("new revision\nПривет ещё раз")
	var mu sync.Mutex
	objects := map[string][]byte{"/test/revision-one": first}
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
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
			objects[r.URL.Path] = data
			w.Header().Set("ETag", `"fixture"`)
			w.WriteHeader(200)
			return
		}
		if r.Method == http.MethodDelete {
			delete(objects, r.URL.Path)
			w.WriteHeader(204)
			return
		}
		data, ok := objects[r.URL.Path]
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
	f := &domain.DriveFile{ID: uuid.New(), OwnerID: 1, RealName: "file.txt", ObjectName: "revision-one", Size: int64(len(first)), MimeType: "text/plain", Version: 1, Scope: "drive", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	require.NoError(t, s.repo.CreateFileWithInitialVersion(ctx, f, 1))
	require.NoError(t, s.GrantPermission(ctx, 1, domain.ResourceFile, f.ID, 3, domain.RoleViewer))
	_, err = s.UploadFileVersion(ctx, 3, f.ID, "file.txt", int64(len(second)), bytes.NewReader(second), 1)
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	_, err = s.UploadFileVersion(ctx, 1, f.ID, "file.pdf", int64(len(second)), bytes.NewReader(second), 1)
	require.Equal(t, 400, apperrors.ToAppError(err).StatusCode)
	next, err := s.UploadFileVersion(ctx, 1, f.ID, "file.txt", int64(len(second)), bytes.NewReader(second), 1)
	require.NoError(t, err)
	require.Equal(t, 2, next)
	check := func(version int, want []byte) {
		reader, size, name, err := s.DownloadFileVersion(ctx, 3, f.ID, version)
		require.NoError(t, err)
		got, err := io.ReadAll(reader)
		require.NoError(t, err)
		require.NoError(t, reader.Close())
		require.Equal(t, want, got)
		require.Equal(t, int64(len(want)), size)
		require.Equal(t, "file.txt", name)
	}
	check(1, first)
	check(2, second)
	next, err = s.RestoreFileVersion(ctx, 1, f.ID, 1, 2)
	require.NoError(t, err)
	require.Equal(t, 3, next)
	check(1, first)
	check(2, second)
	check(3, first)
	_, _, _, err = s.DownloadFileVersion(ctx, 3, f.ID, 999)
	require.Equal(t, 404, apperrors.ToAppError(err).StatusCode)
	require.NoError(t, s.RevokePermission(ctx, 1, domain.ResourceFile, f.ID, 3))
	_, _, _, err = s.DownloadFileVersion(ctx, 3, f.ID, 1)
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
}
