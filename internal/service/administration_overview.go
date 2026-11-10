package service

import (
	"context"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func (s *OrganizationService) AdministrationOverview(ctx context.Context, actor int64, org *uuid.UUID) (*domain.AdministrationOverview, error) {
	return s.repo.AdministrationOverview(ctx, actor, org)
}
func (s *OrganizationService) PlatformAdministrationOverview(ctx context.Context, actor int64) (*domain.PlatformAdministrationOverview, error) {
	return s.repo.PlatformAdministrationOverview(ctx, actor)
}
