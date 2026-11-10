package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/minio/minio-go/v7"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

func TestOfficeRecoveryIntegration(t *testing.T) {
	ctx, db, s := newDrivePostgresTest(t)
	var mu sync.Mutex
	objects := map[string][]byte{}
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
				http.Error(w, "read", 500)
				return
			}
			objects[r.URL.Path] = data
			w.Header().Set("ETag", `"fixture"`)
			return
		}
		if r.Method == http.MethodDelete {
			delete(objects, r.URL.Path)
			w.WriteHeader(204)
			return
		}
		http.NotFound(w, r)
	}))
	defer storage.Close()
	client, err := minio.New(strings.TrimPrefix(storage.URL, "http://"), &minio.Options{Region: "us-east-1"})
	require.NoError(t, err)
	s.fileSvc = &FileService{minioClient: client, bucket: "test", logger: zap.NewNop()}
	file, err := s.CreateDocument(ctx, 1, domain.CreateDocumentRequest{Name: "Recovery", Format: "docx"})
	require.NoError(t, err)
	require.NoError(t, s.GrantPermission(ctx, 1, domain.ResourceFile, file.ID, 3, domain.RoleViewer))
	state, err := s.OfficeSaveState(ctx, 3, file.ID)
	require.NoError(t, err)
	require.Equal(t, 1, state.Version)
	require.Nil(t, state.FailureKind)
	_, err = s.OfficeSaveState(ctx, 5, file.ID)
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	key := activeOnlyOfficeKey(file)
	var unavailable atomic.Bool
	unavailable.Store(true)
	snapshots := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if unavailable.Load() {
			http.Error(w, "retry later", 503)
			return
		}
		w.Write([]byte("checkpoint with comments and revisions"))
	}))
	defer snapshots.Close()
	cb := OnlyOfficeCallback{Status: 6, Key: key, URL: snapshots.URL + "/snapshot", FileType: "docx"}
	require.Error(t, s.SaveOnlyOfficeCallback(ctx, 1, file.ID, cb))
	state, err = s.OfficeSaveState(ctx, 1, file.ID)
	require.NoError(t, err)
	require.Equal(t, 1, state.Version)
	require.Equal(t, "save_failed", *state.FailureKind)
	unavailable.Store(false)
	require.NoError(t, s.SaveOnlyOfficeCallback(ctx, 1, file.ID, cb))
	require.NoError(t, s.SaveOnlyOfficeCallback(ctx, 1, file.ID, cb))
	state, err = s.OfficeSaveState(ctx, 1, file.ID)
	require.NoError(t, err)
	require.Equal(t, 2, state.Version)
	require.Nil(t, state.FailureKind)
	// An obsolete error (including one that finishes after a successful retry) is invisible.
	require.NoError(t, s.repo.RecordOfficeSaveFailure(ctx, file.ID, key, 1, "save_failed"))
	state, err = s.OfficeSaveState(ctx, 1, file.ID)
	require.NoError(t, err)
	require.Nil(t, state.FailureKind)
	require.NoError(t, s.RecordOfficeServerError(ctx, 1, file.ID, key))
	state, err = s.OfficeSaveState(ctx, 1, file.ID)
	require.NoError(t, err)
	require.Equal(t, "document_server_error", *state.FailureKind)
	// A new service instance relies only on durable state, as after an API restart.
	restarted := &DriveService{repo: s.repo, recentRepo: s.recentRepo, fileSvc: s.fileSvc, publicURL: s.publicURL, notifService: s.notifService, gamification: s.gamification, driveSearch: s.driveSearch, logger: s.logger}
	state, err = restarted.OfficeSaveState(ctx, 1, file.ID)
	require.NoError(t, err)
	require.NotNil(t, state.FailedAt)
	_, err = s.RecoverOfficeSession(ctx, 3, file.ID, 2)
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	_, err = s.RecoverOfficeSession(ctx, 1, file.ID, 0)
	require.Equal(t, 400, apperrors.ToAppError(err).StatusCode)
	next, err := restarted.RecoverOfficeSession(ctx, 1, file.ID, 2)
	require.NoError(t, err)
	require.Equal(t, 3, next)
	meta, err := s.repo.GetFileByID(ctx, file.ID)
	require.NoError(t, err)
	require.NotEqual(t, key, activeOnlyOfficeKey(meta))
	mu.Lock()
	data := string(objects["/test/"+meta.ObjectName])
	mu.Unlock()
	require.Equal(t, "checkpoint with comments and revisions", data)
	state, err = s.OfficeSaveState(ctx, 1, file.ID)
	require.NoError(t, err)
	require.Nil(t, state.FailureKind)
	cb.URL = snapshots.URL + "/late"
	require.Equal(t, 409, apperrors.ToAppError(s.SaveOnlyOfficeCallback(ctx, 1, file.ID, cb)).StatusCode)
	require.Equal(t, 409, apperrors.ToAppError(s.RecordOfficeServerError(ctx, 1, file.ID, key)).StatusCode)
	_, err = s.RecoverOfficeSession(ctx, 1, file.ID, 2)
	require.Equal(t, 409, apperrors.ToAppError(err).StatusCode)
	// Restoring an old snapshot retains the recovered version and rejects old callbacks.
	next, err = s.RestoreFileVersion(ctx, 1, file.ID, 1, 3)
	require.NoError(t, err)
	require.Equal(t, 4, next)
	versions, err := s.ListFileVersions(ctx, 3, file.ID, 20, 0)
	require.NoError(t, err)
	require.Len(t, versions, 4)
	require.Equal(t, file.ObjectName, versions[0].ObjectName)
	require.True(t, versions[0].IsCurrent)
	// Two recoveries cannot both succeed against one checkpoint.
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := s.RecoverOfficeSession(ctx, 1, file.ID, 4); results <- err }()
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else {
			require.Equal(t, 409, apperrors.ToAppError(err).StatusCode)
		}
	}
	require.Equal(t, 1, successes)
	// A failed history insert leaves the checkpoint and its session key unchanged.
	before, err := s.repo.GetFileByID(ctx, file.ID)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `CREATE FUNCTION fail_recovery_revision() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'revision unavailable'; END $$; CREATE TRIGGER fail_recovery_revision BEFORE INSERT ON file_versions FOR EACH ROW EXECUTE FUNCTION fail_recovery_revision()`)
	require.NoError(t, err)
	_, err = s.RecoverOfficeSession(ctx, 1, file.ID, 5)
	require.Error(t, err)
	after, err := s.repo.GetFileByID(ctx, file.ID)
	require.NoError(t, err)
	require.Equal(t, before.Version, after.Version)
	require.Equal(t, before.Key, after.Key)
	// An already cancelled request never mutates the saved checkpoint.
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	require.Error(t, s.SaveOnlyOfficeCallback(cancelled, 1, file.ID, OnlyOfficeCallback{Status: 6, Key: activeOnlyOfficeKey(after), URL: snapshots.URL}))
	require.NoError(t, s.repo.SoftDeleteFile(ctx, file.ID))
	_, err = s.RecoverOfficeSession(ctx, 1, file.ID, 5)
	require.Equal(t, 404, apperrors.ToAppError(err).StatusCode)
}
