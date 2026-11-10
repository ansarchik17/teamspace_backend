package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
)

func (s *DriveService) ListFileVersions(ctx context.Context, userID int64, id uuid.UUID, limit, offset int) ([]domain.DriveFileVersion, error) {
	if _, err := s.canAccessFile(ctx, userID, id, domain.RoleViewer); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	versions, err := s.repo.ListFileVersions(ctx, id, limit, offset)
	if err != nil {
		return nil, apperrors.InternalWithErr("list file versions failed", err)
	}
	return versions, nil
}

func (s *DriveService) DownloadFileVersion(ctx context.Context, userID int64, id uuid.UUID, version int, metadataOnly ...bool) (io.ReadCloser, int64, string, error) {
	meta, err := s.canAccessFile(ctx, userID, id, domain.RoleViewer)
	if err != nil {
		return nil, 0, "", err
	}
	v, err := s.repo.GetFileVersion(ctx, id, version)
	if err != nil {
		return nil, 0, "", apperrors.InternalWithErr("read file version failed", err)
	}
	if v == nil {
		return nil, 0, "", apperrors.NotFound("file version not found")
	}
	reader, _, size, err := s.fileSvc.DownloadFile(ctx, v.ObjectName)
	if err != nil {
		return nil, 0, "", err
	}
	if len(metadataOnly) == 0 || !metadataOnly[0] {
		if err := s.auditDriveDownload(ctx, userID, meta, version, false); err != nil {
			reader.Close()
			return nil, 0, "", err
		}
	}
	return reader, size, meta.RealName, nil
}

func (s *DriveService) RestoreFileVersion(ctx context.Context, userID int64, id uuid.UUID, version, expected int) (int, error) {
	meta, err := s.canAccessFile(ctx, userID, id, domain.RoleEditor)
	if err != nil {
		return 0, err
	}
	if expected <= 0 {
		return 0, apperrors.BadRequest("expected_version is required")
	}
	if meta.Version != expected {
		return 0, apperrors.Conflict("Файл изменился. Обновите историю версий и повторите попытку.")
	}
	v, err := s.repo.GetFileVersion(ctx, id, version)
	if err != nil {
		return 0, apperrors.InternalWithErr("read file version failed", err)
	}
	if v == nil {
		return 0, apperrors.NotFound("file version not found")
	}
	if version == meta.Version {
		return 0, apperrors.Conflict("Эта версия уже является текущей.")
	}
	return s.appendDriveRevision(ctx, userID, meta, v.ObjectName, v.Size, expected, version)
}

func (s *DriveService) UploadFileVersion(ctx context.Context, userID int64, id uuid.UUID, filename string, size int64, reader io.Reader, expected int) (int, error) {
	meta, err := s.canAccessFile(ctx, userID, id, domain.RoleEditor)
	if err != nil {
		return 0, err
	}
	if expected <= 0 {
		return 0, apperrors.BadRequest("expected_version is required")
	}
	if meta.Version != expected {
		return 0, apperrors.Conflict("Файл изменился. Обновите историю версий и повторите попытку.")
	}
	if !strings.EqualFold(filepath.Ext(filename), filepath.Ext(meta.RealName)) {
		return 0, apperrors.BadRequest("Новая версия должна иметь тот же формат файла.")
	}
	if size < 0 {
		return 0, apperrors.BadRequest("invalid file size")
	}
	if size > MaxFileSize {
		return 0, apperrors.DriveUploadTooBig(MaxFileSize)
	}
	if err := s.checkDriveUpload(ctx, meta.OwnerID, size, "file"); err != nil {
		return 0, err
	}
	object, err := s.fileSvc.UploadFile(ctx, userID, meta.RealName, "file", size, reader)
	if err != nil {
		return 0, err
	}
	// Recheck permissions after streaming a potentially large upload.
	if _, err = s.canAccessFile(ctx, userID, id, domain.RoleEditor); err != nil {
		_ = s.fileSvc.DeleteFile(ctx, object)
		return 0, err
	}
	next, err := s.appendDriveRevision(ctx, userID, meta, object, size, expected, 0)
	// Definite quota/limit/conflict rejections did not commit this object.
	if err != nil {
		status := apperrors.ToAppError(err).StatusCode
		if status == http.StatusInsufficientStorage || status == http.StatusRequestEntityTooLarge || status == http.StatusConflict {
			_ = s.rollbackDriveUpload(ctx, object)
		}
	}
	// Storage cleanup is left to reconciliation for ambiguous commit failures.
	return next, err
}

func (s *DriveService) appendDriveRevision(ctx context.Context, userID int64, meta *domain.DriveFile, object string, size int64, expected, restoredFrom int) (int, error) {
	next, err := s.repo.AppendFileVersion(ctx, meta.ID, object, size, userID, expected)
	if errors.Is(err, repository.ErrDriveVersionConflict) {
		return 0, apperrors.Conflict("Файл изменился. Обновите историю версий и повторите попытку.")
	}
	if err != nil {
		return 0, apperrors.InternalWithErr("save file version failed", err)
	}
	path, _ := s.buildFilePath(ctx, meta.FolderID, meta.RealName)
	s.logDriveActivity(ctx, &domain.DriveActivity{Action: domain.DriveActivityActionEdited, ResourceType: domain.ResourceFile, ResourceID: meta.ID, ResourceName: meta.RealName, OwnerID: meta.OwnerID, ActorID: userID, FolderID: cloneUUIDPtr(meta.FolderID), ResourcePath: path, ToPath: path, Metadata: rawJSONMap(map[string]any{"previous_version": meta.Version, "new_version": next, "restored_from_version": restoredFrom})})
	s.recordActivity(userID, domain.ResourceFile, meta.ID)
	s.syncDriveFileIndex(meta.ID)
	return next, nil
}
