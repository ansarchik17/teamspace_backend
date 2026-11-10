package domain

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// AIQuotaTestSubject is an operator-configured financial exemption, bound to
// both immutable identities. It grants no access to data or inactive accounts.
type AIQuotaTestSubject struct {
	UserID         int64
	OrganizationID uuid.UUID
}

type AIQuotaTestSubjects []AIQuotaTestSubject

// ParseAIQuotaTestSubjects accepts comma-separated user_id@organization_uuid.
// Wildcards and organization-less users are deliberately unsupported.
func ParseAIQuotaTestSubjects(raw string) (AIQuotaTestSubjects, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var subjects AIQuotaTestSubjects
	seen := map[AIQuotaTestSubject]bool{}
	for _, entry := range strings.Split(raw, ",") {
		parts := strings.Split(strings.TrimSpace(entry), "@")
		if len(parts) != 2 {
			return nil, fmt.Errorf("expected user_id@organization_uuid")
		}
		user, userErr := strconv.ParseInt(parts[0], 10, 64)
		org, orgErr := uuid.Parse(parts[1])
		if userErr != nil || orgErr != nil || user <= 0 || org == uuid.Nil {
			return nil, fmt.Errorf("expected positive user_id and nonzero organization_uuid")
		}
		subject := AIQuotaTestSubject{UserID: user, OrganizationID: org}
		if !seen[subject] {
			subjects = append(subjects, subject)
			seen[subject] = true
		}
	}
	return subjects, nil
}

// Allows exempts only the named user's requests in the named organization.
// Organization-funded background calls in that organization are exempt too;
// other members' personal quotas remain enforced. Check live membership first.
func (s AIQuotaTestSubjects) Allows(org uuid.UUID, user *int64) bool {
	if org == uuid.Nil {
		return false
	}
	for _, subject := range s {
		if subject.UserID > 0 && subject.OrganizationID == org && (user == nil || *user == subject.UserID) {
			return true
		}
	}
	return false
}
