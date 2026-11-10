package service

import (
	"context"
	"strings"
	"time"

	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/fileurl"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

type ProfileSectionService struct {
	repo        *repository.ProfileSectionRepository
	userRepo    *repository.UserRepository
	fileService *FileService
	logger      *zap.Logger
}

func NewProfileSectionService(repo *repository.ProfileSectionRepository, userRepo *repository.UserRepository, fileService *FileService, logger *zap.Logger) *ProfileSectionService {
	return &ProfileSectionService{
		repo:        repo,
		userRepo:    userRepo,
		fileService: fileService,
		logger:      logger.With(zap.String("service", "profile_sections")),
	}
}

func (s *ProfileSectionService) CreateEducation(ctx context.Context, userID int64, req *domain.CreateEducationRequest) (*domain.UserEducationResponse, error) {
	startDate, endDate, err := validateEducationDates(req.StartDate, req.EndDate)
	if err != nil {
		return nil, err
	}
	schoolName, err := requireTrimmed(req.SchoolName, "school_name")
	if err != nil {
		return nil, err
	}
	fieldOfStudy, err := requireTrimmed(req.FieldOfStudy, "field_of_study")
	if err != nil {
		return nil, err
	}

	now := time.Now()
	item := &domain.UserEducation{
		UserID:          userID,
		SchoolName:      schoolName,
		SchoolAvatarURL: normalizeProfileOptionalString(req.SchoolAvatarURL),
		Degree:          normalizeProfileOptionalString(req.Degree),
		FieldOfStudy:    fieldOfStudy,
		StartDate:       startDate,
		EndDate:         endDate,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := s.repo.CreateEducation(ctx, item); err != nil {
		return nil, apperrors.InternalWithErr("Failed to create education", err)
	}
	resp := educationToResponse(item)
	return &resp, nil
}

func (s *ProfileSectionService) ListEducations(ctx context.Context, requesterID, targetUserID int64) (*domain.EducationListResponse, error) {
	if err := s.ensureCanViewUser(ctx, requesterID, targetUserID); err != nil {
		return nil, err
	}
	items, err := s.repo.ListEducationsByUserID(ctx, targetUserID)
	if err != nil {
		return nil, apperrors.InternalWithErr("Failed to load educations", err)
	}
	respItems := make([]domain.UserEducationResponse, 0, len(items))
	for _, item := range items {
		respItems = append(respItems, educationToResponse(item))
	}
	return &domain.EducationListResponse{
		UserID: targetUserID,
		Count:  len(respItems),
		Items:  respItems,
	}, nil
}

func (s *ProfileSectionService) GetEducation(ctx context.Context, requesterID, targetUserID, educationID int64) (*domain.UserEducationResponse, error) {
	if err := s.ensureCanViewUser(ctx, requesterID, targetUserID); err != nil {
		return nil, err
	}
	item, err := s.repo.GetEducationByIDForUser(ctx, educationID, targetUserID)
	if err != nil {
		return nil, mapProfileRepoError("education", err)
	}
	resp := educationToResponse(item)
	return &resp, nil
}

func (s *ProfileSectionService) UpdateEducation(ctx context.Context, userID, educationID int64, req *domain.UpdateEducationRequest) (*domain.UserEducationResponse, error) {
	item, err := s.repo.GetEducationByIDForUser(ctx, educationID, userID)
	if err != nil {
		return nil, mapProfileRepoError("education", err)
	}
	if req.SchoolName != nil {
		value, err := requireTrimmed(*req.SchoolName, "school_name")
		if err != nil {
			return nil, err
		}
		item.SchoolName = value
	}
	if req.SchoolAvatarURL != nil {
		item.SchoolAvatarURL = normalizeProfileOptionalString(req.SchoolAvatarURL)
	}
	if req.Degree != nil {
		item.Degree = normalizeProfileOptionalString(req.Degree)
	}
	if req.FieldOfStudy != nil {
		value, err := requireTrimmed(*req.FieldOfStudy, "field_of_study")
		if err != nil {
			return nil, err
		}
		item.FieldOfStudy = value
	}
	startRaw := formatDate(item.StartDate)
	if req.StartDate != nil {
		startRaw = strings.TrimSpace(*req.StartDate)
	}
	var endRaw *string
	if item.EndDate != nil {
		val := formatDate(*item.EndDate)
		endRaw = &val
	}
	if req.EndDate != nil {
		endRaw = req.EndDate
	}
	startDate, endDate, err := validateEducationDates(startRaw, endRaw)
	if err != nil {
		return nil, err
	}
	item.StartDate = startDate
	item.EndDate = endDate
	item.UpdatedAt = time.Now()
	if err := s.repo.UpdateEducation(ctx, item); err != nil {
		return nil, mapProfileRepoError("education", err)
	}
	resp := educationToResponse(item)
	return &resp, nil
}

func (s *ProfileSectionService) DeleteEducation(ctx context.Context, userID, educationID int64) error {
	if err := s.repo.DeleteEducation(ctx, educationID, userID); err != nil {
		return mapProfileRepoError("education", err)
	}
	return nil
}

func (s *ProfileSectionService) CreateLicenseCertification(ctx context.Context, userID int64, req *domain.CreateLicenseCertificationRequest) (*domain.UserLicenseCertificationResponse, error) {
	issueDate, expirationDate, err := validateLicenseDates(req.IssueDate, req.ExpirationDate)
	if err != nil {
		return nil, err
	}
	name, err := requireTrimmed(req.Name, "name")
	if err != nil {
		return nil, err
	}
	issuingOrganization, err := requireTrimmed(req.IssuingOrganization, "issuing_organization")
	if err != nil {
		return nil, err
	}
	skillName, err := requireTrimmed(req.SkillName, "skill_name")
	if err != nil {
		return nil, err
	}

	attachmentObjectName := normalizeProfileOptionalString(req.AttachmentObjectName)
	if err := validateAttachmentSize(req.AttachmentSizeBytes); err != nil {
		return nil, err
	}
	if err := s.ensureAttachmentAvailable(ctx, attachmentObjectName); err != nil {
		return nil, err
	}

	now := time.Now()
	item := &domain.UserLicenseCertification{
		UserID:                userID,
		Name:                  name,
		IssuingOrganization:   issuingOrganization,
		SkillName:             skillName,
		AvatarURL:             normalizeProfileOptionalString(req.AvatarURL),
		IssueDate:             issueDate,
		ExpirationDate:        expirationDate,
		AttachmentObjectName:  attachmentObjectName,
		AttachmentFileName:    normalizeProfileOptionalString(req.AttachmentFileName),
		AttachmentContentType: normalizeProfileOptionalString(req.AttachmentContentType),
		AttachmentSizeBytes:   req.AttachmentSizeBytes,
		CreatedAt:             now,
		UpdatedAt:             now,
	}
	if err := s.repo.CreateLicenseCertification(ctx, item); err != nil {
		return nil, apperrors.InternalWithErr("Failed to create license certification", err)
	}
	resp, err := s.licenseToResponse(ctx, item)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

func (s *ProfileSectionService) ListLicenseCertifications(ctx context.Context, requesterID, targetUserID int64) (*domain.LicenseCertificationListResponse, error) {
	if err := s.ensureCanViewUser(ctx, requesterID, targetUserID); err != nil {
		return nil, err
	}
	items, err := s.repo.ListLicenseCertificationsByUserID(ctx, targetUserID)
	if err != nil {
		return nil, apperrors.InternalWithErr("Failed to load license certifications", err)
	}
	respItems := make([]domain.UserLicenseCertificationResponse, 0, len(items))
	for _, item := range items {
		resp, mapErr := s.licenseToResponse(ctx, item)
		if mapErr != nil {
			return nil, mapErr
		}
		respItems = append(respItems, resp)
	}
	return &domain.LicenseCertificationListResponse{
		UserID: targetUserID,
		Count:  len(respItems),
		Items:  respItems,
	}, nil
}

func (s *ProfileSectionService) GetLicenseCertification(ctx context.Context, requesterID, targetUserID, itemID int64) (*domain.UserLicenseCertificationResponse, error) {
	if err := s.ensureCanViewUser(ctx, requesterID, targetUserID); err != nil {
		return nil, err
	}
	item, err := s.repo.GetLicenseCertificationByIDForUser(ctx, itemID, targetUserID)
	if err != nil {
		return nil, mapProfileRepoError("license certification", err)
	}
	resp, err := s.licenseToResponse(ctx, item)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

func (s *ProfileSectionService) UpdateLicenseCertification(ctx context.Context, userID, itemID int64, req *domain.UpdateLicenseCertificationRequest) (*domain.UserLicenseCertificationResponse, error) {
	item, err := s.repo.GetLicenseCertificationByIDForUser(ctx, itemID, userID)
	if err != nil {
		return nil, mapProfileRepoError("license certification", err)
	}
	if req.Name != nil {
		value, err := requireTrimmed(*req.Name, "name")
		if err != nil {
			return nil, err
		}
		item.Name = value
	}
	if req.IssuingOrganization != nil {
		value, err := requireTrimmed(*req.IssuingOrganization, "issuing_organization")
		if err != nil {
			return nil, err
		}
		item.IssuingOrganization = value
	}
	if req.SkillName != nil {
		value, err := requireTrimmed(*req.SkillName, "skill_name")
		if err != nil {
			return nil, err
		}
		item.SkillName = value
	}
	if req.AvatarURL != nil {
		item.AvatarURL = normalizeProfileOptionalString(req.AvatarURL)
	}
	issueRaw := formatDate(item.IssueDate)
	if req.IssueDate != nil {
		issueRaw = strings.TrimSpace(*req.IssueDate)
	}
	var expRaw *string
	if item.ExpirationDate != nil {
		val := formatDate(*item.ExpirationDate)
		expRaw = &val
	}
	if req.ExpirationDate != nil {
		expRaw = req.ExpirationDate
	}
	issueDate, expirationDate, err := validateLicenseDates(issueRaw, expRaw)
	if err != nil {
		return nil, err
	}
	item.IssueDate = issueDate
	item.ExpirationDate = expirationDate
	if req.AttachmentObjectName != nil {
		item.AttachmentObjectName = normalizeProfileOptionalString(req.AttachmentObjectName)
	}
	if req.AttachmentFileName != nil {
		item.AttachmentFileName = normalizeProfileOptionalString(req.AttachmentFileName)
	}
	if req.AttachmentContentType != nil {
		item.AttachmentContentType = normalizeProfileOptionalString(req.AttachmentContentType)
	}
	if req.AttachmentSizeBytes != nil {
		if err := validateAttachmentSize(req.AttachmentSizeBytes); err != nil {
			return nil, err
		}
		item.AttachmentSizeBytes = req.AttachmentSizeBytes
	}
	if err := s.ensureAttachmentAvailable(ctx, item.AttachmentObjectName); err != nil {
		return nil, err
	}
	item.UpdatedAt = time.Now()
	if err := s.repo.UpdateLicenseCertification(ctx, item); err != nil {
		return nil, mapProfileRepoError("license certification", err)
	}
	resp, err := s.licenseToResponse(ctx, item)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

func (s *ProfileSectionService) DeleteLicenseCertification(ctx context.Context, userID, itemID int64) error {
	if err := s.repo.DeleteLicenseCertification(ctx, itemID, userID); err != nil {
		return mapProfileRepoError("license certification", err)
	}
	return nil
}

func (s *ProfileSectionService) ensureCanViewUser(ctx context.Context, requesterID, targetUserID int64) error {
	if requesterID != 0 {
		if _, err := s.userRepo.GetByID(ctx, requesterID); err != nil {
			return apperrors.NotFound("Requester not found")
		}
	}
	if _, err := s.userRepo.GetByID(ctx, targetUserID); err != nil {
		return apperrors.NotFound("User not found")
	}
	return nil
}

func (s *ProfileSectionService) ensureAttachmentAvailable(ctx context.Context, objectName *string) error {
	if objectName == nil || *objectName == "" || s.fileService == nil {
		return nil
	}
	if err := s.fileService.ObjectExists(ctx, *objectName); err != nil {
		return apperrors.ToAppError(err)
	}
	return nil
}

func educationToResponse(item *domain.UserEducation) domain.UserEducationResponse {
	resp := domain.UserEducationResponse{
		ID:              item.ID,
		UserID:          item.UserID,
		SchoolName:      item.SchoolName,
		SchoolAvatarURL: fileurl.SignLegacyDownloadURL(item.SchoolAvatarURL),
		Degree:          item.Degree,
		FieldOfStudy:    item.FieldOfStudy,
		StartDate:       formatDate(item.StartDate),
		CreatedAt:       item.CreatedAt.Format(time.RFC3339),
		UpdatedAt:       item.UpdatedAt.Format(time.RFC3339),
	}
	if item.EndDate != nil {
		val := formatDate(*item.EndDate)
		resp.EndDate = &val
	}
	return resp
}

func (s *ProfileSectionService) licenseToResponse(ctx context.Context, item *domain.UserLicenseCertification) (domain.UserLicenseCertificationResponse, error) {
	resp := domain.UserLicenseCertificationResponse{
		ID:                    item.ID,
		UserID:                item.UserID,
		Name:                  item.Name,
		IssuingOrganization:   item.IssuingOrganization,
		SkillName:             item.SkillName,
		AvatarURL:             fileurl.SignLegacyDownloadURL(item.AvatarURL),
		IssueDate:             formatDate(item.IssueDate),
		AttachmentObjectName:  item.AttachmentObjectName,
		AttachmentFileName:    item.AttachmentFileName,
		AttachmentContentType: item.AttachmentContentType,
		AttachmentSizeBytes:   item.AttachmentSizeBytes,
		CreatedAt:             item.CreatedAt.Format(time.RFC3339),
		UpdatedAt:             item.UpdatedAt.Format(time.RFC3339),
	}
	if item.ExpirationDate != nil {
		val := formatDate(*item.ExpirationDate)
		resp.ExpirationDate = &val
	}
	if item.AttachmentObjectName != nil && *item.AttachmentObjectName != "" && s.fileService != nil {
		url, err := s.fileService.GetFileURL(ctx, *item.AttachmentObjectName, time.Hour)
		if err != nil {
			return resp, apperrors.ToAppError(err)
		}
		resp.AttachmentDownloadURL = &url
	}
	return resp, nil
}

func translateField(field string) string {
	switch field {
	case "school_name":
		return "Учебное заведение"
	case "field_of_study":
		return "Специальность"
	case "name":
		return "Название"
	case "issuing_organization":
		return "Организация"
	case "skill_name":
		return "Навык"
	case "start_date":
		return "Дата начала"
	case "end_date":
		return "Дата окончания"
	case "issue_date":
		return "Дата выдачи"
	case "expiration_date":
		return "Срок действия"
	case "attachment_size_bytes":
		return "Размер вложения"
	default:
		return field
	}
}

func requireTrimmed(value, field string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", apperrors.Validation("Поле '" + translateField(field) + "' обязательно для заполнения")
	}
	return trimmed, nil
}

func normalizeProfileOptionalString(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func validateEducationDates(start string, end *string) (time.Time, *time.Time, error) {
	startDate, err := parseDateRequired(start, "start_date")
	if err != nil {
		return time.Time{}, nil, err
	}
	endDate, err := parseOptionalDate(end, "end_date")
	if err != nil {
		return time.Time{}, nil, err
	}
	if endDate != nil && endDate.Before(startDate) {
		return time.Time{}, nil, apperrors.Validation("Дата окончания должна быть больше или равна дате начала")
	}
	return startDate, endDate, nil
}

func validateLicenseDates(issue string, expiration *string) (time.Time, *time.Time, error) {
	issueDate, err := parseDateRequired(issue, "issue_date")
	if err != nil {
		return time.Time{}, nil, err
	}
	expirationDate, err := parseOptionalDate(expiration, "expiration_date")
	if err != nil {
		return time.Time{}, nil, err
	}
	if expirationDate != nil && expirationDate.Before(issueDate) {
		return time.Time{}, nil, apperrors.Validation("Срок действия должен быть больше или равен дате выдачи")
	}
	return issueDate, expirationDate, nil
}

func parseDateRequired(value, field string) (time.Time, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return time.Time{}, apperrors.Validation("Поле '" + translateField(field) + "' обязательно для заполнения")
	}
	parsed, err := time.Parse(time.DateOnly, trimmed)
	if err != nil {
		return time.Time{}, apperrors.Validation("Поле '" + translateField(field) + "' должно быть в формате ГГГГ-ММ-ДД")
	}
	return parsed, nil
}

func parseOptionalDate(value *string, field string) (*time.Time, error) {
	if value == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.DateOnly, trimmed)
	if err != nil {
		return nil, apperrors.Validation("Поле '" + translateField(field) + "' должно быть в формате ГГГГ-ММ-ДД")
	}
	return &parsed, nil
}

func formatDate(value time.Time) string {
	return value.Format(time.DateOnly)
}

func validateAttachmentSize(size *int64) error {
	if size != nil && *size < 0 {
		return apperrors.Validation("Размер вложения должен быть больше или равен 0")
	}
	return nil
}

func mapProfileRepoError(resource string, err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(strings.ToLower(err.Error()), "not found") {
		return apperrors.NotFound(resource + " not found")
	}
	return apperrors.InternalWithErr("Failed to process "+resource, err)
}
