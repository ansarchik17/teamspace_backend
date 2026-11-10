package service

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"html"
	"mime/multipart"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/i18n"
	"github.com/west6ide/workspace-backend/internal/repository"
	"github.com/xuri/excelize/v2"
	"go.uber.org/zap"
)

var (
	ErrForbiddenPublish = fmt.Errorf("insufficient permissions to publish from this source")
	ErrForbiddenView    = fmt.Errorf("this publication is private to another ministry")
	ErrForbiddenAction  = fmt.Errorf("insufficient permissions to perform this action")
)

type publicationNotificationSender interface {
	CreateAndSendNotification(ctx context.Context, req domain.CreateNotificationRequest) error
}

type publicationEmailSender interface {
	Enabled() bool
	SendEmail(ctx context.Context, to, subject, htmlBody, textBody string) error
}

type PublicationService struct {
	repo         *repository.PublicationRepository
	userRepo     *repository.UserRepository
	fileService  *FileService
	notifService publicationNotificationSender
	emailService publicationEmailSender
	appPublicURL string
	logger       *zap.Logger
}

func NewPublicationService(
	repo *repository.PublicationRepository,
	userRepo *repository.UserRepository,
	logger *zap.Logger,
) *PublicationService {
	return &PublicationService{
		repo:     repo,
		userRepo: userRepo,
		logger:   logger.With(zap.String("service", "publication")),
	}
}

func (s *PublicationService) SetFileService(fileService *FileService) {
	s.fileService = fileService
}

func (s *PublicationService) GetFileService() *FileService {
	return s.fileService
}

func (s *PublicationService) SetNotificationSender(sender publicationNotificationSender) {
	s.notifService = sender
}

func (s *PublicationService) SetEmailSender(sender publicationEmailSender) {
	s.emailService = sender
}

func (s *PublicationService) SetAppPublicURL(url string) {
	s.appPublicURL = strings.TrimRight(url, "/")
}

func (s *PublicationService) IsMinisterOrViceMinister(user *domain.User) bool {
	if user == nil {
		return false
	}
	return user.Role == domain.RoleSuperAdmin || user.Role == domain.RoleManager
}

func (s *PublicationService) IsDepartmentLeader(user *domain.User) bool {
	if user == nil {
		return false
	}
	return user.Role == domain.RoleSuperAdmin || user.Role == domain.RoleManager
}

func (s *PublicationService) IsStateBodyLeader(user *domain.User) bool {
	if user == nil {
		return false
	}
	return user.Role == domain.RoleSuperAdmin || user.Role == domain.RoleManager
}

func (s *PublicationService) CreatePublication(ctx context.Context, authorID int64, req domain.CreatePublicationRequest) (*domain.PublicationResponse, error) {
	if err := validatePublicationBannerFields(req.BannerUntil, req.BannerActionText, req.BannerActionURL, req.BannerColor); err != nil {
		return nil, err
	}
	if err := validatePublicationAttachments(authorID, req.Attachments); err != nil {
		return nil, apperrors.Validation(err.Error())
	}
	if len(req.ContentHTML) > 4<<20 || len(req.Content) > 4<<20 {
		return nil, apperrors.Validation("publication content is too large")
	}
	req.ContentHTML = sanitizePublicationHTML(req.ContentHTML)
	if req.CoverImageURL != nil {
		if err := s.validatePublicationCover(ctx, authorID, *req.CoverImageURL); err != nil {
			return nil, err
		}
	}
	author, err := s.userRepo.GetByID(ctx, authorID)
	if err != nil {
		return nil, fmt.Errorf("get author: %w", err)
	}

	// Validate publication permissions
	var ministryID *int64
	var stateBodyID *int64
	var departmentID *int64

	switch req.SourceType {
	case "ministry":
		if !s.IsMinisterOrViceMinister(author) || author.MinistryID == nil {
			return nil, ErrForbiddenPublish
		}
		ministryID = author.MinistryID
	case "state_body":
		if !s.IsStateBodyLeader(author) || author.StateBodyID == nil {
			return nil, ErrForbiddenPublish
		}
		stateBodyID = author.StateBodyID
		ministryID = author.MinistryID
	case "department":
		if !s.IsDepartmentLeader(author) || author.DepartmentID == nil {
			return nil, ErrForbiddenPublish
		}
		departmentID = author.DepartmentID
		stateBodyID = author.StateBodyID
		ministryID = author.MinistryID // Keep ministry reference
	case "user":
		ministryID = author.MinistryID
		stateBodyID = author.StateBodyID
		departmentID = author.DepartmentID
	default:
		return nil, fmt.Errorf("invalid source type: %s", req.SourceType)
	}

	status := req.Status
	if status == "" {
		status = domain.PublicationStatusPublished
	}
	switch status {
	case domain.PublicationStatusDraft, domain.PublicationStatusPublished, domain.PublicationStatusScheduled, domain.PublicationStatusArchived:
		// valid
	default:
		return nil, fmt.Errorf("invalid publication status: %s", status)
	}

	pubID := uuid.New()
	now := time.Now()
	publishedAt := now
	var scheduledAt *time.Time
	if status == domain.PublicationStatusScheduled {
		if req.ScheduledAt == nil || !req.ScheduledAt.After(now) {
			return nil, fmt.Errorf("scheduled_at must be in the future")
		}
		scheduledAt = req.ScheduledAt
		publishedAt = *req.ScheduledAt
	}

	var archiveAt *time.Time
	var archivedAt *time.Time
	if req.ArchiveAt != nil {
		if status == domain.PublicationStatusScheduled && scheduledAt != nil {
			if !req.ArchiveAt.After(*scheduledAt) {
				return nil, fmt.Errorf("archive_at must be after scheduled_at")
			}
		} else if status != domain.PublicationStatusArchived && !req.ArchiveAt.After(now) {
			return nil, fmt.Errorf("archive_at must be in the future")
		}
		archiveAt = req.ArchiveAt
	}
	if status == domain.PublicationStatusArchived {
		archivedAt = &now
	}

	allowComments := true
	if req.AllowComments != nil {
		allowComments = *req.AllowComments
	}

	var targetMinistryID *int64
	var targetMinistryIDs []int64
	var targetDepartmentID *int64
	var targetDepartmentIDs []int64

	switch req.TargetAudience {
	case domain.PublicationAudienceMinistry, domain.PublicationAudienceBranch:
		targetMinistryID, targetMinistryIDs, err = s.validateAudienceTargets(ctx, author, req.TargetAudience, req.TargetMinistryID, req.TargetMinistryIDs)
		if err != nil {
			return nil, err
		}
	case domain.PublicationAudienceDepartment:
		targetDepartmentID, targetDepartmentIDs, err = s.validateAudienceTargets(ctx, author, req.TargetAudience, req.TargetDepartmentID, req.TargetDepartmentIDs)
		if err != nil {
			return nil, err
		}
	case domain.PublicationAudienceAll, "":
		// Entire company visibility
	default:
		return nil, fmt.Errorf("invalid target audience: %s", req.TargetAudience)
	}

	// Rich content validation & fallback
	if req.ContentDoc != nil {
		if err := domain.ValidatePublicationContentDoc(req.ContentDoc); err != nil {
			return nil, fmt.Errorf("invalid content_doc: %w", err)
		}
		if req.Content == "" {
			req.Content = strings.TrimSpace(domain.ExtractPublicationPlainText(req.ContentDoc))
		}
	}
	if req.Content == "" && req.ContentHTML != "" {
		req.Content = strings.TrimSpace(stripHTMLTags(req.ContentHTML))
	}

	var coverImageURL *string
	if req.CoverImageURL != nil && strings.TrimSpace(*req.CoverImageURL) != "" {
		trimmed := strings.TrimSpace(*req.CoverImageURL)
		coverImageURL = &trimmed
	}

	isPinned := false
	var pinnedAt *time.Time
	var pinnedByID *int64
	var pinnedUntil *time.Time
	if req.IsPinned != nil && *req.IsPinned {
		isPinned = true
		pinnedAt = &now
		pinnedByID = &authorID
		pinnedUntil = req.PinnedUntil
	}

	isBanner := false
	var bannerUntil *time.Time
	var bannerActionText *string
	var bannerActionURL *string
	var bannerColor *string
	if req.IsBanner != nil && *req.IsBanner {
		isBanner = true
		bannerUntil = req.BannerUntil
		bannerActionText = req.BannerActionText
		bannerActionURL = req.BannerActionURL
		bannerColor = req.BannerColor
	}

	pub := &domain.Publication{
		ID:                      pubID,
		AuthorID:                authorID,
		ApprovedByID:            &authorID, // Since they publish directly
		SourceType:              req.SourceType,
		MinistryID:              ministryID,
		StateBodyID:             stateBodyID,
		DepartmentID:            departmentID,
		Type:                    req.Type,
		Title:                   req.Title,
		Content:                 &req.Content,
		ContentDoc:              req.ContentDoc,
		ContentHTML:             req.ContentHTML,
		CoverImageURL:           coverImageURL,
		Status:                  status,
		TargetAudience:          req.TargetAudience,
		TargetMinistryID:        targetMinistryID,
		TargetDepartmentID:      targetDepartmentID,
		TargetMinistryIDs:       targetMinistryIDs,
		TargetDepartmentIDs:     targetDepartmentIDs,
		Importance:              req.Importance,
		IsPinned:                isPinned,
		PinnedAt:                pinnedAt,
		PinnedByID:              pinnedByID,
		PinnedUntil:             pinnedUntil,
		IsBanner:                isBanner,
		BannerUntil:             bannerUntil,
		BannerActionText:        bannerActionText,
		BannerActionURL:         bannerActionURL,
		BannerColor:             bannerColor,
		AllowComments:           allowComments,
		RequiresAcknowledgement: req.RequiresAcknowledgement,
		ScheduledAt:             scheduledAt,
		PublishedAt:             publishedAt,
		ArchiveAt:               archiveAt,
		ArchivedAt:              archivedAt,
		CreatedAt:               now,
		UpdatedAt:               now,
	}

	var attachments []domain.PublicationAttachment
	for i, attReq := range req.Attachments {
		if key, storage, _ := publicationMediaKey(authorID, attReq.FileURL); storage {
			attReq.FileURL = key
		}
		attachmentID := uuid.Nil
		if attReq.ID != nil {
			attachmentID = *attReq.ID
		}
		orderIdx := i
		if attReq.OrderIndex != nil {
			orderIdx = *attReq.OrderIndex
		}
		attachments = append(attachments, domain.PublicationAttachment{
			ID:            attachmentID,
			FileURL:       attReq.FileURL,
			FileName:      attReq.FileName,
			FileType:      attReq.FileType,
			FileSize:      attReq.FileSize,
			Caption:       attReq.Caption,
			OrderIndex:    orderIdx,
			MimeType:      attReq.MimeType,
			ThumbnailURL:  attReq.ThumbnailURL,
			VideoProvider: attReq.VideoProvider,
			VideoEmbedURL: attReq.VideoEmbedURL,
		})
	}

	err = s.repo.Create(ctx, pub, attachments)
	if err != nil {
		return nil, err
	}

	return s.GetPublication(ctx, pubID, authorID)
}

