package service

import (
	"bytes"
	"github.com/west6ide/workspace-backend/internal/repository"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
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

func TestOfficeDriveSynchronizationIntegration(t *testing.T) {
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
	file, err := s.CreateDocument(ctx, 1, domain.CreateDocumentRequest{Name: "Original", Format: "docx"})
	require.NoError(t, err)
	require.NoError(t, s.GrantPermission(ctx, 1, domain.ResourceFile, file.ID, 3, domain.RoleEditor))
	cfg, err := s.BuildOnlyOfficeConfig(ctx, 3, file.ID, "edit", "secret", "https://example.com")
	require.NoError(t, err)
	key := cfg.DocumentKey
	downloadURL, err := url.Parse(cfg.DocumentURL)
	require.NoError(t, err)
	token := downloadURL.Query().Get("token")
	mu.Lock()
	original := append([]byte(nil), objects["/test/"+file.ObjectName]...)
	mu.Unlock()
	// The document server has independent URLs for saved snapshots.
	snapshots := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("saved" + r.URL.Path)) }))
	defer snapshots.Close()
	callback := OnlyOfficeCallback{Status: 6, Key: key, URL: snapshots.URL + "/first", FileType: "docx"}
	require.NoError(t, s.SaveOnlyOfficeCallback(ctx, 3, file.ID, callback))
	require.NoError(t, s.SaveOnlyOfficeCallback(ctx, 3, file.ID, callback))
	meta, err := s.repo.GetFileByID(ctx, file.ID)
	require.NoError(t, err)
	require.Equal(t, 2, meta.Version)
	require.Equal(t, key, *meta.Key)
	cfg, err = s.BuildOnlyOfficeConfig(ctx, 3, file.ID, "edit", "secret", "https://example.com")
	require.NoError(t, err)
	require.Equal(t, key, cfg.DocumentKey)
	// Opening URLs remain tied to the revision selected at opening.
	reader, _, _, _, err := s.DownloadFileForOnlyOffice(ctx, file.ID, token, "secret")
	require.NoError(t, err)
	data, err := io.ReadAll(reader.(io.Reader))
	require.NoError(t, err)
	reader.(io.Closer).Close()
	require.Equal(t, original, data)
	folder, err := s.CreateFolder(ctx, 1, nil, "Moved")
	require.NoError(t, err)
	name := "Renamed.docx"
	require.NoError(t, s.UpdateFile(ctx, 1, file.ID, &name, &folder.ID))
	callback.URL = snapshots.URL + "/second"
	require.NoError(t, s.SaveOnlyOfficeCallback(ctx, 3, file.ID, callback))
	callback.Status = 2
	callback.URL = snapshots.URL + "/final"
	require.NoError(t, s.SaveOnlyOfficeCallback(ctx, 3, file.ID, callback))
	require.NoError(t, s.SaveOnlyOfficeCallback(ctx, 3, file.ID, callback))
	meta, err = s.repo.GetFileByID(ctx, file.ID)
	require.NoError(t, err)
	require.Equal(t, 4, meta.Version)
	require.Equal(t, onlyOfficeDocumentKey(file.ID, 4), *meta.Key)
	require.Equal(t, name, meta.RealName)
	require.Equal(t, &folder.ID, meta.FolderID)
	require.Equal(t, int64(1), meta.OwnerID)
	versions, err := s.repo.ListFileVersions(ctx, file.ID, 20, 0)
	require.NoError(t, err)
	require.Len(t, versions, 4)
	var edits int
	require.NoError(t, db.QueryRow(ctx, `SELECT COUNT(*) FROM drive_activity_logs WHERE resource_id=$1 AND action='edited'`, file.ID).Scan(&edits))
	require.Equal(t, 3, edits)
	callback.URL = snapshots.URL + "/late"
	require.Equal(t, 409, apperrors.ToAppError(s.SaveOnlyOfficeCallback(ctx, 3, file.ID, callback)).StatusCode)
	// External version replacement during network IO must win, without an orphan upload.
	mu.Lock()
	before := len(objects)
	mu.Unlock()
	external := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := s.repo.AppendFileVersion(ctx, file.ID, file.ObjectName, file.Size, 1, 4)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Write([]byte("stale editor content"))
	}))
	defer external.Close()
	callback.Key = *meta.Key
	callback.URL = external.URL
	require.Equal(t, 409, apperrors.ToAppError(s.SaveOnlyOfficeCallback(ctx, 3, file.ID, callback)).StatusCode)
	mu.Lock()
	after := len(objects)
	mu.Unlock()
	require.Equal(t, before, after)
	meta, err = s.repo.GetFileByID(ctx, file.ID)
	require.NoError(t, err)
	require.Equal(t, 5, meta.Version)
	require.NoError(t, s.RevokePermission(ctx, 1, domain.ResourceFile, file.ID, 3))
	callback.Key = *meta.Key
	callback.URL = snapshots.URL + "/revoked"
	require.Equal(t, 403, apperrors.ToAppError(s.SaveOnlyOfficeCallback(ctx, 3, file.ID, callback)).StatusCode)
	require.NoError(t, s.repo.SoftDeleteFile(ctx, file.ID))
	require.Error(t, s.SaveOnlyOfficeCallback(ctx, 1, file.ID, callback))
}

func TestOfficeReceiptConcurrencyIntegration(t *testing.T) {
	ctx, _, s := newDrivePostgresTest(t)
	file := quotaTestFile(1, 1)
	require.NoError(t, s.repo.CreateFileWithInitialVersion(ctx, file, 1))
	key := onlyOfficeDocumentKey(file.ID, 1)
	var wg sync.WaitGroup
	applied := make(chan bool, 2)
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, ok, err := s.repo.AppendOfficeVersion(ctx, file.ID, uuid.NewString(), 1, 1, 1, key, "same-event", false)
			applied <- ok
			errs <- err
		}()
	}
	wg.Wait()
	close(applied)
	close(errs)
	count := 0
	for ok := range applied {
		if ok {
			count++
		}
	}
	require.Equal(t, 1, count)
	for err := range errs {
		require.NoError(t, err)
	}
	_, _, err := s.repo.AppendOfficeVersion(ctx, file.ID, "stale", 1, 1, 1, key, "different-event", false)
	require.ErrorIs(t, err, repository.ErrDriveVersionConflict)
	meta, err := s.repo.GetFileByID(ctx, file.ID)
	require.NoError(t, err)
	require.Equal(t, 2, meta.Version)
}
