package domain

import (
	"time"

	"github.com/google/uuid"
)

// Internal read model, not the HTTP DTO. A computed idle period may not yet
// have a persisted ID; reading must not open a reservation period.
type AIBudgetSummary struct {
	EnforcementMode     string
	OrganizationID      uuid.UUID
	ServerTime          time.Time
	Configured          bool
	Revision            int64
	WeeklyBudget        *int64
	MaxFiveHour         *int64
	ServiceQuota        int64
	Window              *AIPeriodWindow
	PeriodID            *uuid.UUID
	EmployeeAllocated   int64
	Allocated           int64
	Committed           int64
	Used                int64
	Reserved            int64
	CarriedReserved     int64
	Available           *int64
	Unallocated         *int64
	Overcommitted       *int64
	RemainingPercent    *int
	Service             AIBudgetServiceUsage
	CanSetCompanyBudget bool
	CanSetServiceQuota  bool
	CanSetUserQuotas    bool
}

type AIBudgetServiceUsage struct {
	Used            int64
	Reserved        int64
	CarriedReserved int64
	Available       int64
}
