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

func newAccessPositionsTestDB(t *testing.T) (context.Context, *pgxpool.Pool, uuid.UUID, uuid.UUID) {
	t.Helper()
	dsn := os.Getenv("TEST_MESSENGER_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_MESSENGER_DATABASE_URL to a disposable PostgreSQL database")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	schema := "access_positions_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	cfg, err := pgxpool.ParseConfig(dsn)
	require.NoError(t, err)
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		db.Close()
		_, cleanupErr := admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
		require.NoError(t, cleanupErr)
	})
	_, err = db.Exec(ctx, `
CREATE TABLE users(id BIGINT PRIMARY KEY,organization_id UUID,role TEXT DEFAULT 'user',is_active BOOLEAN DEFAULT true,
 deleted_at TIMESTAMPTZ,position TEXT,team_id UUID,avatar_url TEXT,username TEXT DEFAULT '');
CREATE TABLE organizations(id UUID PRIMARY KEY,name TEXT,slug TEXT,status TEXT DEFAULT 'active',owner_id BIGINT,
 updated_at TIMESTAMPTZ DEFAULT now());
INSERT INTO users(id,role) VALUES(1,'admin'),(2,'user'),(3,'user'),(4,'user'),(5,'user'),(6,'manager');`)
	require.NoError(t, err)
	a, b := uuid.New(), uuid.New()
	_, err = db.Exec(ctx, `INSERT INTO organizations(id,name,slug,owner_id) VALUES($1,'A','a',2),($2,'B','b',4)`, a, b)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `UPDATE users SET organization_id=CASE WHEN id IN(2,3,6) THEN $1::uuid ELSE $2::uuid END WHERE id<>1`, a, b)
	require.NoError(t, err)
	for _, file := range []string{
		"20260928000100_create_organization_growy_position_roles.sql",
		"20261002000100_organization_access.sql",
		"20261003000100_sync_employee_access_position.sql",
		"20261006000100_iform_access.sql",
		"20261012000100_company_administration_audit.sql",
		"20261012000300_company_modules.sql",
		"20261022120000_employment_admission.sql",
	} {
		data, readErr := os.ReadFile("../../db/migrations/" + file)
		require.NoError(t, readErr)
		_, err = db.Exec(ctx, strings.Split(string(data), "-- +goose Down")[0])
		require.NoError(t, err, file)
	}
	_, err = db.Exec(ctx, `INSERT INTO organization_app_links(organization_id,application,external_company_id,is_tenant_root) SELECT id,'sd',gen_random_uuid(),true FROM organizations`)
	require.NoError(t, err)
	return ctx, db, a, b
}

func TestAccessPositionsPrepareAssignTransferAndDelete(t *testing.T) {
	ctx, db, org, _ := newAccessPositionsTestDB(t)
	r := NewOrganizationRepository(db, zap.NewNop())
	change := func(action string, data AccessChange) {
		t.Helper()
		require.NoError(t, r.ChangeOrganizationAccess(ctx, 2, nil, action, data))
	}
	position := func(name string) domain.AccessPosition {
		t.Helper()
		access, err := r.OrganizationAccess(ctx, 2, nil)
		require.NoError(t, err)
		for _, p := range access.Positions {
			if p.Name == name {
				return p
			}
		}
		t.Fatalf("position %q is absent from management, including empty positions", name)
		return domain.AccessPosition{}
	}
	change("position", AccessChange{Name: "Reviewer"})
	p := position("Reviewer")
	require.Zero(t, p.UserCount)
	grants := map[string][]string{"odo": {"document.read"}, "sd": {"tickets:read"}, "iform": {"iform.use"}}
	for app, permissions := range grants {
		require.False(t, p.Policies[app].Configured)
		change("policy", AccessChange{ID: p.ID, Application: app, Permissions: permissions})
		change("application", AccessChange{Application: app, Enabled: true})
	}

	// A duplicate create must not become an assignment to existing privileges.
	err := r.ChangeOrganizationAccess(ctx, 2, nil, "position", AccessChange{Name: " reviewer "})
	require.Equal(t, apperrors.ErrCodeConflict, apperrors.ToAppError(err).Code)
	require.Zero(t, position("Reviewer").UserCount)

	change("member", AccessChange{ID: p.ID, UserID: 3})
	require.EqualValues(t, 1, position("Reviewer").UserCount)
	for app, permissions := range grants {
		access, err := r.ResolveOwnAccess(ctx, 3, app)
		require.NoError(t, err)
		require.True(t, access.Active && access.Managed)
		require.Equal(t, permissions, access.Permissions)
	}
	change("position", AccessChange{ID: p.ID, Revision: p.Revision, Name: "Senior reviewer"})
	renamed := position("Senior reviewer")
	require.Equal(t, p.ID, renamed.ID)
	var profilePosition string
	require.NoError(t, db.QueryRow(ctx, `SELECT position FROM users WHERE id=3`).Scan(&profilePosition))
	require.Equal(t, renamed.Name, profilePosition)
	for app, permissions := range grants {
		require.Equal(t, permissions, renamed.Policies[app].Permissions)
	}
	err = r.ChangeOrganizationAccess(ctx, 2, nil, "delete", AccessChange{ID: p.ID, Revision: renamed.Revision})
	require.Equal(t, apperrors.ErrCodeConflict, apperrors.ToAppError(err).Code)

	change("position", AccessChange{Name: "Unconfigured"})
	destination := position("Unconfigured")
	change("member", AccessChange{ID: destination.ID, UserID: 3})
	require.Zero(t, position(renamed.Name).UserCount)
	for app, permissions := range grants {
		access, err := r.ResolveOwnAccess(ctx, 3, app)
		require.NoError(t, err)
		require.Empty(t, access.Permissions, "transfer must use the destination policy")
		require.Equal(t, permissions, position(renamed.Name).Policies[app].Permissions)
	}
	change("unassign", AccessChange{UserID: 3})
	require.Zero(t, position(destination.Name).UserCount)
	var assigned int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM organization_access_assignments WHERE user_id=3`).Scan(&assigned))
	require.Zero(t, assigned)
	for app := range grants {
		access, err := r.ResolveOwnAccess(ctx, 3, app)
		require.NoError(t, err)
		require.Empty(t, access.Permissions)
	}

	// Stale revisions must not delete another administrator's changes.
	err = r.ChangeOrganizationAccess(ctx, 2, nil, "delete", AccessChange{ID: renamed.ID, Revision: p.Revision})
	require.Equal(t, apperrors.ErrCodeConflict, apperrors.ToAppError(err).Code)
	change("delete", AccessChange{ID: renamed.ID, Revision: renamed.Revision})
	var policyCount int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM organization_position_permissions WHERE organization_id=$1 AND position_id=$2`, org, renamed.ID).Scan(&policyCount))
	require.Zero(t, policyCount)
	require.Equal(t, destination.ID, position(destination.Name).ID)
}