func (s *PublicationService) UpdatePublication(ctx context.Context, id uuid.UUID, editorID int64, req domain.UpdatePublicationRequest) (*domain.PublicationResponse, error) {
	if req.IsBanner != nil {
		if err := validatePublicationBannerFields(req.BannerUntil, req.BannerActionText, req.BannerActionURL, req.BannerColor); err != nil {
			return nil, err
		}
	}
	if len(req.ContentHTML) > 4<<20 || len(req.Content) > 4<<20 {
		return nil, apperrors.Validation("publication content is too large")
	}
	req.ContentHTML = sanitizePublicationHTML(req.ContentHTML)
	pub, err := s.repo.GetByID(ctx, id, editorID)
	if err != nil {
		return nil, err
	}

	editor, err := s.userRepo.GetByID(ctx, editorID)
	if err != nil {
		return nil, fmt.Errorf("get editor: %w", err)
	}

	if pub.AuthorID != editorID && editor.Role != domain.RoleSuperAdmin {
		return nil, ErrForbiddenAction
	}
	if err := validatePublicationAttachments(pub.AuthorID, req.Attachments); err != nil {
		return nil, apperrors.Validation(err.Error())
	}
	if req.CoverImageURL != nil {
		if err := s.validatePublicationCover(ctx, pub.AuthorID, *req.CoverImageURL); err != nil {
			return nil, err
		}
	}

	var ministryID *int64
	var stateBodyID *int64
	var departmentID *int64

	switch req.SourceType {
	case "ministry":
		if !s.IsMinisterOrViceMinister(editor) || editor.MinistryID == nil {
			return nil, ErrForbiddenPublish
		}
		ministryID = editor.MinistryID
	case "state_body":
		if !s.IsStateBodyLeader(editor) || editor.StateBodyID == nil {
			return nil, ErrForbiddenPublish
		}
		stateBodyID = editor.StateBodyID
		ministryID = editor.MinistryID
	case "department":
		if !s.IsDepartmentLeader(editor) || editor.DepartmentID == nil {
			return nil, ErrForbiddenPublish
		}
		departmentID = editor.DepartmentID
		stateBodyID = editor.StateBodyID
		ministryID = editor.MinistryID
	case "user":
		ministryID = editor.MinistryID
		stateBodyID = editor.StateBodyID
		departmentID = editor.DepartmentID
	default:
		return nil, fmt.Errorf("invalid source type: %s", req.SourceType)
	}
	// Omitting targeting fields during a text edit preserves the audience.
	if req.TargetAudience == pub.TargetAudience {
		if req.TargetMinistryID == nil && req.TargetMinistryIDs == nil {
			req.TargetMinistryID, req.TargetMinistryIDs = pub.TargetMinistryID, pub.TargetMinistryIDs
		}
		if req.TargetDepartmentID == nil && req.TargetDepartmentIDs == nil {
			req.TargetDepartmentID, req.TargetDepartmentIDs = pub.TargetDepartmentID, pub.TargetDepartmentIDs
		}
	}

	allowComments := true
	if req.AllowComments != nil {
		allowComments = *req.AllowComments
	}

	var targetMinistryID *int64
	var targetMinistryIDs []int64
	var targetDepartmentID *int64
	var targetDepartmentIDs []int64

	switch req.TargetAudience {
	case domain.PublicationAudienceMinistry, domain.PublicationAudienceBranch:
		targetMinistryID, targetMinistryIDs, err = s.validateAudienceTargets(ctx, editor, req.TargetAudience, req.TargetMinistryID, req.TargetMinistryIDs)
		if err != nil {
			return nil, err
		}
	case domain.PublicationAudienceDepartment:
		targetDepartmentID, targetDepartmentIDs, err = s.validateAudienceTargets(ctx, editor, req.TargetAudience, req.TargetDepartmentID, req.TargetDepartmentIDs)
		if err != nil {
			return nil, err
		}
	case domain.PublicationAudienceAll, "":
		// Entire company visibility
	default:
		return nil, fmt.Errorf("invalid target audience: %s", req.TargetAudience)
	}

	// Rich content validation & fallback
	if req.ContentDoc != nil {
		if err := domain.ValidatePublicationContentDoc(req.ContentDoc); err != nil {
			return nil, fmt.Errorf("invalid content_doc: %w", err)
		}
		if req.Content == "" {
			req.Content = strings.TrimSpace(domain.ExtractPublicationPlainText(req.ContentDoc))
		}
	}
	if req.Content == "" && req.ContentHTML != "" {
		req.Content = strings.TrimSpace(stripHTMLTags(req.ContentHTML))
	}

	if req.Status != "" {
		switch req.Status {
		case domain.PublicationStatusDraft:
			pub.Status = domain.PublicationStatusDraft
			pub.ScheduledAt = nil
		case domain.PublicationStatusPublished:
			wasPublished := pub.Status == domain.PublicationStatusPublished
			pub.Status = domain.PublicationStatusPublished
			pub.ScheduledAt = nil
			if !wasPublished || pub.PublishedAt.IsZero() || pub.PublishedAt.After(time.Now()) {
				pub.PublishedAt = time.Now()
			}
		case domain.PublicationStatusScheduled:
			if req.ScheduledAt == nil || !req.ScheduledAt.After(time.Now()) {
				return nil, fmt.Errorf("scheduled_at must be in the future")
			}
			pub.Status = domain.PublicationStatusScheduled
			pub.ScheduledAt = req.ScheduledAt
			pub.PublishedAt = *req.ScheduledAt
		case domain.PublicationStatusArchived:
			pub.Status = domain.PublicationStatusArchived
			pub.ScheduledAt = nil
			nowArchive := time.Now()
			pub.ArchivedAt = &nowArchive
			pub.IsPinned = false
			pub.PinnedAt = nil
			pub.PinnedByID = nil
			pub.PinnedUntil = nil
			pub.IsBanner = false
			pub.BannerUntil = nil
		default:
			return nil, fmt.Errorf("invalid publication status: %s", req.Status)
		}
	}

	if req.ArchiveAt != nil {
		if pub.Status == domain.PublicationStatusScheduled && pub.ScheduledAt != nil {
			if !req.ArchiveAt.After(*pub.ScheduledAt) {
				return nil, fmt.Errorf("archive_at must be after scheduled_at")
			}
		} else if pub.Status != domain.PublicationStatusArchived && !req.ArchiveAt.After(time.Now()) {
			return nil, fmt.Errorf("archive_at must be in the future")
		}
		pub.ArchiveAt = req.ArchiveAt
	}
	if pub.Status != domain.PublicationStatusArchived {
		pub.ArchivedAt = nil
		if err := validatePublicationExpiry(pub.ArchiveAt, pub.ScheduledAt, pub.Status, time.Now()); err != nil {
			return nil, err
		}
	}

	if req.CoverImageURL != nil {
		trimmed := strings.TrimSpace(*req.CoverImageURL)
		if trimmed == "" {
			pub.CoverImageURL = nil
		} else {
			pub.CoverImageURL = &trimmed
		}
	}
	if req.ContentDoc != nil {
		pub.ContentDoc = req.ContentDoc
	}
	pub.ContentHTML = req.ContentHTML

	content := req.Content
	now := time.Now()
	pub.SourceType = req.SourceType
	pub.MinistryID = ministryID
	pub.StateBodyID = stateBodyID
	pub.DepartmentID = departmentID
	pub.Type = req.Type
	pub.Title = req.Title
	pub.Content = &content
	pub.TargetAudience = req.TargetAudience
	pub.TargetMinistryID = targetMinistryID
	pub.TargetDepartmentID = targetDepartmentID
	pub.TargetMinistryIDs = targetMinistryIDs
	pub.TargetDepartmentIDs = targetDepartmentIDs
	pub.Importance = req.Importance
	pub.AllowComments = allowComments
	pub.RequiresAcknowledgement = req.RequiresAcknowledgement
	if req.IsPinned != nil {
		pub.IsPinned = *req.IsPinned
		if *req.IsPinned {
			pub.PinnedAt = &now
			pub.PinnedByID = &editorID
			pub.PinnedUntil = req.PinnedUntil
		} else {
			pub.PinnedAt = nil
			pub.PinnedByID = nil
			pub.PinnedUntil = nil
		}
	}
	if req.IsBanner != nil {
		pub.IsBanner = *req.IsBanner
		pub.BannerUntil = req.BannerUntil
		pub.BannerActionText = req.BannerActionText
		pub.BannerActionURL = req.BannerActionURL
		pub.BannerColor = req.BannerColor
	}
	pub.UpdatedAt = now

	attachments := make([]domain.PublicationAttachment, 0, len(req.Attachments))
	if req.Attachments == nil {
		attachments = nil
	}
	for i, attReq := range req.Attachments {
		if key, storage, _ := publicationMediaKey(pub.AuthorID, attReq.FileURL); storage {
			attReq.FileURL = key
		}
		attachmentID := uuid.Nil
		if attReq.ID != nil {
			attachmentID = *attReq.ID
		}
		orderIdx := i
		if attReq.OrderIndex != nil {
			orderIdx = *attReq.OrderIndex
		}
		attachments = append(attachments, domain.PublicationAttachment{
			ID:            attachmentID,
			FileURL:       attReq.FileURL,
			FileName:      attReq.FileName,
			FileType:      attReq.FileType,
			FileSize:      attReq.FileSize,
			Caption:       attReq.Caption,
			OrderIndex:    orderIdx,
			MimeType:      attReq.MimeType,
			ThumbnailURL:  attReq.ThumbnailURL,
			VideoProvider: attReq.VideoProvider,
			VideoEmbedURL: attReq.VideoEmbedURL,
		})
	}

	if err := s.repo.Update(ctx, pub, attachments); err != nil {
		return nil, err
	}

	return s.GetPublication(ctx, id, editorID)
}

func (s *PublicationService) GetPublication(ctx context.Context, id uuid.UUID, currentUserID int64) (*domain.PublicationResponse, error) {
	currentUser, err := s.userRepo.GetByID(ctx, currentUserID)
	if err != nil {
		return nil, fmt.Errorf("get current user: %w", err)
	}

	pub, err := s.repo.GetByID(ctx, id, currentUserID)
	if err != nil {
		return nil, err
	}
	if !publicationVisible(pub, currentUser, time.Now()) {
		return nil, ErrForbiddenView
	}
	_ = s.repo.RecordView(ctx, id, currentUserID)

	resp, err := s.enrichPublication(ctx, pub, currentUserID)
	if err != nil {
		return nil, err
	}

	reactions, userReactions, err := s.repo.GetReactions(ctx, pub.ID, currentUserID)
	if err == nil {
		for i := range reactions {
			for j := range reactions[i].Users {
				reactions[i].Users[j].Avatar = signLegacyFileDownloadURL(reactions[i].Users[j].Avatar)
			}
		}
		resp.Reactions = reactions
		resp.UserReactions = userReactions
	}

	return resp, nil
}

func (s *PublicationService) ListPublications(
	ctx context.Context,
	currentUserID int64,
	pubType, sourceType string,
	ministryID, stateBodyID, departmentID *int64,
	importance string,
	status string,
	page, limit int,
) ([]domain.PublicationResponse, error) {
	currentUser, err := s.userRepo.GetByID(ctx, currentUserID)
	if err != nil {
		return nil, fmt.Errorf("get current user: %w", err)
	}

	// Load matching publications with target filtering
	list, err := s.repo.List(ctx, currentUserID, currentUser.Role == domain.RoleSuperAdmin, currentUser.MinistryID, currentUser.DepartmentID, pubType, sourceType, ministryID, stateBodyID, departmentID, importance, status, page, limit)
	if err != nil {
		return nil, err
	}

	var enrichedList []domain.PublicationResponse
	for i := range list {
		enrich, err := s.enrichPublication(ctx, &list[i], currentUserID)
		if err == nil {
			enrichedList = append(enrichedList, *enrich)
		}
	}

	return enrichedList, nil
}

func (s *PublicationService) PublishPublication(ctx context.Context, id uuid.UUID, userID int64, scheduledAt *time.Time, archiveAt *time.Time) (*domain.PublicationResponse, error) {
	if scheduledAt != nil && !scheduledAt.After(time.Now()) {
		return nil, apperrors.Validation("scheduled_at must be in the future")
	}
	if archiveAt != nil {
		if scheduledAt != nil {
			if !archiveAt.After(*scheduledAt) {
				return nil, apperrors.Validation("archive_at must be after scheduled_at")
			}
		} else if !archiveAt.After(time.Now()) {
			return nil, apperrors.Validation("archive_at must be in the future")
		}
	}
	pub, err := s.repo.GetByID(ctx, id, userID)
	if err != nil {
		return nil, err
	}

	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}

	if pub.AuthorID != userID && user.Role != domain.RoleSuperAdmin {
		return nil, ErrForbiddenAction
	}

	now := time.Now()
	effectiveArchiveAt := archiveAt
	if effectiveArchiveAt == nil {
		effectiveArchiveAt = pub.ArchiveAt
	}
	if err := validatePublicationExpiry(effectiveArchiveAt, scheduledAt, domain.PublicationStatusPublished, now); err != nil {
		return nil, err
	}
	if scheduledAt != nil && scheduledAt.After(now) {
		if err := s.repo.SetStatus(ctx, id, domain.PublicationStatusScheduled, *scheduledAt, scheduledAt, archiveAt); err != nil {
			return nil, err
		}
	} else {
		if err := s.repo.SetStatus(ctx, id, domain.PublicationStatusPublished, now, nil, archiveAt); err != nil {
			return nil, err
		}
	}

	return s.GetPublication(ctx, id, userID)
}

