package domain

// AdministrationAllowed uses persisted identity and ownership, never job titles
// or email addresses. An explicit company ID is reserved for platform routes.
func AdministrationAllowed(role Role, active bool, actor int64, owner *int64, platformOnly bool) bool {
	if !active || actor <= 0 {
		return false
	}
	if role == RoleSuperAdmin {
		return true
	}
	return !platformOnly && owner != nil && *owner == actor
}
