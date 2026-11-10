package domain

import "time"

// MinistryUserExportRow is a flattened row for ministry user export reports.
type MinistryUserExportRow struct {
	UserID         int64
	FIO            string
	Email          string
	Username       string
	LastLoginAt    *time.Time
	MinistryID     int64
	MinistryName   string
	StateBodyID    *int64
	StateBodyName  string
	DepartmentID   *int64
	DepartmentName string
	DivisionID     *int64
	DivisionName   string
	PositionName   string
	IsActive       bool
}
