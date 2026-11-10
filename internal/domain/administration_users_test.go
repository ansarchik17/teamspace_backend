package domain

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestEmployeeBlockProtectsAdministrativeAccounts(t *testing.T) {
	owner := int64(3)
	require.False(t, CanChangeEmployeeBlock(2, 2, RoleUser, &owner))
	require.False(t, CanChangeEmployeeBlock(2, 3, RoleUser, &owner))
	require.False(t, CanChangeEmployeeBlock(2, 4, RoleSuperAdmin, &owner))
	require.False(t, CanChangeEmployeeBlock(2, 0, RoleUser, &owner))
	require.True(t, CanChangeEmployeeBlock(3, 2, RoleUser, &owner))
}
