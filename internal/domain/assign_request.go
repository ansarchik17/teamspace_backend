package domain

type AssignUserRequest struct {
	UserID       int64  `json:"user_id" binding:"required"`
	MinistryID   *int64 `json:"ministry_id"`
	StateBodyID  *int64 `json:"state_body_id"`
	DepartmentID *int64 `json:"department_id"`
	DivisionID   *int64 `json:"division_id"`
}

type AssignLeaderRequest struct {
	UserID   int64  `json:"user_id" binding:"required"`
	UnitID   int64  `json:"unit_id" binding:"required"`
	UnitType string `json:"unit_type" binding:"required"` // ministry, state_body, department, division
}
