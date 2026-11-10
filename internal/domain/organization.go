package domain

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Organization struct {
	ID                 uuid.UUID `json:"id"`
	Name               string    `json:"name"`
	Slug               string    `json:"slug"`
	Status             string    `json:"status"`
	OwnerID            *int64    `json:"owner_id,omitempty"`
	OwnerName          string    `json:"owner_name,omitempty"`
	OwnerEmail         string    `json:"owner_email,omitempty"`
	CanManageStructure bool      `json:"can_manage_structure"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// PlatformOrganization is the view of a tenant available to platform
// administrators. It intentionally contains aggregate data only; customer
// resources remain behind organization-scoped endpoints.
type PlatformOrganization struct {
	Organization
	UserCount int `json:"user_count"`
	UnitCount int `json:"unit_count"`
}

type PlatformOrganizationList struct {
	Items  []PlatformOrganization `json:"items"`
	Total  int                    `json:"total"`
	Limit  int                    `json:"limit"`
	Offset int                    `json:"offset"`
}

// OrganizationOwnerCandidate is an active local user who can become the
// administrator of a customer organization. Platform administrators are not
// returned because they belong to the platform, not to a customer tenant.
type OrganizationOwnerCandidate struct {
	ID       int64  `json:"id"`
	FullName string `json:"full_name"`
	Email    string `json:"email"`
	Username string `json:"username"`
}

type AssignOrganizationOwnerRequest struct {
	UserID int64 `json:"user_id" binding:"required"`
}

type PlatformOrganizationFilter struct {
	Query  string
	Status string
	Limit  int
	Offset int
}

type UpdatePlatformOrganizationRequest struct {
	Name   *string `json:"name"`
	Status *string `json:"status"`
}

func (r UpdatePlatformOrganizationRequest) Validate() error {
	if r.Name != nil {
		name := strings.TrimSpace(*r.Name)
		if name == "" {
			return ErrOrganizationNameRequired
		}
		if len([]rune(name)) > 255 {
			return ErrOrganizationNameTooLong
		}
	}
	if r.Status != nil {
		status := strings.ToLower(strings.TrimSpace(*r.Status))
		if status != "active" && status != "suspended" {
			return ErrOrganizationStatusInvalid
		}
	}
	return nil
}

type CreateOrganizationRequest struct {
	Name string `json:"name" binding:"required"`
	Slug string `json:"slug"`
}

var (
	ErrOrganizationNameRequired  = errors.New("organization name is required")
	ErrOrganizationNameTooLong   = errors.New("organization name is too long")
	ErrOrganizationSlugInvalid   = errors.New("organization slug is invalid")
	ErrOrganizationStatusInvalid = errors.New("organization status is invalid")
)

func (r CreateOrganizationRequest) Normalize() (CreateOrganizationRequest, error) {
	r.Name = strings.TrimSpace(r.Name)
	r.Slug = strings.ToLower(strings.TrimSpace(r.Slug))
	if r.Name == "" {
		return r, ErrOrganizationNameRequired
	}
	if len([]rune(r.Name)) > 255 {
		return r, ErrOrganizationNameTooLong
	}
	if r.Slug == "" {
		r.Slug = strings.ToLower(strings.Join(strings.Fields(r.Name), "-"))
	}
	if len(r.Slug) > 63 {
		return r, ErrOrganizationSlugInvalid
	}
	if r.Slug == "" || r.Slug[0] == '-' || r.Slug[len(r.Slug)-1] == '-' {
		return r, ErrOrganizationSlugInvalid
	}
	reservedHosts := map[string]bool{
		"www": true, "mail": true, "inbox": true, "auth": true,
		"api": true, "meet": true, "growy": true, "lms": true,
		"admin": true, "static": true, "assets": true,
	}
	if reservedHosts[r.Slug] {
		return r, ErrOrganizationSlugInvalid
	}
	for _, ch := range r.Slug {
		if (ch < 'a' || ch > 'z') && (ch < '0' || ch > '9') && ch != '-' {
			return r, ErrOrganizationSlugInvalid
		}
	}
	return r, nil
}

// OrganizationUnit is a customer-defined node in the company structure.
// parent_unit_id makes the tree arbitrarily deep; unit_type is descriptive only.
type OrganizationUnit struct {
	ID             uuid.UUID          `json:"id"`
	OrganizationID uuid.UUID          `json:"organization_id"`
	ParentUnitID   *uuid.UUID         `json:"parent_unit_id,omitempty"`
	Name           string             `json:"name"`
	UnitType       string             `json:"unit_type"`
	Description    *string            `json:"description,omitempty"`
	ManagerID      *int64             `json:"manager_id,omitempty"`
	DisplayOrder   int                `json:"display_order"`
	CreatedAt      time.Time          `json:"created_at"`
	UpdatedAt      time.Time          `json:"updated_at"`
	Children       []OrganizationUnit `json:"children,omitempty"`
}

type OrganizationMember struct {
	Access             *ResolvedAccess `json:"access,omitempty"`
	ID                 int64           `json:"id"`
	KeycloakID         *string         `json:"keycloak_id,omitempty"`
	FullName           string          `json:"full_name"`
	FirstName          string          `json:"first_name"`
	LastName           string          `json:"last_name"`
	Email              string          `json:"email"`
	Username           string          `json:"username"`
	Position           *string         `json:"position,omitempty"`
	AvatarURL          *string         `json:"avatar_url,omitempty"`
	AvailabilityStatus *string         `json:"availability_status,omitempty"`
	UnitID             *uuid.UUID      `json:"unit_id,omitempty"`
	ManagerID          *int64          `json:"manager_id,omitempty"`
	Roles              []string        `json:"roles"`
	IsActive           bool            `json:"is_active"`
}

// GrowyPositionRole maps a company's existing job title to one of Growy's
// fixed roles. Position names remain company-specific; Growy role codes do not.
type GrowyPositionRole struct {
	Position   string   `json:"position"`
	GrowyRole  string   `json:"growy_role"`
	GrowyRoles []string `json:"growy_roles"`
	UserCount  int      `json:"user_count"`
}

type UpdateGrowyPositionRolesRequest struct {
	Mappings []GrowyPositionRole `json:"mappings"`
}

type GrowyHandoffDetails struct {
	OrganizationID   uuid.UUID
	OrganizationName string
	OrganizationSlug string
	Position         string
	GrowyRole        string
	GrowyRoles       []string
}

var GrowyRoleCodes = map[string]struct{}{
	"employee":     {},
	"manager":      {},
	"tenant_admin": {},
	"issuer":       {},
}

func (r UpdateGrowyPositionRolesRequest) Validate() error {
	seen := make(map[string]struct{}, len(r.Mappings))
	for i := range r.Mappings {
		position := strings.TrimSpace(r.Mappings[i].Position)
		roles := r.Mappings[i].GrowyRoles
		// Accept existing clients that still send a single role.
		if roles == nil && r.Mappings[i].GrowyRole != "" {
			roles = []string{r.Mappings[i].GrowyRole}
		}
		if position == "" || len([]rune(position)) > 255 {
			return errors.New("job title must contain between 1 and 255 characters")
		}
		if len(roles) == 0 || len(roles) > len(GrowyRoleCodes) {
			return errors.New("select between 1 and 4 Growy roles")
		}
		seenRoles := make(map[string]struct{}, len(roles))
		for j, role := range roles {
			role = strings.TrimSpace(role)
			if _, ok := GrowyRoleCodes[role]; !ok {
				return errors.New("Growy role must be employee, manager, tenant_admin, or issuer")
			}
			if _, ok := seenRoles[role]; ok {
				return errors.New("Growy roles must be unique")
			}
			seenRoles[role] = struct{}{}
			roles[j] = role
		}
		key := strings.ToLower(position)
		if _, ok := seen[key]; ok {
			return errors.New("job titles must be unique")
		}
		seen[key] = struct{}{}
		r.Mappings[i].Position = position
		r.Mappings[i].GrowyRoles = roles
		r.Mappings[i].GrowyRole = roles[0]
	}
	if len(r.Mappings) > 500 {
		return errors.New("no more than 500 job title mappings may be saved at once")
	}
	return nil
}

// AssignedGrowyRoles preserves compatibility with single-role integrations.
func (d GrowyHandoffDetails) AssignedGrowyRoles() []string {
	if len(d.GrowyRoles) > 0 {
		return d.GrowyRoles
	}
	return []string{d.GrowyRole}
}

type CreateOrganizationUnitRequest struct {
	ParentUnitID *uuid.UUID `json:"parent_unit_id"`
	Name         string     `json:"name" binding:"required"`
	UnitType     string     `json:"unit_type"`
	Description  *string    `json:"description"`
	ManagerID    *int64     `json:"manager_id"`
	DisplayOrder int        `json:"display_order"`
}

type UpdateOrganizationUnitRequest struct {
	ParentUnitID *uuid.UUID `json:"parent_unit_id"`
	ClearParent  bool       `json:"clear_parent"`
	Name         *string    `json:"name"`
	UnitType     *string    `json:"unit_type"`
	Description  *string    `json:"description"`
	ManagerID    *int64     `json:"manager_id"`
	ClearManager bool       `json:"clear_manager"`
	DisplayOrder *int       `json:"display_order"`
}

type AssignUserToUnitRequest struct {
	UnitID    *uuid.UUID `json:"unit_id"`
	ManagerID *int64     `json:"manager_id"`
}

func (r CreateOrganizationUnitRequest) Normalize() (CreateOrganizationUnitRequest, error) {
	r.Name = strings.TrimSpace(r.Name)
	r.UnitType = strings.TrimSpace(r.UnitType)
	if r.UnitType == "" {
		r.UnitType = "team"
	}
	if r.Name == "" {
		return r, ErrOrganizationUnitNameRequired
	}
	if len([]rune(r.Name)) > 255 {
		return r, ErrOrganizationUnitNameTooLong
	}
	if len([]rune(r.UnitType)) > 64 {
		return r, ErrOrganizationUnitTypeTooLong
	}
	if r.DisplayOrder < 0 {
		r.DisplayOrder = 0
	}
	return r, nil
}

func (r UpdateOrganizationUnitRequest) Validate() error {
	if r.ClearParent && r.ParentUnitID != nil {
		return ErrOrganizationUnitParentConflict
	}
	if r.ClearManager && r.ManagerID != nil {
		return ErrOrganizationUnitManagerConflict
	}
	if r.UnitType != nil && strings.TrimSpace(*r.UnitType) == "" {
		return ErrOrganizationUnitTypeRequired
	}
	if r.Name != nil {
		name := strings.TrimSpace(*r.Name)
		if name == "" {
			return ErrOrganizationUnitNameRequired
		}
		if len([]rune(name)) > 255 {
			return ErrOrganizationUnitNameTooLong
		}
	}
	if r.UnitType != nil && len([]rune(strings.TrimSpace(*r.UnitType))) > 64 {
		return ErrOrganizationUnitTypeTooLong
	}
	if r.DisplayOrder != nil && *r.DisplayOrder < 0 {
		return ErrOrganizationUnitOrderInvalid
	}
	return nil
}