func TestAccessPositionsDirectorPlatformAndTenantBoundaries(t *testing.T) {
	ctx, db, a, b := newAccessPositionsTestDB(t)
	r := NewOrganizationRepository(db, zap.NewNop())
	for _, org := range []uuid.UUID{a, b} {
		require.NoError(t, r.ChangeOrganizationAccess(ctx, 1, &org, "position", AccessChange{Name: "Prepared"}))
	}
	accessA, err := r.OrganizationAccess(ctx, 2, nil)
	require.NoError(t, err)
	accessB, err := r.OrganizationAccess(ctx, 4, nil)
	require.NoError(t, err)
	pa, pb := accessA.Positions[0].ID, accessB.Positions[0].ID
	// Both actual owners and a platform administrator can assign and unassign.
	for _, tc := range []struct {
		actor, employee int64
		org             *uuid.UUID
		position        uuid.UUID
	}{{2, 3, nil, pa}, {4, 5, nil, pb}, {1, 3, &a, pa}, {1, 5, &b, pb}} {
		require.NoError(t, r.ChangeOrganizationAccess(ctx, tc.actor, tc.org, "member", AccessChange{ID: tc.position, UserID: tc.employee}))
		require.NoError(t, r.ChangeOrganizationAccess(ctx, tc.actor, tc.org, "unassign", AccessChange{UserID: tc.employee}))
	}
	var beforeAudit int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM organization_access_audit`).Scan(&beforeAudit))
	for _, tc := range []struct {
		name   string
		actor  int64
		org    *uuid.UUID
		action string
		data   AccessChange
	}{
		{"employee cannot create", 3, nil, "position", AccessChange{Name: "Forbidden"}},
		{"manager cannot assign", 6, nil, "member", AccessChange{ID: pa, UserID: 3}},
		{"owner cannot select own platform scope", 2, &a, "member", AccessChange{ID: pa, UserID: 3}},
		{"owner cannot select foreign company", 2, &b, "member", AccessChange{ID: pb, UserID: 5}},
		{"owner cannot assign foreign employee", 2, nil, "member", AccessChange{ID: pa, UserID: 5}},
		{"owner cannot use foreign position", 2, nil, "member", AccessChange{ID: pb, UserID: 3}},
		{"owner cannot unassign foreign employee", 2, nil, "unassign", AccessChange{UserID: 5}},
		{"platform scope still checks company", 1, &a, "member", AccessChange{ID: pb, UserID: 3}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Error(t, r.ChangeOrganizationAccess(ctx, tc.actor, tc.org, tc.action, tc.data))
		})
	}
	var afterAudit, assignments int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM organization_access_audit`).Scan(&afterAudit))
	require.Equal(t, beforeAudit, afterAudit)
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM organization_access_assignments`).Scan(&assignments))
	require.Zero(t, assignments)
	_, err = db.Exec(ctx, `UPDATE users SET role='user' WHERE id=1`)
	require.NoError(t, err)
	require.Error(t, r.ChangeOrganizationAccess(ctx, 1, &a, "member", AccessChange{ID: pa, UserID: 3}))
	_, err = db.Exec(ctx, `UPDATE users SET is_active=false WHERE id=2`)
	require.NoError(t, err)
	require.Error(t, r.ChangeOrganizationAccess(ctx, 2, nil, "member", AccessChange{ID: pa, UserID: 3}))
}
