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

const crmModuleMigration = "20261103000100_crm_company_module.sql"

func TestCRMModuleMigrationPreservesLearningGrantsAndDenials(t *testing.T) {
	ctx, db, a, b := newAccessPositionsTestDB(t)
	_, err := db.Exec(ctx, `UPDATE organization_modules SET available=false,enabled=false,revision=7 WHERE organization_id=$1 AND application='growy'`, b)
	require.NoError(t, err)
	applyCompanyServiceTestMigration(t, db, crmModuleMigration)
	r := NewOrganizationRepository(db, zap.NewNop())
	for _, org := range []uuid.UUID{a, b} {
		modules, err := r.ListAdministrationModules(ctx, 1, &org)
		require.NoError(t, err)
		require.Len(t, modules, 5)
		var crm, learning domain.AdministrationModule
		for _, module := range modules {
			if module.Application == "crm" {
				crm = module
			}
			if module.Application == "growy" {
				learning = module
			}
		}
		require.Equal(t, learning.Available, crm.Available)
		require.Equal(t, learning.Enabled, crm.Enabled)
		require.Equal(t, learning.Effective, crm.Effective)
	}
	_, err = db.Exec(ctx, `UPDATE organization_modules SET enabled=false,revision=9 WHERE organization_id=$1 AND application='crm'`, a)
	require.NoError(t, err)
	applyCompanyServiceTestMigration(t, db, crmModuleMigration)
	var revision int64
	require.NoError(t, db.QueryRow(ctx, `SELECT revision FROM organization_modules WHERE organization_id=$1 AND application='crm' AND NOT enabled`, a).Scan(&revision))
	require.EqualValues(t, 9, revision, "backfill must not overwrite an explicit CRM setting")
	_, err = db.Exec(ctx, `INSERT INTO organization_modules VALUES($1,'unknown',true,true,1)`, a)
	require.Error(t, err, "the module constraint must remain restrictive")
	data, err := os.ReadFile("../../db/migrations/" + crmModuleMigration)
	require.NoError(t, err)
	_, err = db.Exec(ctx, strings.Split(string(data), "-- +goose Down")[1])
	require.NoError(t, err)
	require.NoError(t, db.QueryRow(ctx, `SELECT revision FROM organization_modules WHERE organization_id=$1 AND application='growy' AND NOT available AND NOT enabled`, b).Scan(&revision))
	require.EqualValues(t, 7, revision)
	var count int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM organization_modules WHERE application='crm'`).Scan(&count))
	require.Zero(t, count)
}

func TestCRMModulePermissionsAndLearningAreIndependent(t *testing.T) {
	ctx, db, a, b := newAccessPositionsTestDB(t)
	applyCompanyServiceTestMigration(t, db, "20261005000000_growy_position_multiple_roles.sql")
	applyCompanyServiceTestMigration(t, db, crmModuleMigration)
	r := NewOrganizationRepository(db, zap.NewNop())
	yes, no := true, false
	set := func(actor int64, org *uuid.UUID, app string, available, enabled *bool) error {
		var revision int64
		err := db.QueryRow(ctx, `SELECT revision FROM organization_modules WHERE organization_id=$1 AND application=$2`, a, app).Scan(&revision)
		require.NoError(t, err)
		return r.SetAdministrationModule(ctx, actor, org, app, domain.AdministrationModuleRequest{Available: available, Enabled: enabled, Revision: &revision})
	}
	require.NoError(t, set(1, &a, "crm", &no, nil))
	_, err := r.GrowyCRMHandoffDetailsByUserID(ctx, 2)
	require.ErrorIs(t, err, domain.ErrOrganizationNotFound)
	_, err = r.GrowyHandoffDetailsByUserID(ctx, 2)
	require.NoError(t, err, "revoking CRM must preserve learning")
	require.Equal(t, apperrors.ErrCodeForbidden, apperrors.ToAppError(set(2, nil, "crm", &yes, nil)).Code)
	require.Equal(t, apperrors.ErrCodeForbidden, apperrors.ToAppError(set(2, nil, "crm", nil, &yes)).Code)
	require.Equal(t, apperrors.ErrCodeForbidden, apperrors.ToAppError(set(2, &b, "crm", &yes, nil)).Code)
	require.NoError(t, set(1, &a, "crm", &yes, nil))
	require.NoError(t, set(2, nil, "crm", nil, &no))
	_, err = r.GrowyCRMHandoffDetailsByUserID(ctx, 2)
	require.ErrorIs(t, err, domain.ErrOrganizationNotFound)
	require.NoError(t, set(2, nil, "crm", nil, &yes))
	require.NoError(t, set(2, nil, "growy", nil, &no))
	details, err := r.GrowyCRMHandoffDetailsByUserID(ctx, 2)
	require.NoError(t, err, "CRM must work independently of learning")
	require.Equal(t, a, details.OrganizationID)
	require.Equal(t, "tenant_admin", details.GrowyRole)
	_, err = r.GrowyHandoffDetailsByUserID(ctx, 2)
	require.ErrorIs(t, err, domain.ErrOrganizationNotFound)
	_, err = db.Exec(ctx, `UPDATE users SET is_active=false WHERE id=2`)
	require.NoError(t, err)
	_, err = r.GrowyCRMHandoffDetailsByUserID(ctx, 2)
	require.ErrorIs(t, err, domain.ErrOrganizationNotFound)
	_, err = r.GrowyCRMHandoffDetailsByUserID(ctx, 4)
	require.NoError(t, err, "another company's settings must remain independent")
	_, err = db.Exec(ctx, `UPDATE organizations SET status='suspended' WHERE id=$1`, b)
	require.NoError(t, err)
	_, err = r.GrowyCRMHandoffDetailsByUserID(ctx, 4)
	require.ErrorIs(t, err, domain.ErrOrganizationNotFound)
	var audits int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM organization_admin_audit WHERE organization_id=$1 AND object_type='module' AND object_id='crm'`, a).Scan(&audits))
	require.Equal(t, 4, audits)
}

func TestCRMModuleNewCompanyRequiresPlatformGrant(t *testing.T) {
	ctx, db, _, _ := newAccessPositionsTestDB(t)
	applyCompanyServiceTestMigration(t, db, crmModuleMigration)
	org := uuid.New()
	_, err := db.Exec(ctx, `INSERT INTO organizations(id,name,slug,owner_id) VALUES($1,'New CRM company','new-crm',2)`, org)
	require.NoError(t, err)
	r := NewOrganizationRepository(db, zap.NewNop())
	modules, err := r.ListAdministrationModules(ctx, 1, &org)
	require.NoError(t, err)
	var crm domain.AdministrationModule
	for _, module := range modules {
		if module.Application == "crm" {
			crm = module
		}
	}
	require.Equal(t, "crm", crm.Application)
	require.False(t, crm.Available)
	require.False(t, crm.Enabled)
	require.Zero(t, crm.Revision)
	yes, revision := true, int64(0)
	req := domain.AdministrationModuleRequest{Enabled: &yes, Revision: &revision}
	require.Equal(t, apperrors.ErrCodeForbidden, apperrors.ToAppError(r.SetAdministrationModule(ctx, 1, &org, "crm", req)).Code)
	req.Available = &yes
	require.NoError(t, r.SetAdministrationModule(ctx, 1, &org, "crm", req))
	require.Error(t, r.SetAdministrationModule(ctx, 1, &org, "crm", req), "stale updates must still conflict")
}
