package domain

import "github.com/google/uuid"

type AIFailureOutcome string

const (
	AIFailureNotDispatched AIFailureOutcome = "not_dispatched"
	AIFailureNotExecuted   AIFailureOutcome = "confirmed_not_executed"
	AIFailureCharged       AIFailureOutcome = "confirmed_usage"
	AIFailureUnknown       AIFailureOutcome = "unknown"
)

// AIFailureRequest is evidence from trusted orchestration/provider adapters,
// never a client declaration that a call was free. EventID is stable on retry.
// NotExecuted requires an explicit provider confirmation reference, not merely
// an HTTP error, timeout, cancellation or an empty response.
type AIFailureRequest struct {
	OrganizationID    uuid.UUID
	AttemptID         uuid.UUID
	EventID           uuid.UUID
	Outcome           AIFailureOutcome
	Reason            string // local_error, provider_error, timeout, client_cancelled, invalid_usage
	EvidenceSource    string
	EvidenceReference *string
	Usage             *AISettleRequest // only for confirmed_usage; must name this attempt
}

type AIFailureResult struct {
	OperationID          uuid.UUID
	AttemptID            uuid.UUID
	AttemptStatus        string
	OperationStatus      string
	ChargedMicrocredits  *int64 // nil means unknown, never an invented zero
	ReservedMicrocredits int64
	ReleasedMicrocredits int64
	Applied              bool
}