func (s *PublicationService) UnpublishPublication(ctx context.Context, id uuid.UUID, userID int64) (*domain.PublicationResponse, error) {
	pub, err := s.repo.GetByID(ctx, id, userID)
	if err != nil {
		return nil, err
	}

	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}

	if pub.AuthorID != userID && user.Role != domain.RoleSuperAdmin {
		return nil, ErrForbiddenAction
	}

	if err := s.repo.SetStatus(ctx, id, domain.PublicationStatusDraft, pub.PublishedAt, nil, nil); err != nil {
		return nil, err
	}

	return s.GetPublication(ctx, id, userID)
}

func (s *PublicationService) ArchivePublication(ctx context.Context, id uuid.UUID, userID int64) (*domain.PublicationResponse, error) {
	pub, err := s.repo.GetByID(ctx, id, userID)
	if err != nil {
		return nil, err
	}

	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}

	if pub.AuthorID != userID && user.Role != domain.RoleSuperAdmin {
		return nil, ErrForbiddenAction
	}

	if err := s.repo.Archive(ctx, id, time.Now()); err != nil {
		return nil, err
	}

	return s.GetPublication(ctx, id, userID)
}

func (s *PublicationService) UnarchivePublication(ctx context.Context, id uuid.UUID, userID int64, targetStatus string) (*domain.PublicationResponse, error) {
	pub, err := s.repo.GetByID(ctx, id, userID)
	if err != nil {
		return nil, err
	}

	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}

	if pub.AuthorID != userID && user.Role != domain.RoleSuperAdmin {
		return nil, ErrForbiddenAction
	}
	if pub.Status != domain.PublicationStatusArchived {
		return nil, apperrors.BadRequest("only archived publications can be restored")
	}

	if targetStatus == "" {
		targetStatus = domain.PublicationStatusDraft
	}
	if targetStatus != domain.PublicationStatusDraft && targetStatus != domain.PublicationStatusPublished {
		return nil, apperrors.Validation("target_status must be 'draft' or 'published'")
	}

	if err := s.repo.Unarchive(ctx, id, targetStatus); err != nil {
		return nil, err
	}

	return s.GetPublication(ctx, id, userID)
}

func (s *PublicationService) ArchiveObsoletePublications(ctx context.Context, userID int64, req domain.ArchiveObsoleteRequest) (*domain.ArchiveObsoleteResponse, error) {
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}

	if user.Role != domain.RoleSuperAdmin {
		return nil, ErrForbiddenAction
	}

	var cutoff time.Time
	if req.BeforeDate != nil {
		cutoff = *req.BeforeDate
	} else if req.OlderThanDays != nil && *req.OlderThanDays > 0 {
		cutoff = time.Now().AddDate(0, 0, -*req.OlderThanDays)
	} else {
		return nil, apperrors.Validation("older_than_days or before_date is required")
	}

	count, err := s.repo.ArchiveObsolete(ctx, cutoff, req.Types)
	if err != nil {
		return nil, err
	}

	return &domain.ArchiveObsoleteResponse{
		ArchivedCount: count,
		CutoffDate:    &cutoff,
	}, nil
}

func (s *PublicationService) SetCoverImage(ctx context.Context, id uuid.UUID, userID int64, coverURL string) (*domain.PublicationResponse, error) {
	response, _, err := s.SetUploadedCoverImage(ctx, id, userID, coverURL)
	return response, err
}

// The committed flag prevents cleanup from deleting a saved cover when response enrichment fails.
func (s *PublicationService) SetUploadedCoverImage(ctx context.Context, id uuid.UUID, userID int64, coverURL string) (*domain.PublicationResponse, bool, error) {
	pub, err := s.repo.GetByID(ctx, id, userID)
	if err != nil {
		return nil, false, err
	}

	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, false, fmt.Errorf("get user: %w", err)
	}

	if pub.AuthorID != userID && user.Role != domain.RoleSuperAdmin {
		return nil, false, ErrForbiddenAction
	}

	trimmed := strings.TrimSpace(coverURL)
	if err := s.validatePublicationCover(ctx, pub.AuthorID, trimmed); err != nil {
		return nil, false, err
	}
	var ptr *string
	if trimmed != "" {
		ptr = &trimmed
	}

	if err := s.repo.SetCoverImage(ctx, id, ptr); err != nil {
		return nil, false, err
	}
	response, err := s.GetPublication(ctx, id, userID)
	return response, true, err
}

func (s *PublicationService) RemoveCoverImage(ctx context.Context, id uuid.UUID, userID int64) (*domain.PublicationResponse, error) {
	pub, err := s.repo.GetByID(ctx, id, userID)
	if err != nil {
		return nil, err
	}

	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}

	if pub.AuthorID != userID && user.Role != domain.RoleSuperAdmin {
		return nil, ErrForbiddenAction
	}

	if err := s.repo.SetCoverImage(ctx, id, nil); err != nil {
		return nil, err
	}

	return s.GetPublication(ctx, id, userID)
}

func (s *PublicationService) DeletePublication(ctx context.Context, id uuid.UUID, userID int64) error {
	pub, err := s.repo.GetByID(ctx, id, userID)
	if err != nil {
		return err
	}

	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return err
	}

	// Only author or SuperAdmin can delete
	if pub.AuthorID != userID && user.Role != domain.RoleSuperAdmin {
		return ErrForbiddenAction
	}

	return s.repo.Delete(ctx, id)
}

func (s *PublicationService) LikePublication(ctx context.Context, pubID uuid.UUID, userID int64) error {
	if _, err := s.readablePublication(ctx, pubID, userID); err != nil {
		return err
	}
	return s.repo.AddReaction(ctx, pubID, userID, "👍")
}

func (s *PublicationService) UnlikePublication(ctx context.Context, pubID uuid.UUID, userID int64) error {
	if _, err := s.readablePublication(ctx, pubID, userID); err != nil {
		return err
	}
	return s.repo.RemoveReaction(ctx, pubID, userID, "👍")
}

func (s *PublicationService) ListPublicationLikes(ctx context.Context, pubID uuid.UUID, userID int64) ([]domain.PublicationLikeUser, error) {
	pub, err := s.repo.GetByID(ctx, pubID, userID)
	if err != nil {
		return nil, err
	}

	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}

	if pub.AuthorID != userID && user.Role != domain.RoleSuperAdmin {
		return nil, ErrForbiddenAction
	}

	return s.repo.ListLikes(ctx, pubID)
}

func (s *PublicationService) ListPublicationActivity(ctx context.Context, pubID uuid.UUID, userID int64) (*domain.PublicationActivityResponse, error) {
	pub, err := s.repo.GetByID(ctx, pubID, userID)
	if err != nil {
		return nil, err
	}

	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}

	if pub.AuthorID != userID && user.Role != domain.RoleSuperAdmin {
		return nil, ErrForbiddenAction
	}

	likes, err := s.repo.ListLikes(ctx, pubID)
	if err != nil {
		return nil, err
	}

	views, err := s.repo.ListViews(ctx, pubID)
	if err != nil {
		return nil, err
	}

	return &domain.PublicationActivityResponse{
		Likes:      likes,
		Views:      views,
		LikesCount: len(likes),
		ViewsCount: len(views),
	}, nil
}

func (s *PublicationService) isModeratorOrAuthor(pub *domain.Publication, user *domain.User) bool {
	if user == nil {
		return false
	}
	if user.Role == domain.RoleSuperAdmin || user.Role == domain.RoleManager {
		return true
	}
	if pub != nil && pub.AuthorID == user.ID {
		return true
	}
	return false
}

func (s *PublicationService) AddComment(ctx context.Context, pubID uuid.UUID, authorID int64, req domain.CreateCommentRequest) (*domain.CommentResponse, error) {
	pub, err := s.readablePublication(ctx, pubID, authorID)
	if err != nil {
		return nil, err
	}
	author, err := s.userRepo.GetByID(ctx, authorID)
	if err != nil {
		return nil, fmt.Errorf("get author: %w", err)
	}

	if !pub.AllowComments && !s.isModeratorOrAuthor(pub, author) {
		return nil, apperrors.Forbidden("comments are closed for this publication")
	}

	content := strings.TrimSpace(req.Content)
	if content == "" {
		return nil, apperrors.Validation("comment content cannot be empty")
	}
	if len([]rune(content)) > 5000 {
		return nil, apperrors.Validation("comment content cannot exceed 5000 characters")
	}

	var replyToUserID *int64
	if req.ParentID != nil {
		parent, err := s.repo.GetCommentByID(ctx, *req.ParentID)
		if err != nil || parent.PublicationID != pubID {
			return nil, apperrors.NotFound("parent comment not found")
		}
		if parent.IsHidden {
			return nil, apperrors.BadRequest("cannot reply to a hidden comment")
		}
		ancestor := parent
		for depth := 1; ancestor.ParentID != nil; depth++ {
			if depth >= 64 {
				return nil, apperrors.BadRequest("comment thread is too deep")
			}
			ancestor, err = s.repo.GetCommentByID(ctx, *ancestor.ParentID)
			if err != nil {
				break
			} // A deleted ancestor does not prevent replies to a live child.
		}
		if req.ReplyToUserID != nil {
			replyToUserID = req.ReplyToUserID
		} else {
			replyToUserID = &parent.AuthorID
		}
	}

	commentID := uuid.New()
	now := time.Now()

	comment := &domain.PublicationComment{
		ID:            commentID,
		PublicationID: pubID,
		AuthorID:      authorID,
		ParentID:      req.ParentID,
		ReplyToUserID: replyToUserID,
		Content:       content,
		Status:        domain.CommentStatusActive,
		IsHidden:      false,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	err = s.repo.AddComment(ctx, comment)
	if err != nil {
		return nil, err
	}

	return s.enrichComment(ctx, comment, authorID, nil)
}

func (s *PublicationService) DeleteComment(ctx context.Context, commentID uuid.UUID, userID int64) error {
	comment, err := s.repo.GetCommentByID(ctx, commentID)
	if err != nil {
		return err
	}

	pub, err := s.repo.GetByID(ctx, comment.PublicationID, userID)
	if err != nil {
		return err
	}

	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return err
	}

	// Comment author, publication author, or SuperAdmin/Manager can delete
	if comment.AuthorID != userID && !s.isModeratorOrAuthor(pub, user) {
		return ErrForbiddenAction
	}

	return s.repo.DeleteComment(ctx, commentID)
}

