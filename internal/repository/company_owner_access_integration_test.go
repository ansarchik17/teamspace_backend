package repository

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

func applyCompanyServiceTestMigration(t *testing.T, db *pgxpool.Pool, name string) {
	t.Helper()
	data, err := os.ReadFile("../../db/migrations/" + name)
	require.NoError(t, err)
	_, err = db.Exec(context.Background(), strings.Split(string(data), "-- +goose Down")[0])
	require.NoError(t, err, name)
}

func companyOwnerServiceFixture(t *testing.T) (*UserRepository, *OrganizationRepository, *pgxpool.Pool, uuid.UUID, uuid.UUID) {
	t.Helper()
	users, db, other, ttc := userCreationFixture(t)
	_, err := db.Exec(context.Background(), `ALTER TABLE organizations ADD COLUMN slug TEXT UNIQUE, ADD COLUMN updated_at TIMESTAMPTZ DEFAULT now();
 CREATE TABLE teams(id UUID PRIMARY KEY,organization_id UUID,name TEXT,parent_team_id UUID,manager_id BIGINT,deleted_at TIMESTAMPTZ);`)
	require.NoError(t, err)
	_, err = db.Exec(context.Background(), `UPDATE organizations SET slug=CASE WHEN id=$1 THEN 'tts' ELSE 'other' END`, ttc)
	require.NoError(t, err)
	for _, name := range []string{"20261002000100_organization_access.sql", "20261006000100_iform_access.sql", "20261012000300_company_modules.sql", "20261022120000_employment_admission.sql"} {
		applyCompanyServiceTestMigration(t, db, name)
	}
	return users, NewOrganizationRepository(db, zap.NewNop()), db, ttc, other
}

