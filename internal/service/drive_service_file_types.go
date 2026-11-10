package service

import (
	"context"

	"github.com/west6ide/workspace-backend/internal/repository"
)

// GetFileTypes возвращает список типов файлов с количеством для пользователя
func (s *DriveService) GetFileTypes(ctx context.Context, userID int64) ([]repository.FileTypeInfo, error) {
	return s.repo.GetFileTypes(ctx, userID)
}
