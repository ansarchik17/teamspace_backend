package service

import (
	"context"

	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
)

func ensureUserScopedAccess(ctx context.Context, userRepo *repository.UserRepository, requesterID, targetUserID int64) error {
	if userRepo == nil {
		return apperrors.Internal("user repository is not configured")
	}

	requester, err := userRepo.GetByID(ctx, requesterID)
	if err != nil {
		return apperrors.NotFound("Requester not found")
	}

	target, err := userRepo.GetByID(ctx, targetUserID)
	if err != nil {
		return apperrors.NotFound("User not found")
	}

	if !domain.CanAccessUserByScope(requester, target) {
		shared, err := userRepo.ShareConversation(ctx, requesterID, targetUserID)
		if err != nil {
			return apperrors.InternalWithErr("failed to check shared conversation scope", err)
		}
		if !shared {
			return apperrors.Forbidden("User is outside your ministry scope")
		}
	}

	return nil
}

func ensureUsersExist(ctx context.Context, userRepo *repository.UserRepository, targetUserIDs []int64) error {
	if userRepo == nil {
		return apperrors.Internal("user repository is not configured")
	}

	unique := make([]int64, 0, len(targetUserIDs))
	seen := make(map[int64]bool, len(targetUserIDs))
	for _, targetUserID := range targetUserIDs {
		if targetUserID == 0 || seen[targetUserID] {
			continue
		}
		seen[targetUserID] = true
		unique = append(unique, targetUserID)
	}

	if len(unique) == 0 {
		return nil
	}

	users, err := userRepo.GetByIDs(ctx, unique)
	if err != nil {
		return apperrors.InternalWithErr("failed to load users", err)
	}

	if len(users) < len(unique) {
		return apperrors.NotFound("User not found")
	}

	return nil
}
