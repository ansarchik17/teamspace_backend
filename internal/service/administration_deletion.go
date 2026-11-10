package service

import (
	"context"
	"github.com/google/uuid"
)

func (s *OrganizationService) DeleteAdministrationUser(ctx context.Context, actor, target int64, org *uuid.UUID) error {
	if err := s.repo.DeleteAdministrationUser(ctx, actor, target, org); err != nil {
		return err
	}
	if s.closeUserSessions != nil {
		s.closeUserSessions(target)
	}
	return nil
}

func (s *OrganizationService) DeleteUser(ctx context.Context, actor, target int64) error {
	if err := s.repo.DeleteUser(ctx, actor, target); err != nil {
		return err
	}
	if s.closeUserSessions != nil {
		s.closeUserSessions(target)
	}
	return nil
}

func (s *OrganizationService) DeletePlatformOrganization(ctx context.Context, actor int64, org uuid.UUID) error {
	ids, err := s.repo.DeletePlatformOrganization(ctx, actor, org)
	if err != nil {
		return err
	}
	if s.closeUserSessions != nil {
		for _, id := range ids {
			s.closeUserSessions(id)
		}
	}
	return nil
}
