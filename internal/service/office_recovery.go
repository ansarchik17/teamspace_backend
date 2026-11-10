package service

import (
	"context"
	"net/http"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func (s *DriveService) OfficeSaveState(ctx context.Context, user int64, id uuid.UUID) (*domain.OfficeSaveState, error) {
	if _, err := s.canAccessFile(ctx, user, id, domain.RoleViewer); err != nil {
		return nil, err
	}
	state, err := s.repo.OfficeSaveState(ctx, id)
	if err != nil {
		return nil, apperrors.InternalWithErr("read saved checkpoint", err)
	}
	return state, nil
}

// RecoverOfficeSession deliberately abandons the old DocumentServer session.
// Append a revision of the last durable snapshot so expected_version serializes
// recovery against autosaves, restores and other recovery requests.
func (s *DriveService) RecoverOfficeSession(ctx context.Context, user int64, id uuid.UUID, expected int) (int, error) {
	meta, err := s.canAccessFile(ctx, user, id, domain.RoleEditor)
	if err != nil {
		return 0, err
	}
	if expected <= 0 {
		return 0, apperrors.BadRequest("expected_version is required")
	}
	if meta.Version != expected {
		return 0, apperrors.Conflict("file changed; refresh the saved checkpoint")
	}
	if resolveOnlyOfficeDocType(resolveOnlyOfficeFileType(meta.RealName, meta.MimeType)) == "" {
		return 0, apperrors.New(apperrors.ErrCodeInvalidFileType, "unsupported editor file type", http.StatusUnsupportedMediaType)
	}
	return s.appendDriveRevision(ctx, user, meta, meta.ObjectName, meta.Size, expected, expected)
}

func (s *DriveService) RecordOfficeServerError(ctx context.Context, user int64, id uuid.UUID, key string) error {
	meta, err := s.canAccessFile(ctx, user, id, domain.RoleEditor)
	if err != nil {
		return err
	}
	if key == "" || key != activeOnlyOfficeKey(meta) {
		return apperrors.Conflict("editor session is stale")
	}
	return s.repo.RecordOfficeSaveFailure(ctx, id, key, meta.Version, "document_server_error")
}
