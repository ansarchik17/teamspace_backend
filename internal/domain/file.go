package domain

import (
	"time"

	"github.com/google/uuid"
)

type Folder struct {
	ID            uuid.UUID  `json:"id" db:"id"`
	OwnerID       int64      `json:"owner_id" db:"owner_id"`
	OwnerFio      string     `json:"owner_fio,omitempty" db:"owner_fio"` // Owner full name
	ParentID      *uuid.UUID `json:"parent_id,omitempty" db:"parent_id"`
	Name          string     `json:"name" db:"name"`
	IsPublic      bool       `json:"is_public" db:"is_public"`
	CreatedAt     time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at" db:"updated_at"`
	DeletedAt     *time.Time `json:"-" db:"deleted_at"`
	FilesCount    int        `json:"files_count"`
	HasSubfolders bool       `json:"has_subfolders"`
}

type DriveFile struct {
	Tags              []string   `json:"tags,omitempty"`
	ID                uuid.UUID  `json:"id" db:"id"`
	OwnerID           int64      `json:"owner_id" db:"owner_id"`
	OwnerFio          string     `json:"owner_fio,omitempty" db:"owner_fio"` // Owner full name
	FolderID          *uuid.UUID `json:"folder_id,omitempty" db:"folder_id"`
	RealName          string     `json:"real_name" db:"real_name"`
	ObjectName        string     `json:"-" db:"object_name"`
	ContentHash       string     `json:"-" db:"content_hash"`
	SourceSystemCode  *string    `json:"source_system_code,omitempty" db:"source_system_code"`
	SourceSystemName  *string    `json:"source_system_name,omitempty" db:"source_system_name"`
	ExternalMessageID *string    `json:"external_message_id,omitempty" db:"external_message_id"`
	DownloadURL       string     `json:"download_url" db:"-"`
	Size              int64      `json:"size" db:"size"`
	MimeType          string     `json:"mime_type" db:"mime_type"`
	Version           int        `json:"version" db:"version"`
	Scope             string     `json:"scope" db:"scope"`
	IsPublic          bool       `json:"is_public" db:"is_public"`
	Key               *string    `json:"key,omitempty" db:"onlyoffice_key"`
	CreatedAt         time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at" db:"updated_at"`
	DeletedAt         *time.Time `json:"-" db:"deleted_at"`
}

const (
	LinkRoleViewer = "viewer"
	LinkRoleOwner  = "owner"
	LinkRoleEditor = "editor"
)

type DriveSharedLink struct {
	Token        uuid.UUID  `json:"token" db:"token"`
	ResourceType string     `json:"resource_type" db:"resource_type"`
	ResourceID   uuid.UUID  `json:"resource_id" db:"resource_id"`
	CreatedBy    int64      `json:"created_by" db:"created_by"`
	Role         string     `json:"role" db:"role"`
	ExpiresAt    *time.Time `json:"expires_at" db:"expires_at"`
	CreatedAt    time.Time  `json:"created_at" db:"created_at"`
}

// PublicDriveItem wraps the public drive API response.
type PublicDriveItem struct {
	Children   []DriveItem `json:"children,omitempty"`
	Token      uuid.UUID   `json:"token"`
	AccessRole string      `json:"access_role"`
	ItemType   string      `json:"type"` // file/folder
	File       *DriveFile  `json:"file,omitempty"`
	Folder     *Folder     `json:"folder,omitempty"`
}