func (s *PublicationService) ListComments(ctx context.Context, pubID uuid.UUID, userID int64) ([]domain.CommentResponse, error) {
	pub, err := s.readablePublication(ctx, pubID, userID)
	if err != nil {
		return nil, err
	}
	user, _ := s.userRepo.GetByID(ctx, userID)
	isMod := s.isModeratorOrAuthor(pub, user)

	comments, err := s.repo.ListComments(ctx, pubID, isMod)
	if err != nil {
		return nil, err
	}

	commentIDs := make([]uuid.UUID, 0, len(comments))
	for _, c := range comments {
		commentIDs = append(commentIDs, c.ID)
	}

	reactionsMap, err := s.repo.GetCommentReactions(ctx, commentIDs, userID)
	if err != nil {
		s.logger.Warn("Failed to load comment reactions", zap.Error(err))
		reactionsMap = make(map[uuid.UUID][]domain.ReactionSummary)
	}

	nodes := make(map[uuid.UUID]*domain.CommentResponse, len(comments))
	orderedRootIDs := make([]uuid.UUID, 0)
	childMap := make(map[uuid.UUID][]*domain.CommentResponse)

	for i := range comments {
		c := &comments[i]
		rx := reactionsMap[c.ID]

		// Mask hidden comment content for non-moderators
		if c.IsHidden && !isMod {
			c.Content = "[Комментарий скрыт модератором]"
			c.ModerationReason = nil
		}

		resp, err := s.enrichComment(ctx, c, userID, rx)
		if err != nil {
			continue
		}
		nodes[c.ID] = resp

		if c.ParentID == nil {
			orderedRootIDs = append(orderedRootIDs, c.ID)
		} else {
			childMap[*c.ParentID] = append(childMap[*c.ParentID], resp)
		}
	}

	roots := make([]domain.CommentResponse, 0, len(orderedRootIDs))
	for _, rootID := range orderedRootIDs {
		root := nodes[rootID]
		roots = append(roots, buildCommentTree(root, childMap, make(map[uuid.UUID]bool)))
	}

	// Handle orphan replies if parent was deleted
	for parentID, orphans := range childMap {
		if _, exists := nodes[parentID]; !exists {
			for _, orphan := range orphans {
				roots = append(roots, buildCommentTree(orphan, childMap, make(map[uuid.UUID]bool)))
			}
		}
	}

	return roots, nil
}

func (s *PublicationService) ListReplies(ctx context.Context, pubID, parentID uuid.UUID, userID int64) ([]domain.CommentResponse, error) {
	pub, err := s.readablePublication(ctx, pubID, userID)
	if err != nil {
		return nil, err
	}
	user, _ := s.userRepo.GetByID(ctx, userID)
	isMod := s.isModeratorOrAuthor(pub, user)

	replies, err := s.repo.ListReplies(ctx, pubID, parentID, isMod)
	if err != nil {
		return nil, err
	}

	commentIDs := make([]uuid.UUID, 0, len(replies))
	for _, c := range replies {
		commentIDs = append(commentIDs, c.ID)
	}

	reactionsMap, err := s.repo.GetCommentReactions(ctx, commentIDs, userID)
	if err != nil {
		reactionsMap = make(map[uuid.UUID][]domain.ReactionSummary)
	}

	res := make([]domain.CommentResponse, 0, len(replies))
	for i := range replies {
		c := &replies[i]
		if c.IsHidden && !isMod {
			c.Content = "[Комментарий скрыт модератором]"
			c.ModerationReason = nil
		}
		resp, err := s.enrichComment(ctx, c, userID, reactionsMap[c.ID])
		if err == nil {
			res = append(res, *resp)
		}
	}
	return res, nil
}

func (s *PublicationService) PinComment(ctx context.Context, pubID, commentID uuid.UUID, userID int64, pin bool) (*domain.CommentResponse, error) {
	pub, err := s.readablePublication(ctx, pubID, userID)
	if err != nil {
		return nil, err
	}
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}
	if !s.isModeratorOrAuthor(pub, user) {
		return nil, ErrForbiddenAction
	}

	comment, err := s.repo.GetCommentByID(ctx, commentID)
	if err != nil {
		return nil, err
	}
	if comment.PublicationID != pubID {
		return nil, apperrors.NotFound("comment does not belong to this publication")
	}

	if err := s.repo.PinComment(ctx, commentID, userID, pin); err != nil {
		return nil, err
	}

	updated, err := s.repo.GetCommentByID(ctx, commentID)
	if err != nil {
		return nil, err
	}

	reactionsMap, _ := s.repo.GetCommentReactions(ctx, []uuid.UUID{commentID}, userID)
	return s.enrichComment(ctx, updated, userID, reactionsMap[commentID])
}

func (s *PublicationService) ModerateComment(ctx context.Context, pubID, commentID uuid.UUID, userID int64, req domain.ModerateCommentRequest) (*domain.CommentResponse, error) {
	pub, err := s.readablePublication(ctx, pubID, userID)
	if err != nil {
		return nil, err
	}
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}
	if !s.isModeratorOrAuthor(pub, user) {
		return nil, ErrForbiddenAction
	}

	comment, err := s.repo.GetCommentByID(ctx, commentID)
	if err != nil {
		return nil, err
	}
	if comment.PublicationID != pubID {
		return nil, apperrors.NotFound("comment does not belong to this publication")
	}

	var status string
	var isHidden bool

	switch req.Action {
	case domain.ModerationActionHide:
		status = domain.CommentStatusHidden
		isHidden = true
	case domain.ModerationActionRestore:
		status = domain.CommentStatusActive
		isHidden = false
	case domain.ModerationActionDelete:
		status = domain.CommentStatusDeletedByModerator
		isHidden = true
	default:
		return nil, apperrors.BadRequest("invalid moderation action")
	}

	var reasonPtr *string
	if req.Reason != nil && len([]rune(*req.Reason)) > 1000 {
		return nil, apperrors.BadRequest("moderation reason is too long")
	}
	if req.Reason != nil && strings.TrimSpace(*req.Reason) != "" {
		trimmed := strings.TrimSpace(*req.Reason)
		reasonPtr = &trimmed
	}

	if err := s.repo.ModerateComment(ctx, commentID, status, isHidden, reasonPtr, userID); err != nil {
		return nil, err
	}

	updated, err := s.repo.GetCommentByID(ctx, commentID)
	if err != nil {
		comment.Status = status
		comment.IsHidden = isHidden
		comment.ModerationReason = reasonPtr
		comment.ModeratedByID = &userID
		now := time.Now()
		comment.ModeratedAt = &now
		updated = comment
	}

	reactionsMap, _ := s.repo.GetCommentReactions(ctx, []uuid.UUID{commentID}, userID)
	return s.enrichComment(ctx, updated, userID, reactionsMap[commentID])
}

func (s *PublicationService) LockComments(ctx context.Context, pubID uuid.UUID, userID int64, req domain.LockCommentsRequest) (*domain.PublicationResponse, error) {
	pub, err := s.readablePublication(ctx, pubID, userID)
	if err != nil {
		return nil, err
	}
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}
	if !s.isModeratorOrAuthor(pub, user) {
		return nil, ErrForbiddenAction
	}

	if err := s.repo.SetCommentsAllowed(ctx, pubID, req.AllowComments); err != nil {
		return nil, err
	}

	return s.GetPublication(ctx, pubID, userID)
}

func (s *PublicationService) AddReaction(ctx context.Context, pubID uuid.UUID, userID int64, reaction string) ([]domain.ReactionSummary, error) {
	reaction = strings.TrimSpace(reaction)
	reaction = canonicalReaction(reaction)
	if reaction == "" {
		return nil, apperrors.BadRequest("invalid reaction emoji")
	}
	if _, err := s.readablePublication(ctx, pubID, userID); err != nil {
		return nil, err
	}

	if err := s.repo.AddReaction(ctx, pubID, userID, reaction); err != nil {
		return nil, err
	}

	return s.GetReactions(ctx, pubID, userID)
}

func (s *PublicationService) RemoveReaction(ctx context.Context, pubID uuid.UUID, userID int64, reaction string) ([]domain.ReactionSummary, error) {
	reaction = strings.TrimSpace(reaction)
	reaction = canonicalReaction(reaction)
	if reaction == "" {
		return nil, apperrors.BadRequest("invalid reaction emoji")
	}
	if _, err := s.readablePublication(ctx, pubID, userID); err != nil {
		return nil, err
	}

	if err := s.repo.RemoveReaction(ctx, pubID, userID, reaction); err != nil {
		return nil, err
	}

	return s.GetReactions(ctx, pubID, userID)
}

func (s *PublicationService) GetReactions(ctx context.Context, pubID uuid.UUID, userID int64) ([]domain.ReactionSummary, error) {
	if _, err := s.readablePublication(ctx, pubID, userID); err != nil {
		return nil, err
	}
	reactions, _, err := s.repo.GetReactions(ctx, pubID, userID)
	if err != nil {
		return nil, err
	}
	for i := range reactions {
		for j := range reactions[i].Users {
			reactions[i].Users[j].Avatar = signLegacyFileDownloadURL(reactions[i].Users[j].Avatar)
		}
	}
	return reactions, nil
}

func (s *PublicationService) AddCommentReaction(ctx context.Context, pubID, commentID uuid.UUID, userID int64, reaction string) ([]domain.ReactionSummary, error) {
	reaction = strings.TrimSpace(reaction)
	reaction = canonicalReaction(reaction)
	if reaction == "" {
		return nil, apperrors.BadRequest("invalid reaction emoji")
	}
	if _, err := s.readablePublication(ctx, pubID, userID); err != nil {
		return nil, err
	}
	comment, err := s.repo.GetCommentByID(ctx, commentID)
	if err != nil || comment.PublicationID != pubID {
		return nil, apperrors.NotFound("comment not found")
	}
	if comment.IsHidden {
		return nil, ErrForbiddenAction
	}

	if err := s.repo.AddCommentReaction(ctx, commentID, userID, reaction); err != nil {
		return nil, err
	}

	reactionsMap, err := s.repo.GetCommentReactions(ctx, []uuid.UUID{commentID}, userID)
	if err != nil {
		return nil, err
	}
	reactions := reactionsMap[commentID]
	for i := range reactions {
		for j := range reactions[i].Users {
			reactions[i].Users[j].Avatar = signLegacyFileDownloadURL(reactions[i].Users[j].Avatar)
		}
	}
	return reactions, nil
}

func (s *PublicationService) RemoveCommentReaction(ctx context.Context, pubID, commentID uuid.UUID, userID int64, reaction string) ([]domain.ReactionSummary, error) {
	reaction = strings.TrimSpace(reaction)
	reaction = canonicalReaction(reaction)
	if reaction == "" {
		return nil, apperrors.BadRequest("invalid reaction emoji")
	}
	if _, err := s.readablePublication(ctx, pubID, userID); err != nil {
		return nil, err
	}
	comment, err := s.repo.GetCommentByID(ctx, commentID)
	if err != nil || comment.PublicationID != pubID {
		return nil, apperrors.NotFound("comment not found")
	}

	if err := s.repo.RemoveCommentReaction(ctx, commentID, userID, reaction); err != nil {
		return nil, err
	}

	reactionsMap, err := s.repo.GetCommentReactions(ctx, []uuid.UUID{commentID}, userID)
	if err != nil {
		return nil, err
	}
	reactions := reactionsMap[commentID]
	for i := range reactions {
		for j := range reactions[i].Users {
			reactions[i].Users[j].Avatar = signLegacyFileDownloadURL(reactions[i].Users[j].Avatar)
		}
	}
	return reactions, nil
}

func (s *PublicationService) AcknowledgePublication(ctx context.Context, pubID uuid.UUID, userID int64) (*domain.AcknowledgePublicationResponse, error) {
	pub, err := s.readablePublication(ctx, pubID, userID)
	if err != nil {
		return nil, err
	}
	acknowledgedAt, err := s.repo.AcknowledgeWithView(ctx, pubID, userID, pub.IsBanner && pub.RequiresAcknowledgement)
	if err != nil {
		return nil, err
	}

	return &domain.AcknowledgePublicationResponse{
		Status:         "acknowledged",
		Acknowledged:   true,
		AcknowledgedAt: acknowledgedAt,
	}, nil
}

func (s *PublicationService) UnacknowledgePublication(ctx context.Context, pubID uuid.UUID, userID int64) (*domain.AcknowledgePublicationResponse, error) {
	if _, err := s.readablePublication(ctx, pubID, userID); err != nil {
		return nil, err
	}
	if err := s.repo.Unacknowledge(ctx, pubID, userID); err != nil {
		return nil, err
	}
	return &domain.AcknowledgePublicationResponse{
		Status:         "unacknowledged",
		Acknowledged:   false,
		AcknowledgedAt: time.Time{},
	}, nil
}

