package domain

type SetAIUserQuotaRequest struct {
	WeeklyQuota     *int64
	FiveHourLimit   *int64
	Revision        *int64
	CompanyRevision *int64
}

func (r SetAIUserQuotaRequest) Valid() bool {
	for _, value := range []*int64{r.WeeklyQuota, r.FiveHourLimit} {
		if value == nil || *value < 0 || *value > MaxAIBudgetMicrocredits {
			return false
		}
	}
	return r.Revision != nil && *r.Revision >= 0 && r.CompanyRevision != nil && *r.CompanyRevision >= 0
}
