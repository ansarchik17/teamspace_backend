package repository

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

func TestAccessDependenciesPolicyValidationIsAtomicAndDoesNotExpandStoredGrants(t *testing.T) {
	ctx, db, org, _ := newAccessPositionsTestDB(t)
	r := NewOrganizationRepository(db, zap.NewNop())
	require.NoError(t, r.ChangeOrganizationAccess(ctx, 2, nil, "position", AccessChange{Name: "Reviewer", UserID: 3}))
	settings, err := r.OrganizationAccess(ctx, 2, nil)
	require.NoError(t, err)
	require.Equal(t, domain.AccessDependencies(settings.Catalog), settings.Dependencies)
	id := settings.Positions[0].ID
	require.NoError(t, r.ChangeOrganizationAccess(ctx, 2, nil, "policy", AccessChange{ID: id, Application: "odo", Permissions: []string{"document.read_all"}}))
	var auditBefore int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM organization_admin_audit WHERE organization_id=$1`, org).Scan(&auditBefore))
	err = r.ChangeOrganizationAccess(ctx, 2, nil, "policy", AccessChange{ID: id, Application: "odo", Permissions: []string{"document.delete_draft"}, Revision: 1})
	require.Equal(t, apperrors.ErrCodeAccessPermissionUnavailable, apperrors.ToAppError(err).Code)
	for _, permissions := range [][]string{{"approval.start"}, {"archive.manage"}, {"signature.sign", "signature.read"}} {
		err := r.ChangeOrganizationAccess(ctx, 2, nil, "policy", AccessChange{ID: id, Application: "odo", Permissions: permissions, Revision: 1})
		require.Equal(t, apperrors.ErrCodeAccessDependencies, apperrors.ToAppError(err).Code)
	}
	settings, err = r.OrganizationAccess(ctx, 2, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"document.read_all"}, settings.Positions[0].Policies["odo"].Permissions)
	require.EqualValues(t, 1, settings.Positions[0].Policies["odo"].Revision)
	var auditAfter int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM organization_admin_audit WHERE organization_id=$1`, org).Scan(&auditAfter))
	require.Equal(t, auditBefore, auditAfter, "failed policy must not commit an audit or revision")
	require.NoError(t, r.ChangeOrganizationAccess(ctx, 2, nil, "policy", AccessChange{ID: id, Application: "odo", Permissions: []string{"approval.start", "approval.read", "document.send_to_approval", "document.read_all"}, Revision: 1}))
	require.NoError(t, r.ChangeOrganizationAccess(ctx, 2, nil, "application", AccessChange{Application: "odo", Enabled: true}))
	resolved, err := r.ResolveOwnAccess(ctx, 3, "odo")
	require.NoError(t, err)
	require.Equal(t, []string{"approval.read", "approval.start", "document.read_all", "document.send_to_approval"}, resolved.Permissions)
	require.NotContains(t, resolved.Permissions, "approval.approve")
	require.NotContains(t, resolved.Permissions, "signature.sign")
	// Removing all rights is always a valid explicit denial.
	require.NoError(t, r.ChangeOrganizationAccess(ctx, 2, nil, "policy", AccessChange{ID: id, Application: "odo", Revision: 2}))
	resolved, err = r.ResolveOwnAccess(ctx, 3, "odo")
	require.NoError(t, err)
	require.Empty(t, resolved.Permissions)
	// The client-company contract has no staff-only audit dependency.
	_, err = db.Exec(ctx, `UPDATE organization_app_links SET is_tenant_root=false WHERE organization_id=$1 AND application='sd'`, org)
	require.NoError(t, err)
	settings, err = r.OrganizationAccess(ctx, 2, nil)
	require.NoError(t, err)
	for _, rule := range settings.Dependencies["sd"] {
		require.NotEqual(t, "rbac_audit:view_audit_all_tenant", rule.Permission)
	}
}
