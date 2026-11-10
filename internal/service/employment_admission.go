package service

import (
	"context"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func (s *OrganizationService) IFormEmploymentAccess(ctx context.Context, actor int64, company, request uuid.UUID, action, start string, revision int64) (*domain.EmploymentAdmission, error) {
	return s.repo.IFormEmploymentAccess(ctx, actor, company, request, action, start, revision)
}
func (s *OrganizationService) EmploymentTimezone(ctx context.Context, actor int64, company *uuid.UUID, zone *string, revision int64) (map[string]any, error) {
	return s.repo.EmploymentTimezone(ctx, actor, company, zone, revision)
}
