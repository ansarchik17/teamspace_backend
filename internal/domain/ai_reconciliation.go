package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var ErrAIReconciliationLeaseLost = errors.New("AI reconciliation lease is no longer owned")

type AIReconciliationJob struct {
	AttemptID            uuid.UUID
	OrganizationID       uuid.UUID
	LeaseToken           uuid.UUID
	LeaseUntil           time.Time
	ClaimNumber          int64
	AttemptStatus        string
	ReservationStatus    string
	ProviderID           string
	EndpointKind         string
	RequestedModel       string
	PolicyVersion        string
	UsageAdapterVersion  string
	ProviderRequestID    *string
	PayloadIdentityKnown bool
}

type AIReconciliationOutcome string

const (
	AIReconcileUsage         AIReconciliationOutcome = "usage"
	AIReconcileNotExecuted   AIReconciliationOutcome = "not_executed"
	AIReconcileNotDispatched AIReconciliationOutcome = "not_dispatched"
	AIReconcilePending       AIReconciliationOutcome = "pending"
	AIReconcileManual        AIReconciliationOutcome = "manual"
)

// Evidence must come from a trusted, read-only usage adapter. Neither a provider
// timeout, HTTP 404 nor job age is proof of non-execution. No prompt is needed.
type AIReconciliationEvidence struct {
	Outcome              AIReconciliationOutcome
	Usage                *AISettleRequest
	ProviderConfirmation *string
	Reason               string // safe operational code, never raw provider text
}

type AIReconciliationResult struct {
	JobStatus string
	Reason    string
	Applied   bool // true only after the reconciliation transition commits
}
