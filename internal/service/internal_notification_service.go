package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/i18n"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

type InternalNotificationService struct {
	userRepo            *repository.UserRepository
	notificationRepo    *repository.NotificationRepository
	notificationService *NotificationService
	driveService        *DriveService
	logger              *zap.Logger
}

func NewInternalNotificationService(
	userRepo *repository.UserRepository,
	notificationRepo *repository.NotificationRepository,
	notificationService *NotificationService,
	logger *zap.Logger,
) *InternalNotificationService {
	return &InternalNotificationService{
		userRepo:            userRepo,
		notificationRepo:    notificationRepo,
		notificationService: notificationService,
		logger:              logger.With(zap.String("service", "internal_notification")),
	}
}

func (s *InternalNotificationService) SetDriveService(driveService *DriveService) {
	s.driveService = driveService
}

func (s *InternalNotificationService) Process(ctx context.Context, req domain.InternalNotificationRequest) (*domain.InternalNotificationResponse, *apperrors.AppError) {
	normalized, appErr := validateInternalNotificationRequest(req)
	if appErr != nil {
		return nil, appErr
	}

	processedAt := nowAlmaty()
	if normalized.ExternalMessageID != "" {
		existing, err := s.notificationRepo.FindByExternalMessageID(ctx, normalized.SourceSystemCode, normalized.ExternalMessageID)
		if err != nil {
			return nil, apperrors.InternalWithErr("failed to check duplicate notification", err)
		}
		if existing != nil {
			s.logger.Info("Duplicate internal notification skipped",
				zap.String("source_system_code", normalized.SourceSystemCode),
				zap.String("external_message_id", normalized.ExternalMessageID),
				zap.String("notification_id", existing.ID.String()),
			)
			return &domain.InternalNotificationResponse{
				Status:         "SUCCESS_DUPLICATE",
				NotificationID: existing.ID,
				Message:        i18n.APIMessage(ctx, "Notification already processed"),
				PushStatus:     existing.PushStatus,
				ProcessedAt:    processedAt,
			}, nil
		}
	}

	user, err := s.userRepo.GetByIIN(ctx, normalized.IIN)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "user not found") {
			return nil, apperrors.New("USER_NOT_FOUND", "user not found by iin", 404)
		}
		return nil, apperrors.InternalWithErr("failed to find user by iin", err)
	}

	content := buildRecipientNotificationContent(normalized, user.Language)

	sentAt, appErr := parseOptionalSentAt(normalized.SentAt)
	if appErr != nil {
		return nil, appErr
	}

	notificationID := uuid.New()
	sourceSystemCode := normalized.SourceSystemCode
	sourceSystemName := content.SourceSystemName
	var externalMessageID *string
	if normalized.ExternalMessageID != "" {
		externalMessageID = &normalized.ExternalMessageID
	}

	var importedDriveFile *domain.DriveFile
	if shouldImportNotificationToDrive(normalized) && s.driveService != nil {
		importedDriveFile, err = s.driveService.ImportExternalFileToDrive(ctx, user.ID, ExternalDriveImportRequest{
			AttachmentURL:      normalized.AttachmentURL,
			AttachmentName:     normalized.AttachmentName,
			AttachmentMimeType: normalized.AttachmentMimeType,
			SourceSystemCode:   normalized.SourceSystemCode,
			SourceSystemName:   sourceSystemName,
			ExternalMessageID:  normalized.ExternalMessageID,
		})
		if err != nil {
			return nil, apperrors.ToAppError(err)
		}
	}

	notification := &domain.Notification{
		ID:                notificationID,
		UserID:            user.ID,
		Type:              "external_system",
		Title:             content.Title,
		Content:           content.MessageText,
		ShortText:         stringPointer(content.ShortText),
		SourceSystemName:  stringPointer(content.SourceSystemName),
		NotificationType:  stringPointer(content.NotificationType),
		ApplicationNumber: optionalStringPointer(content.ApplicationNumber),
		ActionURL:         optionalStringPointer(content.ActionURL),
		IsRead:            false,
		Metadata:          internalNotificationMetadata(normalized, content),
		SourceSystemCode:  &sourceSystemCode,
		ExternalMessageID: externalMessageID,
		Status:            content.Status,
		PushStatus:        domain.PushStatusPending,
		SentAt:            sentAt,
		CreatedAt:         processedAt,
		ProcessedAt:       &processedAt,
		UpdatedAt:         &processedAt,
	}

	if err := s.notificationRepo.CreateInternal(ctx, notification); err != nil {
		if normalized.ExternalMessageID != "" {
			existing, lookupErr := s.notificationRepo.FindByExternalMessageID(ctx, normalized.SourceSystemCode, normalized.ExternalMessageID)
			if lookupErr == nil && existing != nil {
				return &domain.InternalNotificationResponse{
					Status:         "SUCCESS_DUPLICATE",
					NotificationID: existing.ID,
					Message:        i18n.APIMessage(ctx, "Notification already processed"),
					PushStatus:     existing.PushStatus,
					ProcessedAt:    nowAlmaty(),
				}, nil
			}
		}
		return nil, apperrors.InternalWithErr("failed to save notification", err)
	}

	pushStatus, pushErr := s.notificationService.SendPushNotificationNow(
		ctx,
		user.ID,
		notification.Title,
		notification.Content,
		map[string]string{
			"notification_id":     notificationID.String(),
			"type":                notification.Type,
			"notification_type":   content.NotificationType,
			"status":              content.Status,
			"source_system_name":  content.SourceSystemName,
			"application_number":  content.ApplicationNumber,
			"action_url":          content.ActionURL,
			"source_system_code":  normalized.SourceSystemCode,
			"external_message_id": normalized.ExternalMessageID,
			"drive_file_id": func() string {
				if importedDriveFile == nil {
					return ""
				}
				return importedDriveFile.ID.String()
			}(),
		},
	)

	var errorMessage *string
	status := domain.NotificationStatusProcessed
	message := i18n.APIMessage(ctx, "Notification accepted")
	if pushErr != nil {
		status = domain.NotificationStatusProcessed
		pushStatus = domain.PushStatusFailed
		msg := pushErr.Error()
		errorMessage = &msg
		message = i18n.APIMessage(ctx, "Notification accepted, push failed")
		s.logger.Warn("Push failed for internal notification",
			zap.String("notification_id", notificationID.String()),
			zap.Int64("user_id", user.ID),
			zap.Error(pushErr),
		)
	}

	updatedAt := nowAlmaty()
	if err := s.notificationRepo.UpdateInternalProcessingResult(ctx, notificationID, status, pushStatus, errorMessage, updatedAt); err != nil {
		return nil, apperrors.InternalWithErr("failed to update notification processing result", err)
	}

	s.logger.Info("Internal notification processed",
		zap.String("source_system_code", normalized.SourceSystemCode),
		zap.String("notification_id", notificationID.String()),
		zap.String("iin_masked", maskIIN(normalized.IIN)),
		zap.String("push_status", pushStatus),
	)

	return &domain.InternalNotificationResponse{
		Status:         "SUCCESS",
		NotificationID: notificationID,
		Message:        message,
		PushStatus:     pushStatus,
		ProcessedAt:    updatedAt,
		DriveFileID: func() *uuid.UUID {
			if importedDriveFile == nil {
				return nil
			}
			return &importedDriveFile.ID
		}(),
	}, nil
}

