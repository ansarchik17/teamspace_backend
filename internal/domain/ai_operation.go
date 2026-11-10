package domain

import "github.com/google/uuid"

type AIOperationDisposition string

const (
	AIOperationCreated        AIOperationDisposition = "created"
	AIOperationInProgress     AIOperationDisposition = "in_progress"
	AIOperationCompleted      AIOperationDisposition = "completed"
	AIOperationStopped        AIOperationDisposition = "stopped"
	AIOperationReconciliation AIOperationDisposition = "reconciliation_required"
)

// AIOperationLookup identifies the logical request, independently of current
// routing, model policy, token estimates and provider attempt number.
// The key is stable across transport retries; Fingerprint covers the original
// user intent/options/source. This is an internal contract, not an HTTP DTO.
type AIOperationLookup struct {
	OrganizationID uuid.UUID
	UserID         *int64
	IdempotencyKey string
	Fingerprint    [32]byte
	OperationKind  string
	SourceKind     *string
	SourceID       *uuid.UUID
}

type AIOperationView struct {
	OperationID   uuid.UUID
	Status        string
	Disposition   AIOperationDisposition
	SourceKind    *string
	SourceID      *uuid.UUID
	AttemptCount  int
	LatestAttempt AIReservation
}

func AIReplayDisposition(operationStatus, attemptStatus string) AIOperationDisposition {
	if operationStatus == "needs_reconciliation" || attemptStatus == "unknown" {
		return AIOperationReconciliation
	}
	switch operationStatus {
	case "completed":
		return AIOperationCompleted
	case "failed", "cancelled":
		return AIOperationStopped
	default:
		return AIOperationInProgress
	}
}
