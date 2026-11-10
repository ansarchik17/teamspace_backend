package domain

import (
	"time"

	"github.com/google/uuid"
)

const (
	AIEnforcementModeEnforced = "enforced"
	AIEnforcementModeObserve  = "observe"
)

type AIBlocker struct {
	Reason   string     `json:"reason"`
	Scope    string     `json:"scope"`
	Enforced bool       `json:"enforced"`
	ResetsAt *time.Time `json:"resets_at"`
}

type AIAccessState struct {
	CanStartRequest bool        `json:"can_start_request"`
	Blockers        []AIBlocker `json:"blockers"`
	RetryAt         *time.Time  `json:"retry_at"`
	RetryKind       string      `json:"retry_kind"`
	RecheckAt       *time.Time  `json:"recheck_at"`
}

type UserAILimits struct {
	ServerTime          time.Time     `json:"server_time"`
	OrganizationID      *uuid.UUID    `json:"organization_id"`
	EnforcementMode     string        `json:"enforcement_mode"`
	CreditPolicyVersion *string       `json:"credit_policy_version"`
	FiveHour            AIQuotaWindow `json:"five_hour"`
	Weekly              AIQuotaWindow `json:"weekly"`
	Access              AIAccessState `json:"access"`
}
