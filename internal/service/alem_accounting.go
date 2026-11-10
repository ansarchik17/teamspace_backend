package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/alem"
	"github.com/west6ide/workspace-backend/internal/domain"
)

const alemChatUsageAdapter = "nitec-chat-v1"

type aiPayerUsers interface {
	GetByID(context.Context, int64) (*domain.User, error)
}

// SetPaidAccounting must be wired at startup. Missing wiring fails closed.
func (s *AlemService) SetPaidAccounting(executor AIPaidCallerService, users aiPayerUsers) {
	s.paidCalls, s.payerUsers = executor, users
}

func (s *UnifiedAIService) SetPaidAccounting(executor AIPaidCallerService, users aiPayerUsers) {
	s.paidCalls, s.payerUsers = executor, users
}

func (s *NotebookResearchService) SetPaidAccounting(executor AIPaidCallerService, users aiPayerUsers) {
	s.paidCalls, s.payerUsers = executor, users
}

func (s *SmartAgentService) SetPaidAccounting(executor AIPaidCallerService, users aiPayerUsers) {
	s.paidCalls, s.payerUsers = executor, users
}

func (s *AlemService) paidUserContext(ctx context.Context, userID int64) (context.Context, error) {
	return aiPaidUserContext(s.paidCalls, s.payerUsers, ctx, userID)
}

func (s *UnifiedAIService) paidUserContext(ctx context.Context, userID int64) (context.Context, error) {
	return aiPaidUserContext(s.paidCalls, s.payerUsers, ctx, userID)
}

func (s *NotebookResearchService) paidUserContext(ctx context.Context, userID int64) (context.Context, error) {
	return aiPaidUserContext(s.paidCalls, s.payerUsers, ctx, userID)
}

func (s *SmartAgentService) paidUserContext(ctx context.Context, userID int64) (context.Context, error) {
	return aiPaidUserContext(s.paidCalls, s.payerUsers, ctx, userID)
}

func aiPaidUserContext(executor AIPaidCallerService, users aiPayerUsers, ctx context.Context, userID int64) (context.Context, error) {
	if executor == nil || users == nil || userID <= 0 {
		return nil, domain.ErrAIPaidCallMissingContext
	}
	user, err := users.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if user == nil || user.ID != userID || !user.IsActive || user.DeletedAt != nil {
		return nil, domain.ErrAIPaidCallInvalidCaller
	}
	org := uuid.Nil
	if user.OrganizationID != nil {
		org = *user.OrganizationID
	}
	caller, err := domain.NewAIPaidUserCaller(org, userID)
	if err != nil {
		return nil, err
	}
	// Reject contradictory supplied accounting context rather than overwriting it.
	if _, err = (domain.AIPaidCallParams{Caller: caller}).ResolveCaller(ctx); err != nil {
		return nil, err
	}
	return domain.WithAIPaidCaller(ctx, caller), nil
}

// One adapter is shared by the entire turn, including protocol/model fallback,
// tool rounds and publication. Each call commits its own accounting transaction.
type alemPaidChat struct {
	quotaCutoff  *domain.AIReservationDenied
	budgetCutoff *domain.AIReservationDenied
	executor     AIPaidCallerService
	client       aiCompletionClient
	chatID       uuid.UUID
	turnID       string
	step         int
	prefix       string
	source       string
	dispatched   bool
}

func newUnifiedAIPaidChat(executor AIPaidCallerService, client aiCompletionClient, chatID uuid.UUID, turnID string) *alemPaidChat {
	return &alemPaidChat{
		executor: executor,
		client:   client,
		chatID:   chatID,
		turnID:   turnID,
		prefix:   "unified_ai",
		source:   "unified_ai_chat",
	}
}

func newNotebookPaidChat(executor AIPaidCallerService, client aiCompletionClient, chatID uuid.UUID, operationID string) *alemPaidChat {
	return &alemPaidChat{
		executor: executor,
		client:   client,
		chatID:   chatID,
		turnID:   operationID,
		prefix:   "notebook",
		source:   "notebook_research",
	}
}

func newSmartAgentPaidChat(executor AIPaidCallerService, client aiCompletionClient, chatID uuid.UUID, sessionID string) *alemPaidChat {
	return &alemPaidChat{
		executor: executor,
		client:   client,
		chatID:   chatID,
		turnID:   sessionID,
		prefix:   "smart_agent",
		source:   "smart_agent_chat",
	}
}

