package domain

import "github.com/google/uuid"

type FolderAccessReportEntry struct {
	FolderID   uuid.UUID      `json:"folder_id"`
	FolderName string         `json:"folder_name"`
	FolderPath string         `json:"folder_path"`
	UserID     int64          `json:"user_id"`
	FullName   string         `json:"full_name"`
	Email      string         `json:"email"`
	Username   string         `json:"username"`
	FirstName  string         `json:"first_name"`
	LastName   string         `json:"last_name"`
	Role       PermissionRole `json:"role"`
}
