package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func setTestDriveQuota(t *testing.T, ctx context.Context, db *pgxpool.Pool, userID, quota, upload int64) {
	t.Helper()
	_, err := db.Exec(ctx, `INSERT INTO drive_storage_accounts(user_id,quota_bytes,max_upload_bytes) VALUES($1,$2,$3)
 ON CONFLICT(user_id) DO UPDATE SET quota_bytes=EXCLUDED.quota_bytes,max_upload_bytes=EXCLUDED.max_upload_bytes`, userID, quota, upload)
	require.NoError(t, err)
}

func quotaTestFile(owner, size int64) *domain.DriveFile {
	return &domain.DriveFile{ID: uuid.New(), OwnerID: owner, RealName: uuid.NewString() + ".txt", ObjectName: uuid.NewString(), Size: size, MimeType: "text/plain", Version: 1, Scope: "drive", CreatedAt: time.Now(), UpdatedAt: time.Now()}
}

func TestDriveStorageAccountingIntegration(t *testing.T) {
	ctx, db, s := newDrivePostgresTest(t)
	_, err := db.Exec(ctx, `CREATE TABLE drive_favorites(user_id BIGINT,resource_type TEXT,resource_id UUID,created_at TIMESTAMPTZ DEFAULT NOW())`)
	require.NoError(t, err)
	storage, err := s.GetDriveStorage(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, int64(10<<30), storage.QuotaBytes)
	require.Equal(t, storage.QuotaBytes, storage.FreeBytes)
	setTestDriveQuota(t, ctx, db, 1, 100, 60)
	f := quotaTestFile(1, 30)
	require.NoError(t, s.repo.CreateFileWithInitialVersion(ctx, f, 1))
	version, err := s.repo.AppendFileVersion(ctx, f.ID, "second", 20, 1, 1)
	require.NoError(t, err)
	require.Equal(t, 2, version)
	storage, err = s.GetDriveStorage(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, int64(50), storage.UsedBytes)
	require.Equal(t, int64(20), storage.ActiveBytes)
	require.Equal(t, int64(30), storage.VersionBytes)
	setTestDriveQuota(t, ctx, db, 1, 50, 60)
	_, err = s.RestoreFileVersion(ctx, 1, f.ID, 1, 2)
	require.NoError(t, err)
	storage, err = s.GetDriveStorage(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, int64(50), storage.UsedBytes)
	require.Equal(t, int64(30), storage.ActiveBytes)
	require.Equal(t, int64(20), storage.VersionBytes)
	require.NoError(t, s.repo.SoftDeleteFile(ctx, f.ID))
	storage, err = s.GetDriveStorage(ctx, 1)
	require.NoError(t, err)
	require.Zero(t, storage.ActiveBytes)
	require.Zero(t, storage.VersionBytes)
	require.Equal(t, int64(50), storage.TrashBytes)
	require.NoError(t, s.RestoreTrashItem(ctx, 1, domain.ResourceFile, f.ID))
	// Another module and files shared by others do not consume this owner's quota.
	other := quotaTestFile(2, 90)
	require.NoError(t, s.repo.CreateFileWithInitialVersion(ctx, other, 2))
	require.NoError(t, s.GrantPermission(ctx, 2, domain.ResourceFile, other.ID, 1, domain.RoleViewer))
	external := quotaTestFile(1, 1000)
	external.Scope = "kanban"
	require.NoError(t, s.repo.CreateFileWithInitialVersion(ctx, external, 1))
	storage, err = s.GetDriveStorage(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, int64(50), storage.UsedBytes)
	summary, err := s.GetWorkspaceSummary(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, storage.UsedBytes, summary.UsedBytes)
	require.Equal(t, storage.UsedBytes, summary.Storage.UsedBytes)
	require.NoError(t, s.repo.SoftDeleteFile(ctx, f.ID))
	_, err = db.Exec(ctx, `UPDATE files SET deleted_at=NOW()-INTERVAL '720 hours' WHERE id=$1`, f.ID)
	require.NoError(t, err)
	storage, err = s.GetDriveStorage(ctx, 1)
	require.NoError(t, err)
	require.Zero(t, storage.UsedBytes)
	require.Equal(t, int64(50), storage.FreeBytes)
}

