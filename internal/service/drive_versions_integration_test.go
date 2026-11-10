package service

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestDriveVersionsIntegration(t *testing.T) {
	ctx, db, s := newDrivePostgresTest(t)
	folder, err := s.CreateFolder(ctx, 1, nil, "versions")
	require.NoError(t, err)
	f := &domain.DriveFile{ID: uuid.New(), OwnerID: 1, FolderID: &folder.ID, RealName: "report.txt", ObjectName: "revision-one", ContentHash: "old-hash", Size: 12, MimeType: "text/plain", Version: 1, Scope: "drive", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	require.NoError(t, s.repo.CreateFileWithInitialVersion(ctx, f, 1))
	require.NoError(t, s.GrantPermission(ctx, 1, domain.ResourceFolder, folder.ID, 2, domain.RoleEditor))
	require.NoError(t, s.GrantPermission(ctx, 1, domain.ResourceFolder, folder.ID, 3, domain.RoleViewer))
	version, err := s.repo.UpdateFileContentNewVersion(ctx, f.ID, "revision-two", 24, 2, "ignored-key")
	require.NoError(t, err)
	require.Equal(t, 2, version)
	versions, err := s.ListFileVersions(ctx, 3, f.ID, 20, 0)
	require.NoError(t, err)
	require.Len(t, versions, 2)
	require.True(t, versions[0].IsCurrent)
	require.Equal(t, 2, versions[0].Version)
	require.Equal(t, int64(2), versions[0].CreatedBy)
	encoded, err := json.Marshal(versions)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "revision-two")
	_, err = s.ListFileVersions(ctx, 5, f.ID, 20, 0)
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	_, err = s.RestoreFileVersion(ctx, 3, f.ID, 1, 2)
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	_, err = s.RestoreFileVersion(ctx, 2, f.ID, 99, 2)
	require.Equal(t, 404, apperrors.ToAppError(err).StatusCode)
	_, err = s.RestoreFileVersion(ctx, 2, f.ID, 1, 1)
	require.Equal(t, 409, apperrors.ToAppError(err).StatusCode)
	version, err = s.RestoreFileVersion(ctx, 2, f.ID, 1, 2)
	require.NoError(t, err)
	require.Equal(t, 3, version)
	meta, err := s.repo.GetFileByID(ctx, f.ID)
	require.NoError(t, err)
	require.Equal(t, "revision-one", meta.ObjectName)
	require.Equal(t, int64(12), meta.Size)
	require.Empty(t, meta.ContentHash)
	require.Equal(t, onlyOfficeDocumentKey(f.ID, 3), *meta.Key)
	versions, err = s.ListFileVersions(ctx, 1, f.ID, 1, 1)
	require.NoError(t, err)
	require.Len(t, versions, 1)
	require.Equal(t, 2, versions[0].Version)
	// Concurrent restore requests must not overwrite one another.
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := s.RestoreFileVersion(ctx, 2, f.ID, 2, 3); errs <- err }()
	}
	wg.Wait()
	close(errs)
	successes, conflicts := 0, 0
	for err := range errs {
		if err == nil {
			successes++
		} else {
			require.Equal(t, 409, apperrors.ToAppError(err).StatusCode)
			conflicts++
		}
	}
	require.Equal(t, 1, successes)
	require.Equal(t, 1, conflicts)
	// A failed revision INSERT rolls back the current file update.
	_, err = db.Exec(ctx, `CREATE FUNCTION fail_revision() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'revision unavailable'; END $$; CREATE TRIGGER fail_revision BEFORE INSERT ON file_versions FOR EACH ROW EXECUTE FUNCTION fail_revision()`)
	require.NoError(t, err)
	_, err = s.RestoreFileVersion(ctx, 1, f.ID, 1, 4)
	require.Error(t, err)
	meta, err = s.repo.GetFileByID(ctx, f.ID)
	require.NoError(t, err)
	require.Equal(t, 4, meta.Version)
	require.Equal(t, "revision-two", meta.ObjectName)
	_, err = db.Exec(ctx, `DROP TRIGGER fail_revision ON file_versions`)
	require.NoError(t, err)
	require.NoError(t, s.RevokePermission(ctx, 1, domain.ResourceFolder, folder.ID, 2))
	_, err = s.RestoreFileVersion(ctx, 2, f.ID, 1, 4)
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	_, _, _, err = s.DownloadFileVersion(ctx, 2, f.ID, 1)
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	require.NoError(t, s.repo.SoftDeleteFile(ctx, f.ID))
	_, err = s.ListFileVersions(ctx, 1, f.ID, 20, 0)
	require.Equal(t, 404, apperrors.ToAppError(err).StatusCode)
	require.NoError(t, s.RestoreTrashItem(ctx, 1, domain.ResourceFile, f.ID))
	versions, err = s.ListFileVersions(ctx, 1, f.ID, 20, 0)
	require.NoError(t, err)
	require.Len(t, versions, 4)
}

