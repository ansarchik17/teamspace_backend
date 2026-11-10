package service

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

func TestDriveTagsSearchIntegration(t *testing.T) {
	ctx, db, s := newDrivePostgresTest(t)
	folder, err := s.CreateFolder(ctx, 1, nil, "Budget folder")
	require.NoError(t, err)
	f := quotaTestFile(1, 4)
	f.RealName = "report.txt"
	f.FolderID = &folder.ID
	require.NoError(t, s.repo.CreateFileWithInitialVersion(ctx, f, 1))
	require.NoError(t, s.GrantPermission(ctx, 1, domain.ResourceFolder, folder.ID, 2, domain.RoleEditor))
	require.NoError(t, s.GrantPermission(ctx, 1, domain.ResourceFolder, folder.ID, 3, domain.RoleViewer))
	tags, err := s.SetFileTags(ctx, 2, f.ID, []string{" Budget ", "budget", "2026", "100%_done"})
	require.NoError(t, err)
	require.Equal(t, []string{"Budget", "2026", "100%_done"}, tags)
	got, err := s.GetFileTags(ctx, 3, f.ID)
	require.NoError(t, err)
	require.Equal(t, tags, got)
	_, err = s.SetFileTags(ctx, 3, f.ID, []string{"denied"})
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	_, err = s.GetFileTags(ctx, 5, f.ID)
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	for _, q := range []string{"REPORT", "bUdGeT", "2026", "%_"} {
		items, err := s.SearchAndFilter(ctx, 3, q, "", 20, 0, nil, "")
		require.NoError(t, err)
		if q == "bUdGeT" {
			require.Len(t, items, 2)
		} else {
			require.Len(t, items, 1)
		}
		for _, item := range items {
			if item.File != nil {
				require.Equal(t, tags, item.File.Tags)
			}
		}
		hidden, err := s.SearchAndFilter(ctx, 5, q, "", 20, 0, nil, "")
		require.NoError(t, err)
		require.Empty(t, hidden)
	}
	items, err := s.SearchAndFilter(ctx, 3, "Budget", "folder", 20, 0, nil, "")
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.NotNil(t, items[0].Folder)
	items, err = s.SearchAndFilter(ctx, 3, "Budget", "", 1, 1, nil, "")
	require.NoError(t, err)
	require.Len(t, items, 1)
	_, err = db.Exec(ctx, `UPDATE files SET deleted_at=NOW() WHERE id=$1`, f.ID)
	require.NoError(t, err)
	items, err = s.SearchAndFilter(ctx, 3, "2026", "", 20, 0, nil, "")
	require.NoError(t, err)
	require.Empty(t, items)
}

func TestDriveDownloadAndDeleteAuditIntegration(t *testing.T) {
	ctx, db, s := newDrivePostgresTest(t)
	_, err := db.Exec(ctx, `ALTER TABLE users ADD COLUMN full_name_local TEXT`)
	require.NoError(t, err)
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/test/audit-object" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Length", "4")
		w.Header().Set("Last-Modified", time.Unix(1, 0).UTC().Format(http.TimeFormat))
		w.Header().Set("ETag", `"fixture"`)
		if r.Method != http.MethodHead {
			_, _ = io.WriteString(w, "test")
		}
	}))
	defer storage.Close()
	client, err := minio.New(strings.TrimPrefix(storage.URL, "http://"), &minio.Options{Region: "us-east-1"})
	require.NoError(t, err)
	s.fileSvc = &FileService{minioClient: client, bucket: "test", logger: zap.NewNop()}
	f := quotaTestFile(1, 4)
	f.ObjectName = "audit-object"
	require.NoError(t, s.repo.CreateFileWithInitialVersion(ctx, f, 1))
	require.NoError(t, s.GrantPermission(ctx, 1, domain.ResourceFile, f.ID, 3, domain.RoleViewer))
	count := func(want int) {
		t.Helper()
		var got int
		require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM drive_activity_logs WHERE resource_id=$1 AND action='downloaded'`, f.ID).Scan(&got))
		require.Equal(t, want, got)
	}
	_, _, _, _, err = s.DownloadFileByID(ctx, 5, f.ID)
	require.Error(t, err)
	count(0)
	reader, _, _, _, err := s.DownloadFileByID(ctx, 3, f.ID)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	count(1)
	reader, _, _, err = s.DownloadFileVersion(ctx, 3, f.ID, 1, true)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	count(1)
	reader, _, _, err = s.DownloadFileVersion(ctx, 3, f.ID, 1)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	count(2)
	link, err := s.CreatePublicLink(ctx, 1, "file", f.ID, "viewer", time.Hour)
	require.NoError(t, err)
	reader, _, _, _, err = s.DownloadPublicFile(ctx, link.Token)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	count(3)
	entries, total, err := s.repo.ListDriveActivities(ctx, domain.DriveActivityListFilter{ViewerUserID: 1, Limit: 20})
	require.NoError(t, err)
	require.Equal(t, 3, total)
	require.Equal(t, int64(0), entries[0].ActorID)
	require.Equal(t, "Anonymous", entries[0].ActorFullName)
	require.Contains(t, string(entries[0].Metadata), `"public_link": true`)
	// Never issue an unlogged download if the audit store is unavailable.
	_, err = db.Exec(ctx, `ALTER TABLE drive_activity_logs RENAME TO unavailable_activity_logs`)
	require.NoError(t, err)
	reader, _, _, _, err = s.DownloadFileByID(ctx, 3, f.ID)
	require.Error(t, err)
	require.Nil(t, reader)
	_, err = db.Exec(ctx, `ALTER TABLE unavailable_activity_logs RENAME TO drive_activity_logs`)
	require.NoError(t, err)
	count(3)
	_, err = db.Exec(ctx, `UPDATE files SET object_name='missing' WHERE id=$1`, f.ID)
	require.NoError(t, err)
	_, _, _, _, err = s.DownloadFileByID(ctx, 3, f.ID)
	require.Error(t, err)
	count(3)
	_, err = s.DeleteFile(ctx, 3, f.ID)
	require.Error(t, err)
	_, err = s.DeleteFile(ctx, 1, f.ID)
	require.NoError(t, err)
	entries, total, err = s.repo.ListDriveActivities(ctx, domain.DriveActivityListFilter{ViewerUserID: 1, Limit: 20})
	require.NoError(t, err)
	require.Equal(t, 4, total)
	require.Equal(t, domain.DriveActivityActionDeleted, entries[0].Action)
	require.Equal(t, f.RealName, entries[0].ResourceName)
	entries, _, err = s.repo.ListDriveActivities(ctx, domain.DriveActivityListFilter{ViewerUserID: 5, Limit: 20})
	require.NoError(t, err)
	require.Empty(t, entries)
}
