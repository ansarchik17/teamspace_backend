package domain

func HasGlobalUserScope(user *User) bool {
	if user == nil {
		return false
	}

	switch user.Role {
	case RoleSuperAdmin:
		return true
	default:
		return false
	}
}

func CanAccessUserByScope(requester, target *User) bool {
	if requester == nil || target == nil {
		return false
	}

	if requester.ID == target.ID {
		return true
	}

	if target.ID == SupportUserID {
		return true
	}

	if HasGlobalUserScope(requester) {
		return true
	}

	if requester.OrganizationID != nil || target.OrganizationID != nil {
		return requester.OrganizationID != nil && target.OrganizationID != nil && *requester.OrganizationID == *target.OrganizationID
	}

	if requester.MinistryID == nil || target.MinistryID == nil {
		return false
	}

	return *requester.MinistryID == *target.MinistryID
}
