package service

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
	"strings"
	"time"
)

func (s *OrganizationService) OffboardContact(ctx context.Context, actor, target int64, req domain.ContactOffboardingRequest, dryRun bool) (*domain.ContactOffboardingResult, error) {
	req.Reason = strings.TrimSpace(req.Reason)
	if req.SuccessorID <= 0 || req.Reason == "" || len([]rune(req.Reason)) > 500 {
		return nil, apperrors.Validation("successor_id and reason (1 to 500 characters) are required")
	}
	result, err := s.repo.OffboardContact(ctx, actor, target, req, dryRun)
	if err == nil && !dryRun && result.Applied && s.offboardingFileReindex != nil {
		// Same best-effort behavior as ordinary Drive edits; serialized indexing avoids a burst of downloads.
		go s.reindexOffboardingFiles(context.WithoutCancel(ctx), target)
	}
	return result, err
}
func (s *OrganizationService) ContactOffboarding(ctx context.Context, actor, target int64) (*domain.ContactOffboardingResult, error) {
	return s.repo.ContactOffboarding(ctx, actor, target)
}
func (s *OrganizationService) SetOffboardingIdentitySync(sync func(context.Context, int64) error) {
	s.offboardingIdentitySync = sync
}

// The durable outbox retries Keycloak disable/logout without weakening the immediate local barrier.
func (s *OrganizationService) RunOffboardingIdentitySync(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		s.syncOffboardingIdentities(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (s *OrganizationService) syncOffboardingIdentities(ctx context.Context) {
	if s.offboardingIdentitySync == nil {
		return
	}
	for i := 0; i < 10; i++ {
		user, err := s.repo.ClaimOffboardingIdentity(ctx)
		if errors.Is(err, pgx.ErrNoRows) {
			return
		}
		if err != nil {
			s.logger.Warn("claim offboarding identity sync failed", zap.Error(err))
			return
		}
		operation, cancel := context.WithTimeout(ctx, 30*time.Second)
		err = s.offboardingIdentitySync(operation, user)
		if err == nil {
			err = s.repo.CompleteOffboardingIdentity(operation, user)
		}
		cancel()
		if err != nil {
			s.logger.Warn("offboarding identity sync pending", zap.Int64("user_id", user))
		}
	}
}

func (s *OrganizationService) SetOffboardingFileReindex(index func(context.Context, uuid.UUID) error) {
	s.offboardingFileReindex = index
}
func (s *OrganizationService) reindexOffboardingFiles(ctx context.Context, user int64) {
	batch, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	ids, err := s.repo.OffboardingFileIDs(batch, user)
	if err != nil {
		s.logger.Warn("offboarding search refresh could not load files", zap.Int64("user_id", user))
		return
	}
	for _, id := range ids {
		if batch.Err() != nil {
			return
		}
		operation, stop := context.WithTimeout(batch, 60*time.Second)
		err = s.offboardingFileReindex(operation, id)
		stop()
		if err != nil {
			s.logger.Warn("offboarding search refresh failed", zap.String("file_id", id.String()))
		}
	}
}
