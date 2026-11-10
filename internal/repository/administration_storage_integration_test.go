package repository

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

func newAdministrationStorageTestDB(t *testing.T) (context.Context, *pgxpool.Pool, uuid.UUID, uuid.UUID) {
	t.Helper()
	dsn := os.Getenv("TEST_MESSENGER_DATABASE_URL")
	if dsn == "" {
		t.Skip("set disposable TEST_MESSENGER_DATABASE_URL")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	schema := "storage_admin_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	cfg, err := pgxpool.ParseConfig(dsn)
	require.NoError(t, err)
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { db.Close(); _, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); admin.Close() })
	_, err = db.Exec(ctx, `
CREATE TABLE users(id BIGINT PRIMARY KEY,organization_id UUID,role TEXT DEFAULT 'user',is_active BOOLEAN DEFAULT true,deleted_at TIMESTAMPTZ,full_name_local TEXT,first_name TEXT,last_name TEXT,username TEXT,updated_at TIMESTAMPTZ);
CREATE TABLE organizations(id UUID PRIMARY KEY,name TEXT,slug TEXT,status TEXT DEFAULT 'active',owner_id BIGINT,updated_at TIMESTAMPTZ DEFAULT now());
CREATE TABLE files(id UUID PRIMARY KEY,owner_id BIGINT REFERENCES users(id),object_name TEXT,size BIGINT,scope TEXT DEFAULT 'drive',deleted_at TIMESTAMPTZ);
CREATE TABLE file_versions(id UUID PRIMARY KEY,file_id UUID REFERENCES files(id),object_name TEXT,size BIGINT);
INSERT INTO users(id,role) VALUES(1,'admin'),(2,'user'),(3,'user'),(4,'user'),(5,'user'),(6,'user');`)
	require.NoError(t, err)
	a, b := uuid.New(), uuid.New()
	_, err = db.Exec(ctx, `INSERT INTO organizations(id,name,slug,owner_id) VALUES($1,'A','a',2),($2,'B','b',4)`, a, b)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `UPDATE users SET organization_id=CASE WHEN id IN(2,3) THEN $1::uuid ELSE $2::uuid END WHERE id IN(2,3,4,5)`, a, b)
	require.NoError(t, err)
	for _, file := range []string{"20260927140000_drive_storage_quotas.sql", "20261012000100_company_administration_audit.sql", "20261012000400_company_storage_quotas.sql", "20261013000100_company_shared_storage.sql", "20261014000100_company_drive_only_budget.sql"} {
		data, err := os.ReadFile("../../db/migrations/" + file)
		require.NoError(t, err)
		_, err = db.Exec(ctx, strings.Split(string(data), "-- +goose Down")[0])
		require.NoError(t, err, file)
	}
	_, err = db.Exec(ctx, `UPDATE drive_storage_policy SET quota_bytes=100`)
	require.NoError(t, err)
	return ctx, db, a, b
}

func TestEmployeeDefaultQuotaPreservesOverrides(t *testing.T) {
	ctx, db, _, _ := newAdministrationStorageTestDB(t)
	_, err := db.Exec(ctx, `INSERT INTO drive_storage_accounts(user_id,quota_bytes) VALUES(3,2147483648)`)
	require.NoError(t, err)
	data, err := os.ReadFile("../../db/migrations/20261013000200_employee_default_quotas.sql")
	require.NoError(t, err)
	_, err = db.Exec(ctx, strings.Split(string(data), "-- +goose Down")[0])
	require.NoError(t, err)
	var standard, custom int64
	require.NoError(t, db.QueryRow(ctx, `SELECT quota_bytes FROM drive_storage_policy`).Scan(&standard))
	require.NoError(t, db.QueryRow(ctx, `SELECT quota_bytes FROM drive_storage_accounts WHERE user_id=3`).Scan(&custom))
	require.Equal(t, int64(5368709120), standard)
	require.Equal(t, int64(2147483648), custom)
}

