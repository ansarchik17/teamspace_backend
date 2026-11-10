package service

import (
	"context"
	"io"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
)

func testDriveOperations(t *testing.T, ctx context.Context, db *pgxpool.Pool, s *DriveService, root, nested *domain.Folder, file *domain.DriveFile, payload []byte) {
	t.Helper()
	// Both legacy PUT and explicit move reject descendants without modifying the tree.
	err := s.UpdateFolder(ctx, 1, root.ID, nil, &nested.ID)
	require.Equal(t, 400, apperrors.ToAppError(err).StatusCode)
	_, err = s.MoveItem(ctx, 1, domain.ResourceFolder, root.ID, &nested.ID)
	require.Equal(t, 400, apperrors.ToAppError(err).StatusCode)
	_, err = s.CopyItem(ctx, 1, domain.ResourceFolder, root.ID, &nested.ID)
	require.Equal(t, 400, apperrors.ToAppError(err).StatusCode)
	target, err := s.CreateFolder(ctx, 1, nil, "Operation target")
	require.NoError(t, err)
	copied, err := s.CopyItem(ctx, 1, domain.ResourceFolder, nested.ID, &target.ID)
	require.NoError(t, err)
	folderCopy := copied.(*domain.Folder)
	copiedFiles, err := s.repo.ListFilesByFolder(ctx, &folderCopy.ID)
	require.NoError(t, err)
	require.Len(t, copiedFiles, 1)
	clone := copiedFiles[0]
	require.NotEqual(t, file.ID, clone.ID)
	require.NotEqual(t, *file.Key, *clone.Key)
	r, _, _, _, err := s.DownloadFileByID(ctx, 1, clone.ID)
	require.NoError(t, err)
	data, err := io.ReadAll(r)
	r.Close()
	require.NoError(t, err)
	require.Equal(t, payload, data)
	_, err = s.CopyItem(ctx, 1, domain.ResourceFolder, nested.ID, &target.ID)
	require.Equal(t, 409, apperrors.ToAppError(err).StatusCode)
	// A direct share of one file must not permit copying its entire parent tree.
	require.NoError(t, s.repo.UpsertPermission(ctx, &domain.Permission{ResourceType: domain.ResourceFile, ResourceID: file.ID, UserID: 4, Role: domain.RoleViewer, CreatedAt: file.CreatedAt}))
	_, err = s.CopyItem(ctx, 4, domain.ResourceFolder, nested.ID, nil)
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	name := "  Renamed.txt  "
	result, err := s.BatchItems(ctx, 1, domain.DriveBatchRequest{Action: "rename", Items: []domain.DriveBatchItem{{ResourceType: domain.ResourceFile, ResourceID: clone.ID, Name: &name}, {ResourceType: domain.ResourceFile, ResourceID: uuid.New(), Name: &name}}})
	require.NoError(t, err)
	require.Equal(t, 1, result.Succeeded)
	require.Equal(t, 1, result.Failed)
	require.Equal(t, 404, result.Results[1].Status)
	updated, err := s.repo.GetFileByID(ctx, clone.ID)
	require.NoError(t, err)
	require.Equal(t, "Renamed.txt", updated.RealName)
	result, err = s.BatchItems(ctx, 1, domain.DriveBatchRequest{Action: "move", Items: []domain.DriveBatchItem{{ResourceType: domain.ResourceFile, ResourceID: clone.ID}}})
	require.NoError(t, err)
	require.Equal(t, 1, result.Succeeded)
	updated, err = s.repo.GetFileByID(ctx, clone.ID)
	require.NoError(t, err)
	require.Nil(t, updated.FolderID)
	result, err = s.BatchItems(ctx, 3, domain.DriveBatchRequest{Action: "move", TargetParentID: &root.ID, Items: []domain.DriveBatchItem{{ResourceType: domain.ResourceFile, ResourceID: file.ID}}})
	require.NoError(t, err)
	require.Equal(t, 403, result.Results[0].Status)
	item := domain.DriveBatchItem{ResourceType: domain.ResourceFile, ResourceID: clone.ID, Name: &name}
	_, err = s.BatchItems(ctx, 1, domain.DriveBatchRequest{Action: "rename", Items: []domain.DriveBatchItem{item, item}})
	require.Equal(t, 400, apperrors.ToAppError(err).StatusCode)
	result, err = s.BatchItems(ctx, 1, domain.DriveBatchRequest{Action: "copy", TargetParentID: &target.ID, Items: []domain.DriveBatchItem{item}})
	require.NoError(t, err)
	require.Equal(t, 1, result.Succeeded)
	second := result.Results[0].Result.(*domain.DriveFile)
	result, err = s.BatchItems(ctx, 1, domain.DriveBatchRequest{Action: "delete", Items: []domain.DriveBatchItem{{ResourceType: domain.ResourceFile, ResourceID: second.ID}}})
	require.NoError(t, err)
	require.Equal(t, 1, result.Succeeded)
	// Recursive copy must roll back folders when a child version cannot be stored.
	rollbackTarget, err := s.CreateFolder(ctx, 1, nil, "Rollback target")
	require.NoError(t, err)
	_, err = db.Exec(ctx, `CREATE FUNCTION fail_copy_version() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'version unavailable'; END $$; CREATE TRIGGER fail_copy_version BEFORE INSERT ON file_versions FOR EACH ROW EXECUTE FUNCTION fail_copy_version()`)
	require.NoError(t, err)
	var before, after int
	require.NoError(t, db.QueryRow(ctx, `SELECT COUNT(*) FROM folders`).Scan(&before))
	_, err = s.CopyItem(ctx, 1, domain.ResourceFolder, nested.ID, &rollbackTarget.ID)
	require.Error(t, err)
	require.NoError(t, db.QueryRow(ctx, `SELECT COUNT(*) FROM folders`).Scan(&after))
	require.Equal(t, before, after)
	_, err = db.Exec(ctx, `DROP TRIGGER fail_copy_version ON file_versions`)
	require.NoError(t, err)
	// Two simultaneous opposite moves must never produce a cycle.
	a, err := s.CreateFolder(ctx, 1, nil, "Concurrent A")
	require.NoError(t, err)
	b, err := s.CreateFolder(ctx, 1, nil, "Concurrent B")
	require.NoError(t, err)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); errs <- s.repo.MoveFolder(ctx, a.ID, &b.ID) }()
	go func() { defer wg.Done(); errs <- s.repo.MoveFolder(ctx, b.ID, &a.ID) }()
	wg.Wait()
	close(errs)
	passed, rejected := 0, 0
	for e := range errs {
		if e == nil {
			passed++
		} else {
			require.ErrorIs(t, e, repository.ErrFolderCycle)
			rejected++
		}
	}
	require.Equal(t, 1, passed)
	require.Equal(t, 1, rejected)
}

func TestDriveBatchValidation(t *testing.T) {
	s := &DriveService{}
	for _, req := range []domain.DriveBatchRequest{{Action: "bad"}, {Action: "move"}, {Action: "copy", Items: make([]domain.DriveBatchItem, 101)}} {
		_, err := s.BatchItems(context.Background(), 1, req)
		require.Equal(t, 400, apperrors.ToAppError(err).StatusCode)
	}
	for _, name := range []string{"", " ", "../file", "x/y", "x\\y", "x\n", ".."} {
		require.Error(t, validateDriveName(name))
	}
	require.NoError(t, validateDriveName("Отчёт 2026.txt"))
}
