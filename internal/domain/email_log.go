package domain

import (
	"time"

	"github.com/google/uuid"
)

// EmailLog represents a log of an incoming email notification
type EmailLog struct {
	ID               uuid.UUID  `json:"id" db:"id"`
	SentEmail        string     `json:"sent_email" db:"sent_email"`
	MatchedUserID    *int64     `json:"matched_user_id,omitempty" db:"matched_user_id"`
	MatchedUserEmail *string    `json:"matched_user_email,omitempty" db:"-"` // Filled via JOIN
	MatchedUserName  *string    `json:"matched_user_name,omitempty" db:"-"`  // Filled via JOIN
	Content          string     `json:"content" db:"content"`
	Status           string     `json:"status" db:"status"`                 // matched, unmatched
	Timestamp        *time.Time `json:"timestamp,omitempty" db:"timestamp"` // Email sent time
	CreatedAt        time.Time  `json:"created_at" db:"created_at"`
}

// IncomingEmailRequest represents the payload for the incoming email endpoint
type IncomingEmailRequest struct {
	Email     string     `json:"email" binding:"required,email"`
	Text      string     `json:"text"`
	Timestamp *time.Time `json:"timestamp"`
}

type CreateEmailMappingRequest struct {
	IncomingEmail string `json:"incoming_email" binding:"required,email"`
	TargetEmail   string `json:"target_email" binding:"required,email"`
}
