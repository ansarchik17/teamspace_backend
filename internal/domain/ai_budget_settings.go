package domain

const MaxAIBudgetMicrocredits int64 = 1_000_000_000_000_000

// Internal command; canonical JSON strings are validated by the future handler.
type SetAIBudgetRequest struct {
	WeeklyBudget *int64
	MaxFiveHour  *int64
	ServiceQuota *int64 // Deprecated compatibility field; never allocates funds.
	Revision     *int64
}

func (r SetAIBudgetRequest) Valid() bool {
	for _, value := range []*int64{r.WeeklyBudget, r.MaxFiveHour} {
		if value == nil || *value < 0 || *value > MaxAIBudgetMicrocredits {
			return false
		}
	}
	if r.ServiceQuota != nil && (*r.ServiceQuota < 0 || *r.ServiceQuota > MaxAIBudgetMicrocredits) {
		return false
	}
	return r.Revision != nil && *r.Revision >= 0
}
