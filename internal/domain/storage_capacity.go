package domain

import "time"

type StorageCapacity struct {
	TotalBytes   int64     `json:"total_bytes"`
	FreeBytes    int64     `json:"free_bytes"`
	ReserveBytes int64     `json:"reserve_bytes"`
	ObservedAt   time.Time `json:"observed_at"`
}

// Other commitments include existing allocations of companies without a budget.
func MaximumCompanyBudget(c StorageCapacity, used, otherCommitted, otherRemaining int64) int64 {
	a := c.TotalBytes - c.ReserveBytes - otherCommitted
	b := c.FreeBytes - c.ReserveBytes - otherRemaining + used
	if b < a {
		a = b
	}
	if a < 0 {
		return 0
	}
	return a
}

type CompanyStorageCapacity struct {
	StorageCapacity
	OtherCommittedBytes int64 `json:"other_committed_bytes"`
	OtherRemainingBytes int64 `json:"other_remaining_bytes"`
	MaxQuotaBytes       int64 `json:"max_quota_bytes"`
}
