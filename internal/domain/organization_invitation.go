package domain

import (
	"github.com/google/uuid"
	"time"
)

type OrganizationInvitation struct {
	ID                uuid.UUID  `json:"id"`
	OrganizationID    uuid.UUID  `json:"organization_id"`
	OrganizationName  string     `json:"organization_name"`
	Email             string     `json:"email"`
	TeamID            *uuid.UUID `json:"team_id,omitempty"`
	Status            string     `json:"status"`
	ExpiresAt         time.Time  `json:"expires_at"`
	CreatedAt         time.Time  `json:"created_at"`
	AcceptedAt        *time.Time `json:"accepted_at,omitempty"`
	AcceptedBy        *int64     `json:"accepted_by,omitempty"`
	RegisteredAccount bool       `json:"-"`
	CodeHash          *string    `json:"-"`
	CodeExpiresAt     *time.Time `json:"-"`
	CodeSentAt        *time.Time `json:"-"`
	CodeAttempts      int        `json:"-"`
	ResumableIDs      []string   `json:"-"`
}

type CreateInvitationRequest struct {
	Email  string     `json:"email" binding:"required,email,max=254"`
	TeamID *uuid.UUID `json:"team_id"`
}

type InvitationPage struct {
	Items []OrganizationInvitation `json:"items"`
	Total int                      `json:"total"`
}

type InvitationResult struct {
	OrganizationID uuid.UUID `json:"organization_id"`
	UserID         int64     `json:"user_id"`
	AlreadyMember  bool      `json:"already_member"`
	Registered     bool      `json:"registered"`
}
