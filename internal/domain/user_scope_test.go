package domain

import (
	"github.com/google/uuid"
	"testing"
)

func TestCanAccessUserByScope(t *testing.T) {
	ministryA := int64(10)
	ministryB := int64(20)

	tests := []struct {
		name      string
		requester *User
		target    *User
		want      bool
	}{
		{
			name:      "same user is always visible",
			requester: &User{ID: 1, Role: RoleUser, MinistryID: &ministryA},
			target:    &User{ID: 1, Role: RoleUser, MinistryID: &ministryB},
			want:      true,
		},
		{
			name:      "same ministry is visible",
			requester: &User{ID: 1, Role: RoleUser, MinistryID: &ministryA},
			target:    &User{ID: 2, Role: RoleUser, MinistryID: &ministryA},
			want:      true,
		},
		{
			name:      "different ministry is hidden for regular user",
			requester: &User{ID: 1, Role: RoleUser, MinistryID: &ministryA},
			target:    &User{ID: 2, Role: RoleUser, MinistryID: &ministryB},
			want:      false,
		},
		{
			name:      "global role can access any user",
			requester: &User{ID: 1, Role: RoleSuperAdmin},
			target:    &User{ID: 2, Role: RoleUser, MinistryID: &ministryB},
			want:      true,
		},
		{
			name:      "support user is always visible",
			requester: &User{ID: 1, Role: RoleUser, MinistryID: &ministryA},
			target:    &User{ID: SupportUserID, Role: RoleUser},
			want:      true,
		},
		{
			name:      "user without ministry cannot access other ministry",
			requester: &User{ID: 1, Role: RoleUser},
			target:    &User{ID: 2, Role: RoleUser, MinistryID: &ministryA},
			want:      false,
		},
		{
			name:      "nil requester is denied",
			requester: nil,
			target:    &User{ID: 2, Role: RoleUser, MinistryID: &ministryA},
			want:      false,
		},
		{
			name:      "nil target is denied",
			requester: &User{ID: 1, Role: RoleUser, MinistryID: &ministryA},
			target:    nil,
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CanAccessUserByScope(tt.requester, tt.target); got != tt.want {
				t.Fatalf("CanAccessUserByScope() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBAS022CorporateScopeOverridesLegacyMinistry(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	m := int64(9)
	actor := &User{ID: 11, OrganizationID: &a, MinistryID: &m}
	for _, tc := range []struct {
		org  *uuid.UUID
		want bool
	}{{&a, true}, {&b, false}, {nil, false}} {
		if got := CanAccessUserByScope(actor, &User{ID: 21, OrganizationID: tc.org, MinistryID: &m}); got != tc.want {
			t.Fatalf("scope=%v got=%v", tc.org, got)
		}
	}
	if CanAccessUserByScope(&User{ID: 31, MinistryID: &m}, actor) {
		t.Fatal("legacy actor entered corporate scope")
	}
}
