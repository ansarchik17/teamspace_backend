package repository

import (
	"context"
	"fmt"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func (r *PublicationRepository) AudienceTargetsExist(ctx context.Context, audience string, ids []int64) (bool, error) {
	column := "ministry_id"
	if audience == domain.PublicationAudienceDepartment {
		column = "department_id"
	} else if audience != domain.PublicationAudienceMinistry && audience != domain.PublicationAudienceBranch {
		return false, fmt.Errorf("unsupported audience")
	}
	var count int
	err := r.db.QueryRow(ctx, fmt.Sprintf("SELECT COUNT(DISTINCT %s) FROM users WHERE deleted_at IS NULL AND %s = ANY($1)", column, column), ids).Scan(&count)
	return count == len(ids), err
}

// Apply company scope before the legacy audience union for recipients and analytics.
func audienceUserFilter(pub *domain.Publication, prefix string, parameter int) (string, []interface{}, error) {
	if pub == nil || pub.AuthorID <= 0 {
		return "", nil, fmt.Errorf("publication author is required for audience scoping")
	}
	readerID := prefix + "id"
	if prefix == "" {
		readerID = "users.id"
	}
	scope := " AND " + publicationOrganizationScope(fmt.Sprintf("$%d", parameter), readerID, false)
	args := []interface{}{pub.AuthorID}
	var column string
	var single *int64
	var ids []int64
	switch pub.TargetAudience {
	case domain.PublicationAudienceAll:
		return scope, args, nil
	case domain.PublicationAudienceMinistry, domain.PublicationAudienceBranch:
		column, single, ids = "ministry_id", pub.TargetMinistryID, pub.TargetMinistryIDs
	case domain.PublicationAudienceDepartment:
		column, single, ids = "department_id", pub.TargetDepartmentID, pub.TargetDepartmentIDs
	default:
		return "", nil, fmt.Errorf("unsupported publication audience")
	}
	targets := append([]int64{}, ids...)
	if single != nil {
		targets = append(targets, *single)
	}
	if len(targets) == 0 {
		return "", nil, fmt.Errorf("publication audience has no targets")
	}
	return scope + fmt.Sprintf(" AND %s%s = ANY($%d)", prefix, column, parameter+1), append(args, targets), nil
}
