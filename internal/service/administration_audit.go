package service

import (
	"context"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func (s *OrganizationService) ListAdministrationAudit(ctx context.Context, actor int64, org *uuid.UUID, platform bool, filter domain.AdministrationAuditFilter) (*domain.AdministrationAuditPage, error) {
	return s.repo.ListAdministrationAudit(ctx, actor, org, platform, filter)
}
