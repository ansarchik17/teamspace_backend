package domain

import (
	"time"

	"github.com/google/uuid"
)

// Internal models; HTTP string encoding is added by the future API handler.
type AIQuotaWindow struct {
	Limit            *int64
	Used             int64
	Reserved         int64
	CarriedReserved  int64
	Available        *int64
	RemainingPercent *int
	State            string
	Window           *AIPeriodWindow
	PeriodID         *uuid.UUID
}

type AIEmployeeQuota struct {
	UserID            int64
	FullName          string
	Username          string
	Email             string
	IsActive          bool
	IsBlocked         bool
	IsDirector        bool
	Configured        bool
	Revision          int64
	WeeklyQuota       *int64
	FiveHourLimit     *int64
	EffectiveFiveHour *int64
	Committed         int64
	Weekly            AIQuotaWindow
	FiveHour          AIQuotaWindow
}

type AIEmployeeQuotaPage struct {
	Items           []AIEmployeeQuota
	Total           int
	Limit           int
	Offset          int
	ServerTime      time.Time
	CompanyRevision int64
	Window          *AIPeriodWindow
	PeriodID        *uuid.UUID
}

type AIEmployeeQuotaDetail struct {
	Item            AIEmployeeQuota
	CompanyRevision int64
	ServerTime      time.Time
}
