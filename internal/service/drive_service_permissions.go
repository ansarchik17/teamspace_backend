package service

import (
	"context"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

// GetMyRole возвращает роль текущего пользователя для указанного ресурса
func (s *DriveService) GetMyRole(ctx context.Context, userID int64, t domain.ResourceType, id uuid.UUID) (domain.PermissionRole, error) {
	switch t {
	case domain.ResourceFolder:
		f, err := s.repo.GetFolderByID(ctx, id)
		if err != nil || f == nil || f.DeletedAt != nil {
			return "", apperrors.NotFound("folder not found")
		}

		role, err := s.getEffectiveFolderWorkspaceRole(ctx, userID, f)
		if err != nil {
			return "", apperrors.Internal("failed to check permissions")
		}
		if role != nil {
			return *role, nil
		}
		return "", nil
	case domain.ResourceFile:
		f, err := s.repo.GetFileByID(ctx, id)
		if err != nil || f == nil || f.DeletedAt != nil {
			return "", apperrors.NotFound("file not found")
		}

		role, err := s.getEffectiveFileRole(ctx, userID, f)
		if err != nil {
			return "", apperrors.Internal("failed to check permissions")
		}
		if role != nil {
			return *role, nil
		}
		return "", nil
	default:
		return "", apperrors.BadRequest("invalid resource type")
	}
}

// owner is the existing ACL value for manage-access, distinct from owner_id.
func normalizeDrivePermission(role domain.PermissionRole) (domain.PermissionRole, error) {
	if role == "manager" {
		role = domain.RoleOwner
	}
	switch role {
	case domain.RoleViewer, domain.RoleEditor, domain.RoleOwner:
		return role, nil
	}
	return "", apperrors.BadRequest("invalid permission role")
}

func (s *DriveService) requireDriveManager(ctx context.Context, userID int64, t domain.ResourceType, id uuid.UUID) (int64, error) {
	switch t {
	case domain.ResourceFolder:
		f, err := s.canAccessFolder(ctx, userID, id, domain.RoleOwner)
		if err != nil {
			return 0, err
		}
		return f.OwnerID, nil
	case domain.ResourceFile:
		f, err := s.canAccessFile(ctx, userID, id, domain.RoleOwner)
		if err != nil {
			return 0, err
		}
		return f.OwnerID, nil
	}
	return 0, apperrors.BadRequest("invalid resource_type")
}

func (s *DriveService) RevokePublicLink(ctx context.Context, userID int64, token uuid.UUID) error {
	link, err := s.repo.GetSharedLinkByToken(ctx, token)
	if err != nil {
		return apperrors.Internal("link lookup failed")
	}
	if link == nil {
		return apperrors.NotFound("link not found")
	}
	if _, err = s.requireDriveManager(ctx, userID, domain.ResourceType(link.ResourceType), link.ResourceID); err != nil {
		return err
	}
	return s.repo.DeleteSharedLink(ctx, token)
}

func (s *DriveService) validateDriveRecipients(ctx context.Context, ids []int64) error {
	for _, id := range ids {
		if id <= 0 {
			return apperrors.BadRequest("invalid user_id")
		}
		exists, err := s.repo.UserExists(ctx, id)
		if err != nil {
			return apperrors.Internal("user lookup failed")
		}
		if !exists {
			return apperrors.BadRequest("target user not found")
		}
	}
	return nil
}
