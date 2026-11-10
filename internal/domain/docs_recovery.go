package domain

import "time"

// OfficeSaveState reports a durable checkpoint, never unsaved browser edits.
type OfficeSaveState struct {
	Version     int        `json:"version"`
	SavedAt     time.Time  `json:"saved_at"`
	FailureKind *string    `json:"failure_kind,omitempty"`
	FailedAt    *time.Time `json:"failed_at,omitempty"`
}

type OfficeRecoveryRequest struct {
	ExpectedVersion int `json:"expected_version" binding:"required,min=1"`
}
