package domain

import (
	"errors"
	"strings"
)

// Each rule requires at least one of AnyOf. Order chooses the least additional
// scope in the editor; existing broader read grants satisfy the same rule.
type AccessDependency struct {
	Permission string   `json:"permission"`
	AnyOf      []string `json:"any_of"`
}

var ErrAccessDependencies = errors.New("permission prerequisites are missing")

// AccessDependencies describes usable combinations in the service UI as well
// as API prerequisites. It never infers a decision, signature, or wider scope
// from a read grant. Conditional iForm process roles have no unconditional edge.
func AccessDependencies(catalog map[string][]AccessPermission) map[string][]AccessDependency {
	rules := map[string][]AccessDependency{"odo": {}, "sd": {}, "iform": {}}
	add := func(app, code string, anyOf ...string) {
		rules[app] = append(rules[app], AccessDependency{Permission: code, AnyOf: anyOf})
	}
	reads := []string{"document.read", "document.read_department", "document.read_all"}
	for _, code := range []string{"document.create", "document.update", "document.register", "document.send_to_approval", "document.send_to_signing", "document.archive", "document.close", "document.view_history", "document.delete_draft", "assignment.read", "approval.read", "signature.read", "report.read"} {
		add("odo", code, reads...)
	}
	// Archive grants already supply their own object scope. Do not force ordinary
	// document access upon a user who only works with archived documents.
	for _, code := range []string{"attachment.upload", "attachment.download", "attachment.delete"} {
		add("odo", code, append(append([]string{}, reads...), "archive.read", "archive.view_all", "archive.manage")...)
	}
	for _, code := range []string{"assignment.create", "assignment.update", "assignment.complete", "assignment.close", "assignment.cancel"} {
		add("odo", code, "assignment.read")
	}
	for _, code := range []string{"approval.start", "approval.approve", "approval.reject", "approval.return_for_revision"} {
		add("odo", code, "approval.read")
	}
	add("odo", "approval.start", "document.send_to_approval")
	add("odo", "document.send_to_approval", "approval.read")
	add("odo", "document.send_to_signing", "signature.read")
	for _, code := range []string{"signature.sign", "signature.reject", "signature.verify"} {
		add("odo", code, "signature.read")
	}
	add("odo", "report.export", "report.read")
	add("odo", "archive.manage", "archive.export")
	for _, code := range []string{"npa.project.create", "npa.project.manage", "npa.document.edit", "npa.comment.create", "npa.suggestion.create", "npa.suggestion.resolve", "npa.version.manage", "npa.project.finalize", "npa.audit.read"} {
		add("odo", code, "npa.project.read")
	}
	for _, permission := range AccessCatalog()["sd"] {
		if strings.HasSuffix(permission.Code, ":read_all") {
			add("sd", permission.Code, strings.TrimSuffix(permission.Code, "_all"))
		}
	}
	add("sd", "rbac_audit:view_audit_all_tenant", "rbac_audit:view_audit")
	// A client-company catalog must not advertise staff-only dependencies.
	for app, dependencies := range rules {
		allowed := map[string]bool{}
		for _, permission := range catalog[app] {
			allowed[permission.Code] = true
		}
		filtered := []AccessDependency{}
		for _, rule := range dependencies {
			if !allowed[rule.Permission] || !AccessPermissionAvailable(app, rule.Permission) {
				continue
			}
			choices := []string{}
			for _, code := range rule.AnyOf {
				if allowed[code] {
					choices = append(choices, code)
				}
			}
			filtered = append(filtered, AccessDependency{Permission: rule.Permission, AnyOf: choices})
		}
		rules[app] = filtered
	}
	return rules
}

func validateAccessDependencies(app string, selected map[string]bool) error {
	for _, rule := range AccessDependencies(AccessCatalog())[app] {
		if !selected[rule.Permission] {
			continue
		}
		found := false
		for _, code := range rule.AnyOf {
			found = found || selected[code]
		}
		if !found {
			return ErrAccessDependencies
		}
	}
	return nil
}
