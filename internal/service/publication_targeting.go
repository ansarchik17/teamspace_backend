package service

import (
	"context"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

// Normalize legacy single targets and multi-select targets into one audience.
func normalizePublicationTargets(single *int64, multiple []int64, fallback *int64) ([]int64, error) {
	if len(multiple) > 100 {
		return nil, apperrors.Validation("at most 100 audience targets are allowed")
	}
	values := append([]int64{}, multiple...)
	if single != nil {
		values = append(values, *single)
	}
	if len(values) == 0 && fallback != nil {
		values = append(values, *fallback)
	}
	seen := map[int64]bool{}
	ids := make([]int64, 0, len(values))
	for _, id := range values {
		if id <= 0 {
			return nil, apperrors.Validation("audience target IDs must be positive")
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 || len(ids) > 100 {
		return nil, apperrors.Validation("audience must contain 1..100 targets")
	}
	return ids, nil
}

func (s *PublicationService) validateAudienceTargets(ctx context.Context, user *domain.User, audience string, single *int64, multiple []int64) (*int64, []int64, error) {
	own := user.MinistryID
	if audience == domain.PublicationAudienceDepartment {
		own = user.DepartmentID
	}
	ids, err := normalizePublicationTargets(single, multiple, own)
	if err != nil {
		return nil, nil, err
	}
	if !domain.HasGlobalUserScope(user) {
		for _, id := range ids {
			if own == nil || *own != id {
				return nil, nil, apperrors.Forbidden("only superadmin can target other subdivisions")
			}
		}
	}
	known, err := s.repo.AudienceTargetsExist(ctx, audience, ids)
	if err != nil {
		return nil, nil, err
	}
	if !known {
		return nil, nil, apperrors.Validation("audience target has no existing user membership")
	}
	if len(ids) == 1 {
		return &ids[0], ids, nil
	}
	return nil, ids, nil
}
