package service

import (
	"context"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func (s *OrganizationService) AdministrationAIBudget(ctx context.Context, actor int64, org *uuid.UUID) (*domain.AIBudgetSummary, error) {
	return s.repo.AdministrationAIBudget(ctx, actor, org)
}

func (s *OrganizationService) SetAdministrationAIBudget(ctx context.Context, actor int64, org *uuid.UUID, req domain.SetAIBudgetRequest) error {
	return s.repo.SetAdministrationAIBudget(ctx, actor, org, req)
}

func (s *OrganizationService) AdministrationAIUsers(ctx context.Context, actor int64, org *uuid.UUID, filter domain.AdministrationUserFilter) (*domain.AIEmployeeQuotaPage, error) {
	return s.repo.AdministrationAIUsers(ctx, actor, org, filter)
}

func (s *OrganizationService) SetAdministrationAIUserQuota(ctx context.Context, actor, target int64, org *uuid.UUID, req domain.SetAIUserQuotaRequest) error {
	return s.repo.SetAdministrationAIUserQuota(ctx, actor, target, org, req)
}

func (s *OrganizationService) AdministrationAIUser(ctx context.Context, actor, target int64, org *uuid.UUID) (*domain.AIEmployeeQuotaDetail, error) {
	return s.repo.AdministrationAIUser(ctx, actor, target, org)
}

func (s *OrganizationService) GetUserAILimits(ctx context.Context, userID int64) (*domain.UserAILimits, error) {
	return s.repo.GetUserAILimits(ctx, userID)
}

func (s *OrganizationService) PlatformPersonalAIQuota(ctx context.Context, actor, target int64, req *domain.SetAIUserQuotaRequest) (map[string]any, error) {
	return s.repo.PlatformPersonalAIQuota(ctx, actor, target, req)
}