// GetPublicationAnalytics returns complete reading and acknowledgement analytics for a publication
func (s *PublicationService) GetPublicationAnalytics(ctx context.Context, pubID uuid.UUID, userID int64) (*domain.PublicationAnalyticsResponse, error) {
	pub, err := s.readablePublication(ctx, pubID, userID)
	if err != nil {
		return nil, err
	}
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}
	if !s.isModeratorOrAuthor(pub, user) {
		return nil, ErrForbiddenAction
	}
	return s.repo.GetPublicationAnalytics(ctx, pubID)
}

// ListPublicationReaders returns paginated list of target employees with read and acknowledgement status
func (s *PublicationService) ListPublicationReaders(ctx context.Context, pubID uuid.UUID, userID int64, filter domain.PublicationReadersFilter) (*domain.PublicationReadersResponse, error) {
	pub, err := s.readablePublication(ctx, pubID, userID)
	if err != nil {
		return nil, err
	}
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}
	if !s.isModeratorOrAuthor(pub, user) {
		return nil, ErrForbiddenAction
	}

	resp, err := s.repo.ListPublicationReaders(ctx, pubID, filter)
	if err != nil {
		return nil, err
	}
	for i := range resp.Readers {
		resp.Readers[i].User.AvatarURL = signLegacyFileDownloadURL(resp.Readers[i].User.AvatarURL)
	}
	return resp, nil
}

func sanitizeExportFileName(title string) string {
	var b strings.Builder
	for _, r := range title {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			(r >= 'а' && r <= 'я') || (r >= 'А' && r <= 'Я') || r == '-' || r == '_' || r == ' ' {
			b.WriteRune(r)
		}
	}
	res := strings.TrimSpace(b.String())
	if res == "" {
		return "readers"
	}
	if len([]rune(res)) > 50 {
		res = string([]rune(res)[:50])
	}
	return strings.ReplaceAll(res, " ", "_")
}

// CSV quoting does not prevent spreadsheet applications from evaluating formulas.
func safeCSVCell(value string) string {
	trimmed := strings.TrimLeft(value, " \t\r\n")
	if strings.ContainsAny(value[:len(value)-len(trimmed)], "\t\r\n") ||
		(len(trimmed) > 0 && strings.ContainsRune("=+-@", rune(trimmed[0]))) {
		return "'" + value
	}
	return value
}

// ExportPublicationReaders exports reader data to an Excel or CSV file
func (s *PublicationService) ExportPublicationReaders(ctx context.Context, pubID uuid.UUID, userID int64, filter domain.PublicationReadersExportFilter) ([]byte, string, string, error) {
	pub, err := s.readablePublication(ctx, pubID, userID)
	if err != nil {
		return nil, "", "", err
	}
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, "", "", fmt.Errorf("get user: %w", err)
	}
	if !s.isModeratorOrAuthor(pub, user) {
		return nil, "", "", ErrForbiddenAction
	}

	readersResp, err := s.repo.ListPublicationReaders(ctx, pubID, domain.PublicationReadersFilter{
		Status:       filter.Status,
		DepartmentID: filter.DepartmentID,
		MinistryID:   filter.MinistryID,
		Search:       filter.Search,
		Page:         1,
		Limit:        100000,
	})
	if err != nil {
		return nil, "", "", fmt.Errorf("load readers for export: %w", err)
	}

	analytics, _ := s.repo.GetPublicationAnalytics(ctx, pubID)

	cleanTitle := sanitizeExportFileName(pub.Title)
	nowStr := time.Now().Format("20060102_1504")

	if strings.ToLower(strings.TrimSpace(filter.Format)) == "csv" {
		var buf bytes.Buffer
		buf.WriteString("\xef\xbb\xbf") // UTF-8 BOM for Excel compatibility
		w := csv.NewWriter(&buf)

		_ = w.Write([]string{
			"№",
			"Сотрудник",
			"Email",
			"Должность",
			"Отдел",
			"Филиал",
			"Статус прочтения",
			"Дата и время прочтения",
			"Статус ознакомления",
			"Дата и время ознакомления",
		})

		for i, r := range readersResp.Readers {
			viewStatus := "Не прочитано"
			viewTime := "-"
			if r.IsViewed {
				viewStatus = "Прочитано"
				if r.ViewedAt != nil {
					viewTime = r.ViewedAt.Format("02.01.2006 15:04:05")
				}
			}

			ackStatus := "Не ознакомлен"
			ackTime := "-"
			if r.IsAcknowledged {
				ackStatus = "Ознакомлен"
				if r.AcknowledgedAt != nil {
					ackTime = r.AcknowledgedAt.Format("02.01.2006 15:04:05")
				}
			}

			pos := "-"
			if r.User.Position != nil && *r.User.Position != "" {
				pos = *r.User.Position
			}
			dept := "-"
			if r.DepartmentName != nil && *r.DepartmentName != "" {
				dept = *r.DepartmentName
			}
			min := "-"
			if r.MinistryName != nil && *r.MinistryName != "" {
				min = *r.MinistryName
			}

			_ = w.Write([]string{
				fmt.Sprintf("%d", i+1),
				safeCSVCell(r.User.FullName),
				safeCSVCell(r.Email),
				safeCSVCell(pos),
				safeCSVCell(dept),
				safeCSVCell(min),
				viewStatus,
				viewTime,
				ackStatus,
				ackTime,
			})
		}
		w.Flush()

		fileName := fmt.Sprintf("readers_%s_%s.csv", cleanTitle, nowStr)
		return buf.Bytes(), fileName, "text/csv; charset=utf-8", nil
	}

	// Default: XLSX
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()

	const sheet = "Ознакомление"
	defaultSheet := f.GetSheetName(f.GetActiveSheetIndex())
	if defaultSheet != sheet {
		_ = f.SetSheetName(defaultSheet, sheet)
	}

	_ = f.SetCellValue(sheet, "A1", "Отчет по ознакомлению с публикацией")
	_ = f.SetCellValue(sheet, "A2", "Публикация: "+pub.Title)

	summaryText := fmt.Sprintf("Всего в выборке: %d", readersResp.TotalCount)
	if analytics != nil {
		summaryText += fmt.Sprintf(" | Целевая аудитория: %d | Прочитали: %d (%.1f%%) | Ознакомились: %d (%.1f%%)",
			analytics.TotalTargetUsers, analytics.ViewsCount, analytics.ViewPercent, analytics.AcknowledgedCount, analytics.AcknowledgementPercent)
	}
	_ = f.SetCellValue(sheet, "A3", summaryText)

	titleStyle, err := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true, Size: 14, Color: "#1E3A8A"},
	})
	if err == nil {
		_ = f.SetCellStyle(sheet, "A1", "A1", titleStyle)
	}

	subStyle, err := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true, Size: 11, Color: "#374151"},
	})
	if err == nil {
		_ = f.SetCellStyle(sheet, "A2", "A3", subStyle)
	}

	headers := []string{
		"№",
		"Сотрудник",
		"Email",
		"Должность",
		"Отдел",
		"Филиал",
		"Статус прочтения",
		"Дата и время прочтения",
		"Статус ознакомления",
		"Дата и время ознакомления",
	}

	headerStyle, err := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Color: "#FFFFFF", Size: 11},
		Fill:      excelize.Fill{Type: "pattern", Color: []string{"#2563EB"}, Pattern: 1},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center", WrapText: true},
		Border: []excelize.Border{
			{Type: "left", Color: "#D1D5DB", Style: 1},
			{Type: "top", Color: "#D1D5DB", Style: 1},
			{Type: "right", Color: "#D1D5DB", Style: 1},
			{Type: "bottom", Color: "#D1D5DB", Style: 1},
		},
	})

	for colIdx, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(colIdx+1, 5)
		_ = f.SetCellValue(sheet, cell, h)
		if err == nil {
			_ = f.SetCellStyle(sheet, cell, cell, headerStyle)
		}
	}
	_ = f.SetRowHeight(sheet, 5, 28)

	dataCenterStyle, _ := f.NewStyle(&excelize.Style{
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
		Border: []excelize.Border{
			{Type: "left", Color: "#E5E7EB", Style: 1},
			{Type: "top", Color: "#E5E7EB", Style: 1},
			{Type: "right", Color: "#E5E7EB", Style: 1},
			{Type: "bottom", Color: "#E5E7EB", Style: 1},
		},
	})

	dataLeftStyle, _ := f.NewStyle(&excelize.Style{
		Alignment: &excelize.Alignment{Horizontal: "left", Vertical: "center"},
		Border: []excelize.Border{
			{Type: "left", Color: "#E5E7EB", Style: 1},
			{Type: "top", Color: "#E5E7EB", Style: 1},
			{Type: "right", Color: "#E5E7EB", Style: 1},
			{Type: "bottom", Color: "#E5E7EB", Style: 1},
		},
	})

	rowIdx := 6
	for i, r := range readersResp.Readers {
		viewStatus := "Не прочитано"
		viewTime := "-"
		if r.IsViewed {
			viewStatus = "Прочитано"
			if r.ViewedAt != nil {
				viewTime = r.ViewedAt.Format("02.01.2006 15:04:05")
			}
		}

		ackStatus := "Не ознакомлен"
		ackTime := "-"
		if r.IsAcknowledged {
			ackStatus = "Ознакомлен"
			if r.AcknowledgedAt != nil {
				ackTime = r.AcknowledgedAt.Format("02.01.2006 15:04:05")
			}
		}

		pos := "-"
		if r.User.Position != nil && *r.User.Position != "" {
			pos = *r.User.Position
		}
		dept := "-"
		if r.DepartmentName != nil && *r.DepartmentName != "" {
			dept = *r.DepartmentName
		}
		min := "-"
		if r.MinistryName != nil && *r.MinistryName != "" {
			min = *r.MinistryName
		}

		vals := []struct {
			col int
			val interface{}
			ctr bool
		}{
			{1, i + 1, true},
			{2, r.User.FullName, false},
			{3, r.Email, false},
			{4, pos, false},
			{5, dept, false},
			{6, min, false},
			{7, viewStatus, true},
			{8, viewTime, true},
			{9, ackStatus, true},
			{10, ackTime, true},
		}

		for _, v := range vals {
			cell, _ := excelize.CoordinatesToCellName(v.col, rowIdx)
			_ = f.SetCellValue(sheet, cell, v.val)
			if v.ctr && dataCenterStyle != 0 {
				_ = f.SetCellStyle(sheet, cell, cell, dataCenterStyle)
			} else if !v.ctr && dataLeftStyle != 0 {
				_ = f.SetCellStyle(sheet, cell, cell, dataLeftStyle)
			}
		}
		_ = f.SetRowHeight(sheet, rowIdx, 20)
		rowIdx++
	}

	colWidths := map[string]float64{
		"A": 6,
		"B": 28,
		"C": 26,
		"D": 22,
		"E": 20,
		"F": 20,
		"G": 18,
		"H": 22,
		"I": 20,
		"J": 22,
	}
	for col, width := range colWidths {
		_ = f.SetColWidth(sheet, col, col, width)
	}

	buf, err := f.WriteToBuffer()
	if err != nil {
		return nil, "", "", fmt.Errorf("write excel buffer: %w", err)
	}

	fileName := fmt.Sprintf("readers_%s_%s.xlsx", cleanTitle, nowStr)
	return buf.Bytes(), fileName, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", nil
}

func (s *PublicationService) SharePublication(ctx context.Context, pubID uuid.UUID, userID int64) error {
	if _, err := s.readablePublication(ctx, pubID, userID); err != nil {
		return err
	}
	return s.repo.IncrementShares(ctx, pubID)
}

// Helper methods to resolve names & structures

