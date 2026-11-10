package domain

import "testing"

func TestCanDeleteEmployee(t *testing.T) {
	owner := int64(2)
	for _, tc := range []struct {
		actor, target int64
		role          Role
		want          bool
	}{
		{0, 3, RoleUser, false}, {1, 3, RoleUser, true}, {2, 3, RoleUser, true}, {2, 2, RoleUser, false},
		{1, 2, RoleUser, false}, {1, 4, RoleSuperAdmin, false}, {1, 0, RoleUser, false},
	} {
		if got := CanDeleteEmployee(tc.actor, tc.target, tc.role, &owner); got != tc.want {
			t.Errorf("actor=%d target=%d: got %t", tc.actor, tc.target, got)
		}
	}
}
