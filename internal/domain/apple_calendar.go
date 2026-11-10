package domain

import (
	"time"

	"github.com/google/uuid"
)

// AppleCalendarSyncStatus represents the sync status for a user
type AppleCalendarSyncStatus struct {
	UserID                   int64   `json:"user_id" db:"user_id"`
	AppleCalendarEmail       *string `json:"apple_calendar_email" db:"apple_calendar_email"`
	AppleAppSpecificPassword *string `json:"-" db:"apple_app_specific_password"` // never send to client
	AppleCalendarSyncEnabled bool    `json:"apple_calendar_sync_enabled" db:"apple_calendar_sync_enabled"`
	AppleCalendarID          *string `json:"apple_calendar_id" db:"apple_calendar_id"`
}

// AppleCalendarEventMapping represents the mapping between our events and Apple Calendar events
type AppleCalendarEventMapping struct {
	ID              uuid.UUID `json:"id" db:"id"`
	EventID         uuid.UUID `json:"event_id" db:"event_id"`
	UserID          int64     `json:"user_id" db:"user_id"`
	AppleEventID    string    `json:"apple_event_id" db:"apple_event_id"`
	AppleCalendarID string    `json:"apple_calendar_id" db:"apple_calendar_id"`
	LastSyncedAt    time.Time `json:"last_synced_at" db:"last_synced_at"`
	CreatedAt       time.Time `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time `json:"updated_at" db:"updated_at"`
}

// ConnectAppleCalendarRequest is the request to connect Apple Calendar
type ConnectAppleCalendarRequest struct {
	Email               string `json:"email" binding:"required,email"`
	AppSpecificPassword string `json:"app_specific_password" binding:"required"`
}

// AppleCalendarSyncStatusResponse is what we return to the client
type AppleCalendarSyncStatusResponse struct {
	SyncEnabled     bool    `json:"sync_enabled"`
	AppleCalendarID *string `json:"apple_calendar_id,omitempty"`
	AppleEmail      *string `json:"apple_email,omitempty"`
	IsConnected     bool    `json:"is_connected"`
}

// AppleCalendar represents a calendar found in iCloud
type AppleCalendar struct {
	ID          string `json:"id"` // The path/href
	Name        string `json:"name"`
	Description string `json:"description"`
	Color       string `json:"color"`
}
