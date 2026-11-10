package domain

import "github.com/google/uuid"

type AdministrationCapabilities struct {
	ManageUsers             bool `json:"manage_users"`
	DeleteUsers             bool `json:"delete_users"`
	ManageStructure         bool `json:"manage_structure"`
	ManagePermissions       bool `json:"manage_permissions"`
	ManageModules           bool `json:"manage_modules"`
	SetModuleAvailability   bool `json:"set_module_availability"`
	SetCompanyQuota         bool `json:"set_company_quota"`
	SetUserQuotas           bool `json:"set_user_quotas"`
	ManageIdentityProviders bool `json:"manage_identity_providers"`
	ManageSDLink            bool `json:"manage_sd_link"`
	ManageCompanyLifecycle  bool `json:"manage_company_lifecycle"`
	AssignOwner             bool `json:"assign_owner"`
	ReadAudit               bool `json:"read_audit"`
}

// This describes an already authorized route scope, never grants authorization.
func CompanyAdministrationCapabilities(platform, active, budgetConfigured bool) AdministrationCapabilities {
	return AdministrationCapabilities{
		DeleteUsers: platform || active,
		ManageUsers: active, ManageStructure: active, ManagePermissions: active, ManageModules: active,
		SetModuleAvailability: platform && active, SetCompanyQuota: platform && active,
		SetUserQuotas: active && (platform || budgetConfigured), ManageIdentityProviders: platform && active,
		ManageSDLink: platform && active, ManageCompanyLifecycle: platform, AssignOwner: platform, ReadAudit: true,
	}
}

type AdministrationCompany struct {
	ID      uuid.UUID `json:"id"`
	Name    string    `json:"name"`
	Slug    string    `json:"slug"`
	Status  string    `json:"status"`
	OwnerID *int64    `json:"owner_id"`
}

type AdministrationUserStatistics struct {
	Total    int64 `json:"total"`
	Active   int64 `json:"active"`
	Inactive int64 `json:"inactive"`
	Blocked  int64 `json:"blocked"`
}

type AdministrationIntegrationStatistics struct {
	Total    int64 `json:"total"`
	Enabled  int64 `json:"enabled"`
	Pending  int64 `json:"pending"`
	Error    int64 `json:"error"`
	Disabled int64 `json:"disabled"`
}

type AdministrationOverview struct {
	Organization AdministrationCompany               `json:"organization"`
	Capabilities AdministrationCapabilities          `json:"capabilities"`
	Users        AdministrationUserStatistics        `json:"users"`
	UnitCount    int64                               `json:"unit_count"`
	Modules      []AdministrationModule              `json:"modules"`
	Storage      *AdministrationStorage              `json:"storage"`
	Integrations AdministrationIntegrationStatistics `json:"integrations"`
	RecentAudit  *AdministrationAuditPage            `json:"recent_audit"`
}

type AdministrationCompanyStatistics struct {
	Total        int64 `json:"total"`
	Active       int64 `json:"active"`
	Suspended    int64 `json:"suspended"`
	WithoutOwner int64 `json:"without_owner"`
}

type PlatformAdministrationCapabilities struct {
	CreateCompanies        bool `json:"create_companies"`
	ManageCompanyLifecycle bool `json:"manage_company_lifecycle"`
	AssignOwners           bool `json:"assign_owners"`
	ReadGlobalAudit        bool `json:"read_global_audit"`
}

type PlatformAdministrationOverview struct {
	Capabilities               PlatformAdministrationCapabilities  `json:"capabilities"`
	Companies                  AdministrationCompanyStatistics     `json:"companies"`
	Users                      AdministrationUserStatistics        `json:"users"`
	UnitCount                  int64                               `json:"unit_count"`
	CompaniesWithStorageBudget int64                               `json:"companies_with_storage_budget"`
	Integrations               AdministrationIntegrationStatistics `json:"integrations"`
	RecentAudit                *AdministrationAuditPage            `json:"recent_audit"`
}
