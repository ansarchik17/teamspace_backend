package service

import (
	"context"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func driveItemKey(item domain.DriveItem) string {
	switch item.Type {
	case domain.ResourceFolder:
		if item.Folder != nil {
			return "folder:" + item.Folder.ID.String()
		}
	case domain.ResourceFile:
		if item.File != nil {
			return "file:" + item.File.ID.String()
		}
	}
	return ""
}

// ListSharedItemsFiltered возвращает отфильтрованные и отсортированные расшаренные элементы
func (s *DriveService) ListSharedItemsFiltered(
	ctx context.Context,
	userID int64,
	limit, offset int,
	fileType string,
	ownerID *int64,
	sortBy, sortOrder string,
) ([]domain.DriveItem, error) {
	folders, err := s.repo.ListSharedFoldersPaginated(ctx, userID, -1, 0) // Получаем все для фильтрации
	if err != nil {
		return nil, apperrors.Internal("list shared folders failed")
	}
	files, err := s.repo.ListSharedFilesPaginated(ctx, userID, -1, 0) // Получаем все для фильтрации
	if err != nil {
		return nil, apperrors.Internal("list shared files failed")
	}

	seen := make(map[string]struct{})

	// Объединяем в один список
	allItems := make([]domain.DriveItem, 0, len(folders)+len(files))
	for _, f := range folders {
		item := domain.DriveItem{Type: domain.ResourceFolder, Folder: f}
		key := driveItemKey(item)
		if key != "" {
			seen[key] = struct{}{}
		}
		allItems = append(allItems, item)
	}
	for _, f := range files {
		s.fillFileUrl(f)
		item := domain.DriveItem{Type: domain.ResourceFile, File: f}
		key := driveItemKey(item)
		if key != "" {
			seen[key] = struct{}{}
		}
		allItems = append(allItems, item)
	}

	// Используем общую функцию фильтрации и сортировки
	filtered := FilterAndSortDriveItems(allItems, fileType, ownerID, sortBy, sortOrder)

	// Пагинация
	return PaginateItems(filtered, limit, offset), nil
}

// ListOwnItemsFiltered возвращает отфильтрованные и отсортированные личные элементы (root)
func (s *DriveService) ListOwnItemsFiltered(
	ctx context.Context,
	userID int64,
	limit, offset int,
	fileType string,
	ownerID *int64,
	sortBy, sortOrder string,
) ([]domain.DriveItem, error) {
	// Получаем ВСЕ корневые элементы (root)
	folders, err := s.repo.ListRootFoldersByOwnerPaginated(ctx, userID, -1, 0)
	if err != nil {
		return nil, apperrors.Internal("list own root folders failed")
	}
	files, err := s.repo.ListRootFilesByOwnerPaginated(ctx, userID, -1, 0)
	if err != nil {
		return nil, apperrors.Internal("list own root files failed")
	}

	// Объединяем
	allItems := make([]domain.DriveItem, 0, len(folders)+len(files))
	for _, f := range folders {
		allItems = append(allItems, domain.DriveItem{Type: domain.ResourceFolder, Folder: f})
	}
	for _, f := range files {
		s.fillFileUrl(f)
		allItems = append(allItems, domain.DriveItem{Type: domain.ResourceFile, File: f})
	}

	filtered := FilterAndSortDriveItems(allItems, fileType, ownerID, sortBy, sortOrder)

	return PaginateItems(filtered, limit, offset), nil
}

// ListAllDriveItemsFiltered возвращает отфильтрованные личные и расшаренные
func (s *DriveService) ListAllDriveItemsFiltered(
	ctx context.Context,
	userID int64,
	limit, offset int,
	fileType string,
	ownerID *int64,
	sortBy, sortOrder string,
) (own []domain.DriveItem, shared []domain.DriveItem, err error) {
	own, err = s.ListOwnItemsFiltered(ctx, userID, limit, offset, fileType, ownerID, sortBy, sortOrder)
	if err != nil {
		return nil, nil, err
	}

	shared, err = s.ListSharedItemsFiltered(ctx, userID, limit, offset, fileType, ownerID, sortBy, sortOrder)
	if err != nil {
		return nil, nil, err
	}

	return own, shared, nil
}

// ListFolderChildrenFiltered возвращает содержимое папки с фильтрацией
func (s *DriveService) ListFolderChildrenFiltered(
	ctx context.Context,
	userID int64,
	folderID uuid.UUID,
	limit, offset int,
	fileType string,
	ownerID *int64,
	sortBy, sortOrder string,
) ([]domain.DriveItem, error) {
	// Проверка доступа
	if _, err := s.canWorkInFolder(ctx, userID, folderID, domain.RoleViewer); err != nil {
		return nil, err
	}

	// Получаем все элементы
	folders, err := s.repo.ListFoldersByParent(ctx, &folderID)
	if err != nil {
		return nil, apperrors.Internal("list folders failed")
	}
	files, err := s.repo.ListFilesByFolder(ctx, &folderID)
	if err != nil {
		return nil, apperrors.Internal("list files failed")
	}

	allItems := make([]domain.DriveItem, 0, len(folders)+len(files))
	for _, f := range folders {
		allItems = append(allItems, domain.DriveItem{Type: domain.ResourceFolder, Folder: f})
	}
	for _, f := range files {
		s.fillFileUrl(f)
		allItems = append(allItems, domain.DriveItem{Type: domain.ResourceFile, File: f})
	}

	filtered := FilterAndSortDriveItems(allItems, fileType, ownerID, sortBy, sortOrder)

	return PaginateItems(filtered, limit, offset), nil
}

// ListSharingUsers returns a list of owners who shared files/folders with current user
func (s *DriveService) ListSharingUsers(ctx context.Context, userID int64) ([]domain.UserBasic, error) {
	users, err := s.repo.ListSharingUsers(ctx, userID)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to list sharing users", err)
	}
	return users, nil
}
