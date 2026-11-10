package domain

import "testing"

func TestCompanyAdministrationCapabilities(t *testing.T) {
	for _, platform := range []bool{false, true} {
		for _, active := range []bool{false, true} {
			for _, budget := range []bool{false, true} {
				c := CompanyAdministrationCapabilities(platform, active, budget)
				if c.ManageUsers != active || c.ManageStructure != active || c.ManagePermissions != active || c.ManageModules != active {
					t.Fatal("company mutations must require active company")
				}
				if c.SetCompanyQuota != (platform && active) || c.SetModuleAvailability != (platform && active) || c.ManageSDLink != (platform && active) || c.ManageIdentityProviders != (platform && active) {
					t.Fatal("platform mutations must require platform scope and active company")
				}
				if c.SetUserQuotas != (active && (platform || budget)) {
					t.Fatal("employee quota capability must match budget policy")
				}
				if c.ManageCompanyLifecycle != platform || c.AssignOwner != platform || !c.ReadAudit {
					t.Fatal("recovery and audit capabilities mismatch")
				}
			}
		}
	}
}