func (s *PublicationService) enrichPublication(ctx context.Context, pub *domain.Publication, currentUserID int64) (*domain.PublicationResponse, error) {
	// Load author full details to resolve Ministry & Department names
	authorDetails, err := s.userRepo.GetFullDetailsByID(ctx, pub.AuthorID)
	if err != nil {
		s.logger.Warn("Failed to resolve publication author details, using fallback", zap.Error(err), zap.Int64("author_id", pub.AuthorID))
		authorDetails = &domain.UserDetailResponse{
			UserResponse: domain.UserResponse{
				ID:        pub.AuthorID,
				FirstName: "Пользователь",
				LastName:  "",
				FullName:  "Пользователь",
			},
		}
	}

	authorMini := domain.UserMini{
		ID:        authorDetails.ID,
		FirstName: authorDetails.FirstName,
		LastName:  authorDetails.LastName,
		FullName:  authorDetails.FirstName + " " + authorDetails.LastName,
		AvatarURL: signLegacyFileDownloadURL(authorDetails.AvatarURL),
		Position:  authorDetails.Position,
	}

	var approvedMini *domain.UserMini
	if pub.ApprovedByID != nil {
		if *pub.ApprovedByID == pub.AuthorID {
			approvedMini = &authorMini
		} else {
			appr, err := s.userRepo.GetByID(ctx, *pub.ApprovedByID)
			if err == nil {
				approvedMini = &domain.UserMini{
					ID:        appr.ID,
					FirstName: appr.FirstName,
					LastName:  appr.LastName,
					FullName:  appr.FirstName + " " + appr.LastName,
					AvatarURL: signLegacyFileDownloadURL(appr.AvatarURL),
					Position:  appr.Position,
				}
			}
		}
	}

	// Resolve Source properties (От кого)
	var sourceName string
	var sourceSubtitle string
	var sourceAvatar *string

	switch pub.SourceType {
	case "ministry":
		if authorDetails.MinistryName != nil {
			sourceName = *authorDetails.MinistryName
		} else {
			sourceName = "Министерство"
		}
		sourceSubtitle = "Официальная публикация"
		// Default to nil, or we can use author's avatar as brand identifier.
		// Let's use nil/default logo in frontend, but return leader's avatar if useful.
		sourceAvatar = signLegacyFileDownloadURL(authorDetails.AvatarURL)
	case "department":
		if authorDetails.DepartmentName != nil {
			sourceName = *authorDetails.DepartmentName
		} else {
			sourceName = "Департамент"
		}
		sourceSubtitle = "Публикация от имени департамента"
		sourceAvatar = signLegacyFileDownloadURL(authorDetails.AvatarURL)
	default: // "user"
		sourceName = authorDetails.FirstName + " " + authorDetails.LastName
		if authorDetails.Position != nil {
			sourceSubtitle = *authorDetails.Position
		} else {
			sourceSubtitle = ""
		}
		sourceAvatar = signLegacyFileDownloadURL(authorDetails.AvatarURL)
	}

	attachments := make([]domain.PublicationAttachment, 0, len(pub.Attachments))
	for _, att := range pub.Attachments {
		att = s.signPublicationAttachment(pub.AuthorID, att)
		attachments = append(attachments, att)
	}

	var signedCover *string
	if pub.CoverImageURL != nil && *pub.CoverImageURL != "" {
		if s.validatePublicationCover(ctx, pub.AuthorID, *pub.CoverImageURL) == nil {
			signedCover = signLegacyFileDownloadURL(pub.CoverImageURL)
		}
	}

	contentStr := ""
	if pub.Content != nil {
		contentStr = *pub.Content
	}

	status := pub.Status
	if status == "" {
		status = domain.PublicationStatusPublished
	}

	reactions, userReactions, err := s.repo.GetReactions(ctx, pub.ID, currentUserID)
	if err == nil {
		for i := range reactions {
			for j := range reactions[i].Users {
				reactions[i].Users[j].Avatar = signLegacyFileDownloadURL(reactions[i].Users[j].Avatar)
			}
		}
	} else {
		reactions = []domain.ReactionSummary{}
		userReactions = []string{}
	}

	var pinnedMini *domain.UserMini
	if pub.PinnedByID != nil {
		if *pub.PinnedByID == pub.AuthorID {
			pinnedMini = &authorMini
		} else {
			pUser, err := s.userRepo.GetByID(ctx, *pub.PinnedByID)
			if err == nil {
				pinnedMini = &domain.UserMini{
					ID:        pUser.ID,
					FirstName: pUser.FirstName,
					LastName:  pUser.LastName,
					FullName:  pUser.FirstName + " " + pUser.LastName,
					AvatarURL: signLegacyFileDownloadURL(pUser.AvatarURL),
					Position:  pUser.Position,
				}
			}
		}
	}

	resp := &domain.PublicationResponse{
		ID:                      pub.ID,
		SourceType:              pub.SourceType,
		Type:                    pub.Type,
		Title:                   pub.Title,
		Content:                 contentStr,
		ContentDoc:              pub.ContentDoc,
		ContentHTML:             sanitizePublicationHTML(pub.ContentHTML),
		CoverImageURL:           signedCover,
		Status:                  status,
		TargetAudience:          pub.TargetAudience,
		TargetMinistryID:        pub.TargetMinistryID,
		TargetDepartmentID:      pub.TargetDepartmentID,
		TargetMinistryIDs:       pub.TargetMinistryIDs,
		TargetDepartmentIDs:     pub.TargetDepartmentIDs,
		MinistryID:              pub.MinistryID,
		StateBodyID:             pub.StateBodyID,
		DepartmentID:            pub.DepartmentID,
		Importance:              pub.Importance,
		IsPinned:                pub.IsPinned,
		PinnedAt:                pub.PinnedAt,
		PinnedUntil:             pub.PinnedUntil,
		PinnedBy:                pinnedMini,
		IsBanner:                pub.IsBanner,
		BannerUntil:             pub.BannerUntil,
		BannerActionText:        pub.BannerActionText,
		BannerActionURL:         pub.BannerActionURL,
		BannerColor:             pub.BannerColor,
		IsBannerDismissed:       pub.IsBannerDismissed,
		NotificationSentAt:      pub.NotificationSentAt,
		AllowComments:           pub.AllowComments,
		RequiresAcknowledgement: pub.RequiresAcknowledgement,
		ScheduledAt:             pub.ScheduledAt,
		PublishedAt:             pub.PublishedAt,
		ArchiveAt:               pub.ArchiveAt,
		ArchivedAt:              pub.ArchivedAt,
		ViewsCount:              pub.ViewsCount,
		LikesCount:              pub.LikesCount,
		CommentsCount:           pub.CommentsCount,
		SharesCount:             pub.SharesCount,
		IsLiked:                 pub.IsLiked,
		IsAcknowledged:          pub.IsAcknowledged,
		Acknowledged:            pub.IsAcknowledged,
		CreatedAt:               pub.CreatedAt,
		UpdatedAt:               pub.UpdatedAt,
		Attachments:             attachments,
		Reactions:               reactions,
		UserReactions:           userReactions,
		SourceName:              sourceName,
		SourceSubtitle:          sourceSubtitle,
		SourceAvatar:            sourceAvatar,
		Author:                  authorMini,
		ApprovedBy:              approvedMini,
	}

	return resp, nil
}

func (s *PublicationService) enrichComment(ctx context.Context, comment *domain.PublicationComment, currentUserID int64, reactions []domain.ReactionSummary) (*domain.CommentResponse, error) {
	author, err := s.userRepo.GetByID(ctx, comment.AuthorID)
	if err != nil {
		return nil, err
	}

	var replyToUser *domain.UserMini
	if comment.ReplyToUserID != nil {
		if u, err := s.userRepo.GetByID(ctx, *comment.ReplyToUserID); err == nil {
			replyToUser = &domain.UserMini{
				ID:        u.ID,
				FirstName: u.FirstName,
				LastName:  u.LastName,
				FullName:  u.FirstName + " " + u.LastName,
				AvatarURL: signLegacyFileDownloadURL(u.AvatarURL),
				Position:  u.Position,
			}
		}
	}

	var pinnedBy *domain.UserMini
	if comment.PinnedByID != nil {
		if u, err := s.userRepo.GetByID(ctx, *comment.PinnedByID); err == nil {
			pinnedBy = &domain.UserMini{
				ID:        u.ID,
				FirstName: u.FirstName,
				LastName:  u.LastName,
				FullName:  u.FirstName + " " + u.LastName,
				AvatarURL: signLegacyFileDownloadURL(u.AvatarURL),
				Position:  u.Position,
			}
		}
	}

	if reactions == nil {
		reactions = []domain.ReactionSummary{}
	}

	status := comment.Status
	if status == "" {
		status = domain.CommentStatusActive
	}

	return &domain.CommentResponse{
		ID:            comment.ID,
		PublicationID: comment.PublicationID,
		ParentID:      comment.ParentID,
		Content:       comment.Content,
		CreatedAt:     comment.CreatedAt,
		UpdatedAt:     comment.UpdatedAt,
		Author: domain.UserMini{
			ID:        author.ID,
			FirstName: author.FirstName,
			LastName:  author.LastName,
			FullName:  author.FirstName + " " + author.LastName,
			AvatarURL: signLegacyFileDownloadURL(author.AvatarURL),
			Position:  author.Position,
		},
		ReplyToUser:      replyToUser,
		IsPinned:         comment.IsPinned,
		PinnedAt:         comment.PinnedAt,
		PinnedBy:         pinnedBy,
		IsHidden:         comment.IsHidden,
		Status:           status,
		ModerationReason: comment.ModerationReason,
		RepliesCount:     0,
		Replies:          make([]domain.CommentResponse, 0),
		Reactions:        reactions,
	}, nil
}

func (s *PublicationService) RecordView(ctx context.Context, id uuid.UUID, userID int64) error {
	return s.repo.RecordView(ctx, id, userID)
}

func detectMimeAndType(filename, headerContentType string) (mimeType string, fileType string) {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".jpg", ".jpeg":
		return "image/jpeg", domain.AttachmentTypeImage
	case ".png":
		return "image/png", domain.AttachmentTypeImage
	case ".webp":
		return "image/webp", domain.AttachmentTypeImage
	case ".gif":
		return "image/gif", domain.AttachmentTypeImage
	case ".svg":
		return "image/svg+xml", domain.AttachmentTypeImage
	case ".pdf":
		return "application/pdf", domain.AttachmentTypePDF
	case ".doc":
		return "application/msword", domain.AttachmentTypeDocument
	case ".docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document", domain.AttachmentTypeDocument
	case ".xls":
		return "application/vnd.ms-excel", domain.AttachmentTypeDocument
	case ".xlsx":
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", domain.AttachmentTypeDocument
	case ".ppt":
		return "application/vnd.ms-powerpoint", domain.AttachmentTypeDocument
	case ".pptx":
		return "application/vnd.openxmlformats-officedocument.presentationml.presentation", domain.AttachmentTypeDocument
	case ".txt":
		return "text/plain", domain.AttachmentTypeDocument
	case ".csv":
		return "text/csv", domain.AttachmentTypeDocument
	case ".zip":
		return "application/zip", domain.AttachmentTypeDocument
	case ".rar":
		return "application/x-rar-compressed", domain.AttachmentTypeDocument
	case ".mp4":
		return "video/mp4", domain.AttachmentTypeVideo
	case ".webm":
		return "video/webm", domain.AttachmentTypeVideo
	case ".mov":
		return "video/quicktime", domain.AttachmentTypeVideo
	default:
		if headerContentType != "" && headerContentType != "application/octet-stream" {
			if strings.HasPrefix(headerContentType, "image/") {
				return headerContentType, domain.AttachmentTypeImage
			}
			if strings.HasPrefix(headerContentType, "video/") {
				return headerContentType, domain.AttachmentTypeVideo
			}
			if headerContentType == "application/pdf" {
				return headerContentType, domain.AttachmentTypePDF
			}
			return headerContentType, domain.AttachmentTypeDocument
		}
		return "application/octet-stream", domain.AttachmentTypeDocument
	}
}