func TestDriveStorageGuardsIntegration(t *testing.T) {
	ctx, db, s := newDrivePostgresTest(t)
	setTestDriveQuota(t, ctx, db, 1, 100, 100)
	f := quotaTestFile(1, 60)
	require.NoError(t, s.repo.CreateFileWithInitialVersion(ctx, f, 1))
	// Preflight rejects before touching the absent FileService.
	_, err := s.UploadFileToFolder(ctx, 1, nil, "no-room.txt", 41, "text/plain", nil)
	require.Equal(t, 507, apperrors.ToAppError(err).StatusCode)
	_, err = s.UploadFileToFolder(ctx, 1, nil, "too-big.txt", 101, "text/plain", nil)
	require.Equal(t, 413, apperrors.ToAppError(err).StatusCode)
	require.NoError(t, s.GrantPermission(ctx, 1, domain.ResourceFile, f.ID, 2, domain.RoleEditor))
	_, err = s.UploadFileVersion(ctx, 2, f.ID, "new.txt", 41, nil, 1)
	require.Equal(t, 507, apperrors.ToAppError(err).StatusCode, "editor must use file owner's quota")
	// The transaction-level guard also blocks callers bypassing preflight.
	tooLarge := quotaTestFile(1, 41)
	err = s.repo.CreateFileWithInitialVersion(ctx, tooLarge, 1)
	require.Equal(t, 507, apperrors.ToAppError(err).StatusCode)
	meta, err := s.repo.GetFileByID(ctx, tooLarge.ID)
	require.Error(t, err)
	require.Nil(t, meta)
	_, err = s.repo.AppendFileVersion(ctx, f.ID, "revision-blocked", 41, 1, 1)
	require.Equal(t, 507, apperrors.ToAppError(err).StatusCode)
	meta, err = s.repo.GetFileByID(ctx, f.ID)
	require.NoError(t, err)
	require.Equal(t, 1, meta.Version)
	// Exact boundary is accepted; zero free bytes disables further positive uploads.
	exact := quotaTestFile(1, 40)
	require.NoError(t, s.repo.CreateFileWithInitialVersion(ctx, exact, 1))
	storage, err := s.GetDriveStorage(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, int64(100), storage.UsedBytes)
	require.Zero(t, storage.FreeBytes)
	require.False(t, storage.CanUpload)
	// Lowered quotas do not prevent renaming or deletion of existing files.
	setTestDriveQuota(t, ctx, db, 1, 10, 100)
	name := "renamed.txt"
	require.NoError(t, s.repo.UpdateFile(ctx, f.ID, &name, nil))
	require.NoError(t, s.repo.SoftDeleteFile(ctx, f.ID))
	storage, err = s.GetDriveStorage(ctx, 1)
	require.NoError(t, err)
	require.True(t, storage.QuotaExceeded)
	require.Zero(t, storage.FreeBytes)
	// A user cannot inspect storage without authentication.
	_, err = s.GetDriveStorage(ctx, 0)
	require.Equal(t, 401, apperrors.ToAppError(err).StatusCode)
}

