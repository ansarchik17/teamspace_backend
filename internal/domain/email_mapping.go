package domain

import (
	"time"

	"github.com/google/uuid"
)

// EmailMapping represents a mapping from an incoming email to a target email
type EmailMapping struct {
	ID            uuid.UUID `json:"id" db:"id"`
	IncomingEmail string    `json:"incoming_email" db:"incoming_email"`
	TargetEmail   string    `json:"target_email" db:"target_email"`
	UserID        *int64    `json:"user_id,omitempty" db:"user_id"`
	IsActive      bool      `json:"is_active" db:"is_active"`
	CreatedAt     time.Time `json:"created_at" db:"created_at"`
	UpdatedAt     time.Time `json:"updated_at" db:"updated_at"`
}
