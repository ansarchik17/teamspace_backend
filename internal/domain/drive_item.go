package domain

import "time"

type DriveItem struct {
	Type   ResourceType `json:"type" example:"folder"` // "folder" | "file"
	Folder *Folder      `json:"folder,omitempty"`
	File   *DriveFile   `json:"file,omitempty"`
}

type ListAllDriveResp struct {
	Own    []DriveItem `json:"own"`
	Shared []DriveItem `json:"shared"`
}

type RecentItem struct {
	LastAccessedAt time.Time `json:"last_accessed_at"`
	DriveItem
}
