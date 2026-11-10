package service

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/keycloak"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

type testProviderAdmin struct {
	mu        sync.Mutex
	providers map[string]keycloak.IdentityProviderRepresentation
	mappers   map[string][]keycloak.IdentityProviderMapperRepresentation
}

func (a *testProviderAdmin) CreateIdentityProvider(_ context.Context, _ string, p keycloak.IdentityProviderRepresentation) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.providers[p.Alias]; ok {
		return errors.New("exists")
	}
	a.providers[p.Alias] = p
	return nil
}
func (a *testProviderAdmin) UpdateIdentityProvider(_ context.Context, _ string, alias string, p keycloak.IdentityProviderRepresentation) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.providers[alias] = p
	return nil
}
func (a *testProviderAdmin) GetIdentityProvider(_ context.Context, _ string, alias string) (*keycloak.IdentityProviderRepresentation, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	p, ok := a.providers[alias]
	if !ok {
		return nil, errors.New("missing")
	}
	return &p, nil
}
func (a *testProviderAdmin) DeleteIdentityProvider(_ context.Context, _ string, alias string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.providers, alias)
	delete(a.mappers, alias)
	return nil
}
func (a *testProviderAdmin) CreateIdentityProviderMapper(_ context.Context, _ string, alias string, m keycloak.IdentityProviderMapperRepresentation) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.mappers[alias] = append(a.mappers[alias], m)
	return nil
}
func (a *testProviderAdmin) GetIdentityProviderMappers(_ context.Context, _ string, alias string) ([]keycloak.IdentityProviderMapperRepresentation, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]keycloak.IdentityProviderMapperRepresentation{}, a.mappers[alias]...), nil
}

