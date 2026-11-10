package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/i18n"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

type EmailLogService struct {
	repo                *repository.EmailLogRepository
	mappingRepo         *repository.EmailMappingRepository
	userRepo            *repository.UserRepository
	emailService        *EmailService
	notificationService *NotificationService
	logger              *zap.Logger
}

func NewEmailLogService(
	repo *repository.EmailLogRepository,
	mappingRepo *repository.EmailMappingRepository,
	userRepo *repository.UserRepository,
	emailService *EmailService,
	notificationService *NotificationService,
	logger *zap.Logger,
) *EmailLogService {
	return &EmailLogService{
		repo:                repo,
		mappingRepo:         mappingRepo,
		userRepo:            userRepo,
		emailService:        emailService,
		notificationService: notificationService,
		logger:              logger.With(zap.String("service", "email_log")),
	}
}

// ProcessIncomingEmail checks if the sender exists in the database and logs the result
// Also checks email mapping and returns target email if found
func (s *EmailLogService) ProcessIncomingEmail(ctx context.Context, email, content string, timestamp *time.Time) (*domain.EmailLog, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	log := &domain.EmailLog{
		SentEmail: email,
		Content:   content,
		Status:    "unmatched",
		Timestamp: timestamp,
	}

	var targetEmail string

	// Check if there's an email mapping for this incoming email
	mapping, err := s.mappingRepo.GetByIncomingEmail(ctx, email)
	if err != nil {
		s.logger.Warn("Failed to check email mapping", zap.Error(err), zap.String("email", email))
	}

	if mapping != nil {
		s.logger.Info("Email mapping found",
			zap.String("incoming_email", email),
			zap.String("target_email", mapping.TargetEmail),
		)
		targetEmail = mapping.TargetEmail
	}

	// Try to find user by email
	user, err := s.userRepo.GetByEmail(ctx, email)
	if err == nil && user != nil {
		log.MatchedUserID = &user.ID
		log.Status = "matched"

		// If no specific mapping found, fallback to user's email
		if targetEmail == "" {
			targetEmail = user.Email
			s.logger.Info("User found, using user email as target", zap.String("target_email", targetEmail))
		}
	} else {
		// Just log the error if any, but continue processing as unmatched
		if err != nil && !strings.Contains(err.Error(), "user not found") {
			s.logger.Warn("Failed to lookup user by email", zap.Error(err), zap.String("email", email))
		}
	}

	// Find user by targetEmail to send push notifications
	var targetUser *domain.User
	if targetEmail != "" {
		// Ignore error here, user might not exist
		u, err := s.userRepo.GetByEmail(ctx, targetEmail)
		if err == nil {
			targetUser = u
		}
	}

	// Send notification if we have a target
	if targetEmail != "" {
		go func(target string, user *domain.User) {
			locale := i18n.Russian
			if user != nil {
				locale = i18n.ResolveRecipient(user.Language)
			}
			subject := i18n.Text(locale, "email.forward.subject", map[string]any{"Sender": email})
			template := BuildNotificationEmailTemplate(NotificationEmailTemplateInput{
				Language:  locale,
				Title:     subject,
				Message:   content,
				CreatedAt: time.Now().UTC(),
				AppName:   "Team Workspace",
			})

			// 1. Send Email (always try to forward email)
			if err := s.emailService.SendEmail(context.Background(), target, template.Subject, template.HTMLBody, template.TextBody); err != nil {
				s.logger.Error("Failed to forward email",
					zap.Error(err),
					zap.String("target_email", target),
				)
			} else {
				s.logger.Info("Email forwarded successfully",
					zap.String("target_email", target),
				)
			}

			// 2. Send Push Notification (if user exists in our system)
			if user != nil {
				pushTitle := i18n.SystemNotification(i18n.ResolveRecipient(user.Language), "Новое письмо")
				pushBody := i18n.SystemNotification(i18n.ResolveRecipient(user.Language), "От: %s\n%s", email, content)

				pushData := map[string]string{
					"type":         "email_notification",
					"from_email":   email,
					"target_email": target,
				}

				if err := s.notificationService.SendPushNotification(user.ID, pushTitle, pushBody, pushData); err != nil {
					s.logger.Error("Failed to send push notification for email",
						zap.Error(err),
						zap.Int64("user_id", user.ID),
					)
				} else {
					s.logger.Info("Push notification sent for email",
						zap.Int64("user_id", user.ID),
					)
				}
			}
		}(targetEmail, targetUser)
	}

	// Save log
	if err := s.repo.Create(ctx, log); err != nil {
		return nil, err
	}

	return log, nil
}

func (s *EmailLogService) GetAllLogs(ctx context.Context, limit, offset int) ([]*domain.EmailLog, error) {
	return s.repo.GetAll(ctx, limit, offset)
}

// CheckMapping checks if a mapping already exists for the given incoming email
func (s *EmailLogService) CheckMapping(ctx context.Context, incomingEmail string) (*domain.EmailMapping, error) {
	return s.mappingRepo.GetByIncomingEmail(ctx, incomingEmail)
}

// CreateMapping creates a new email mapping
func (s *EmailLogService) CreateMapping(ctx context.Context, incomingEmail, targetEmail string) (*domain.EmailMapping, error) {
	mapping := &domain.EmailMapping{
		IncomingEmail: incomingEmail,
		TargetEmail:   targetEmail,
		IsActive:      true,
	}

	// Check if already exists
	existing, err := s.mappingRepo.GetByIncomingEmail(ctx, incomingEmail)
	if err == nil && existing != nil {
		return nil, fmt.Errorf("mapping for email %s already exists", incomingEmail)
	}

	if err := s.mappingRepo.Create(ctx, mapping); err != nil {
		return nil, err
	}

	return mapping, nil
}

// GetMappings returns all email mappings
func (s *EmailLogService) GetMappings(ctx context.Context, limit, offset int) ([]*domain.EmailMapping, error) {
	return s.mappingRepo.GetAll(ctx, limit, offset)
}

// DeleteMapping deletes an email mapping by ID
func (s *EmailLogService) DeleteMapping(ctx context.Context, id string) error {
	return s.mappingRepo.Delete(ctx, id)
}

func (s *EmailLogService) SendTestEmail(ctx context.Context, to, subject, message string) error {
	if s.emailService == nil {
		return ErrSMTPNotConfigured
	}

	locale := i18n.Russian
	if s.userRepo != nil {
		locale = recipientEmailLocale(ctx, s.userRepo, to)
	}
	template := BuildNotificationEmailTemplate(NotificationEmailTemplateInput{
		Language:  locale,
		Title:     strings.TrimSpace(subject),
		Message:   strings.TrimSpace(message),
		CreatedAt: time.Now().UTC(),
		AppName:   "Team Workspace",
	})

	return s.emailService.SendEmail(ctx, strings.TrimSpace(to), template.Subject, template.HTMLBody, template.TextBody)
}
