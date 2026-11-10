package domain

import (
	"time"

	"github.com/google/uuid"
)

type UserDevice struct {
	ID          uuid.UUID  `json:"id"`
	UserID      int64      `json:"user_id"`
	FCMToken    string     `json:"fcm_token"`
	DeviceType  string     `json:"device_type"`
	Platform    string     `json:"platform"`
	DeviceID    string     `json:"device_id,omitempty"`
	VoIPToken   *string    `json:"voip_token,omitempty"`
	Active      bool       `json:"active"`
	LastUsedAt  time.Time  `json:"last_used_at"`
	LoggedOutAt *time.Time `json:"logged_out_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`

	OSVersion   *string    `json:"os_version,omitempty"`
	AppVersion  *string    `json:"app_version,omitempty"`
	BuildNumber *string    `json:"build_number,omitempty"`
	LastSeenAt  *time.Time `json:"last_seen_at,omitempty"`
}
type AdminUserListItem struct {
	UserID           int64      `json:"user_id"`
	FullName         string     `json:"full_name"`
	Email            string     `json:"email"`
	Username         string     `json:"username"`
	PositionName     string     `json:"position_name"`
	OrganizationName string     `json:"organization_name"`
	LastLoginAt      *time.Time `json:"last_login_at"`
	LastSeenAt       *time.Time `json:"last_seen_at"`
	IsOnline         bool       `json:"is_online"`
	Platform         string     `json:"platform"`
	DeviceType       string     `json:"device_type"`
	OSVersion        *string    `json:"os_version"`
	AppVersion       *string    `json:"app_version"`
	BuildNumber      *string    `json:"build_number"`
}
