package domain

import "errors"

var ErrAccessPermissionUnavailable = errors.New("permission has no independently enforced service operation")

// Retain registry entries for reading old policies, but do not advertise an
// unimplemented action as a working grant. Re-enable only with consumer tests.
func AccessUnavailablePermissions() map[string][]string {
	return map[string][]string{
		"odo": {"document.delete_draft", "attachment.delete"},
		"sd": {
			"clients:export", "projects:export", "objects:export", "inventory:export", "warehouse:export", "tickets:export", "knowledge:export", "workers:export", "settings:export", "groups:export",
			"reports:create", "reports:update", "reports:delete",
			"knowledge:read_all", "settings:read_all", "groups:read_all",
		},
		"iform": {},
	}
}

func AccessPermissionAvailable(app, code string) bool {
	for _, unavailable := range AccessUnavailablePermissions()[app] {
		if code == unavailable {
			return false
		}
	}
	return true
}
