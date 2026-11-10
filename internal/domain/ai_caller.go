package domain

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

var (
	ErrAIPaidCallMissingContext   = errors.New("AI paid call requires authenticated payer context")
	ErrAIPaidCallInvalidCaller    = errors.New("invalid AI paid caller identity")
	ErrAIPaidCallInvalidRequest   = errors.New("invalid AI paid call request")
	ErrAIPaidCallContextMismatch  = errors.New("AI paid call context does not match request caller")
	ErrAIRateLimitExceeded        = errors.New("AI request rate limit exceeded")
	ErrAIConcurrencyLimitExceeded = errors.New("AI concurrent generation limit exceeded")
	ErrAILimiterUnavailable       = errors.New("AI generation limiter is temporarily unavailable")
)

// AIPaidCaller represents the server-authenticated identity authorized to incur AI charges.
// OrganizationID may be empty for a personal account without a company.
// Service payers always require a real organization.
type AIPaidCaller struct {
	OrganizationID uuid.UUID
	UserID         *int64
	IsService      bool
}

func (c AIPaidCaller) Validate() error {
	if c.IsService && c.OrganizationID == uuid.Nil {
		return ErrAIPaidCallInvalidCaller
	}
	if c.IsService {
		if c.UserID != nil {
			return ErrAIPaidCallInvalidCaller
		}
		return nil
	}
	if c.UserID == nil || *c.UserID <= 0 {
		return ErrAIPaidCallInvalidCaller
	}
	return nil
}

func NewAIPaidUserCaller(orgID uuid.UUID, userID int64) (AIPaidCaller, error) {
	caller := AIPaidCaller{OrganizationID: orgID, UserID: &userID, IsService: false}
	if err := caller.Validate(); err != nil {
		return AIPaidCaller{}, err
	}
	return caller, nil
}

func NewAIPaidServiceCaller(orgID uuid.UUID) (AIPaidCaller, error) {
	caller := AIPaidCaller{OrganizationID: orgID, UserID: nil, IsService: true}
	if err := caller.Validate(); err != nil {
		return AIPaidCaller{}, err
	}
	return caller, nil
}

type aiPaidCallerContextKey struct{}

func WithAIPaidCaller(ctx context.Context, caller AIPaidCaller) context.Context {
	return context.WithValue(ctx, aiPaidCallerContextKey{}, caller)
}

func AIPaidCallerFromContext(ctx context.Context) (AIPaidCaller, bool) {
	if ctx == nil {
		return AIPaidCaller{}, false
	}
	caller, ok := ctx.Value(aiPaidCallerContextKey{}).(AIPaidCaller)
	if !ok || caller.Validate() != nil {
		return AIPaidCaller{}, false
	}
	return caller, true
}

// AIPaidCallParams encapsulates the parameters needed to reserve, trace and settle a paid AI call.
type AIPaidCallParams struct {
	Caller              AIPaidCaller // Optional if supplied via context; if both supplied, they must match
	IdempotencyKey      string
	OperationKind       string
	Model               AICreditModelKey
	UsageAdapterVersion string
	Microcredits        int64
	Fingerprint         [32]byte
	AttemptFingerprint  [32]byte
	AttemptNumber       int32
	SourceKind          *string
	SourceID            *uuid.UUID
}

// ResolveCaller extracts and strictly verifies the server-authenticated caller from the context and params.
// A call without a verified personal or company-service payer cannot pass.
func (p AIPaidCallParams) ResolveCaller(ctx context.Context) (AIPaidCaller, error) {
	var ctxCaller AIPaidCaller
	var hasCtxCaller bool
	if ctx != nil {
		ctxCaller, hasCtxCaller = ctx.Value(aiPaidCallerContextKey{}).(AIPaidCaller)
	}
	if hasCtxCaller && ctxCaller.Validate() != nil {
		return AIPaidCaller{}, ErrAIPaidCallInvalidCaller
	}
	// Only the zero value means omitted; a partially populated identity is invalid.
	hasParamCaller := p.Caller != (AIPaidCaller{})
	if hasParamCaller && p.Caller.Validate() != nil {
		return AIPaidCaller{}, ErrAIPaidCallInvalidCaller
	}

	if !hasCtxCaller && !hasParamCaller {
		return AIPaidCaller{}, ErrAIPaidCallMissingContext
	}

	if hasCtxCaller && hasParamCaller {
		if ctxCaller.OrganizationID != p.Caller.OrganizationID || ctxCaller.IsService != p.Caller.IsService {
			return AIPaidCaller{}, ErrAIPaidCallContextMismatch
		}
		if ctxCaller.UserID != nil && p.Caller.UserID != nil && *ctxCaller.UserID != *p.Caller.UserID {
			return AIPaidCaller{}, ErrAIPaidCallContextMismatch
		}
		if (ctxCaller.UserID == nil) != (p.Caller.UserID == nil) {
			return AIPaidCaller{}, ErrAIPaidCallContextMismatch
		}
		return ctxCaller, nil
	}

	if hasCtxCaller {
		return ctxCaller, nil
	}

	return p.Caller, nil
}

// ToReserveRequest builds an AIReserveRequest from verified parameters.
func (p AIPaidCallParams) ToReserveRequest(caller AIPaidCaller) (AIReserveRequest, error) {
	if err := caller.Validate(); err != nil {
		return AIReserveRequest{}, err
	}
	if p.IdempotencyKey == "" || p.OperationKind == "" || p.Model == (AICreditModelKey{}) || p.UsageAdapterVersion == "" || p.Microcredits <= 0 {
		return AIReserveRequest{}, ErrAIPaidCallInvalidRequest
	}
	if p.Fingerprint == [32]byte{} || p.AttemptFingerprint == [32]byte{} {
		return AIReserveRequest{}, ErrAIPaidCallInvalidRequest
	}
	attemptNumber := p.AttemptNumber
	if attemptNumber <= 0 {
		attemptNumber = 1
	}

	return AIReserveRequest{
		OrganizationID:      caller.OrganizationID,
		UserID:              caller.UserID,
		OperationKind:       p.OperationKind,
		IdempotencyKey:      p.IdempotencyKey,
		Fingerprint:         p.Fingerprint,
		AttemptFingerprint:  p.AttemptFingerprint,
		AttemptNumber:       attemptNumber,
		Model:               p.Model,
		UsageAdapterVersion: p.UsageAdapterVersion,
		Microcredits:        p.Microcredits,
		SourceKind:          p.SourceKind,
		SourceID:            p.SourceID,
	}, nil
}
