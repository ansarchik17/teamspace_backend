package service

import (
	"context"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func (s *OrganizationService) RecordSecurityEvent(ctx context.Context, event domain.SecurityAuditEvent) error {
	return s.repo.RecordSecurityEvent(ctx, event)
}
func (s *OrganizationService) SecurityAudit(ctx context.Context, actor int64, org *uuid.UUID, platform bool, f domain.SecurityAuditFilter) (*domain.SecurityAuditPage, error) {
	return s.repo.SecurityAudit(ctx, actor, org, platform, f)
}
