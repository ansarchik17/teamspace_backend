package domain

import (
	"time"

	"github.com/google/uuid"
)

type PermissionRole string

const (
	RoleViewer PermissionRole = "viewer"
	RoleEditor PermissionRole = "editor"
	RoleOwner  PermissionRole = "owner"
)

type ResourceType string

const (
	ResourceFile   ResourceType = "file"
	ResourceFolder ResourceType = "folder"
)

type Permission struct {
	ID           int64          `json:"id" db:"id"`
	ResourceType ResourceType   `json:"resource_type" db:"resource_type"`
	ResourceID   uuid.UUID      `json:"resource_id" db:"resource_id"`
	UserID       int64          `json:"user_id" db:"user_id"`
	Role         PermissionRole `json:"role" db:"role"`
	CreatedAt    time.Time      `json:"created_at" db:"created_at"`
}

type UserBasicListResponse struct {
	Items  []UserBasic `json:"items"`
	Total  int         `json:"total"`
	Page   int         `json:"page"`
	Limit  int         `json:"limit"`
	Offset int         `json:"offset"`
}

type FileVersion struct {
	ID         uuid.UUID `json:"id" db:"id"`
	FileID     uuid.UUID `json:"file_id" db:"file_id"`
	Version    int       `json:"version" db:"version"`
	ObjectName string    `json:"-" db:"object_name"`
	Size       int64     `json:"size" db:"size"`
	CreatedBy  int64     `json:"created_by" db:"created_by"`
	CreatedAt  time.Time `json:"created_at" db:"created_at"`
}
