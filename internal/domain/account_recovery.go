package domain

import (
	"github.com/google/uuid"
	"time"
)

type RecoveryEmailState struct {
	Email      *string                 `json:"email"`
	VerifiedAt *time.Time              `json:"verified_at"`
	Pending    *RecoveryEmailChallenge `json:"pending"`
}
type RecoveryEmailChallenge struct {
	ID        uuid.UUID `json:"id"`
	Email     string    `json:"email"`
	Purpose   string    `json:"purpose"`
	ExpiresAt time.Time `json:"expires_at"`
	RetryAt   time.Time `json:"retry_at"`
}
type PasswordRecovery struct {
	Locale        string
	ID            uuid.UUID
	UserID        int64
	Email         string
	EmailVersion  uuid.UUID
	State         string
	Attempts      int
	KeycloakID    string
	InvalidBefore time.Time
}
