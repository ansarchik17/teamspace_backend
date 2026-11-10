package repository

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

func TestAccessManagedWorkRolesDoNotInheritPlatformAdministration(t *testing.T) {
	ctx, db, _, _ := newAccessPositionsTestDB(t)
	r := NewOrganizationRepository(db, zap.NewNop())
	_, err := db.Exec(ctx, `UPDATE users SET role='admin' WHERE id=3`)
	require.NoError(t, err)
	for _, app := range []string{"odo", "sd", "iform"} {
		require.NoError(t, r.ChangeOrganizationAccess(ctx, 2, nil, "application", AccessChange{Application: app, Enabled: true}))
		for _, user := range []int64{2, 3, 6} {
			access, err := r.ResolveOwnAccess(ctx, user, app)
			require.NoError(t, err)
			require.True(t, access.Active && access.Managed)
			if user == 2 {
				require.Equal(t, domain.OwnerAccessPermissions(app, true), access.Permissions, "live owner must receive company work permissions")
			} else {
				require.Empty(t, access.Permissions, "platform role or manager title must not create a work grant")
			}
			require.Equal(t, []string{"user"}, access.Roles)
		}
	}
	// These are work identities only; administration keeps its own authorization.
	_, err = r.OrganizationAccess(ctx, 2, nil)
	require.NoError(t, err, "owner lost administration while work grants were revoked")
}
