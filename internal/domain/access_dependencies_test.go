package domain

import (
	"errors"
	"reflect"
	"testing"
)

func TestAccessDependenciesRejectIncompletePoliciesWithoutAddingGrants(t *testing.T) {
	for app, policies := range map[string][][]string{
		"odo": {{"document.create"}, {"approval.start", "approval.read", "document.read"}, {"assignment.complete", "document.read"}, {"signature.sign", "document.read"}, {"report.export", "document.read"}, {"archive.manage"}, {"npa.project.manage"}},
		"sd":  {{"rbac_audit:view_audit_all_tenant"}},
	} {
		for _, policy := range policies {
			original := append([]string{}, policy...)
			got, err := ValidateAccessPermissions(app, policy)
			if !errors.Is(err, ErrAccessDependencies) || got != nil || !reflect.DeepEqual(policy, original) {
				t.Fatalf("incomplete %s policy was changed/accepted: %v -> %v (%v)", app, policy, got, err)
			}
		}
	}
}

func TestAccessDependenciesAcceptMinimalScopesAndIndependentIFormRoles(t *testing.T) {
	for app, policies := range map[string][][]string{
		"odo": {nil, {"document.read"}, {"document.read_all"}, {"archive.read"}, {"archive.view_all"}, {"archive.export"}, {"archive.manage", "archive.export"}, {"attachment.download", "archive.read"}, {"document.create", "document.read_department"}, {"assignment.complete", "assignment.read", "document.read"}, {"approval.start", "approval.read", "document.send_to_approval", "document.read"}, {"signature.sign", "signature.read", "document.read"}, {"report.export", "report.read", "document.read"}, {"npa.project.manage", "npa.project.read"}},
		"sd":  {{"rbac_audit:view_audit_all_tenant", "rbac_audit:view_audit"}, {"tickets:create"}},
	} {
		for _, policy := range policies {
			got, err := ValidateAccessPermissions(app, policy)
			if err != nil || len(got) != len(policy) {
				t.Fatalf("minimal %s policy rejected or broadened: %v -> %v (%v)", app, policy, got, err)
			}
		}
	}
	for _, permission := range AccessCatalog()["iform"] {
		got, err := ValidateAccessPermissions("iform", []string{permission.Code})
		if err != nil || !reflect.DeepEqual(got, []string{permission.Code}) {
			t.Fatalf("iForm role gained another function: %v (%v)", got, err)
		}
	}
}

func TestAccessDependenciesOnlyReferenceTheirCompanyCatalog(t *testing.T) {
	for _, client := range []bool{false, true} {
		catalog := AccessCatalog()
		if client {
			catalog["sd"] = SDClientPermissions(catalog["sd"])
		}
		for app, rules := range AccessDependencies(catalog) {
			allowed := map[string]bool{}
			all := []string{}
			for _, permission := range catalog[app] {
				allowed[permission.Code] = true
				if AccessPermissionAvailable(app, permission.Code) {
					all = append(all, permission.Code)
				}
			}
			for _, rule := range rules {
				if !allowed[rule.Permission] || len(rule.AnyOf) == 0 {
					t.Fatalf("unresolvable %s dependency: %+v", app, rule)
				}
				for _, code := range rule.AnyOf {
					if !allowed[code] {
						t.Fatalf("%s dependency leaked a foreign/staff-only code: %s", app, code)
					}
				}
			}
			if _, err := ValidateAccessPermissions(app, all); err != nil {
				t.Fatalf("complete %s catalog is not selectable: %v", app, err)
			}
		}
	}
}
