package repository

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

func TestOrganizationAdministrationLifecycle(t *testing.T) {
	dsn := os.Getenv("TEST_MESSENGER_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_MESSENGER_DATABASE_URL to a disposable PostgreSQL database")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	schema := "admin_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	cfg, err := pgxpool.ParseConfig(dsn)
	require.NoError(t, err)
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	})
	_, err = pool.Exec(ctx, `
CREATE TABLE users(id BIGINT PRIMARY KEY,role TEXT NOT NULL DEFAULT 'user',is_active BOOLEAN NOT NULL DEFAULT TRUE,
deleted_at TIMESTAMPTZ,organization_id UUID,team_id UUID,manager_id BIGINT,updated_at TIMESTAMPTZ DEFAULT now(),
full_name_local TEXT,first_name TEXT,last_name TEXT,position TEXT,username TEXT DEFAULT '',email TEXT DEFAULT '');
CREATE TABLE organizations(id UUID PRIMARY KEY DEFAULT gen_random_uuid(),name TEXT NOT NULL,slug TEXT UNIQUE NOT NULL,
status TEXT NOT NULL DEFAULT 'active',owner_id BIGINT REFERENCES users(id),created_at TIMESTAMPTZ DEFAULT now(),updated_at TIMESTAMPTZ DEFAULT now());
CREATE TABLE teams(id UUID PRIMARY KEY,organization_id UUID,deleted_at TIMESTAMPTZ);
INSERT INTO users(id,role,is_active) VALUES(1,'admin',true),(2,'user',true),(3,'user',true),(4,'user',false),(5,'admin',true);`)
	require.NoError(t, err)
	admissionMigration, err := os.ReadFile("../../db/migrations/20261022120000_employment_admission.sql")
	require.NoError(t, err)
	_, err = pool.Exec(ctx, strings.Split(string(admissionMigration), "-- +goose Down")[0])
	require.NoError(t, err)
	r := NewOrganizationRepository(pool, zap.NewNop())
	migration, err := os.ReadFile("../../db/migrations/20261012000100_company_administration_audit.sql")
	require.NoError(t, err)
	_, err = pool.Exec(ctx, strings.Split(string(migration), "-- +goose Down")[0])
	require.NoError(t, err)
	blockMigration, err := os.ReadFile("../../db/migrations/20261012000200_company_user_blocks.sql")
	require.NoError(t, err)
	_, err = pool.Exec(ctx, strings.Split(string(blockMigration), "-- +goose Down")[0])
	require.NoError(t, err)
	moduleMigration, err := os.ReadFile("../../db/migrations/20261012000300_company_modules.sql")
	require.NoError(t, err)
	_, err = pool.Exec(ctx, strings.Split(string(moduleMigration), "-- +goose Down")[0])
	require.NoError(t, err)
	company, err := r.CreatePlatformOrganization(ctx, 1, domain.CreateOrganizationRequest{Name: "Company", Slug: "company"})
	require.NoError(t, err)
	other, err := r.CreatePlatformOrganization(ctx, 1, domain.CreateOrganizationRequest{Name: "Other", Slug: "other"})
	require.NoError(t, err)
	_, err = r.AssignPlatformOrganizationOwner(ctx, 1, company.ID, 2)
	require.NoError(t, err)
	_, err = r.accessScope(ctx, 2, nil)
	require.NoError(t, err)
	_, err = r.CreatePlatformOrganization(ctx, 2, domain.CreateOrganizationRequest{Name: "Forbidden", Slug: "forbidden"})
	require.Equal(t, apperrors.ErrCodeForbidden, apperrors.ToAppError(err).Code)
	_, err = r.AssignPlatformOrganizationOwner(ctx, 2, company.ID, 3)
	require.Equal(t, apperrors.ErrCodeForbidden, apperrors.ToAppError(err).Code)
	_, err = r.UpdatePlatformOrganization(ctx, 2, company.ID, domain.UpdatePlatformOrganizationRequest{})
	require.Equal(t, apperrors.ErrCodeForbidden, apperrors.ToAppError(err).Code)
	for _, target := range []int64{4, 5, 999} {
		_, err = r.AssignPlatformOrganizationOwner(ctx, 1, company.ID, target)
		require.Error(t, err)
		var owner int64
		require.NoError(t, pool.QueryRow(ctx, "SELECT owner_id FROM organizations WHERE id=$1", company.ID).Scan(&owner))
		require.Equal(t, int64(2), owner)
	}
	_, err = pool.Exec(ctx, "UPDATE users SET organization_id=$1 WHERE id=3", other.ID)
	require.NoError(t, err)
	_, err = r.AssignPlatformOrganizationOwner(ctx, 1, company.ID, 3)
	require.Error(t, err)
	team := uuid.New()
	_, err = pool.Exec(ctx, "UPDATE users SET organization_id=$1,team_id=$2,manager_id=2 WHERE id=3", company.ID, team)
	require.NoError(t, err)
	_, err = r.AssignPlatformOrganizationOwner(ctx, 1, company.ID, 3)
	require.NoError(t, err)
	_, err = r.accessScope(ctx, 2, nil)
	require.Equal(t, apperrors.ErrCodeForbidden, apperrors.ToAppError(err).Code)
	_, err = r.accessScope(ctx, 3, nil)
	require.NoError(t, err)
	var savedTeam uuid.UUID
	var manager int64
	require.NoError(t, pool.QueryRow(ctx, "SELECT team_id,manager_id FROM users WHERE id=3").Scan(&savedTeam, &manager))
	require.Equal(t, team, savedTeam)
	require.Equal(t, int64(2), manager)
	users, err := r.ListAdministrationUsers(ctx, 3, nil, domain.AdministrationUserFilter{Limit: 25})
	require.NoError(t, err)
	require.Equal(t, 2, users.Total)
	_, err = pool.Exec(ctx, "UPDATE users SET username='Employee' WHERE id=2")
	require.NoError(t, err)
	matched, err := r.ListAdministrationUsers(ctx, 3, nil, domain.AdministrationUserFilter{Query: "employee", Limit: 25})
	require.NoError(t, err)
	require.Equal(t, 1, matched.Total)
	require.Equal(t, int64(2), matched.Items[0].ID)
	require.NoError(t, r.SetEmployeeBlocked(ctx, 3, 2, nil, true))
	userRepo := NewUserRepository(pool, zap.NewNop())
	userActive, err := userRepo.IsUserActive(ctx, 2)
	require.NoError(t, err)
	require.False(t, userActive)
	_, err = pool.Exec(ctx, "UPDATE users SET is_active=true WHERE id=2")
	require.NoError(t, err)
	userActive, err = userRepo.IsUserActive(ctx, 2)
	require.NoError(t, err)
	require.False(t, userActive, "identity sync must not reactivate a blocked user")
	require.Error(t, r.SetEmployeeBlocked(ctx, 3, 3, nil, true))
	require.Error(t, r.SetEmployeeBlocked(ctx, 3, 1, nil, true))
	require.NoError(t, r.SetEmployeeBlocked(ctx, 3, 2, nil, false))
	userActive, err = userRepo.IsUserActive(ctx, 2)
	require.NoError(t, err)
	require.True(t, userActive)
	_, err = pool.Exec(ctx, `ALTER TABLE organization_admin_audit ADD CONSTRAINT test_block_audit_failure CHECK(action <> 'user.block.update') NOT VALID`)
	require.NoError(t, err)
	require.Error(t, r.SetEmployeeBlocked(ctx, 3, 2, nil, true))
	userActive, err = userRepo.IsUserActive(ctx, 2)
	require.NoError(t, err)
	require.True(t, userActive, "failed audit must roll back blocking")
	_, err = pool.Exec(ctx, "ALTER TABLE organization_admin_audit DROP CONSTRAINT test_block_audit_failure")
	require.NoError(t, err)
	_, err = pool.Exec(ctx, "UPDATE users SET organization_id=$1 WHERE id=4", other.ID)
	require.NoError(t, err)
	require.Error(t, r.SetEmployeeBlocked(ctx, 3, 4, nil, true), "cross-company employee must be rejected")
	suspended := "suspended"
	_, err = r.UpdatePlatformOrganization(ctx, 1, company.ID, domain.UpdatePlatformOrganizationRequest{Status: &suspended})
	require.NoError(t, err)
	_, err = r.accessScope(ctx, 3, nil)
	require.Error(t, err)
	active := "active"
	_, err = r.UpdatePlatformOrganization(ctx, 1, company.ID, domain.UpdatePlatformOrganizationRequest{Status: &active})
	require.NoError(t, err)
	_, err = r.accessScope(ctx, 3, nil)
	require.NoError(t, err)
	page, err := r.ListAdministrationAudit(ctx, 3, nil, false, domain.AdministrationAuditFilter{Limit: 100})
	require.NoError(t, err)
	require.NotEmpty(t, page.Items)
	for _, item := range page.Items {
		require.Equal(t, company.ID, item.OrganizationID)
	}
	_, err = r.ListAdministrationAudit(ctx, 2, nil, false, domain.AdministrationAuditFilter{Limit: 100})
	require.Equal(t, apperrors.ErrCodeForbidden, apperrors.ToAppError(err).Code)
	all, err := r.ListAdministrationAudit(ctx, 1, nil, true, domain.AdministrationAuditFilter{Limit: 1})
	require.NoError(t, err)
	require.Len(t, all.Items, 1)
	require.NotNil(t, all.NextBeforeID)
	_, err = pool.Exec(ctx, `ALTER TABLE organization_admin_audit ADD CONSTRAINT test_audit_failure CHECK(action <> 'organization.update') NOT VALID`)
	require.NoError(t, err)
	name := "Must roll back"
	_, err = r.UpdatePlatformOrganization(ctx, 1, company.ID, domain.UpdatePlatformOrganizationRequest{Name: &name})
	require.Error(t, err)
	var savedName string
	require.NoError(t, pool.QueryRow(ctx, "SELECT name FROM organizations WHERE id=$1", company.ID).Scan(&savedName))
	require.Equal(t, "Company", savedName)
	_, err = pool.Exec(ctx, "ALTER TABLE organization_admin_audit DROP CONSTRAINT test_audit_failure")
	require.NoError(t, err)
	providerMigration, err := os.ReadFile("../../db/migrations/20260914000000_create_organization_identity_providers.sql")
	require.NoError(t, err)
	_, err = pool.Exec(ctx, strings.Split(string(providerMigration), "-- +goose Down")[0])
	require.NoError(t, err)
	providers := NewOrganizationIdentityProviderRepository(pool, zap.NewNop())
	provider, err := providers.Create(ctx, 1, company.ID, "oidc", "test-provider", "Corporate login", map[string]string{"clientSecret": "never-log-this"}, true, false)
	require.NoError(t, err)
	_, err = providers.ResolveEnabledAliasByOrganizationSlug(ctx, "company")
	require.Error(t, err, "disabled provider must not be offered")
	_, err = pool.Exec(ctx, "UPDATE organization_identity_providers SET enabled=true,status='active' WHERE id=$1", provider.ID)
	require.NoError(t, err)
	alias, err := providers.ResolveEnabledAliasByOrganizationSlug(ctx, " COMPANY ")
	require.NoError(t, err)
	require.Equal(t, "test-provider", alias)
	_, err = providers.ResolveEnabledAliasByOrganizationSlug(ctx, "other")
	require.Error(t, err, "provider must never leak across companies")
	_, err = pool.Exec(ctx, "UPDATE organization_identity_providers SET status='error' WHERE id=$1", provider.ID)
	require.NoError(t, err)
	_, err = providers.ResolveEnabledAliasByOrganizationSlug(ctx, "company")
	require.Error(t, err, "failed configuration must not be offered")
	_, err = pool.Exec(ctx, "UPDATE organization_identity_providers SET status='active' WHERE id=$1", provider.ID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, "UPDATE organizations SET status='suspended' WHERE id=$1", company.ID)
	require.NoError(t, err)
	_, err = providers.ResolveEnabledAliasByOrganizationSlug(ctx, "company")
	require.Error(t, err, "suspended company must not offer corporate login")
	_, err = pool.Exec(ctx, "UPDATE organizations SET status='active' WHERE id=$1", company.ID)
	require.NoError(t, err)
	diagnostic := "private-diagnostic-token"
	_, err = providers.Update(ctx, 1, company.ID, provider.ID, nil, map[string]string{"clientSecret": "another-secret"}, nil, nil, nil, &diagnostic, nil)
	require.NoError(t, err)
	require.NoError(t, providers.Delete(ctx, 1, company.ID, provider.ID))
	providerAudit, err := r.ListAdministrationAudit(ctx, 1, &company.ID, true, domain.AdministrationAuditFilter{Limit: 100})
	require.NoError(t, err)
	var providerEvents int
	for _, entry := range providerAudit.Items {
		if entry.ObjectType != "identity_provider" {
			continue
		}
		providerEvents++
		payload := string(entry.Before) + string(entry.After)
		require.NotContains(t, payload, "never-log-this")
		require.NotContains(t, payload, "another-secret")
		require.NotContains(t, payload, diagnostic)
		require.NotContains(t, payload, "clientSecret")
	}
	require.Equal(t, 3, providerEvents)
	t.Run("module administration", func(t *testing.T) {
		yes, no := true, false
		rev := int64(0)
		modules, err := r.ListAdministrationModules(ctx, 3, nil)
		require.NoError(t, err)
		require.Len(t, modules, 4)
		for _, m := range modules {
			require.False(t, m.Effective)
			require.Zero(t, m.Revision)
		}
		require.Error(t, r.RequireUserModule(ctx, 2, "iform"))
		require.Error(t, r.SetAdministrationModule(ctx, 3, nil, "iform", domain.AdministrationModuleRequest{Enabled: &yes, Revision: &rev}))
		require.Error(t, r.SetAdministrationModule(ctx, 3, nil, "iform", domain.AdministrationModuleRequest{Available: &yes, Revision: &rev}))
		require.Error(t, r.SetAdministrationModule(ctx, 3, &other.ID, "iform", domain.AdministrationModuleRequest{Available: &yes, Revision: &rev}))
		require.Error(t, r.SetAdministrationModule(ctx, 2, nil, "iform", domain.AdministrationModuleRequest{Enabled: &no, Revision: &rev}))
		require.NoError(t, r.SetAdministrationModule(ctx, 1, &company.ID, "iform", domain.AdministrationModuleRequest{Available: &yes, Revision: &rev}))
		rev = 1
		require.NoError(t, r.SetAdministrationModule(ctx, 3, nil, "iform", domain.AdministrationModuleRequest{Enabled: &yes, Revision: &rev}))
		require.NoError(t, r.RequireUserModule(ctx, 2, "iform"))
		require.Equal(t, apperrors.ErrCodeConflict, apperrors.ToAppError(r.SetAdministrationModule(ctx, 3, nil, "iform", domain.AdministrationModuleRequest{Enabled: &no, Revision: &rev})).Code)
		rev = 2
		require.NoError(t, r.SetAdministrationModule(ctx, 1, &company.ID, "iform", domain.AdministrationModuleRequest{Available: &no, Revision: &rev}))
		require.Error(t, r.RequireUserModule(ctx, 2, "iform"))
		rev = 3
		require.Error(t, r.SetAdministrationModule(ctx, 3, nil, "iform", domain.AdministrationModuleRequest{Enabled: &yes, Revision: &rev}))
		_, err = pool.Exec(ctx, `ALTER TABLE organization_admin_audit ADD CONSTRAINT module_audit_failure CHECK(action <> 'module.update') NOT VALID`)
		require.NoError(t, err)
		require.Error(t, r.SetAdministrationModule(ctx, 1, &company.ID, "iform", domain.AdministrationModuleRequest{Available: &yes, Revision: &rev}))
		require.Error(t, r.RequireUserModule(ctx, 2, "iform"), "failed audit must roll back availability")
		modules, err = r.ListAdministrationModules(ctx, 3, nil)
		require.NoError(t, err)
		for _, m := range modules {
			if m.Application == "iform" {
				require.Equal(t, int64(3), m.Revision)
				require.False(t, m.Available)
				require.True(t, m.Enabled)
			}
		}
		_, err = pool.Exec(ctx, "ALTER TABLE organization_admin_audit DROP CONSTRAINT module_audit_failure")
		require.NoError(t, err)
		audit, err := r.ListAdministrationAudit(ctx, 3, nil, false, domain.AdministrationAuditFilter{Limit: 100})
		require.NoError(t, err)
		count := 0
		for _, item := range audit.Items {
			if item.ObjectType == "module" {
				count++
				require.Equal(t, "iform", item.ObjectID)
			}
		}
		require.Equal(t, 3, count)
	})
	_, err = pool.Exec(ctx, "UPDATE users SET is_active=false WHERE id=1")
	require.NoError(t, err)
	_, err = r.UpdatePlatformOrganization(ctx, 1, company.ID, domain.UpdatePlatformOrganizationRequest{Status: &suspended})
	require.Equal(t, apperrors.ErrCodeForbidden, apperrors.ToAppError(err).Code)
}
