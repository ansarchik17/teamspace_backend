package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrAIReservationInvalid  = errors.New("invalid AI reservation request")
	ErrAIReservationConflict = errors.New("AI reservation idempotency conflict")
	ErrAIAccountingDisabled  = errors.New("AI accounting is disabled")
)

// AIReserveRequest is an internal, trusted server contract, never a client DTO.
// Microcredits must cover the adapter's bounded input and output. This method
// does not estimate tokens or authorize access to the underlying document/chat.
type AIReserveRequest struct {
	OrganizationID      uuid.UUID
	UserID              *int64 // nil exclusively for a trusted company service payer
	OperationKind       string
	IdempotencyKey      string
	Fingerprint         [32]byte
	AttemptFingerprint  [32]byte // digest of the bounded, actual provider payload
	AttemptNumber       int32
	Model               AICreditModelKey
	UsageAdapterVersion string
	Microcredits        int64
	SourceKind          *string
	SourceID            *uuid.UUID
}

type AIReservation struct {
	OperationID       uuid.UUID
	AttemptID         uuid.UUID
	ReservationID     uuid.UUID
	CompanyPeriodID   uuid.UUID
	FiveHourPeriodID  *uuid.UUID
	Microcredits      int64
	AttemptStatus     string
	ReservationStatus string
	AttemptNumber     int32
	OperationStatus   string
	Disposition       AIOperationDisposition
	Settlement        *AISettlement // stored expense on replay, never a new charge
	// Created is true only for the transaction that created this attempt. A
	// replay must never send another provider request, even if still reserved.
	Created bool
}

// RecheckAt is an accounting boundary, not a promise that access will return:
// unresolved holds may survive it. No company amounts are exposed by this error.
type AIReservationDenied struct {
	Reason    string
	Scope     string
	RecheckAt *time.Time
	// Internal admission hint only; never expose company capacity in an API response.
	AvailableMicrocredits *int64 `json:"-"`
}

func (e *AIReservationDenied) Error() string { return "AI reservation denied: " + e.Reason }
