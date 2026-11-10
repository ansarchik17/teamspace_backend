package domain

import (
	"time"

	"github.com/google/uuid"
)

// GoogleCalendarSyncStatus represents the sync status for a user
type GoogleCalendarSyncStatus struct {
	UserID                    int64      `json:"user_id" db:"user_id"`
	GoogleAccessToken         *string    `json:"-" db:"google_access_token"`  // never send to client
	GoogleRefreshToken        *string    `json:"-" db:"google_refresh_token"` // never send to client
	GoogleTokenExpiry         *time.Time `json:"-" db:"google_token_expiry"`
	GoogleCalendarSyncEnabled bool       `json:"google_calendar_sync_enabled" db:"google_calendar_sync_enabled"`
	GoogleCalendarID          *string    `json:"google_calendar_id" db:"google_calendar_id"`
}

// GoogleCalendarEventMapping represents the mapping between our events and Google Calendar events
type GoogleCalendarEventMapping struct {
	ID               uuid.UUID `json:"id" db:"id"`
	EventID          uuid.UUID `json:"event_id" db:"event_id"`
	UserID           int64     `json:"user_id" db:"user_id"`
	GoogleEventID    string    `json:"google_event_id" db:"google_event_id"`
	GoogleCalendarID string    `json:"google_calendar_id" db:"google_calendar_id"`
	LastSyncedAt     time.Time `json:"last_synced_at" db:"last_synced_at"`
	CreatedAt        time.Time `json:"created_at" db:"created_at"`
	UpdatedAt        time.Time `json:"updated_at" db:"updated_at"`
}

// EnableGoogleCalendarSyncRequest is the request to enable Google Calendar sync
type EnableGoogleCalendarSyncRequest struct {
	Code string `json:"code" binding:"required"` // OAuth code from Google
}

// GoogleCalendarSyncStatusResponse is what we return to the client
type GoogleCalendarSyncStatusResponse struct {
	SyncEnabled      bool    `json:"sync_enabled"`
	GoogleCalendarID *string `json:"google_calendar_id,omitempty"`
	GoogleEmail      *string `json:"google_email,omitempty"`
	IsConnected      bool    `json:"is_connected"`
}

// GoogleAuthURLResponse contains the URL for OAuth
type GoogleAuthURLResponse struct {
	AuthURL string `json:"auth_url"`
}