func aiProviderForModel(model string) string {
	if model == "gpt-4o" {
		return "openai-direct"
	}
	return "nitec"
}

type alemSingleAttempt struct{ client *alem.Client }

func (c alemSingleAttempt) CreateChatCompletion(ctx context.Context, req alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error) {
	return c.client.CreateChatCompletionOnce(ctx, req)
}

func (c *alemPaidChat) CreateChatCompletion(ctx context.Context, req alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error) {
	c.quotaCutoff, c.budgetCutoff = nil, nil
	return c.createChatCompletion(ctx, req, 0)
}

func (c *alemPaidChat) createChatCompletion(ctx context.Context, req alem.ChatCompletionRequest, adjustments int) (*alem.ChatCompletionResponse, error) {
	if c.executor == nil || c.client == nil {
		return nil, domain.ErrAIPaidCallMissingContext
	}
	caller, err := (domain.AIPaidCallParams{}).ResolveCaller(ctx)
	if err != nil {
		return nil, err
	}
	if caller.IsService {
		return nil, domain.ErrAIPaidCallInvalidCaller
	}
	if req.MaxTokens <= 0 || req.MaxTokens > 16384 || req.Stream || len(req.Messages) == 0 {
		return nil, domain.ErrAIPaidCallInvalidRequest
	}
	key := domain.AICreditModelKey{ProviderID: aiProviderForModel(req.Model), EndpointKind: "chat", ModelID: req.Model, PolicyVersion: domain.AICreditPolicyV1}
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	if len(payload) > 512*1024 {
		return nil, domain.ErrAIPaidCallInvalidRequest
	}
	// Reservation only: UTF-8 bytes of the full wire payload cover text,
	// history, tool schemas/results and framing. Images require extra headroom.
	// Final expense always comes from provider usage, never from this estimate.
	input := int64(len(payload) + 512*len(req.Messages))
	for _, msg := range req.Messages {
		if parts, ok := msg.Content.([]alem.ContentPart); ok {
			for _, part := range parts {
				if part.Type == "image_url" {
					input += 32768
				}
			}
		}
	}
	output := int64(req.MaxTokens)
	estimate, err := domain.CalculateAICredits(key, domain.AICreditUsage{Status: "derived", InputTokens: &input, OutputTokens: &output})
	if err != nil {
		return nil, err
	}
	if adjustments == 0 {
		c.step++
	}
	prefix := c.prefix
	if prefix == "" {
		prefix = "alem"
	}
	source := c.source
	if source == "" {
		source = "alem_chat"
	}
	id := prefix + ":" + c.turnID + ":" + strconv.Itoa(c.step)
	fingerprint := sha256.Sum256(payload)
	params := domain.AIPaidCallParams{IdempotencyKey: id, OperationKind: "chat", Model: key,
		UsageAdapterVersion: alemChatUsageAdapter, Microcredits: estimate.Microcredits,
		Fingerprint: fingerprint, AttemptFingerprint: fingerprint, AttemptNumber: 1, SourceKind: &source, SourceID: &c.chatID}
	resp, reservation, _, err := ExecutePaidCallTyped(ctx, c.executor, params, func(ctx context.Context, _ domain.AIReservation) (*alem.ChatCompletionResponse, domain.AISettleRequest, error) {
		c.dispatched = true
		response, callErr := c.client.CreateChatCompletion(ctx, req)
		evidence, usageErr := alemChatEvidence(response)
		return response, evidence, errors.Join(callErr, usageErr)
	})
	if err != nil {
		var denied *domain.AIReservationDenied
		// At most three adjustments across the company, weekly and five-hour gates.
		// Only a rejected reservation can be retried; no provider call has happened.
		if !reservation.Created && adjustments < 3 && errors.As(err, &denied) && denied.AvailableMicrocredits != nil {
			switch denied.Reason {
			case "RESERVATION_TOO_LARGE", "RESERVATION_PENDING", "COMPANY_WEEKLY_EXHAUSTED", "USER_WEEKLY_EXHAUSTED", "FIVE_HOUR_EXHAUSTED":
				inputCost := estimate.Microcredits - output*estimate.Rates.Output
				available := *denied.AvailableMicrocredits
				if estimate.Rates.Output > 0 && available > inputCost {
					nextOutput := (available - inputCost) / estimate.Rates.Output
					if nextOutput > 0 && nextOutput < output {
						if denied.Scope == "five_hour" || denied.Scope == "weekly" {
							c.budgetCutoff = denied
						}
						req.MaxTokens = int(nextOutput)
						// Rebuild the actual payload, fingerprint and full reservation estimate.
						return c.createChatCompletion(ctx, req, adjustments+1)
					}
				}
			}
		}
		// Financial capacity can be positive yet too small even for the input.
		// No request was dispatched: report insufficient budget, not a running
		// generation. Account-level uncertainty has no financial capacity hint.
		if !reservation.Created && errors.As(err, &denied) && denied.Reason == "RESERVATION_PENDING" &&
			denied.Scope != "account" && denied.AvailableMicrocredits != nil && *denied.AvailableMicrocredits > 0 {
			insufficient := *denied
			insufficient.Reason = "RESERVATION_TOO_LARGE"
			insufficient.RecheckAt = nil
			if denied.Scope == "five_hour" || denied.Scope == "weekly" {
				c.quotaCutoff = &insufficient
			}
			return resp, &insufficient
		}
		if !reservation.Created && errors.As(err, &denied) && denied.Reason == "RESERVATION_TOO_LARGE" && (denied.Scope == "five_hour" || denied.Scope == "weekly") {
			c.quotaCutoff = denied
		}
		return resp, err
	}
	if !reservation.Created {
		// Accounting replays do not contain the generated text. Never call the
		// provider again or report a nil response as a successful generation.
		return nil, domain.ErrAIReservationConflict
	}
	if adjustments > 0 && c.budgetCutoff != nil && resp != nil && len(resp.Choices) > 0 && resp.Choices[0].FinishReason == "length" {
		c.quotaCutoff = c.budgetCutoff
	}
	return resp, nil
}