func TestCompanyServiceDefaultsRepairOnlyTTCAndInitializeFutureCompanies(t *testing.T) {
	_, orgs, db, ttc, other := companyOwnerServiceFixture(t)
	ctx := context.Background()
	_, err := db.Exec(ctx, `DELETE FROM organization_modules`)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO organization_modules(organization_id,application,available,enabled,revision) VALUES($1,'sd',false,false,7)`, ttc)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO organization_access_apps(organization_id,application,enabled,revision) VALUES($1,'sd',false,7)`, ttc)
	require.NoError(t, err)
	applyCompanyServiceTestMigration(t, db, "20261027000300_company_service_defaults.sql")
	var count int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM organization_modules WHERE organization_id=$1 AND available AND enabled`, ttc).Scan(&count))
	require.Equal(t, 2, count)
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM organization_modules WHERE organization_id=$1`, other).Scan(&count))
	require.Zero(t, count, "existing unrelated companies must not be changed")
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM organization_access_apps WHERE organization_id=$1`, other).Scan(&count))
	require.Zero(t, count)
	var revision int64
	require.NoError(t, db.QueryRow(ctx, `SELECT revision FROM organization_modules WHERE organization_id=$1 AND application='sd' AND NOT available AND NOT enabled`, ttc).Scan(&revision))
	require.EqualValues(t, 7, revision)
	require.NoError(t, db.QueryRow(ctx, `SELECT revision FROM organization_access_apps WHERE organization_id=$1 AND application='sd' AND NOT enabled`, ttc).Scan(&revision))
	require.EqualValues(t, 7, revision)
	_, err = orgs.IFormIdentity(ctx, 2)
	require.NoError(t, err, "TTC owner no longer needs a position to open iForm")
	future := uuid.New()
	_, err = db.Exec(ctx, `INSERT INTO organizations(id,name,slug) VALUES($1,'Future company','future')`, future)
	require.NoError(t, err)
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM organization_modules WHERE organization_id=$1 AND available AND enabled`, future).Scan(&count))
	require.Equal(t, 3, count)
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM organization_access_apps WHERE organization_id=$1 AND enabled`, future).Scan(&count))
	require.Equal(t, 3, count)
}

func TestCompanyOwnerHasLiveTenantScopedAccessToAllServices(t *testing.T) {
	users, orgs, db, ttc, _ := companyOwnerServiceFixture(t)
	ctx := context.Background()
	applyCompanyServiceTestMigration(t, db, "20261027000300_company_service_defaults.sql")
	sd := uuid.New()
	_, err := db.Exec(ctx, `INSERT INTO organization_app_links VALUES($1,'sd',$2,true)`, ttc, sd)
	require.NoError(t, err)
	employee := newUserCreationCandidate(ttc)
	title := "Директор"
	employee.Position = &title
	require.NoError(t, users.CreateNewUser(ctx, employee))
	for _, app := range []string{"odo", "sd", "iform"} {
		access, err := orgs.ResolveOwnAccess(ctx, 2, app)
		require.NoError(t, err)
		require.True(t, access.Active)
		require.True(t, access.Managed)
		require.Equal(t, domain.OwnerAccessPermissions(app, true), access.Permissions)
		employeeAccess, err := orgs.ResolveOwnAccess(ctx, employee.ID, app)
		require.NoError(t, err)
		require.Empty(t, employeeAccess.Permissions, "job title cannot grant ownership")
		members, err := orgs.ListMembersForApplication(ctx, 2, app)
		require.NoError(t, err)
		for _, member := range members {
			if member.ID == 2 {
				require.Equal(t, access.Permissions, member.Access.Permissions)
			}
		}
	}
	foreign, err := orgs.ResolveSDExternalAccess(ctx, sd, "workspace-user:1")
	require.NoError(t, err)
	require.False(t, foreign.Active)
	require.Empty(t, foreign.Permissions)
	sdAccess, err := orgs.ResolveSDExternalAccess(ctx, sd, "workspace-user:2")
	require.NoError(t, err)
	require.True(t, sdAccess.Active)
	require.Equal(t, domain.OwnerAccessPermissions("sd", true), sdAccess.Permissions)
	profile, err := orgs.SDHandoffProfile(ctx, 2)
	require.NoError(t, err)
	require.Equal(t, sd, profile.CompanyID)
	view, err := orgs.IFormContext(ctx, 2, ttc)
	require.NoError(t, err)
	for _, member := range view.Members {
		if member.ID == domain.IFormSubject(2) {
			for _, role := range []string{"TENANT_ADMIN", "HR", "DIRECTOR", "SIGNER", "APPROVER"} {
				require.Contains(t, member.Roles, role)
			}
		}
	}
	_, err = db.Exec(ctx, `UPDATE organizations SET owner_id=$2 WHERE id=$1`, ttc, employee.ID)
	require.NoError(t, err)
	for _, app := range []string{"odo", "sd", "iform"} {
		former, err := orgs.ResolveOwnAccess(ctx, 2, app)
		require.NoError(t, err)
		require.Empty(t, former.Permissions, "ownership transfer must revoke defaults immediately")
		current, err := orgs.ResolveOwnAccess(ctx, employee.ID, app)
		require.NoError(t, err)
		require.NotEmpty(t, current.Permissions)
	}
	_, err = orgs.IFormIdentity(ctx, 2)
	require.Error(t, err)
	for _, app := range []string{"odo", "sd", "iform"} {
		_, err = db.Exec(ctx, `UPDATE organization_modules SET enabled=false WHERE organization_id=$1 AND application=$2`, ttc, app)
		require.NoError(t, err)
		access, err := orgs.ResolveOwnAccess(ctx, employee.ID, app)
		require.NoError(t, err)
		require.False(t, access.Active)
		require.Empty(t, access.Permissions)
	}
	_, err = orgs.IFormIdentity(ctx, employee.ID)
	require.Error(t, err)
}

func TestCompanyOwnerAccessHonorsLiveRevocation(t *testing.T) {
	for _, scenario := range []string{"inactive", "deleted", "suspended", "moved", "not_owner", "managed_off", "unavailable", "not_started", "cancelled_admission"} {
		t.Run(scenario, func(t *testing.T) {
			_, orgs, db, ttc, other := companyOwnerServiceFixture(t)
			ctx := context.Background()
			applyCompanyServiceTestMigration(t, db, "20261027000300_company_service_defaults.sql")
			var err error
			switch scenario {
			case "inactive":
				_, err = db.Exec(ctx, `UPDATE users SET is_active=false WHERE id=2`)
			case "deleted":
				_, err = db.Exec(ctx, `UPDATE users SET deleted_at=now() WHERE id=2`)
			case "suspended":
				_, err = db.Exec(ctx, `UPDATE organizations SET status='suspended' WHERE id=$1`, ttc)
			case "moved":
				_, err = db.Exec(ctx, `UPDATE users SET organization_id=$1 WHERE id=2`, other)
			case "not_owner":
				_, err = db.Exec(ctx, `UPDATE organizations SET owner_id=NULL WHERE id=$1`, ttc)
			case "managed_off":
				_, err = db.Exec(ctx, `UPDATE organization_access_apps SET enabled=false WHERE organization_id=$1`, ttc)
			case "unavailable":
				_, err = db.Exec(ctx, `UPDATE organization_modules SET available=false WHERE organization_id=$1`, ttc)
			case "not_started":
				_, err = db.Exec(ctx, `INSERT INTO employee_admissions(organization_id,request_id,actor_id,employee_id,start_date) VALUES($1,$2,2,2,CURRENT_DATE+2)`, ttc, uuid.New())
			case "cancelled_admission":
				_, err = db.Exec(ctx, `INSERT INTO employee_admissions(organization_id,request_id,actor_id,employee_id,start_date,cancelled_at) VALUES($1,$2,2,2,CURRENT_DATE-1,now())`, ttc, uuid.New())
			}
			require.NoError(t, err)
			for _, app := range []string{"odo", "sd", "iform"} {
				access, err := orgs.ResolveOwnAccess(ctx, 2, app)
				if err == nil {
					require.Empty(t, access.Permissions)
				}
			}
			_, err = orgs.IFormIdentity(ctx, 2)
			require.Error(t, err)
		})
	}
}