func validateInternalNotificationRequest(req domain.InternalNotificationRequest) (domain.InternalNotificationRequest, *apperrors.AppError) {
	req.IIN = strings.TrimSpace(req.IIN)
	req.Title = strings.TrimSpace(req.Title)
	req.MessageText = strings.TrimSpace(req.MessageText)
	req.ShortText = strings.TrimSpace(req.ShortText)
	req.SourceSystemCode = strings.ToUpper(strings.TrimSpace(req.SourceSystemCode))
	req.SourceSystemName = strings.TrimSpace(req.SourceSystemName)
	req.NotificationType = strings.ToUpper(strings.TrimSpace(req.NotificationType))
	req.Status = strings.ToUpper(strings.TrimSpace(req.Status))
	req.ExternalMessageID = strings.TrimSpace(req.ExternalMessageID)
	req.ApplicationNumber = strings.TrimSpace(req.ApplicationNumber)
	req.ActionURL = strings.TrimSpace(req.ActionURL)
	req.Language = strings.ToLower(strings.TrimSpace(req.Language))
	req.SentAt = strings.TrimSpace(req.SentAt)
	req.AttachmentURL = strings.TrimSpace(req.AttachmentURL)
	req.AttachmentName = strings.TrimSpace(req.AttachmentName)
	req.AttachmentMimeType = strings.TrimSpace(req.AttachmentMimeType)

	switch {
	case req.IIN == "":
		return req, apperrors.Validation("iin is required")
	case len([]rune(req.IIN)) != 12:
		return req, apperrors.Validation("iin must contain 12 characters")
	case req.SourceSystemCode == "":
		return req, apperrors.Validation("sourceSystemCode is required")
	}
	return req, nil
}

