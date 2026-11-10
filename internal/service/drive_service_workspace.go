package service

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
)

func (s *DriveService) GetWorkspaceSummary(ctx context.Context, userID int64) (*domain.DriveSummary, error) {
	if userID <= 0 {
		return nil, apperrors.Unauthorized("unauthorized")
	}
	summary, err := s.repo.GetDriveSummary(ctx, userID)
	if err != nil {
		return nil, apperrors.InternalWithErr("get drive summary failed", err)
	}
	summary.Storage, err = s.GetDriveStorage(ctx, userID)
	if err != nil {
		return nil, err
	}
	summary.UsedBytes = summary.Storage.UsedBytes
	return summary, nil
}

func (s *DriveService) AddFavorite(ctx context.Context, userID int64, resourceType domain.ResourceType, resourceID uuid.UUID) error {
	switch resourceType {
	case domain.ResourceFile:
		if _, err := s.canAccessFile(ctx, userID, resourceID, domain.RoleViewer); err != nil {
			return err
		}
	case domain.ResourceFolder:
		if _, err := s.canWorkInFolder(ctx, userID, resourceID, domain.RoleViewer); err != nil {
			return err
		}
	default:
		return apperrors.BadRequest("invalid resource type")
	}
	if err := s.repo.AddFavorite(ctx, userID, resourceType, resourceID); err != nil {
		return apperrors.InternalWithErr("add favorite failed", err)
	}
	return nil
}

func (s *DriveService) RemoveFavorite(ctx context.Context, userID int64, resourceType domain.ResourceType, resourceID uuid.UUID) error {
	if err := s.repo.RemoveFavorite(ctx, userID, resourceType, resourceID); err != nil {
		return apperrors.InternalWithErr("remove favorite failed", err)
	}
	return nil
}

func (s *DriveService) ListFavorites(ctx context.Context, userID int64, limit, offset int) ([]domain.DriveItem, error) {
	refs, err := s.repo.ListFavoriteRefs(ctx, userID)
	if err != nil {
		return nil, apperrors.InternalWithErr("list favorites failed", err)
	}

	items := make([]domain.DriveItem, 0, len(refs))
	for _, ref := range refs {
		switch ref.ResourceType {
		case domain.ResourceFile:
			file, accessErr := s.canAccessFile(ctx, userID, ref.ResourceID, domain.RoleViewer)
			if accessErr != nil || file == nil || file.DeletedAt != nil || file.Scope != "drive" {
				_ = s.repo.RemoveFavorite(ctx, userID, ref.ResourceType, ref.ResourceID)
				continue
			}
			s.fillFileUrl(file)
			items = append(items, domain.DriveItem{Type: domain.ResourceFile, File: file})
		case domain.ResourceFolder:
			folder, accessErr := s.canWorkInFolder(ctx, userID, ref.ResourceID, domain.RoleViewer)
			if accessErr != nil || folder == nil || folder.DeletedAt != nil {
				_ = s.repo.RemoveFavorite(ctx, userID, ref.ResourceType, ref.ResourceID)
				continue
			}
			items = append(items, domain.DriveItem{Type: domain.ResourceFolder, Folder: folder})
		}
	}

	if offset >= len(items) {
		return []domain.DriveItem{}, nil
	}
	end := len(items)
	if limit > 0 && offset+limit < end {
		end = offset + limit
	}
	return items[offset:end], nil
}

func (s *DriveService) ListTrash(ctx context.Context, userID int64, limit, offset int) ([]domain.DriveTrashItem, error) {
	items, err := s.repo.ListTrash(ctx, userID)
	if err != nil {
		return nil, apperrors.InternalWithErr("list drive trash failed", err)
	}
	for i := range items {
		items[i].ExpiresAt = items[i].DeletedAt.Add(domain.DriveTrashRetention)
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].DeletedAt.After(items[j].DeletedAt) })
	if offset >= len(items) {
		return []domain.DriveTrashItem{}, nil
	}
	end := len(items)
	if limit > 0 && offset+limit < end {
		end = offset + limit
	}
	return items[offset:end], nil
}