func alemChatEvidence(resp *alem.ChatCompletionResponse) (domain.AISettleRequest, error) {
	evidence := domain.AISettleRequest{UsageAdapterVersion: alemChatUsageAdapter, UsageSource: "provider_usage"}
	if resp == nil {
		return evidence, domain.ErrAICreditUnknownUsage
	}
	evidence.ActualModel = resp.Model
	if resp.ID != "" {
		id := resp.ID
		evidence.ProviderRequestID = &id
	}
	var wire struct {
		Input        *int64 `json:"prompt_tokens"`
		Output       *int64 `json:"completion_tokens"`
		Total        *int64 `json:"total_tokens"`
		InputDetails *struct {
			Cached *int64 `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
		OutputDetails *struct {
			Reasoning *int64 `json:"reasoning_tokens"`
		} `json:"completion_tokens_details"`
	}
	if len(resp.AccountingUsage) == 0 {
		return evidence, domain.ErrAICreditUnknownUsage
	}
	if err := json.Unmarshal(resp.AccountingUsage, &wire); err != nil {
		return evidence, domain.ErrAICreditInvalidUsage
	}
	evidence.ReportedInputTokens, evidence.ReportedOutputTokens, evidence.ReportedTotalTokens = wire.Input, wire.Output, wire.Total
	evidence.Usage = domain.AICreditUsage{Status: "unknown", InputTokens: wire.Input, OutputTokens: wire.Output, TotalTokens: wire.Total}
	if wire.InputDetails != nil {
		evidence.Usage.CachedInput = wire.InputDetails.Cached
	}
	if wire.OutputDetails != nil {
		evidence.Usage.ReasoningOutput = wire.OutputDetails.Reasoning
	}
	if resp.Model == "" {
		return evidence, domain.ErrAICreditUnknownModel
	}
	verifiedUsage := evidence.Usage
	verifiedUsage.Status = "verified"
	calc, err := domain.CalculateAICredits(domain.AICreditModelKey{ProviderID: aiProviderForModel(resp.Model), EndpointKind: "chat", ModelID: resp.Model, PolicyVersion: domain.AICreditPolicyV1}, verifiedUsage)
	if err != nil {
		return evidence, err
	}
	// A zero report is not proof that a generation was free.
	if calc.Microcredits == 0 {
		return evidence, fmt.Errorf("zero usage requires reconciliation: %w", domain.ErrAICreditUnknownUsage)
	}
	evidence.Usage = verifiedUsage
	return evidence, nil
}