func (s *PublicationService) UploadAttachments(ctx context.Context, pubID uuid.UUID, userID int64, files []*multipart.FileHeader, captions []string) ([]domain.PublicationAttachment, error) {
	pub, err := s.CheckPublicationEditAccess(ctx, pubID, userID)
	if err != nil {
		return nil, err
	}
	if err := validatePublicationUploadBatch(files, false); err != nil {
		return nil, apperrors.Validation(err.Error())
	}
	if len(files) == 0 {
		return nil, apperrors.BadRequest("no files provided")
	}
	if s.fileService == nil {
		return nil, fmt.Errorf("file storage service is not available")
	}

	nextIdx, err := s.repo.GetNextAttachmentOrderIndex(ctx, pubID)
	if err != nil {
		return nil, err
	}

	var created []domain.PublicationAttachment
	keys := []string{}
	committed := false
	defer func() {
		if !committed && len(keys) > 0 {
			s.queueFailedPublicationUploads(ctx, keys)
		}
	}()
	for i, header := range files {
		if header.Size > 100*1024*1024 {
			return nil, apperrors.BadRequest(fmt.Sprintf("file %s exceeds 100MB limit", header.Filename))
		}

		mimeType, fileType := detectMimeAndType(header.Filename, header.Header.Get("Content-Type"))
		file, err := header.Open()
		if err != nil {
			return nil, fmt.Errorf("open uploaded file %s: %w", header.Filename, err)
		}

		objectName, err := s.fileService.UploadFile(ctx, pub.AuthorID, header.Filename, fileType, header.Size, file)
		_ = file.Close()
		if err != nil {
			return nil, fmt.Errorf("upload file %s: %w", header.Filename, err)
		}
		keys = append(keys, objectName)

		var captionPtr *string
		if i < len(captions) && strings.TrimSpace(captions[i]) != "" {
			trimmed := strings.TrimSpace(captions[i])
			captionPtr = &trimmed
		}

		fileSize := header.Size
		att := domain.PublicationAttachment{
			ID:            uuid.New(),
			PublicationID: pubID,
			FileURL:       objectName,
			FileName:      header.Filename,
			FileType:      fileType,
			FileSize:      &fileSize,
			Caption:       captionPtr,
			OrderIndex:    nextIdx + i,
			MimeType:      &mimeType,
			CreatedAt:     time.Now(),
		}

		created = append(created, att)
	}
	if err := s.repo.AddAttachments(ctx, pubID, created); err != nil {
		return nil, err
	}
	committed = true
	for i := range created {
		created[i] = s.signPublicationAttachment(pub.AuthorID, created[i])
	}
	return created, nil
}

func (s *PublicationService) UploadGallery(ctx context.Context, pubID uuid.UUID, userID int64, files []*multipart.FileHeader, captions []string) ([]domain.PublicationAttachment, error) {
	pub, err := s.CheckPublicationEditAccess(ctx, pubID, userID)
	if err != nil {
		return nil, err
	}
	if err := validatePublicationUploadBatch(files, true); err != nil {
		return nil, apperrors.Validation(err.Error())
	}
	if len(files) == 0 {
		return nil, apperrors.BadRequest("no gallery images provided")
	}
	if len(files) > 50 {
		return nil, apperrors.BadRequest("gallery cannot exceed 50 images")
	}
	if s.fileService == nil {
		return nil, fmt.Errorf("file storage service is not available")
	}

	for _, header := range files {
		ext := strings.ToLower(filepath.Ext(header.Filename))
		switch ext {
		case ".jpg", ".jpeg", ".png", ".webp", ".gif":
			// valid image
		default:
			return nil, apperrors.BadRequest(fmt.Sprintf("file %s is not an allowed image format (jpg, jpeg, png, webp, gif)", header.Filename))
		}
		if header.Size > 25*1024*1024 {
			return nil, apperrors.BadRequest(fmt.Sprintf("image %s exceeds 25MB limit", header.Filename))
		}
	}

	nextIdx, err := s.repo.GetNextAttachmentOrderIndex(ctx, pubID)
	if err != nil {
		return nil, err
	}

	var created []domain.PublicationAttachment
	keys := []string{}
	committed := false
	defer func() {
		if !committed && len(keys) > 0 {
			s.queueFailedPublicationUploads(ctx, keys)
		}
	}()
	for i, header := range files {
		mimeType, _ := detectMimeAndType(header.Filename, header.Header.Get("Content-Type"))
		file, err := header.Open()
		if err != nil {
			return nil, fmt.Errorf("open gallery image %s: %w", header.Filename, err)
		}

		objectName, err := s.fileService.UploadFile(ctx, pub.AuthorID, header.Filename, domain.AttachmentTypeImage, header.Size, file)
		_ = file.Close()
		if err != nil {
			return nil, fmt.Errorf("upload gallery image %s: %w", header.Filename, err)
		}
		keys = append(keys, objectName)

		var captionPtr *string
		if i < len(captions) && strings.TrimSpace(captions[i]) != "" {
			trimmed := strings.TrimSpace(captions[i])
			captionPtr = &trimmed
		}

		fileSize := header.Size
		att := domain.PublicationAttachment{
			ID:            uuid.New(),
			PublicationID: pubID,
			FileURL:       objectName,
			FileName:      header.Filename,
			FileType:      domain.AttachmentTypeImage,
			FileSize:      &fileSize,
			Caption:       captionPtr,
			OrderIndex:    nextIdx + i,
			MimeType:      &mimeType,
			CreatedAt:     time.Now(),
		}

		created = append(created, att)
	}
	if err := s.repo.AddAttachments(ctx, pubID, created); err != nil {
		return nil, err
	}
	committed = true
	for i := range created {
		created[i] = s.signPublicationAttachment(pub.AuthorID, created[i])
	}
	return created, nil
}

func (s *PublicationService) AddVideoEmbed(ctx context.Context, pubID uuid.UUID, userID int64, req domain.AddVideoEmbedRequest) (*domain.PublicationAttachment, error) {
	if _, err := s.CheckPublicationEditAccess(ctx, pubID, userID); err != nil {
		return nil, err
	}

	trimmedURL := strings.TrimSpace(req.VideoURL)
	if trimmedURL == "" {
		return nil, apperrors.BadRequest("video_url is required")
	}

	provider, embedURL, _, err := domain.ParseVideoEmbed(trimmedURL)
	if err != nil {
		return nil, apperrors.BadRequest(fmt.Sprintf("invalid video url: %s", err.Error()))
	}

	nextIdx, err := s.repo.GetNextAttachmentOrderIndex(ctx, pubID)
	if err != nil {
		return nil, err
	}

	var captionPtr *string
	if req.Caption != nil && strings.TrimSpace(*req.Caption) != "" {
		trimmed := strings.TrimSpace(*req.Caption)
		captionPtr = &trimmed
	}

	fileName := fmt.Sprintf("%s video", strings.Title(provider))
	mimeType := "video/embed"
	if provider == domain.VideoProviderDirect {
		mimeType = "video/mp4"
	}

	att := domain.PublicationAttachment{
		ID:            uuid.New(),
		PublicationID: pubID,
		FileURL:       embedURL,
		FileName:      fileName,
		FileType:      domain.AttachmentTypeVideo,
		FileSize:      nil,
		Caption:       captionPtr,
		OrderIndex:    nextIdx,
		MimeType:      &mimeType,
		VideoProvider: &provider,
		VideoEmbedURL: &embedURL,
		CreatedAt:     time.Now(),
	}

	batch := []domain.PublicationAttachment{att}
	if err := s.repo.AddAttachments(ctx, pubID, batch); err != nil {
		return nil, err
	}
	att = batch[0]

	pub, err := s.CheckPublicationEditAccess(ctx, pubID, userID)
	if err != nil {
		return nil, err
	}
	res := s.signPublicationAttachment(pub.AuthorID, att)
	return &res, nil
}

func (s *PublicationService) DeleteAttachment(ctx context.Context, pubID, attachmentID uuid.UUID, userID int64) error {
	pub, err := s.CheckPublicationEditAccess(ctx, pubID, userID)
	if err != nil {
		return err
	}
	return s.repo.DeletePublicationAttachment(ctx, pubID, attachmentID, pub.AuthorID)
}

func (s *PublicationService) ReorderAttachments(ctx context.Context, pubID uuid.UUID, userID int64, orderedIDs []uuid.UUID) ([]domain.PublicationAttachment, error) {
	pub, err := s.CheckPublicationEditAccess(ctx, pubID, userID)
	if err != nil {
		return nil, err
	}
	if len(orderedIDs) == 0 {
		return nil, apperrors.BadRequest("ordered attachment IDs cannot be empty")
	}

	if err := s.repo.ReorderAttachments(ctx, pubID, orderedIDs); err != nil {
		if errors.Is(err, repository.ErrInvalidPublicationAttachmentOrder) {
			return nil, apperrors.BadRequest(err.Error())
		}
		return nil, err
	}

	attachments, err := s.repo.GetAttachmentsByPubID(ctx, pubID)
	if err != nil {
		return nil, err
	}

	signed := make([]domain.PublicationAttachment, 0, len(attachments))
	for _, att := range attachments {
		signed = append(signed, s.signPublicationAttachment(pub.AuthorID, att))
	}
	return signed, nil
}

// PinPublication pins a publication to the top of feeds
func (s *PublicationService) PinPublication(ctx context.Context, id uuid.UUID, userID int64, req domain.PinPublicationRequest) (*domain.PublicationResponse, error) {
	pub, err := s.readablePublication(ctx, id, userID)
	if err != nil {
		return nil, err
	}
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}
	if !s.isModeratorOrAuthor(pub, user) {
		return nil, ErrForbiddenAction
	}

	if err := s.repo.PinPublication(ctx, id, userID, req.PinnedUntil); err != nil {
		return nil, err
	}

	return s.GetPublication(ctx, id, userID)
}

// UnpinPublication unpins a publication from the top of feeds
func (s *PublicationService) UnpinPublication(ctx context.Context, id uuid.UUID, userID int64) (*domain.PublicationResponse, error) {
	pub, err := s.readablePublication(ctx, id, userID)
	if err != nil {
		return nil, err
	}
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}
	if !s.isModeratorOrAuthor(pub, user) {
		return nil, ErrForbiddenAction
	}

	if err := s.repo.UnpinPublication(ctx, id); err != nil {
		return nil, err
	}

	return s.GetPublication(ctx, id, userID)
}

// SetBanner configures banner mode for a publication
func (s *PublicationService) SetBanner(ctx context.Context, id uuid.UUID, userID int64, req domain.SetBannerRequest) (*domain.PublicationResponse, error) {
	pub, err := s.readablePublication(ctx, id, userID)
	if err != nil {
		return nil, err
	}
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}
	if !s.isModeratorOrAuthor(pub, user) {
		return nil, ErrForbiddenAction
	}
	if err := validatePublicationBannerFields(req.BannerUntil, req.BannerActionText, req.BannerActionURL, req.BannerColor); err != nil {
		return nil, err
	}

	if err := s.repo.SetBanner(ctx, id, req.IsBanner, req.BannerUntil, req.BannerActionText, req.BannerActionURL, req.BannerColor); err != nil {
		return nil, err
	}

	return s.GetPublication(ctx, id, userID)
}

func validatePublicationBannerFields(until *time.Time, actionText, actionURL, color *string) error {
	if until != nil && !until.After(time.Now()) {
		return apperrors.Validation("banner_until must be in the future")
	}
	if actionText != nil && len(*actionText) > 100 {
		return apperrors.Validation("banner_action_text is too long")
	}
	if actionURL != nil {
		raw := strings.TrimSpace(*actionURL)
		if len(raw) > 500 || !validBannerActionURL(raw) {
			return apperrors.Validation("banner_action_url must be an HTTPS URL or an internal path")
		}
	}
	if color != nil {
		switch *color {
		case domain.BannerColorUrgent, domain.BannerColorWarning, domain.BannerColorInfo, domain.BannerColorPrimary:
		default:
			return apperrors.Validation("invalid banner_color")
		}
	}
	return nil
}

func validBannerActionURL(raw string) bool {
	if raw == "" {
		return true
	}
	if strings.HasPrefix(raw, "/") && !strings.HasPrefix(raw, "//") && !strings.ContainsAny(raw, "\r\n\\") {
		return true
	}
	parsed, err := url.Parse(raw)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil
}

// DismissBanner hides a banner in the current user's interface
func (s *PublicationService) DismissBanner(ctx context.Context, id uuid.UUID, userID int64) error {
	pub, err := s.readablePublication(ctx, id, userID)
	if err != nil {
		return err
	}
	if pub.RequiresAcknowledgement {
		return apperrors.BadRequest("this banner requires acknowledgement")
	}
	return s.repo.DismissBanner(ctx, id, userID)
}

