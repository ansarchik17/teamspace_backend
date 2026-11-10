package domain

type AdministrationUser struct {
	ID         int64   `json:"id"`
	FullName   string  `json:"full_name"`
	Username   string  `json:"username"`
	Email      string  `json:"email"`
	Position   *string `json:"position,omitempty"`
	Role       Role    `json:"role"`
	IsActive   bool    `json:"is_active"`
	IsBlocked  bool    `json:"is_blocked"`
	IsDirector bool    `json:"is_director"`
}
type AdministrationUserFilter struct {
	Query  string
	Limit  int
	Offset int
}
type AdministrationUserPage struct {
	Items  []AdministrationUser `json:"items"`
	Total  int                  `json:"total"`
	Limit  int                  `json:"limit"`
	Offset int                  `json:"offset"`
}
type AdministrationUserStatusRequest struct {
	Blocked *bool `json:"blocked"`
}

func CanChangeEmployeeBlock(actor, target int64, role Role, owner *int64) bool {
	return target > 0 && actor != target && role != RoleSuperAdmin && (owner == nil || *owner != target)
}