func TestAdministrationStorageBudgetsAndAccounting(t *testing.T) {
	ctx, db, a, _ := newAdministrationStorageTestDB(t)
	r := NewOrganizationRepository(db, zap.NewNop())
	r.SetStorageCapacityProvider(testStorageCapacity{})
	request := func(quota, revision int64) domain.AdministrationStorageRequest {
		return domain.AdministrationStorageRequest{QuotaBytes: &quota, Revision: &revision}
	}
	summary, err := r.AdministrationStorage(ctx, 2, nil)
	require.NoError(t, err)
	require.Nil(t, summary.QuotaBytes)
	require.Equal(t, int64(200), summary.AllocatedBytes)
	require.Equal(t, apperrors.ErrCodeForbidden, apperrors.ToAppError(r.SetAdministrationStorage(ctx, 2, &a, request(250, 0))).Code)
	require.Error(t, r.SetAdministrationStorage(ctx, 1, &a, request(199, 0)))
	require.Error(t, r.SetAdministrationUserStorage(ctx, 2, 3, nil, request(150, 0)), "director needs a configured budget")
	require.NoError(t, r.SetAdministrationStorage(ctx, 1, &a, request(250, 0)))
	require.NoError(t, r.SetAdministrationUserStorage(ctx, 2, 3, nil, request(150, 0)))
	require.Error(t, r.SetAdministrationUserStorage(ctx, 2, 5, nil, request(1, 0)), "foreign employee")
	require.Error(t, r.SetAdministrationUserStorage(ctx, 3, 2, nil, request(1, 0)), "ordinary employee")
	require.Error(t, r.SetAdministrationUserStorage(ctx, 2, 3, nil, request(151, 0)), "stale revision")
	require.Error(t, r.SetAdministrationUserStorage(ctx, 2, 2, nil, request(101, 0)), "over-allocation")
	require.Error(t, r.SetAdministrationStorage(ctx, 1, &a, request(240, 1)), "budget below allocations")
	require.NoError(t, r.SetAdministrationStorage(ctx, 1, &a, request(300, 1)))
	// Both updates fit separately; the combined updates must not exceed the budget.
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, item := range []struct{ id, quota, revision int64 }{{2, 150, 0}, {3, 200, 1}} {
		wg.Add(1)
		go func(id, quota, revision int64) {
			defer wg.Done()
			<-start
			results <- r.SetAdministrationUserStorage(ctx, 2, id, nil, request(quota, revision))
		}(item.id, item.quota, item.revision)
	}
	close(start)
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		}
	}
	require.Equal(t, 1, success)
	summary, err = r.AdministrationStorage(ctx, 2, nil)
	require.NoError(t, err)
	require.Equal(t, int64(300), summary.AllocatedBytes)
	_, err = db.Exec(ctx, `UPDATE users SET organization_id=$1 WHERE id=6`, a)
	require.Error(t, err, "new membership cannot bypass allocations")
	var org *uuid.UUID
	require.NoError(t, db.QueryRow(ctx, `SELECT organization_id FROM users WHERE id=6`).Scan(&org))
	require.Nil(t, org)
	_, err = db.Exec(ctx, `UPDATE users SET organization_id=$1 WHERE id=5`, a)
	require.Error(t, err, "company transfer cannot bypass budget")
	page, err := r.AdministrationStorageUsers(ctx, 2, nil, 25, 0)
	require.NoError(t, err)
	require.Equal(t, 2, page.Total)
	for _, u := range page.Items {
		require.NoError(t, r.SetAdministrationUserStorage(ctx, 2, u.UserID, nil, request(100, u.Revision)))
	}
	// Force an audit failure and verify that quota + accounting stay unchanged.
	_, err = db.Exec(ctx, `ALTER TABLE organization_admin_audit ADD CONSTRAINT fail_storage_audit CHECK(action <> 'storage.user_quota.update') NOT VALID`)
	require.NoError(t, err)
	page, err = r.AdministrationStorageUsers(ctx, 2, nil, 25, 0)
	require.NoError(t, err)
	require.Error(t, r.SetAdministrationUserStorage(ctx, 2, 3, nil, request(150, page.Items[1].Revision)))
	page, err = r.AdministrationStorageUsers(ctx, 2, nil, 25, 0)
	require.NoError(t, err)
	require.Equal(t, int64(100), page.Items[1].QuotaBytes)
	_, err = db.Exec(ctx, `ALTER TABLE organization_admin_audit DROP CONSTRAINT fail_storage_audit`)
	require.NoError(t, err)
	insert := func(owner, size int64) (uuid.UUID, error) {
		id := uuid.New()
		_, err := db.Exec(ctx, `INSERT INTO files(id,owner_id,object_name,size) VALUES($1,$2,$3,$4)`, id, owner, id.String(), size)
		return id, err
	}
	f, err := insert(2, 100)
	require.NoError(t, err)
	_, err = insert(3, 100)
	require.NoError(t, err)
	page, err = r.AdministrationStorageUsers(ctx, 2, nil, 25, 0)
	require.NoError(t, err)
	require.NoError(t, r.SetAdministrationUserStorage(ctx, 2, 2, nil, request(0, page.Items[0].Revision)), "lowering personal quota preserves files")
	require.NoError(t, r.SetAdministrationUserStorage(ctx, 2, 3, nil, request(201, page.Items[1].Revision)))
	_, err = insert(3, 100)
	require.NoError(t, err)
	_, err = insert(3, 1)
	require.Equal(t, apperrors.ErrCodeDriveQuotaExceeded, apperrors.ToAppError(err).Code, "company rejects even within personal quota")
	_, err = db.Exec(ctx, `INSERT INTO file_versions(id,file_id,object_name,size) VALUES($1,$2,'extra-version',1)`, uuid.New(), f)
	require.Equal(t, apperrors.ErrCodeDriveQuotaExceeded, apperrors.ToAppError(err).Code)
	_, err = db.Exec(ctx, `UPDATE files SET deleted_at=now() WHERE id=$1`, f)
	require.NoError(t, err)
	summary, err = r.AdministrationStorage(ctx, 2, nil)
	require.NoError(t, err)
	require.Equal(t, int64(300), summary.UsedBytes, "recoverable trash consumes company quota")

	require.NoError(t, r.SetAdministrationStorage(ctx, 1, &a, request(301, 2)))
	_, err = db.Exec(ctx, `INSERT INTO drive_storage_accounts(user_id,quota_bytes) VALUES(6,1)`)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `UPDATE users SET organization_id=$1 WHERE id=6`, a)
	require.NoError(t, err)
	start = make(chan struct{})
	results = make(chan error, 2)
	for _, owner := range []int64{3, 6} {
		wg.Add(1)
		go func(owner int64) { defer wg.Done(); <-start; _, err := insert(owner, 1); results <- err }(owner)
	}
	close(start)
	wg.Wait()
	close(results)
	success = 0
	for err := range results {
		if err == nil {
			success++
		} else {
			require.Equal(t, apperrors.ErrCodeDriveQuotaExceeded, apperrors.ToAppError(err).Code)
		}
	}
	require.Equal(t, 1, success, "concurrent uploads must not exceed company quota")
	summary, err = r.AdministrationStorage(ctx, 2, nil)
	require.NoError(t, err)
	require.Equal(t, int64(301), summary.UsedBytes)
	storage, err := NewDriveRepository(db, zap.NewNop()).GetDriveStorage(ctx, 3)
	require.NoError(t, err)
	require.Zero(t, storage.FreeBytes)
	require.False(t, storage.CanUpload)
	_, err = db.Exec(ctx, `UPDATE files SET deleted_at=now()-INTERVAL '721 hours' WHERE id=$1`, f)
	require.NoError(t, err)
	summary, err = r.AdministrationStorage(ctx, 2, nil)
	require.NoError(t, err)
	require.Equal(t, int64(201), summary.UsedBytes)
	_, err = insert(5, 100)
	require.NoError(t, err, "another company has its own budget")
	_, err = db.Exec(ctx, `UPDATE drive_storage_accounts SET quota_bytes=101 WHERE user_id=5`)
	require.NoError(t, err)
	_, err = insert(5, 1)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `UPDATE drive_storage_accounts SET quota_bytes=99 WHERE user_id=5`)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `UPDATE users SET organization_id=$1 WHERE id=5`, a)
	require.Equal(t, apperrors.ErrCodeDriveQuotaExceeded, apperrors.ToAppError(err).Code, "allocated quota fits but moving existing storage must be checked")
	// Down migration restores personal accounting without dropping existing quotas.
	data, err := os.ReadFile("../../db/migrations/20261012000400_company_storage_quotas.sql")
	require.NoError(t, err)
	_, err = db.Exec(ctx, strings.Split(string(data), "-- +goose Down")[1])
	require.NoError(t, err)
	var quota int64
	require.NoError(t, db.QueryRow(ctx, `SELECT quota_bytes FROM drive_storage_accounts WHERE user_id=3`).Scan(&quota))
	require.Equal(t, int64(201), quota)
}
