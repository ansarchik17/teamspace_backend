package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type DriveActivityAction string

const (
	DriveActivityActionDownloaded DriveActivityAction = "downloaded"
	DriveActivityActionCreated    DriveActivityAction = "created"
	DriveActivityActionUploaded   DriveActivityAction = "uploaded"
	DriveActivityActionRenamed    DriveActivityAction = "renamed"
	DriveActivityActionMoved      DriveActivityAction = "moved"
	DriveActivityActionEdited     DriveActivityAction = "edited"
	DriveActivityActionDeleted    DriveActivityAction = "deleted"
	DriveActivityActionCopied     DriveActivityAction = "copied"
	DriveActivityActionRestored   DriveActivityAction = "restored"
)

type DriveActivity struct {
	ID           uuid.UUID           `json:"id"`
	Action       DriveActivityAction `json:"action"`
	ResourceType ResourceType        `json:"resource_type"`
	ResourceID   uuid.UUID           `json:"resource_id"`
	ResourceName string              `json:"resource_name"`
	OwnerID      int64               `json:"owner_id"`
	ActorID      int64               `json:"actor_id"`
	FolderID     *uuid.UUID          `json:"folder_id,omitempty"`
	FromFolderID *uuid.UUID          `json:"from_folder_id,omitempty"`
	ToFolderID   *uuid.UUID          `json:"to_folder_id,omitempty"`
	ResourcePath string              `json:"resource_path,omitempty"`
	FromPath     string              `json:"from_path,omitempty"`
	ToPath       string              `json:"to_path,omitempty"`
	Metadata     json.RawMessage     `json:"metadata,omitempty"`
	CreatedAt    time.Time           `json:"created_at"`
}

type DriveActivityEntry struct {
	ID            uuid.UUID           `json:"id"`
	Action        DriveActivityAction `json:"action"`
	ResourceType  ResourceType        `json:"resource_type"`
	ResourceID    uuid.UUID           `json:"resource_id"`
	ResourceName  string              `json:"resource_name"`
	OwnerID       int64               `json:"owner_id"`
	ActorID       int64               `json:"actor_id"`
	ActorFullName string              `json:"actor_full_name"`
	ActorUsername string              `json:"actor_username"`
	ActorEmail    string              `json:"actor_email"`
	FolderID      *uuid.UUID          `json:"folder_id,omitempty"`
	FromFolderID  *uuid.UUID          `json:"from_folder_id,omitempty"`
	ToFolderID    *uuid.UUID          `json:"to_folder_id,omitempty"`
	ResourcePath  string              `json:"resource_path,omitempty"`
	FromPath      string              `json:"from_path,omitempty"`
	ToPath        string              `json:"to_path,omitempty"`
	Metadata      json.RawMessage     `json:"metadata,omitempty"`
	CreatedAt     time.Time           `json:"created_at"`
}

// DriveActivityEntryDoc mirrors DriveActivityEntry for Swagger generation.
// json.RawMessage is represented as a generic object because swag cannot resolve it directly.
type DriveActivityEntryDoc struct {
	ID            uuid.UUID              `json:"id"`
	Action        DriveActivityAction    `json:"action"`
	ResourceType  ResourceType           `json:"resource_type"`
	ResourceID    uuid.UUID              `json:"resource_id"`
	ResourceName  string                 `json:"resource_name"`
	OwnerID       int64                  `json:"owner_id"`
	ActorID       int64                  `json:"actor_id"`
	ActorFullName string                 `json:"actor_full_name"`
	ActorUsername string                 `json:"actor_username"`
	ActorEmail    string                 `json:"actor_email"`
	FolderID      *uuid.UUID             `json:"folder_id,omitempty"`
	FromFolderID  *uuid.UUID             `json:"from_folder_id,omitempty"`
	ToFolderID    *uuid.UUID             `json:"to_folder_id,omitempty"`
	ResourcePath  string                 `json:"resource_path,omitempty"`
	FromPath      string                 `json:"from_path,omitempty"`
	ToPath        string                 `json:"to_path,omitempty"`
	Metadata      map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt     time.Time              `json:"created_at"`
}

type DriveActivityListResponse struct {
	Items      []DriveActivityEntry `json:"items"`
	Page       int                  `json:"page"`
	Limit      int                  `json:"limit"`
	TotalCount int                  `json:"total_count"`
	TotalPages int                  `json:"total_pages"`
	HasNext    bool                 `json:"has_next"`
	HasPrev    bool                 `json:"has_prev"`
}

type DriveActivityListResponseDoc struct {
	Items      []DriveActivityEntryDoc `json:"items"`
	Page       int                     `json:"page"`
	Limit      int                     `json:"limit"`
	TotalCount int                     `json:"total_count"`
	TotalPages int                     `json:"total_pages"`
	HasNext    bool                    `json:"has_next"`
	HasPrev    bool                    `json:"has_prev"`
}

type DriveActivityListFilter struct {
	ViewerUserID     int64
	OwnerID          int64
	ActorID          *int64
	FolderID         *uuid.UUID
	FolderPathPrefix string
	Limit            int
	Offset           int
}

type DriveActivityAccessProfile struct {
	CanViewAll     bool
	ManualOverride bool
	PositionBased  bool
	PositionName   string
}
