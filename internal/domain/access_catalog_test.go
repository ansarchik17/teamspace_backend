package domain

import (
	"reflect"
	"strings"
	"testing"
)

func TestAccessCatalogArchivePermissionsAreExplicitAndApplicationScoped(t *testing.T) {
	requested := []string{"archive.read", "archive.write", "archive.export", "archive.audit", "archive.view_all", "archive.manage"}
	got, err := ValidateAccessPermissions("odo", requested)
	if err != nil || len(got) != len(requested) {
		t.Fatalf("archive permissions not assignable: %v, %v", got, err)
	}
	for _, input := range [][]string{nil, {"document.read"}, {"archive.read"}, {"archive.manage", "archive.export"}} {
		got, err := ValidateAccessPermissions("odo", input)
		if err != nil || len(got) != len(input) {
			t.Fatalf("validation unexpectedly adds permissions: %v -> %v (%v)", input, got, err)
		}
	}
	for _, app := range []string{"sd", "iform"} {
		if _, err := ValidateAccessPermissions(app, requested); err == nil {
			t.Fatalf("archive permissions leaked into %s", app)
		}
	}
}

func TestAccessCatalogRejectsSystemUnknownAndCrossApplicationGrants(t *testing.T) {
	for app, values := range map[string][]string{
		"odo":   {"*", "all", "admin", "admin.users", "admin.roles", "permission.manage", "position.manage", "user.manage", "department.manage", "dictionary.manage", "delegation.manage", "audit.read", "npa.project.manage_all", "archive.unknown", "iform.sign", "tickets:read"},
		"sd":    {"system_audit:view_audit_all_system", "equipment:read", "tickets:unknown", "archive.read", "iform.use"},
		"iform": {"TENANT_ADMIN", "admin", "archive.read", "workers:update", "iform.unknown"},
	} {
		for _, code := range values {
			t.Run(app+"/"+code, func(t *testing.T) {
				if _, err := ValidateAccessPermissions(app, []string{code}); err == nil {
					t.Fatal("non-delegatable permission accepted")
				}
			})
		}
	}
	if _, err := ValidateAccessPermissions("unknown", nil); err == nil {
		t.Fatal("unknown application accepted")
	}
}

func TestSDReadAllAddsOnlyBaseReadAndDeduplicates(t *testing.T) {
	input := []string{"tickets:read_all", "tickets:read_all", "tickets:read"}
	original := append([]string(nil), input...)
	got, err := ValidateAccessPermissions("sd", input)
	if err != nil || !reflect.DeepEqual(got, []string{"tickets:read", "tickets:read_all"}) {
		t.Fatalf("unexpected normalized permissions: %v (%v)", got, err)
	}
	if !reflect.DeepEqual(input, original) {
		t.Fatal("validation mutated submitted policy")
	}
}

func TestSDClientCatalogDoesNotDelegateWarehouseOrTenantAdministration(t *testing.T) {
	for _, permission := range SDClientPermissions(AccessCatalog()["sd"]) {
		resource, _, _ := strings.Cut(permission.Code, ":")
		if resource == "warehouse" || resource == "settings" || resource == "rbac_audit" || resource == "system_audit" {
			t.Fatalf("staff-only permission offered to client company: %s", permission.Code)
		}
	}
}

func TestIFormCatalogMatchesExplicitRoleMapping(t *testing.T) {
	for _, permission := range AccessCatalog()["iform"] {
		role, ok := IFormPermissionRoles[permission.Code]
		if !ok || role == "TENANT_ADMIN" {
			t.Fatalf("invalid role mapping for %s: %q", permission.Code, role)
		}
		got := IFormRoles([]string{permission.Code}, false)
		if !reflect.DeepEqual(got, []string{role}) {
			t.Fatalf("%s grants unexpected roles: %v", permission.Code, got)
		}
	}
	if len(AccessCatalog()["iform"]) != len(IFormPermissionRoles) {
		t.Fatal("iForm role mapping contains a permission absent from the catalog")
	}
}
