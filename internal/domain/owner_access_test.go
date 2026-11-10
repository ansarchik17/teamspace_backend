package domain

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOwnerAccessRemainsInsideApplicationCatalog(t *testing.T) {
	for _, app := range []string{"odo", "iform", "sd"} {
		permissions := OwnerAccessPermissions(app, true)
		validated, err := ValidateAccessPermissions(app, permissions)
		require.NoError(t, err)
		require.Equal(t, validated, permissions)
		for _, item := range AccessCatalog()[app] {
			if AccessPermissionAvailable(app, item.Code) {
				require.Contains(t, permissions, item.Code)
			} else {
				require.NotContains(t, permissions, item.Code, "ownership cannot enable an unimplemented service operation")
			}
		}
	}
	client := OwnerAccessPermissions("sd", false)
	require.Contains(t, client, "tickets:create")
	require.NotContains(t, client, "settings:delete", "client owner cannot become SD service-staff administrator")
	require.Empty(t, OwnerAccessPermissions("unknown", true))
}
