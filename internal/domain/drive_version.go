package domain

import (
	"github.com/google/uuid"
	"time"
)

// DriveFileVersion never exposes the storage key to clients.
type DriveFileVersion struct {
	ID            uuid.UUID `json:"id"`
	FileID        uuid.UUID `json:"file_id"`
	Version       int       `json:"version"`
	ObjectName    string    `json:"-"`
	Size          int64     `json:"size"`
	CreatedBy     int64     `json:"created_by"`
	CreatedByName string    `json:"created_by_name"`
	CreatedAt     time.Time `json:"created_at"`
	IsCurrent     bool      `json:"is_current"`
}

const DriveTrashRetention = 30 * 24 * time.Hour