func TestCompanyIdentityProviderScopeAndCompensation(t *testing.T) {
	dsn := os.Getenv("TEST_MESSENGER_DATABASE_URL")
	if dsn == "" {
		t.Skip("set disposable TEST_MESSENGER_DATABASE_URL")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	schema := "provider_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	cfg, err := pgxpool.ParseConfig(dsn)
	require.NoError(t, err)
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { db.Close(); _, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); admin.Close() })
	_, err = db.Exec(ctx, `CREATE TABLE users(id BIGINT PRIMARY KEY,role TEXT,is_active BOOLEAN,organization_id UUID,deleted_at TIMESTAMPTZ);
CREATE TABLE organizations(id UUID PRIMARY KEY,name TEXT,slug TEXT,status TEXT,owner_id BIGINT,updated_at TIMESTAMPTZ DEFAULT now());
INSERT INTO users(id,role,is_active) VALUES(1,'admin',true),(2,'user',true),(3,'user',true),(4,'user',true);`)
	require.NoError(t, err)
	a, b := uuid.New(), uuid.New()
	_, err = db.Exec(ctx, `INSERT INTO organizations(id,name,slug,status,owner_id) VALUES($1,'A','a','active',2),($2,'B','b','active',4)`, a, b)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `UPDATE users SET organization_id=$1 WHERE id IN(2,3)`, a)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `UPDATE users SET organization_id=$1 WHERE id=4`, b)
	require.NoError(t, err)
	for _, file := range []string{"20260914000000_create_organization_identity_providers.sql", "20261012000100_company_administration_audit.sql"} {
		data, err := os.ReadFile("../../db/migrations/" + file)
		require.NoError(t, err)
		_, err = db.Exec(ctx, strings.Split(string(data), "-- +goose Down")[0])
		require.NoError(t, err)
	}
	kc := &testProviderAdmin{providers: map[string]keycloak.IdentityProviderRepresentation{}, mappers: map[string][]keycloak.IdentityProviderMapperRepresentation{}}
	s := &OrganizationIdentityProviderService{repo: repository.NewOrganizationIdentityProviderRepository(db, zap.NewNop()), kcAdmin: kc, realm: "test", logger: zap.NewNop()}
	_, _, err = s.ForCurrentCompany(ctx, 2)
	require.Error(t, err, "director cannot manage corporate login")
	_, _, err = s.ForCurrentCompany(ctx, 3)
	require.Error(t, err, "ordinary employee")
	req := domain.CreateOrganizationIdentityProviderRequest{ProviderType: "oidc", DisplayName: "Corporate", ClientSecret: "never-store", Settings: map[string]string{"authorizationUrl": "https://id.example/auth", "tokenUrl": "https://id.example/token", "clientId": "public-client"}}
	_, err = s.Create(ctx, 2, a, req)
	require.Error(t, err, "platform API stays platform-only")
	_, err = s.Create(ctx, 2, b, req)
	require.Error(t, err, "scope cannot select foreign company")
	p, err := s.Create(ctx, 1, a, req)
	require.NoError(t, err)
	require.True(t, p.SecretSet)
	for _, actor := range []int64{2, 3, 4} {
		_, err = s.List(ctx, actor, a)
		require.Error(t, err, "director and employees cannot read provider settings")
		_, err = s.Get(ctx, actor, a, p.ID)
		require.Error(t, err)
		_, err = s.Update(ctx, actor, a, p.ID, domain.UpdateOrganizationIdentityProviderRequest{})
		require.Error(t, err)
		require.Error(t, s.Delete(ctx, actor, a, p.ID))
	}
	_, err = db.Exec(ctx, `UPDATE users SET is_active=false WHERE id=1`)
	require.NoError(t, err)
	_, err = s.List(ctx, 1, a)
	require.Error(t, err, "inactive platform administrator cannot read settings")
	_, err = db.Exec(ctx, `UPDATE users SET is_active=true WHERE id=1`)
	require.NoError(t, err)
	var stored string
	require.NoError(t, db.QueryRow(ctx, `SELECT settings::text FROM organization_identity_providers WHERE id=$1`, p.ID).Scan(&stored))
	require.NotContains(t, stored, "never-store")
	require.Equal(t, a.String(), kc.mappers[p.Alias][0].Config["attribute.value"])
	disabled := false
	_, err = s.Update(ctx, 1, a, p.ID, domain.UpdateOrganizationIdentityProviderRequest{Enabled: &disabled})
	require.NoError(t, err)
	name := "Changed"
	updated, err := s.Update(ctx, 1, a, p.ID, domain.UpdateOrganizationIdentityProviderRequest{DisplayName: &name})
	require.NoError(t, err)
	require.Equal(t, "disabled", updated.Status)
	_, err = db.Exec(ctx, `ALTER TABLE organization_admin_audit ADD CONSTRAINT fail_update CHECK(action <> 'identity_provider.update') NOT VALID`)
	require.NoError(t, err)
	secret := "must-roll-back"
	_, err = s.Update(ctx, 1, a, p.ID, domain.UpdateOrganizationIdentityProviderRequest{ClientSecret: &secret})
	require.Error(t, err)
	remote, err := kc.GetIdentityProvider(ctx, "test", p.Alias)
	require.NoError(t, err)
	require.Equal(t, "never-store", remote.Config["clientSecret"])
	_, err = db.Exec(ctx, `ALTER TABLE organization_admin_audit DROP CONSTRAINT fail_update; ALTER TABLE organization_admin_audit ADD CONSTRAINT fail_delete CHECK(action <> 'identity_provider.delete') NOT VALID`)
	require.NoError(t, err)
	require.Error(t, s.Delete(ctx, 1, a, p.ID))
	_, err = kc.GetIdentityProvider(ctx, "test", p.Alias)
	require.NoError(t, err)
	require.Len(t, kc.mappers[p.Alias], 1)
	_, err = db.Exec(ctx, `ALTER TABLE organization_admin_audit DROP CONSTRAINT fail_delete`)
	require.NoError(t, err)
	require.NoError(t, s.Delete(ctx, 1, a, p.ID))
	_, err = kc.GetIdentityProvider(ctx, "test", p.Alias)
	require.Error(t, err)
	_, err = db.Exec(ctx, `ALTER TABLE organization_admin_audit ADD CONSTRAINT fail_create CHECK(action <> 'identity_provider.create') NOT VALID`)
	require.NoError(t, err)
	_, err = s.Create(ctx, 1, a, req)
	require.Error(t, err)
	require.Empty(t, kc.providers)
	_, err = db.Exec(ctx, `ALTER TABLE organization_admin_audit DROP CONSTRAINT fail_create`)
	require.NoError(t, err)
	// A previously authorized platform actor cannot survive role revocation.
	_, err = db.Exec(ctx, `UPDATE users SET role='user' WHERE id=1`)
	require.NoError(t, err)
	_, err = s.Create(ctx, 1, a, req)
	require.Error(t, err)
	require.Empty(t, kc.providers)
}