func TestDriveStorageCopyTransferIntegration(t *testing.T) {
	ctx, db, s := newDrivePostgresTest(t)
	setTestDriveQuota(t, ctx, db, 1, 100, 100)
	setTestDriveQuota(t, ctx, db, 2, 30, 100)
	folder, err := s.CreateFolder(ctx, 1, nil, "source")
	require.NoError(t, err)
	f := quotaTestFile(1, 40)
	f.FolderID = &folder.ID
	require.NoError(t, s.repo.CreateFileWithInitialVersion(ctx, f, 1))
	require.NoError(t, s.GrantPermission(ctx, 1, domain.ResourceFolder, folder.ID, 2, domain.RoleViewer))
	_, err = s.CopyItem(ctx, 2, domain.ResourceFile, f.ID, nil)
	require.Equal(t, 507, apperrors.ToAppError(err).StatusCode)
	_, err = s.CopyItem(ctx, 2, domain.ResourceFolder, folder.ID, nil)
	require.Equal(t, 507, apperrors.ToAppError(err).StatusCode)
	var count int
	require.NoError(t, db.QueryRow(ctx, `SELECT COUNT(*) FROM folders WHERE owner_id=2`).Scan(&count))
	require.Zero(t, count, "failed recursive copy must roll back folders")
	err = s.TransferOwnership(ctx, 1, domain.ResourceFile, f.ID, 2)
	require.Equal(t, 507, apperrors.ToAppError(err).StatusCode)
	meta, err := s.repo.GetFileByID(ctx, f.ID)
	require.NoError(t, err)
	require.Equal(t, int64(1), meta.OwnerID)
	setTestDriveQuota(t, ctx, db, 2, 80, 100)
	copied, err := s.CopyItem(ctx, 2, domain.ResourceFile, f.ID, nil)
	require.NoError(t, err)
	require.NotNil(t, copied)
	require.NoError(t, s.TransferOwnership(ctx, 1, domain.ResourceFile, f.ID, 2))
	storage, err := s.GetDriveStorage(ctx, 2)
	require.NoError(t, err)
	require.Equal(t, int64(80), storage.UsedBytes, "copies count separately even with a shared storage object")
	storage, err = s.GetDriveStorage(ctx, 1)
	require.NoError(t, err)
	require.Zero(t, storage.UsedBytes)
}

func TestDriveStorageConcurrentIntegration(t *testing.T) {
	for _, copies := range []bool{false, true} {
		t.Run(fmt.Sprintf("copies=%v", copies), func(t *testing.T) {
			ctx, db, s := newDrivePostgresTest(t)
			setTestDriveQuota(t, ctx, db, 1, 100, 100)
			source := quotaTestFile(2, 60)
			require.NoError(t, s.repo.CreateFileWithInitialVersion(ctx, source, 2))
			ready := make(chan struct{})
			errs := make(chan error, 8)
			var wg sync.WaitGroup
			for range 8 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-ready
					var err error
					if copies {
						_, err = s.repo.CopyFile(ctx, source.ID, nil, 1)
					} else {
						err = s.repo.CreateFileWithInitialVersion(ctx, quotaTestFile(1, 60), 1)
					}
					errs <- err
				}()
			}
			close(ready)
			wg.Wait()
			close(errs)
			success := 0
			for err := range errs {
				if err == nil {
					success++
				} else {
					status := apperrors.ToAppError(err).StatusCode
					require.Contains(t, []int{507, 409}, status, err.Error())
				}
			}
			require.Equal(t, 1, success)
			storage, err := s.GetDriveStorage(ctx, 1)
			require.NoError(t, err)
			require.Equal(t, int64(60), storage.UsedBytes)
		})
	}
}

func TestDriveStorageOfficeIntegration(t *testing.T) {
	ctx, db, s := newDrivePostgresTest(t)
	setTestDriveQuota(t, ctx, db, 1, 5, 100)
	f := quotaTestFile(1, 4)
	require.NoError(t, s.repo.CreateFileWithInitialVersion(ctx, f, 1))
	require.NoError(t, s.GrantPermission(ctx, 1, domain.ResourceFile, f.ID, 2, domain.RoleEditor))
	for _, chunked := range []bool{false, true} {
		t.Run(fmt.Sprintf("chunked=%v", chunked), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if chunked {
					w.(http.Flusher).Flush()
				} else {
					w.Header().Set("Content-Length", "2")
				}
				_, _ = w.Write([]byte("ab"))
			}))
			defer server.Close()
			err := s.SaveOnlyOfficeCallback(ctx, 2, f.ID, OnlyOfficeCallback{Status: 2, URL: server.URL, Key: onlyOfficeDocumentKey(f.ID, 1)})
			require.Equal(t, 507, apperrors.ToAppError(err).StatusCode)
			meta, err := s.repo.GetFileByID(ctx, f.ID)
			require.NoError(t, err)
			require.Equal(t, 1, meta.Version)
		})
	}
}
