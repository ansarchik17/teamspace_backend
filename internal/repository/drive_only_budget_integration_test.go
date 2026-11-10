package repository

import (
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

func TestCompanyDriveBudgetIgnoresLegacyMailReservation(t *testing.T) {
	ctx, db, org, _ := newAdministrationStorageTestDB(t)
	migration, err := os.ReadFile("../../db/migrations/20261014000100_company_drive_only_budget.sql")
	require.NoError(t, err)
	parts := strings.Split(string(migration), "-- +goose Down")
	_, err = db.Exec(ctx, parts[1])
	require.NoError(t, err)
	// A company configured under the previous shared budget has 200 for
	// employees and 100 reserved for mail. Preserve its settings and files.
	_, err = db.Exec(ctx, `INSERT INTO organization_storage_accounts(organization_id,quota_bytes,mail_quota_bytes,mail_managed,revision) VALUES($1,300,100,true,4) ON CONFLICT(organization_id) DO UPDATE SET quota_bytes=300,mail_quota_bytes=100,mail_managed=true,revision=4`, org)
	require.NoError(t, err)
	file := uuid.New()
	_, err = db.Exec(ctx, `INSERT INTO files(id,owner_id,object_name,size) VALUES($1,2,'existing-drive-file',50)`, file)
	require.NoError(t, err)
	_, err = db.Exec(ctx, parts[0])
	require.NoError(t, err)
	r := NewOrganizationRepository(db, zap.NewNop())
	r.SetStorageCapacityProvider(testStorageCapacity{})
	state, err := r.AdministrationStorage(ctx, 2, nil)
	require.NoError(t, err, "Drive overview must not call AxisMail")
	require.Equal(t, int64(300), *state.QuotaBytes)
	require.Equal(t, int64(200), state.AllocatedBytes)
	require.Equal(t, int64(50), state.UsedBytes)
	require.Equal(t, int64(100), *state.UnallocatedBytes)
	require.Equal(t, int64(4), state.Revision)
	quota, revision := int64(200), int64(0)
	require.NoError(t, r.SetAdministrationUserStorage(ctx, 2, 3, nil, domain.AdministrationStorageRequest{QuotaBytes: &quota, Revision: &revision}), "director can distribute all 300 bytes to Drive")
	_, err = db.Exec(ctx, `INSERT INTO files(id,owner_id,object_name,size) VALUES($1,3,'full-employee-quota',200)`, uuid.New())
	require.NoError(t, err, "mail reservation must not restrict Drive uploads")
	_, err = db.Exec(ctx, `INSERT INTO files(id,owner_id,object_name,size) VALUES($1,3,'over-personal-quota',1)`, uuid.New())
	require.Equal(t, apperrors.ErrCodeDriveQuotaExceeded, apperrors.ToAppError(err).Code)
	drive, err := NewDriveRepository(db, zap.NewNop()).GetDriveStorage(ctx, 3)
	require.NoError(t, err)
	require.Equal(t, int64(300), *drive.CompanyQuotaBytes)
	require.Equal(t, int64(250), *drive.CompanyUsedBytes)
	require.False(t, drive.CanUpload)
	quota, revision = 350, state.Revision
	require.NoError(t, r.SetAdministrationStorage(ctx, 1, &org, domain.AdministrationStorageRequest{QuotaBytes: &quota, Revision: &revision}), "platform can update a formerly mail-managed company without AxisMail")
	var storedMail int64
	var managed bool
	require.NoError(t, db.QueryRow(ctx, `SELECT mail_quota_bytes,mail_managed FROM organization_storage_accounts WHERE organization_id=$1`, org).Scan(&storedMail, &managed))
	require.Equal(t, int64(100), storedMail, "legacy values remain available for rollback")
	require.True(t, managed)
	var size int64
	require.NoError(t, db.QueryRow(ctx, `SELECT size FROM files WHERE id=$1`, file).Scan(&size))
	require.Equal(t, int64(50), size)
}
