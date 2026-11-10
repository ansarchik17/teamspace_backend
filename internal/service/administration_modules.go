package service

import (
	"context"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func (s *OrganizationService) ListAdministrationModules(ctx context.Context, actor int64, org *uuid.UUID) ([]domain.AdministrationModule, error) {
	return s.repo.ListAdministrationModules(ctx, actor, org)
}
func (s *OrganizationService) SetAdministrationModule(ctx context.Context, actor int64, org *uuid.UUID, app string, req domain.AdministrationModuleRequest) error {
	return s.repo.SetAdministrationModule(ctx, actor, org, app, req)
}

func (s *OrganizationService) RequireUserModule(ctx context.Context, actor int64, app string) error {
	return s.repo.RequireUserModule(ctx, actor, app)
}
