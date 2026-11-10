package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

// PublicLinkTarget selects an item within the linked folder. Zero means the root.
type PublicLinkTarget struct {
	Type domain.ResourceType
	ID   uuid.UUID
}

func (s *DriveService) publicLinkDownloadURL(token, fileID uuid.UUID) string {
	return fmt.Sprintf("%s/api/v1/public/drive/files/%s/download?resource_type=file&resource_id=%s", s.publicURL, token, fileID)
}

func (s *DriveService) resolvePublicLinkTarget(ctx context.Context, token uuid.UUID, targets ...PublicLinkTarget) (*domain.DriveSharedLink, error) {
	link, err := s.getActivePublicLink(ctx, token)
	if err != nil {
		return nil, err
	}
	if len(targets) == 0 || targets[0].ID == uuid.Nil {
		return link, nil
	}
	target := targets[0]
	if target.Type != domain.ResourceFile && target.Type != domain.ResourceFolder {
		return nil, apperrors.BadRequest("invalid resource_type")
	}
	if target.ID == link.ResourceID && string(target.Type) == link.ResourceType {
		return link, nil
	}
	if link.ResourceType != "folder" {
		return nil, apperrors.Forbidden("item outside shared folder")
	}
	parent := &target.ID
	if target.Type == domain.ResourceFile {
		file, err := s.repo.GetFileByID(ctx, target.ID)
		if err != nil {
			return nil, apperrors.Internal("file lookup failed")
		}
		if file == nil || file.DeletedAt != nil {
			return nil, apperrors.NotFound("file not found")
		}
		parent = file.FolderID
	}
	seen := make(map[uuid.UUID]bool)
	for parent != nil && !seen[*parent] {
		if *parent == link.ResourceID {
			resolved := *link
			resolved.ResourceID, resolved.ResourceType = target.ID, string(target.Type)
			return &resolved, nil
		}
		seen[*parent] = true
		folder, err := s.repo.GetFolderByID(ctx, *parent)
		if err != nil {
			return nil, apperrors.Internal("folder lookup failed")
		}
		if folder == nil || folder.DeletedAt != nil {
			break
		}
		parent = folder.ParentID
	}
	return nil, apperrors.Forbidden("item outside shared folder")
}

// Always consult the database: revocation takes effect on the next request.
func (s *DriveService) getActivePublicLink(ctx context.Context, token uuid.UUID) (*domain.DriveSharedLink, error) {
	link, err := s.repo.GetSharedLinkByToken(ctx, token)
	if err != nil {
		return nil, apperrors.Internal("link lookup failed")
	}
	if link == nil {
		return nil, apperrors.NotFound("link not found")
	}
	if link.ExpiresAt != nil && !time.Now().Before(*link.ExpiresAt) {
		return nil, apperrors.Forbidden("link expired")
	}
	if _, err := s.requireDriveManager(ctx, link.CreatedBy, domain.ResourceType(link.ResourceType), link.ResourceID); err != nil {
		return nil, err
	}
	return link, nil
}

func (s *DriveService) ListPublicLinks(ctx context.Context, userID int64, resourceType domain.ResourceType, resourceID uuid.UUID, limit, offset int) ([]domain.DriveSharedLink, error) {
	if limit < 1 || limit > 100 || offset < 0 {
		return nil, apperrors.BadRequest("invalid pagination")
	}
	if _, err := s.requireDriveManager(ctx, userID, resourceType, resourceID); err != nil {
		return nil, err
	}
	links, err := s.repo.ListSharedLinks(ctx, resourceType, resourceID, limit, offset)
	if err != nil {
		return nil, apperrors.Internal("link lookup failed")
	}
	return links, nil
}
