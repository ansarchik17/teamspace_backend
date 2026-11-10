package service

import (
	"context"

	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

const DriveMaxRequestBytes int64 = MaxVideoSize + 1024*1024
const DriveMaxFilesPerRequest = 20

func (s *DriveService) GetDriveStorage(ctx context.Context, userID int64) (*domain.DriveStorage, error) {
	if userID <= 0 {
		return nil, apperrors.Unauthorized("unauthorized")
	}
	storage, err := s.repo.GetDriveStorage(ctx, userID)
	if err != nil {
		return nil, apperrors.InternalWithErr("get drive storage failed", err)
	}
	storage.MaxRequestBytes = DriveMaxRequestBytes
	storage.MaxFilesPerRequest = DriveMaxFilesPerRequest
	storage.UploadLimits = map[string]int64{"file": driveMinBytes(storage.MaxUploadBytes, MaxFileSize), "image": driveMinBytes(storage.MaxUploadBytes, MaxImageSize), "audio": driveMinBytes(storage.MaxUploadBytes, MaxAudioSize), "video": driveMinBytes(storage.MaxUploadBytes, MaxVideoSize)}
	return storage, nil
}

// This preflight avoids unnecessary object uploads. Database triggers enforce
// the same total at commit time under concurrent writes and on non-HTTP paths.
func (s *DriveService) CheckDriveCapacity(ctx context.Context, userID, size int64) error {
	if size < 0 {
		return apperrors.BadRequest("invalid file size")
	}
	storage, err := s.GetDriveStorage(ctx, userID)
	if err != nil {
		return err
	}
	if size > storage.FreeBytes {
		return apperrors.DriveQuotaExceeded(map[string]any{"quota_bytes": storage.QuotaBytes, "used_bytes": storage.UsedBytes, "free_bytes": storage.FreeBytes, "requested_bytes": size})
	}
	return nil
}

func (s *DriveService) checkDriveUpload(ctx context.Context, ownerID, size int64, category string) error {
	if size < 0 {
		return apperrors.BadRequest("invalid file size")
	}
	storage, err := s.GetDriveStorage(ctx, ownerID)
	if err != nil {
		return err
	}
	limit, ok := storage.UploadLimits[category]
	if !ok {
		limit = storage.UploadLimits["file"]
	}
	if size > limit {
		return apperrors.DriveUploadTooBig(limit)
	}
	if size > storage.FreeBytes {
		return apperrors.DriveQuotaExceeded(map[string]any{"quota_bytes": storage.QuotaBytes, "used_bytes": storage.UsedBytes, "free_bytes": storage.FreeBytes, "requested_bytes": size})
	}
	return nil
}

func driveMinBytes(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