func (s *DriveService) RestoreTrashItem(ctx context.Context, userID int64, resourceType domain.ResourceType, resourceID uuid.UUID) error {
	switch resourceType {
	case domain.ResourceFile:
		file, err := s.repo.GetFileByID(ctx, resourceID)
		if err != nil || file == nil || file.OwnerID != userID || file.DeletedAt == nil || file.Scope != "drive" {
			return apperrors.NotFound("file not found in trash")
		}
		if !time.Now().Before(file.DeletedAt.Add(domain.DriveTrashRetention)) {
			return apperrors.Conflict("Срок восстановления истёк: объект находится в корзине более 30 дней.")
		}
		if file.FolderID != nil {
			parent, parentErr := s.repo.GetFolderByID(ctx, *file.FolderID)
			if parentErr != nil || parent == nil || parent.DeletedAt != nil {
				return apperrors.Conflict("restore the parent folder first")
			}
		}
		if err := s.repo.RestoreFile(ctx, userID, resourceID); err != nil {
			if errors.Is(err, repository.ErrDriveTrashUnavailable) {
				return apperrors.Conflict("Объект больше недоступен для восстановления. Обновите корзину.")
			}
			return apperrors.InternalWithErr("restore file failed", err)
		}
		s.syncDriveFileIndex(resourceID)
		path, _ := s.buildFilePath(ctx, file.FolderID, file.RealName)
		s.logDriveActivity(ctx, &domain.DriveActivity{
			Action:       domain.DriveActivityActionRestored,
			ResourceType: domain.ResourceFile,
			ResourceID:   resourceID,
			ResourceName: file.RealName,
			OwnerID:      file.OwnerID,
			ActorID:      userID,
			FolderID:     cloneUUIDPtr(file.FolderID),
			ToFolderID:   cloneUUIDPtr(file.FolderID),
			ResourcePath: path,
			ToPath:       path,
		})
	case domain.ResourceFolder:
		folder, err := s.repo.GetFolderByID(ctx, resourceID)
		if err != nil || folder == nil || folder.OwnerID != userID || folder.DeletedAt == nil {
			return apperrors.NotFound("folder not found in trash")
		}
		if !time.Now().Before(folder.DeletedAt.Add(domain.DriveTrashRetention)) {
			return apperrors.Conflict("Срок восстановления истёк: объект находится в корзине более 30 дней.")
		}
		if folder.ParentID != nil {
			parent, parentErr := s.repo.GetFolderByID(ctx, *folder.ParentID)
			if parentErr != nil || parent == nil || parent.DeletedAt != nil {
				return apperrors.Conflict("restore the parent folder first")
			}
		}
		if err := s.repo.RestoreFolderTree(ctx, userID, resourceID); err != nil {
			if errors.Is(err, repository.ErrDriveTrashUnavailable) {
				return apperrors.Conflict("Объект больше недоступен для восстановления. Обновите корзину.")
			}
			return apperrors.InternalWithErr("restore folder failed", err)
		}
		s.queueDriveFolderIndexSync(resourceID)
		path, _ := s.buildFolderPathByParent(ctx, folder.ParentID, folder.Name)
		s.logDriveActivity(ctx, &domain.DriveActivity{
			Action:       domain.DriveActivityActionRestored,
			ResourceType: domain.ResourceFolder,
			ResourceID:   resourceID,
			ResourceName: folder.Name,
			OwnerID:      folder.OwnerID,
			ActorID:      userID,
			FolderID:     cloneUUIDPtr(folder.ParentID),
			ToFolderID:   cloneUUIDPtr(folder.ParentID),
			ResourcePath: path,
			ToPath:       path,
		})
	default:
		return apperrors.BadRequest("invalid resource type")
	}

	_ = s.repo.RemoveFavorite(ctx, userID, resourceType, resourceID)
	return nil
}