// ListBanners retrieves active banners visible to the current user
func (s *PublicationService) ListBanners(ctx context.Context, userID int64) ([]domain.PublicationResponse, error) {
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}

	banners, err := s.repo.ListBanners(ctx, userID, user.MinistryID, user.DepartmentID)
	if err != nil {
		return nil, err
	}

	res := make([]domain.PublicationResponse, 0, len(banners))
	for i := range banners {
		enriched, err := s.enrichPublication(ctx, &banners[i], userID)
		if err == nil {
			res = append(res, *enriched)
		}
	}
	return res, nil
}

// BroadcastPublication triggers push and/or email notifications for an important/urgent publication
func (s *PublicationService) BroadcastPublication(ctx context.Context, id uuid.UUID, userID int64, req domain.BroadcastPublicationRequest) (*domain.PublicationBroadcastStatsResponse, error) {
	release, acquired, err := s.repo.AcquireBroadcastLock(ctx, id)
	if err != nil {
		return nil, err
	}
	if !acquired {
		return nil, apperrors.BadRequest("broadcast already in progress")
	}
	defer release()

	pub, err := s.readablePublication(ctx, id, userID)
	if err != nil {
		return nil, err
	}
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}
	if !s.isModeratorOrAuthor(pub, user) {
		return nil, ErrForbiddenAction
	}

	if pub.Status != domain.PublicationStatusPublished {
		return nil, apperrors.BadRequest("cannot broadcast an unpublished publication")
	}
	if !publicationBroadcastable(pub, time.Now()) {
		return nil, apperrors.BadRequest("cannot broadcast an expired or future publication")
	}

	// Safety check against rapid duplicate broadcasts
	if pub.NotificationSentAt != nil && !req.Force {
		if time.Since(*pub.NotificationSentAt) < 5*time.Minute {
			_ = s.repo.CompleteQueuedBroadcast(ctx, pub.ID)
			return nil, apperrors.BadRequest("broadcast already sent recently, use force=true to resend")
		}
	}

	sendPush := true
	if req.SendPush != nil {
		sendPush = *req.SendPush
	}
	sendEmail := (pub.Importance == domain.PublicationImportanceUrgent)
	if req.SendEmail != nil {
		sendEmail = *req.SendEmail
	}
	if (pub.Importance == domain.PublicationImportanceImportant || pub.Importance == domain.PublicationImportanceUrgent) && !sendPush {
		return nil, apperrors.BadRequest("push delivery is required for important publications")
	}
	if pub.Importance == domain.PublicationImportanceUrgent && !sendEmail {
		return nil, apperrors.BadRequest("email delivery is required for urgent publications")
	}
	if !sendPush && !sendEmail {
		return nil, apperrors.BadRequest("select at least one delivery channel")
	}
	if sendPush && s.notifService == nil {
		return nil, fmt.Errorf("push sender is not configured")
	}
	if sendEmail && (s.emailService == nil || !s.emailService.Enabled()) {
		return nil, fmt.Errorf("email sender is not configured")
	}

	recipients, err := s.repo.GetTargetAudienceRecipients(ctx, pub)
	if err != nil {
		return nil, err
	}

	pushCount := 0
	emailCount := 0
	var deliveryErrors []error

	// Push notifications via NotificationService (FCM + WebSocket + DB)
	if sendPush && s.notifService != nil {
		titlePrefix := ""
		if pub.Importance == domain.PublicationImportanceUrgent {
			titlePrefix = "🚨 ВАЖНО: "
		} else if pub.Importance == domain.PublicationImportanceImportant {
			titlePrefix = "📢 "
		}
		notifTitle := titlePrefix + pub.Title
		snippet := ""
		if pub.Content != nil {
			snippet = *pub.Content
		}
		if len([]rune(snippet)) > 150 {
			snippet = string([]rune(snippet)[:147]) + "..."
		}

		for _, rec := range recipients {
			if rec.ID == userID {
				continue
			}
			if !req.Force {
				sent, err := s.repo.HasBroadcastDelivery(ctx, pub.ID, rec.ID, "push")
				if err != nil {
					deliveryErrors = append(deliveryErrors, err)
					continue
				}
				if sent {
					continue
				}
			}
			notifReq := domain.CreateNotificationRequest{
				UserID:        rec.ID,
				Type:          domain.NotificationTypePublicationAlert,
				TitleTemplate: &domain.NotificationTemplate{Source: titlePrefix + "%s", Args: []any{pub.Title}},
				Title:         notifTitle,
				Content:       snippet,
				Metadata: map[string]string{
					"publication_id": pub.ID.String(),
					"importance":     pub.Importance,
					"source_type":    pub.SourceType,
				},
			}
			if err := s.notifService.CreateAndSendNotification(ctx, notifReq); err != nil {
				deliveryErrors = append(deliveryErrors, err)
				continue
			}
			if err := s.repo.RecordBroadcastDelivery(ctx, pub.ID, rec.ID, "push"); err != nil {
				deliveryErrors = append(deliveryErrors, err)
				continue
			}
			pushCount++
		}
	}

	// Email notifications
	if sendEmail && s.emailService != nil && s.emailService.Enabled() {

		for _, rec := range recipients {
			if rec.ID == userID {
				continue
			}
			if strings.TrimSpace(rec.Email) == "" {
				deliveryErrors = append(deliveryErrors, fmt.Errorf("recipient %d has no email address", rec.ID))
				continue
			}
			if !req.Force {
				sent, err := s.repo.HasBroadcastDelivery(ctx, pub.ID, rec.ID, "email")
				if err != nil {
					deliveryErrors = append(deliveryErrors, err)
					continue
				}
				if sent {
					continue
				}
			}
			emailTemplate := s.buildPublicationEmail(pub, i18n.ResolveRecipient(rec.Language))
			if err := s.emailService.SendEmail(ctx, rec.Email, emailTemplate.Subject, emailTemplate.HTMLBody, emailTemplate.TextBody); err != nil {
				deliveryErrors = append(deliveryErrors, err)
				continue
			}
			if err := s.repo.RecordBroadcastDelivery(ctx, pub.ID, rec.ID, "email"); err != nil {
				deliveryErrors = append(deliveryErrors, err)
				continue
			}
			emailCount++
		}
	}

	status := "completed"
	var errorMessage *string
	if len(deliveryErrors) > 0 {
		status = "failed"
		message := fmt.Sprintf("%d recipient deliveries failed", len(deliveryErrors))
		errorMessage = &message
	}
	recordCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.repo.RecordBroadcast(recordCtx, pub.ID, userID, pub.Importance, pub.TargetAudience, pushCount, emailCount, status, errorMessage); err != nil {
		return nil, err
	}
	if len(deliveryErrors) > 0 {
		return nil, fmt.Errorf("broadcast partially failed: %w", errors.Join(deliveryErrors...))
	}
	if err := s.repo.CompleteQueuedBroadcast(recordCtx, pub.ID); err != nil {
		return nil, err
	}

	return s.repo.GetBroadcastStats(ctx, pub.ID)
}

// GetBroadcastStats returns delivery and acknowledgement stats for a publication
func (s *PublicationService) GetBroadcastStats(ctx context.Context, id uuid.UUID, userID int64) (*domain.PublicationBroadcastStatsResponse, error) {
	pub, err := s.readablePublication(ctx, id, userID)
	if err != nil {
		return nil, err
	}
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}
	if !s.isModeratorOrAuthor(pub, user) {
		return nil, ErrForbiddenAction
	}

	return s.repo.GetBroadcastStats(ctx, id)
}

// ListAcknowledgements returns the list of users who acknowledged the publication
func (s *PublicationService) ListAcknowledgements(ctx context.Context, id uuid.UUID, userID int64, page, limit int) (*domain.PublicationAcknowledgementsResponse, error) {
	pub, err := s.readablePublication(ctx, id, userID)
	if err != nil {
		return nil, err
	}
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}
	if !s.isModeratorOrAuthor(pub, user) {
		return nil, ErrForbiddenAction
	}

	acks, total, err := s.repo.ListAcknowledgements(ctx, id, page, limit)
	if err != nil {
		return nil, err
	}

	for i := range acks {
		acks[i].User.AvatarURL = signLegacyFileDownloadURL(acks[i].User.AvatarURL)
	}

	return &domain.PublicationAcknowledgementsResponse{
		TotalCount: total,
		Users:      acks,
	}, nil
}

func (s *PublicationService) renderPublicationEmailHTML(pub *domain.Publication, languages ...i18n.Locale) string {
	locale := i18n.Russian
	if len(languages) > 0 {
		locale = i18n.ResolveRecipient(string(languages[0]))
	}
	appURL := s.appPublicURL
	if appURL == "" {
		appURL = "https://workspace.alem.kz"
	}
	pubURL := fmt.Sprintf("%s/publications/%s", appURL, pub.ID.String())

	contentHTML := sanitizePublicationHTML(pub.ContentHTML)
	if contentHTML == "" && pub.Content != nil {
		contentHTML = fmt.Sprintf("<p>%s</p>", html.EscapeString(*pub.Content))
	}

	badgeColor := "#ef4444" // red for urgent
	if pub.Importance == domain.PublicationImportanceImportant {
		badgeColor = "#f59e0b" // orange/yellow
	}

	return fmt.Sprintf(`
<!DOCTYPE html>
<html lang="%s">
<head><meta charset="utf-8"></head>
<body style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif; background-color: #f9fafb; margin: 0; padding: 24px;">
  <div style="max-width: 600px; margin: 0 auto; background-color: #ffffff; border-radius: 12px; overflow: hidden; border: 1px solid #e5e7eb; box-shadow: 0 4px 6px -1px rgba(0,0,0,0.05);">
    <div style="background-color: %s; padding: 12px 24px; color: #ffffff; font-weight: bold; font-size: 14px; text-transform: uppercase; letter-spacing: 0.05em;">
      %s
    </div>
    <div style="padding: 24px;">
      <h1 style="font-size: 22px; color: #111827; margin-top: 0; margin-bottom: 16px; line-height: 1.4;">%s</h1>
      <div style="font-size: 15px; color: #374151; line-height: 1.6; margin-bottom: 24px;">
        %s
      </div>
      <div style="text-align: center; margin-top: 32px; margin-bottom: 16px;">
        <a href="%s" style="background-color: #2563eb; color: #ffffff; text-decoration: none; padding: 12px 28px; border-radius: 8px; font-weight: 600; font-size: 15px; display: inline-block;">
          %s
        </a>
      </div>
      <div style="margin-top:24px;font-size:12px;color:#6b7280;">%s</div>
    </div>
  </div>
</body>
</html>`, locale, badgeColor, html.EscapeString(i18n.Text(locale, "email.publication.alert", map[string]any{"Importance": emailImportance(locale, pub.Importance)})), html.EscapeString(pub.Title), contentHTML, html.EscapeString(pubURL), html.EscapeString(i18n.Text(locale, "email.publication.open", nil)), html.EscapeString(i18n.Text(locale, "email.footer", nil)))
}

func (s *PublicationService) buildPublicationEmail(pub *domain.Publication, locale i18n.Locale) NotificationEmailTemplateData {
	subject := i18n.Text(locale, "email.publication.subject", map[string]any{"Importance": emailImportance(locale, pub.Importance), "Title": pub.Title})
	content := ""
	if pub.Content != nil {
		content = *pub.Content
	}
	appURL := firstNonEmpty(s.appPublicURL, "https://workspace.alem.kz")
	text := subject + "\n\n" + content + "\n\n" + i18n.Text(locale, "email.publication.open", nil) + ": " + strings.TrimRight(appURL, "/") + "/publications/" + pub.ID.String()
	text += "\n\n" + i18n.Text(locale, "email.footer", nil)
	return NotificationEmailTemplateData{Subject: subject, HTMLBody: s.renderPublicationEmailHTML(pub, locale), TextBody: text}
}
