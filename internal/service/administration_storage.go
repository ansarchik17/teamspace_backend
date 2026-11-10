package service

import (
	"context"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func (s *OrganizationService) AdministrationStorage(ctx context.Context, actor int64, org *uuid.UUID) (*domain.AdministrationStorage, error) {
	return s.repo.AdministrationStorage(ctx, actor, org)
}
func (s *OrganizationService) SetAdministrationStorage(ctx context.Context, actor int64, org *uuid.UUID, req domain.AdministrationStorageRequest) error {
	return s.repo.SetAdministrationStorage(ctx, actor, org, req)
}
func (s *OrganizationService) AdministrationStorageUsers(ctx context.Context, actor int64, org *uuid.UUID, limit, offset int) (*domain.AdministrationStorageUsers, error) {
	return s.repo.AdministrationStorageUsers(ctx, actor, org, limit, offset)
}
func (s *OrganizationService) SetAdministrationUserStorage(ctx context.Context, actor, target int64, org *uuid.UUID, req domain.AdministrationStorageRequest) error {
	return s.repo.SetAdministrationUserStorage(ctx, actor, target, org, req)
}

func (s *OrganizationService) AdministrationStorageCapacity(ctx context.Context, actor int64, org uuid.UUID) (*domain.CompanyStorageCapacity, error) {
	return s.repo.AdministrationStorageCapacity(ctx, actor, org)
}
