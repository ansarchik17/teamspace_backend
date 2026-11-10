package domain

import (
	"time"

	"github.com/google/uuid"
)

// LoginLog represents a log of a user login event
type LoginLog struct {
	ID        uuid.UUID `json:"id" db:"id"`
	UserID    int64     `json:"user_id" db:"user_id"`
	Email     string    `json:"email" db:"email"`
	LoginTime time.Time `json:"login_time" db:"login_time"`

	// Joined fields for display
	UserName  *string `json:"username,omitempty" db:"-"`
	FirstName *string `json:"first_name,omitempty" db:"-"`
	LastName  *string `json:"last_name,omitempty" db:"-"`
}

// LoginAnalyticsResponse aggregates login data
type LoginAnalyticsResponse struct {
	TotalLogins int64       `json:"total_logins"`
	UniqueUsers int64       `json:"unique_users"`
	Logs        []*LoginLog `json:"logs"`
}

// LoginStatsRequest defines filters for analytics
type LoginStatsRequest struct {
	StartDate *time.Time `form:"start_date" time_format:"2006-01-02T15:04:05Z07:00"` // Full timestamp
	EndDate   *time.Time `form:"end_date" time_format:"2006-01-02T15:04:05Z07:00"`   // Full timestamp
	StartDay  *string    `form:"start_day"`                                          // Just date: "2026-02-10"
	EndDay    *string    `form:"end_day"`                                            // Just date: "2026-02-10"
	Limit     int        `form:"limit"`
	Offset    int        `form:"offset"`
}
