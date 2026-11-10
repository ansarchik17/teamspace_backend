package domain

import "github.com/google/uuid"

type OrganizationTreeManager struct {
	ID        int64   `json:"id"`
	FullName  string  `json:"full_name"`
	Position  *string `json:"position,omitempty"`
	AvatarURL *string `json:"avatar_url,omitempty"`
}

type OrganizationTreeNode struct {
	ID                   uuid.UUID                `json:"id"`
	ParentUnitID         *uuid.UUID               `json:"parent_unit_id,omitempty"`
	Name                 string                   `json:"name"`
	UnitType             string                   `json:"unit_type"`
	Description          *string                  `json:"description,omitempty"`
	DisplayOrder         int                      `json:"display_order"`
	Manager              *OrganizationTreeManager `json:"manager,omitempty"`
	EmployeeCount        int                      `json:"employee_count"`
	SubtreeEmployeeCount int                      `json:"subtree_employee_count"`
	Children             []*OrganizationTreeNode  `json:"children"`
}

type OrganizationTree struct {
	OrganizationID          uuid.UUID               `json:"organization_id"`
	CanManageStructure      bool                    `json:"can_manage_structure"`
	Items                   []*OrganizationTreeNode `json:"items"`
	TotalUnits              int                     `json:"total_units"`
	ActiveEmployeeCount     int                     `json:"active_employee_count"`
	UnassignedEmployeeCount int                     `json:"unassigned_employee_count"`
}