func TestDriveTrashRetentionIntegration(t *testing.T) {
	ctx, db, s := newDrivePostgresTest(t)
	// Summary/favorites use the production favorites schema.
	_, err := db.Exec(ctx, `CREATE TABLE drive_favorites(user_id BIGINT,resource_type TEXT,resource_id UUID,created_at TIMESTAMPTZ DEFAULT NOW())`)
	require.NoError(t, err)
	root, err := s.CreateFolder(ctx, 1, nil, "trash-root")
	require.NoError(t, err)
	child, err := s.CreateFolder(ctx, 1, &root.ID, "child")
	require.NoError(t, err)
	earlier, err := s.CreateFolder(ctx, 1, &root.ID, "earlier")
	require.NoError(t, err)
	newFile := func(owner int64, folder *uuid.UUID, name string) *domain.DriveFile {
		f := &domain.DriveFile{ID: uuid.New(), OwnerID: owner, FolderID: folder, RealName: name, ObjectName: uuid.NewString(), Size: 1, MimeType: "text/plain", Version: 1, Scope: "drive", CreatedAt: time.Now(), UpdatedAt: time.Now()}
		require.NoError(t, s.repo.CreateFileWithInitialVersion(ctx, f, owner))
		return f
	}
	own := newFile(1, &child.ID, "own.txt")
	shared := newFile(2, &child.ID, "shared.txt")
	old := newFile(1, &child.ID, "old.txt")
	require.NoError(t, s.repo.SoftDeleteFile(ctx, old.ID))
	require.NoError(t, s.repo.SoftDeleteFolderTree(ctx, earlier.ID))
	_, err = db.Exec(ctx, `UPDATE files SET deleted_at=NOW()-INTERVAL '1 day' WHERE id=$1`, old.ID)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `UPDATE folders SET deleted_at=NOW()-INTERVAL '1 day' WHERE id=$1`, earlier.ID)
	require.NoError(t, err)
	require.NoError(t, s.repo.SoftDeleteFolderTree(ctx, root.ID))
	err = s.RestoreTrashItem(ctx, 1, domain.ResourceFile, own.ID)
	require.Equal(t, 409, apperrors.ToAppError(err).StatusCode)
	err = s.RestoreTrashItem(ctx, 2, domain.ResourceFolder, root.ID)
	require.Equal(t, 404, apperrors.ToAppError(err).StatusCode)
	items, err := s.ListTrash(ctx, 1, 100, 0)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, items[0].DeletedAt.Add(domain.DriveTrashRetention), items[0].ExpiresAt)
	require.NoError(t, s.RestoreTrashItem(ctx, 1, domain.ResourceFolder, root.ID))
	for _, f := range []*domain.DriveFile{own, shared} {
		meta, err := s.repo.GetFileByID(ctx, f.ID)
		require.NoError(t, err)
		require.Nil(t, meta.DeletedAt)
	}
	meta, err := s.repo.GetFileByID(ctx, old.ID)
	require.NoError(t, err)
	require.NotNil(t, meta.DeletedAt)
	previous, err := s.repo.GetFolderByID(ctx, earlier.ID)
	require.NoError(t, err)
	require.NotNil(t, previous.DeletedAt)
	for _, age := range []string{"719 hours", "720 hours", "721 hours"} {
		f := newFile(1, nil, age+".txt")
		_, err = db.Exec(ctx, `UPDATE files SET deleted_at=NOW()-$2::interval WHERE id=$1`, f.ID, age)
		require.NoError(t, err)
		err = s.RestoreTrashItem(ctx, 1, domain.ResourceFile, f.ID)
		if age == "719 hours" {
			require.NoError(t, err)
		} else {
			require.Equal(t, 409, apperrors.ToAppError(err).StatusCode)
			require.Error(t, s.repo.RestoreFile(ctx, 1, f.ID))
		}
	}
	_, err = db.Exec(ctx, `UPDATE folders SET deleted_at=NOW()-INTERVAL '720 hours' WHERE id=$1`, earlier.ID)
	require.NoError(t, err)
	err = s.RestoreTrashItem(ctx, 1, domain.ResourceFolder, earlier.ID)
	require.Equal(t, 409, apperrors.ToAppError(err).StatusCode)
	require.Error(t, s.repo.RestoreFolderTree(ctx, 1, earlier.ID))
	items, err = s.ListTrash(ctx, 1, 100, 0)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, old.ID, items[0].File.ID)
	summary, err := s.GetWorkspaceSummary(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, int64(1), summary.TrashCount)
}
