package domain

import "github.com/google/uuid"

// Contact exposes directory fields only, without identity-provider IDs or roles.
type Contact struct {
	ID                 int64      `json:"id"`
	FullName           string     `json:"full_name"`
	Email              string     `json:"email"`
	Username           string     `json:"username"`
	Position           *string    `json:"position,omitempty"`
	AvatarURL          *string    `json:"avatar_url,omitempty"`
	AvailabilityStatus *string    `json:"availability_status,omitempty"`
	UnitID             *uuid.UUID `json:"unit_id,omitempty"`
	UnitName           *string    `json:"unit_name,omitempty"`
	ManagerID          *int64     `json:"manager_id,omitempty"`
}

type ContactFilter struct {
	IDs       []int64
	ManagerID *int64
	Query     string
	UnitID    *uuid.UUID
	Position  string
	Limit     int
	Offset    int
}

type ContactList struct {
	Items  []Contact `json:"items"`
	Total  int       `json:"total"`
	Limit  int       `json:"limit"`
	Offset int       `json:"offset"`
}

type ContactUnit struct {
	ID           uuid.UUID  `json:"id"`
	ParentUnitID *uuid.UUID `json:"parent_unit_id,omitempty"`
	Name         string     `json:"name"`
	UnitType     string     `json:"unit_type"`
}

type ContactFilters struct {
	Units     []ContactUnit `json:"units"`
	Positions []string      `json:"positions"`
}

// ContactProfile contains only information intended for the company directory.
type ContactProfile struct {
	Contact
	Actions            *ContactActions `json:"actions"`
	WorkEmail          *string         `json:"work_email,omitempty"`
	Phone              *string         `json:"phone,omitempty"`
	PersonalEmail      *string         `json:"personal_email,omitempty"`
	PersonalPhone      *string         `json:"personal_phone,omitempty"`
	Privacy            *ContactPrivacy `json:"privacy,omitempty"`
	Manager            *Contact        `json:"manager,omitempty"`
	ManagementChain    []Contact       `json:"management_chain"`
	HierarchyTruncated bool            `json:"hierarchy_truncated"`
	DirectReportsTotal int             `json:"direct_reports_total"`
}

type UpdateContactRequest struct {
	WorkEmail               *string            `json:"work_email"`
	Phone                   *string            `json:"phone"`
	PersonalEmail           *string            `json:"personal_email"`
	PersonalPhone           *string            `json:"personal_phone"`
	PersonalEmailVisibility *ContactVisibility `json:"personal_email_visibility" enums:"private,admins,managers,company"`
	PersonalPhoneVisibility *ContactVisibility `json:"personal_phone_visibility" enums:"private,admins,managers,company"`
}
