package repository

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"go.uber.org/zap"
)

func TestAccessApplicationPolicySavingAndActivation(t *testing.T) {
	ctx, db, org, _ := newAccessPositionsTestDB(t)
	r := NewOrganizationRepository(db, zap.NewNop())
	require.NoError(t, r.ChangeOrganizationAccess(ctx, 2, nil, "position", AccessChange{Name: "Prepared", UserID: 3}))
	settings, err := r.OrganizationAccess(ctx, 2, nil)
	require.NoError(t, err)
	id := settings.Positions[0].ID
	for _, app := range []string{"odo", "sd", "iform"} {
		t.Run(app, func(t *testing.T) {
			// A configured empty set is distinct from a never-saved policy.
			require.NoError(t, r.ChangeOrganizationAccess(ctx, 2, nil, "policy", AccessChange{ID: id, Application: app}))
			settings, err := r.OrganizationAccess(ctx, 2, nil)
			require.NoError(t, err)
			require.True(t, settings.Positions[0].Policies[app].Configured)
			require.Empty(t, settings.Positions[0].Policies[app].Permissions)
			require.False(t, settings.Apps[app].Enabled, "saving must not enable management")
			require.True(t, settings.Apps[app].Available && settings.Apps[app].ModuleEnabled)
			require.NoError(t, r.ChangeOrganizationAccess(ctx, 2, nil, "application", AccessChange{Application: app, Enabled: true}))
			resolved, err := r.ResolveOwnAccess(ctx, 3, app)
			require.NoError(t, err)
			require.True(t, resolved.Active && resolved.Managed)
			require.Empty(t, resolved.Permissions)

			// Switching off the module does not erase the stored management mode.
			_, err = db.Exec(ctx, `UPDATE organization_modules SET enabled=false WHERE organization_id=$1 AND application=$2`, org, app)
			require.NoError(t, err)
			settings, err = r.OrganizationAccess(ctx, 2, nil)
			require.NoError(t, err)
			require.True(t, settings.Apps[app].Enabled)
			require.False(t, settings.Apps[app].ModuleEnabled)
			resolved, err = r.ResolveOwnAccess(ctx, 3, app)
			require.NoError(t, err)
			require.False(t, resolved.Active)
			require.True(t, resolved.Managed, "disabled module must not restore legacy roles")
			require.NoError(t, r.ChangeOrganizationAccess(ctx, 2, nil, "application", AccessChange{Application: app, Enabled: false, Revision: 1}))
			err = r.ChangeOrganizationAccess(ctx, 2, nil, "application", AccessChange{Application: app, Enabled: true, Revision: 2})
			require.Equal(t, apperrors.ErrCodeForbidden, apperrors.ToAppError(err).Code)
			_, err = db.Exec(ctx, `UPDATE organization_modules SET enabled=true,available=false WHERE organization_id=$1 AND application=$2`, org, app)
			require.NoError(t, err)
			err = r.ChangeOrganizationAccess(ctx, 2, nil, "application", AccessChange{Application: app, Enabled: true, Revision: 2})
			require.Equal(t, apperrors.ErrCodeForbidden, apperrors.ToAppError(err).Code)
		})
	}
}

func TestAccessApplicationRevisionsAndSDLink(t *testing.T) {
	ctx, db, org, _ := newAccessPositionsTestDB(t)
	r := NewOrganizationRepository(db, zap.NewNop())
	require.NoError(t, r.ChangeOrganizationAccess(ctx, 2, nil, "position", AccessChange{Name: "Reviewer"}))
	settings, err := r.OrganizationAccess(ctx, 2, nil)
	require.NoError(t, err)
	id := settings.Positions[0].ID
	require.NoError(t, r.ChangeOrganizationAccess(ctx, 2, nil, "policy", AccessChange{ID: id, Application: "odo", Permissions: []string{"document.read"}}))
	require.NoError(t, r.ChangeOrganizationAccess(ctx, 1, &org, "policy", AccessChange{ID: id, Application: "odo", Permissions: []string{"document.create", "document.read"}, Revision: 1}))
	err = r.ChangeOrganizationAccess(ctx, 2, nil, "policy", AccessChange{ID: id, Application: "odo", Permissions: []string{"document.read_all"}, Revision: 1})
	require.Equal(t, apperrors.ErrCodeConflict, apperrors.ToAppError(err).Code)
	settings, err = r.OrganizationAccess(ctx, 2, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"document.create", "document.read"}, settings.Positions[0].Policies["odo"].Permissions)
	require.NoError(t, r.ChangeOrganizationAccess(ctx, 2, nil, "application", AccessChange{Application: "odo", Enabled: true}))
	require.NoError(t, r.ChangeOrganizationAccess(ctx, 1, &org, "application", AccessChange{Application: "odo", Enabled: false, Revision: 1}))
	err = r.ChangeOrganizationAccess(ctx, 2, nil, "application", AccessChange{Application: "odo", Enabled: true, Revision: 1})
	require.Equal(t, apperrors.ErrCodeConflict, apperrors.ToAppError(err).Code)
	_, err = db.Exec(ctx, `DELETE FROM organization_app_links WHERE organization_id=$1`, org)
	require.NoError(t, err)
	err = r.ChangeOrganizationAccess(ctx, 2, nil, "application", AccessChange{Application: "sd", Enabled: true})
	require.Equal(t, apperrors.ErrCodeConflict, apperrors.ToAppError(err).Code)
	settings, err = r.OrganizationAccess(ctx, 2, nil)
	require.NoError(t, err)
	require.False(t, settings.Apps["odo"].Enabled)
	require.False(t, settings.Apps["sd"].Enabled)
}
