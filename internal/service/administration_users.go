package service

import (
	"context"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func (s *OrganizationService) ListAdministrationUsers(ctx context.Context, actor int64, org *uuid.UUID, f domain.AdministrationUserFilter) (*domain.AdministrationUserPage, error) {
	return s.repo.ListAdministrationUsers(ctx, actor, org, f)
}
func (s *OrganizationService) SetEmployeeBlocked(ctx context.Context, actor, target int64, org *uuid.UUID, blocked bool) error {
	if err := s.repo.SetEmployeeBlocked(ctx, actor, target, org, blocked); err != nil {
		return err
	}
	if blocked && s.closeUserSessions != nil {
		s.closeUserSessions(target)
	}
	return nil
}
func (s *OrganizationService) SetUserSessionCloser(close func(int64)) { s.closeUserSessions = close }
