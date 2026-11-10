package domain

import "testing"

func TestUpdateGrowyPositionRolesValidate(t *testing.T) {
	t.Run("normalizes several roles and retains primary for older clients", func(t *testing.T) {
		req := UpdateGrowyPositionRolesRequest{Mappings: []GrowyPositionRole{{Position: " Преподаватель ", GrowyRoles: []string{" tenant_admin ", "employee", "issuer"}}}}
		if err := req.Validate(); err != nil {
			t.Fatal(err)
		}
		item := req.Mappings[0]
		if item.Position != "Преподаватель" || item.GrowyRole != "tenant_admin" || len(item.GrowyRoles) != 3 || item.GrowyRoles[0] != "tenant_admin" {
			t.Fatalf("unexpected normalized mapping: %+v", item)
		}
	})

	t.Run("rejects empty duplicated and unauthorized role sets", func(t *testing.T) {
		for _, roles := range [][]string{{}, {"employee", "employee"}, {"employee", "superuser"}, {"admin"}, {"manager", "individual_user"}, {"manager", ""}} {
			req := UpdateGrowyPositionRolesRequest{Mappings: []GrowyPositionRole{{Position: "Сотрудник", GrowyRole: "employee", GrowyRoles: roles}}}
			if err := req.Validate(); err == nil {
				t.Errorf("accepted invalid roles %v", roles)
			}
		}
	})
	t.Run("accepts company title mapped to fixed Growy role", func(t *testing.T) {
		req := UpdateGrowyPositionRolesRequest{Mappings: []GrowyPositionRole{{Position: "Руководитель", GrowyRole: "manager"}}}
		if err := req.Validate(); err != nil {
			t.Fatalf("expected valid mapping, got %v", err)
		}
	})

	t.Run("rejects platform-only and unknown roles", func(t *testing.T) {
		for _, role := range []string{"admin", "superuser", "unknown"} {
			req := UpdateGrowyPositionRolesRequest{Mappings: []GrowyPositionRole{{Position: "Сотрудник", GrowyRole: role}}}
			if err := req.Validate(); err == nil {
				t.Errorf("expected role %q to be rejected", role)
			}
		}
	})

	t.Run("rejects duplicate titles ignoring case", func(t *testing.T) {
		req := UpdateGrowyPositionRolesRequest{Mappings: []GrowyPositionRole{
			{Position: "Руководитель", GrowyRole: "manager"},
			{Position: " руководитель ", GrowyRole: "employee"},
		}}
		if err := req.Validate(); err == nil {
			t.Fatal("expected duplicate title to be rejected")
		}
	})
}
