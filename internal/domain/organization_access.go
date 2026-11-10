package domain

import "github.com/google/uuid"

type AccessPermission struct {
	Code        string `json:"code"`
	Group       string `json:"group"`
	Name        string `json:"name"`
	Description string `json:"description"`
}
type AccessPolicy struct {
	Permissions []string `json:"permissions"`
	Revision    int64    `json:"revision"`
	Configured  bool     `json:"configured"`
}
type AccessPosition struct {
	ID        uuid.UUID               `json:"id"`
	Name      string                  `json:"name"`
	Revision  int64                   `json:"revision"`
	UserCount int                     `json:"user_count"`
	Policies  map[string]AccessPolicy `json:"policies"`
}
type AccessApp struct {
	Available     bool  `json:"available"`
	ModuleEnabled bool  `json:"module_enabled"`
	Enabled       bool  `json:"enabled"`
	Revision      int64 `json:"revision"`
}
type OrganizationAccess struct {
	MemberPositions map[string]string             `json:"member_positions"`
	Positions       []AccessPosition              `json:"positions"`
	Catalog         map[string][]AccessPermission `json:"catalog"`
	Dependencies    map[string][]AccessDependency `json:"dependencies"`
	Unavailable     map[string][]string           `json:"unavailable_permissions"`
	Apps            map[string]AccessApp          `json:"apps"`
	SDCompanyID     *uuid.UUID                    `json:"sd_company_id"`
}
type ResolvedAccess struct {
	ModuleAvailable    bool     `json:"module_available"`
	ModuleEnabled      bool     `json:"module_enabled"`
	DepartmentID       string   `json:"department_id"`
	AvatarURL          string   `json:"avatar_url"`
	Roles              []string `json:"roles"`
	OrganizationActive bool     `json:"organization_active"`
	Linked             bool     `json:"linked"`
	Active             bool     `json:"active"`
	Managed            bool     `json:"managed"`
	OrganizationID     string   `json:"organization_id"`
	Position           string   `json:"position"`
	Permissions        []string `json:"permissions"`
}

type AccessSDCompany struct {
	ID           uuid.UUID `json:"id"`
	Name         string    `json:"name"`
	IsTenantRoot bool      `json:"is_tenant_root"`
}

type SDWorkspaceIdentity struct {
	UserID    int64     `json:"user_id"`
	Linked    bool      `json:"linked"`
	Active    bool      `json:"active"`
	CompanyID uuid.UUID `json:"company_id"`
}

// SDHandoffProfile is read from the authenticated user's current company membership.
type SDHandoffProfile struct {
	UserID    int64     `json:"user_id"`
	Subject   string    `json:"subject"`
	CompanyID uuid.UUID `json:"company_id"`
	Username  string    `json:"username"`
	Email     string    `json:"email"`
	FirstName string    `json:"first_name"`
	LastName  string    `json:"last_name"`
}

type SDCompanyProvision struct {
	OrganizationID uuid.UUID        `json:"organization_id"`
	Name           string           `json:"name"`
	Slug           string           `json:"slug"`
	Address        string           `json:"address"`
	Owner          SDHandoffProfile `json:"owner"`
	LinkedCompany  *AccessSDCompany `json:"-"`
}