func parseOptionalSentAt(value string) (*time.Time, *apperrors.AppError) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}

	layouts := []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			if layout != time.RFC3339 {
				parsed = time.Date(parsed.Year(), parsed.Month(), parsed.Day(), parsed.Hour(), parsed.Minute(), parsed.Second(), parsed.Nanosecond(), almatyLocation())
			}
			return &parsed, nil
		}
	}
	return nil, apperrors.Validation("sentAt must be ISO date-time")
}

func internalNotificationMetadata(req domain.InternalNotificationRequest, content notificationContent) map[string]string {
	meta := map[string]string{
		"source_system_code": req.SourceSystemCode,
		"source_system_name": content.SourceSystemName,
		"notification_type":  content.NotificationType,
		"status":             content.Status,
		"language":           content.Language,
	}
	if req.ExternalMessageID != "" {
		meta["external_message_id"] = req.ExternalMessageID
	}
	if content.ApplicationNumber != "" {
		meta["application_number"] = content.ApplicationNumber
	}
	if content.ActionURL != "" {
		meta["action_url"] = content.ActionURL
	}
	if req.SentAt != "" {
		meta["sent_at"] = req.SentAt
	}
	if req.Title != "" {
		meta["requested_title"] = req.Title
	}
	if req.MessageText != "" {
		meta["requested_message_text"] = req.MessageText
	}
	if req.AttachmentURL != "" {
		meta["attachment_url"] = req.AttachmentURL
	}
	if req.AttachmentName != "" {
		meta["attachment_name"] = req.AttachmentName
	}
	return meta
}

func shouldImportNotificationToDrive(req domain.InternalNotificationRequest) bool {
	if strings.TrimSpace(req.AttachmentURL) == "" {
		return false
	}
	if req.ImportToDrive != nil {
		return *req.ImportToDrive
	}
	return strings.EqualFold(strings.TrimSpace(req.SourceSystemCode), "DOCUMENTOLOG")
}

func stringPointer(value string) *string {
	return &value
}

func optionalStringPointer(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return &value
}

func nowAlmaty() time.Time {
	return time.Now().In(almatyLocation())
}

func almatyLocation() *time.Location {
	loc, err := time.LoadLocation("Asia/Almaty")
	if err != nil {
		return time.FixedZone("Asia/Almaty", 5*60*60)
	}
	return loc
}

func maskIIN(iin string) string {
	iin = strings.TrimSpace(iin)
	if len(iin) <= 4 {
		return "****"
	}
	return fmt.Sprintf("%s******%s", iin[:2], iin[len(iin)-4:])
}
