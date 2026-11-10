package domain

import "sort"

// The resolver checks live ownership, membership, company and module status.
// SD client companies retain the narrower contract of their linked tenant.
func OwnerAccessPermissions(app string, sdTenantRoot bool) []string {
	items := AccessCatalog()[app]
	if app == "sd" && !sdTenantRoot {
		items = SDClientPermissions(items)
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if AccessPermissionAvailable(app, item.Code) {
			out = append(out, item.Code)
		}
	}
	sort.Strings(out)
	return out
}
