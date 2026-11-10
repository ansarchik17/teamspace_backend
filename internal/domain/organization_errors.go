package domain

import "errors"

var (
	ErrOrganizationUnitTypeRequired    = errors.New("organization unit type is required")
	ErrOrganizationUnitParentConflict  = errors.New("parent_unit_id and clear_parent cannot be combined")
	ErrOrganizationUnitManagerConflict = errors.New("manager_id and clear_manager cannot be combined")
	ErrOrganizationNotFound            = errors.New("organization not found")
	ErrOrganizationUnitNameRequired    = errors.New("organization unit name is required")
	ErrOrganizationUnitNameTooLong     = errors.New("organization unit name is too long")
	ErrOrganizationUnitTypeTooLong     = errors.New("organization unit type is too long")
	ErrOrganizationUnitOrderInvalid    = errors.New("organization unit order cannot be negative")
)
