package service

import (
	"context"
	"encoding/base64"
	"strings"
	"time"

	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

type InternalDriveImportService struct {
	userRepo     userByIINRepository
	driveService *DriveService
}

type userByIINRepository interface {
	GetByIIN(ctx context.Context, iin string) (*domain.User, error)
}

func NewInternalDriveImportService(userRepo userByIINRepository, driveService *DriveService) *InternalDriveImportService {
	return &InternalDriveImportService{
		userRepo:     userRepo,
		driveService: driveService,
	}
}

func (s *InternalDriveImportService) Import(ctx context.Context, req domain.InternalDriveImportRequest) (*domain.InternalDriveImportResponse, *apperrors.AppError) {
	normalized, payload, appErr := validateInternalDriveImportRequest(req)
	if appErr != nil {
		return nil, appErr
	}
	if s.driveService == nil {
		return nil, apperrors.Internal("drive service is not available")
	}

	user, err := s.userRepo.GetByIIN(ctx, normalized.IIN)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "user not found") {
			return nil, apperrors.New("USER_NOT_FOUND", "user not found by iin", 404)
		}
		return nil, apperrors.InternalWithErr("failed to find user by iin", err)
	}

	driveFile, importErr := s.driveService.ImportExternalFileToDrive(ctx, user.ID, ExternalDriveImportRequest{
		AttachmentURL:      normalized.AttachmentURL,
		AttachmentName:     normalized.AttachmentName,
		AttachmentMimeType: normalized.AttachmentMimeType,
		AttachmentContent:  payload,
		SourceSystemCode:   normalized.SourceSystemCode,
		SourceSystemName:   normalized.SourceSystemName,
		ExternalMessageID:  normalized.ExternalMessageID,
	})
	if importErr != nil {
		return nil, apperrors.ToAppError(importErr)
	}

	return &domain.InternalDriveImportResponse{
		Status:      "SUCCESS",
		Message:     "File imported to drive",
		DriveFileID: driveFile.ID,
		ImportedAt:  time.Now().UTC(),
	}, nil
}

func validateInternalDriveImportRequest(req domain.InternalDriveImportRequest) (domain.InternalDriveImportRequest, []byte, *apperrors.AppError) {
	req.IIN = strings.TrimSpace(req.IIN)
	req.AttachmentName = strings.TrimSpace(req.AttachmentName)
	req.AttachmentMimeType = strings.TrimSpace(req.AttachmentMimeType)
	req.AttachmentURL = strings.TrimSpace(req.AttachmentURL)
	req.AttachmentContent = strings.TrimSpace(req.AttachmentContent)
	req.SourceSystemCode = strings.ToUpper(strings.TrimSpace(req.SourceSystemCode))
	req.SourceSystemName = strings.TrimSpace(req.SourceSystemName)
	req.ExternalMessageID = strings.TrimSpace(req.ExternalMessageID)

	switch {
	case req.IIN == "":
		return req, nil, apperrors.Validation("iin is required")
	case len([]rune(req.IIN)) != 12:
		return req, nil, apperrors.Validation("iin must contain 12 characters")
	case req.AttachmentName == "":
		return req, nil, apperrors.Validation("attachmentName is required")
	case req.SourceSystemCode == "":
		return req, nil, apperrors.Validation("sourceSystemCode is required")
	case req.AttachmentURL == "" && req.AttachmentContent == "":
		return req, nil, apperrors.Validation("attachmentUrl or attachmentContent is required")
	}

	var payload []byte
	if req.AttachmentContent != "" {
		decoded, err := base64.StdEncoding.DecodeString(req.AttachmentContent)
		if err != nil {
			return req, nil, apperrors.Validation("attachmentContent must be valid base64")
		}
		payload = decoded
	}

	return req, payload, nil
}
