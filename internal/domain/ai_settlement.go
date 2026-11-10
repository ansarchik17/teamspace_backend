package domain

import (
	"errors"

	"github.com/google/uuid"
)

var (
	ErrAISettlementInvalid  = errors.New("invalid AI settlement request")
	ErrAISettlementNotFound = errors.New("AI attempt not found in company")
	ErrAISettlementConflict = errors.New("AI settlement conflicts with recorded result")
)

// AISettleRequest is internal evidence from a trusted usage adapter, not a client
// DTO. ActualModel must be confirmed by the response or the adapter's contract.
// Missing usage remains missing; this command cannot waive an unknown charge.
type AISettleRequest struct {
	OrganizationID       uuid.UUID
	AttemptID            uuid.UUID
	ActualModel          string
	UsageAdapterVersion  string
	UsageSource          string
	ProviderRequestID    *string
	Usage                AICreditUsage
	ReportedInputTokens  *int64
	ReportedOutputTokens *int64
	ReportedTotalTokens  *int64
}

type AISettlement struct {
	OperationID          uuid.UUID
	AttemptID            uuid.UUID
	ChargedMicrocredits  int64
	ReservedMicrocredits int64
	ReleasedMicrocredits int64
	OverrunMicrocredits  int64
	OperationStatus      string
	Applied              bool // false for a byte-equivalent normalized result replay
}
