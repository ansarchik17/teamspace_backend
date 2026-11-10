package domain

import (
	"time"

	"github.com/google/uuid"
)

type DriveFavoriteRef struct {
	ResourceType ResourceType
	ResourceID   uuid.UUID
	CreatedAt    time.Time
}

type DriveSummary struct {
	Storage        *DriveStorage `json:"storage"`
	UsedBytes      int64         `json:"used_bytes"`
	FilesCount     int64         `json:"files_count"`
	FoldersCount   int64         `json:"folders_count"`
	SharedCount    int64         `json:"shared_count"`
	FavoritesCount int64         `json:"favorites_count"`
	TrashCount     int64         `json:"trash_count"`
}

type DriveTrashItem struct {
	DeletedAt time.Time `json:"deleted_at"`
	ExpiresAt time.Time `json:"expires_at"`
	DriveItem
}
