package domain

import "testing"

func TestAdministrationAllowed(t *testing.T) {
	owner, other := int64(7), int64(8)
	for _, tc := range []struct {
		name     string
		role     Role
		active   bool
		actor    int64
		owner    *int64
		platform bool
		want     bool
	}{
		{"platform without company", RoleSuperAdmin, true, 7, nil, true, true},
		{"inactive platform", RoleSuperAdmin, false, 7, nil, true, false},
		{"director own company", Role("user"), true, 7, &owner, false, true},
		{"director other company", Role("user"), true, 7, &other, false, false},
		{"director platform route", Role("user"), true, 7, &owner, true, false},
		{"inactive director", Role("user"), false, 7, &owner, false, false},
		{"employee", Role("user"), true, 7, nil, false, false},
		{"title director", Role("director"), true, 7, nil, false, false},
		{"invented admin", Role("company_admin"), true, 7, nil, false, false},
		{"invalid identity", RoleSuperAdmin, true, 0, nil, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := AdministrationAllowed(tc.role, tc.active, tc.actor, tc.owner, tc.platform); got != tc.want {
				t.Fatalf("allowed=%v, want %v", got, tc.want)
			}
		})
	}
}
