package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

type OrganizationService struct {
	repo                    *repository.OrganizationRepository
	logger                  *zap.Logger
	files                   contactFileStorage
	offboardingFileReindex  func(context.Context, uuid.UUID) error
	offboardingIdentitySync func(context.Context, int64) error
	directoryFetch          func(context.Context, domain.DirectorySettings, string) ([]domain.ContactImportRow, error)
	closeUserSessions       func(int64)
}

func NewOrganizationService(repo *repository.OrganizationRepository, logger *zap.Logger) *OrganizationService {
	return &OrganizationService{repo: repo, logger: logger.With(zap.String("service", "organization"))}
}

func (s *OrganizationService) CreateOrganization(ctx context.Context, userID int64, req domain.CreateOrganizationRequest) (*domain.Organization, error) {
	normalized, err := req.Normalize()
	if err != nil {
		return nil, err
	}
	organization, err := s.repo.CreateOrganization(ctx, userID, normalized)
	if err != nil {
		return nil, err
	}
	if err := s.ProvisionGrowyTenant(ctx, organization.ID, organization.Name, organization.Slug); err != nil {
		s.logger.Warn("Growy tenant provisioning failed after Workspace company creation; SSO will retry it", zap.Error(err), zap.String("organization_id", organization.ID.String()))
	}
	s.provisionSDForOrganization(ctx, userID, organization.ID)
	return organization, nil
}

func (s *OrganizationService) ListPlatformOrganizations(ctx context.Context, userID int64, filter domain.PlatformOrganizationFilter) (*domain.PlatformOrganizationList, error) {
	filter.Query = strings.TrimSpace(filter.Query)
	filter.Status = strings.ToLower(strings.TrimSpace(filter.Status))
	if filter.Status != "" && filter.Status != "active" && filter.Status != "suspended" {
		return nil, domain.ErrOrganizationStatusInvalid
	}
	if filter.Limit <= 0 || filter.Limit > 100 {
		filter.Limit = 25
	}
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	return s.repo.ListPlatformOrganizations(ctx, userID, filter)
}

func (s *OrganizationService) GetPlatformOrganization(ctx context.Context, userID int64, organizationID uuid.UUID) (*domain.PlatformOrganization, error) {
	return s.repo.GetPlatformOrganization(ctx, userID, organizationID)
}

func (s *OrganizationService) CreatePlatformOrganization(ctx context.Context, userID int64, req domain.CreateOrganizationRequest) (*domain.PlatformOrganization, error) {
	normalized, err := req.Normalize()
	if err != nil {
		return nil, err
	}
	organization, err := s.repo.CreatePlatformOrganization(ctx, userID, normalized)
	if err != nil {
		return nil, err
	}
	if err := s.ProvisionGrowyTenant(ctx, organization.ID, organization.Name, organization.Slug); err != nil {
		s.logger.Warn("Growy tenant provisioning failed after Workspace company creation; SSO will retry it", zap.Error(err), zap.String("organization_id", organization.ID.String()))
	}
	return organization, nil
}

func (s *OrganizationService) ListPlatformOrganizationOwnerCandidates(ctx context.Context, userID int64, organizationID *uuid.UUID, query string, limit int) ([]domain.OrganizationOwnerCandidate, error) {
	query = strings.TrimSpace(query)
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	return s.repo.ListPlatformOrganizationOwnerCandidates(ctx, userID, organizationID, query, limit)
}

func (s *OrganizationService) AssignPlatformOrganizationOwner(ctx context.Context, userID int64, organizationID uuid.UUID, targetUserID int64) (*domain.PlatformOrganization, error) {
	if targetUserID <= 0 {
		return nil, fmt.Errorf("organization owner user id is invalid")
	}
	organization, err := s.repo.AssignPlatformOrganizationOwner(ctx, userID, organizationID, targetUserID)
	if err != nil {
		return nil, err
	}
	s.provisionSDForOrganization(ctx, userID, organizationID)
	return organization, nil
}

// A platform company is initially created without an owner. Provision its SD
// space once the verified owner has been assigned; a later launch retries outages.
func (s *OrganizationService) provisionSDForOrganization(ctx context.Context, actor int64, org uuid.UUID) {
	if strings.TrimSpace(os.Getenv("SD_ACCESS_API_URL")) == "" || strings.TrimSpace(os.Getenv("SD_ACCESS_INTERNAL_TOKEN")) == "" {
		return
	}
	if _, err := s.ProvisionSDCompany(ctx, actor, org, ""); err != nil {
		s.logger.Warn("SenimDesk company provisioning failed; SSO will retry it", zap.Error(err), zap.String("organization_id", org.String()))
	}
}

func (s *OrganizationService) UpdatePlatformOrganization(ctx context.Context, userID int64, organizationID uuid.UUID, req domain.UpdatePlatformOrganizationRequest) (*domain.PlatformOrganization, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	return s.repo.UpdatePlatformOrganization(ctx, userID, organizationID, req)
}

func (s *OrganizationService) CurrentOrganization(ctx context.Context, userID int64) (*domain.Organization, error) {
	return s.repo.CurrentOrganization(ctx, userID)
}

func (s *OrganizationService) GrowyHandoffDetails(ctx context.Context, userID int64) (*domain.GrowyHandoffDetails, error) {
	return s.repo.GrowyHandoffDetailsByUserID(ctx, userID)
}

func (s *OrganizationService) GrowyCRMHandoffDetails(ctx context.Context, userID int64) (*domain.GrowyHandoffDetails, error) {
	return s.repo.GrowyCRMHandoffDetailsByUserID(ctx, userID)
}

func (s *OrganizationService) ListUnits(ctx context.Context, userID int64) ([]domain.OrganizationUnit, error) {
	return s.repo.ListUnits(ctx, userID)
}

func (s *OrganizationService) ListMembers(ctx context.Context, userID int64) ([]domain.OrganizationMember, error) {
	return s.repo.ListMembers(ctx, userID)
}

func (s *OrganizationService) ListPlatformUnits(ctx context.Context, userID int64, organizationID uuid.UUID) ([]domain.OrganizationUnit, error) {
	return s.repo.ListPlatformUnits(ctx, userID, organizationID)
}

func (s *OrganizationService) ListPlatformMembers(ctx context.Context, userID int64, organizationID uuid.UUID) ([]domain.OrganizationMember, error) {
	return s.repo.ListPlatformMembers(ctx, userID, organizationID)
}

func (s *OrganizationService) ListGrowyPositionRoles(ctx context.Context, userID int64) ([]domain.GrowyPositionRole, error) {
	return s.repo.ListGrowyPositionRoles(ctx, userID)
}

func (s *OrganizationService) ListPlatformGrowyPositionRoles(ctx context.Context, userID int64, organizationID uuid.UUID) ([]domain.GrowyPositionRole, error) {
	return s.repo.ListPlatformGrowyPositionRoles(ctx, userID, organizationID)
}

func (s *OrganizationService) SaveGrowyPositionRoles(ctx context.Context, userID int64, req domain.UpdateGrowyPositionRolesRequest) error {
	if err := req.Validate(); err != nil {
		return err
	}
	return s.repo.SaveGrowyPositionRoles(ctx, userID, req.Mappings)
}

func (s *OrganizationService) SavePlatformGrowyPositionRoles(ctx context.Context, userID int64, organizationID uuid.UUID, req domain.UpdateGrowyPositionRolesRequest) error {
	if err := req.Validate(); err != nil {
		return err
	}
	return s.repo.SavePlatformGrowyPositionRoles(ctx, userID, organizationID, req.Mappings)
}

func (s *OrganizationService) CreateUnit(ctx context.Context, userID int64, req domain.CreateOrganizationUnitRequest) (*domain.OrganizationUnit, error) {
	normalized, err := req.Normalize()
	if err != nil {
		return nil, err
	}
	return s.repo.CreateUnit(ctx, userID, normalized)
}

func (s *OrganizationService) CreatePlatformUnit(ctx context.Context, userID int64, organizationID uuid.UUID, req domain.CreateOrganizationUnitRequest) (*domain.OrganizationUnit, error) {
	normalized, err := req.Normalize()
	if err != nil {
		return nil, err
	}
	return s.repo.CreatePlatformUnit(ctx, userID, organizationID, normalized)
}

func (s *OrganizationService) UpdateUnit(ctx context.Context, userID int64, unitID uuid.UUID, req domain.UpdateOrganizationUnitRequest) (*domain.OrganizationUnit, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	return s.repo.UpdateUnit(ctx, userID, unitID, req)
}

func (s *OrganizationService) UpdatePlatformUnit(ctx context.Context, userID int64, organizationID, unitID uuid.UUID, req domain.UpdateOrganizationUnitRequest) (*domain.OrganizationUnit, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	return s.repo.UpdatePlatformUnit(ctx, userID, organizationID, unitID, req)
}

func (s *OrganizationService) ArchiveUnit(ctx context.Context, userID int64, unitID uuid.UUID) error {
	return s.repo.ArchiveUnit(ctx, userID, unitID)
}

func (s *OrganizationService) ArchivePlatformUnit(ctx context.Context, userID int64, organizationID, unitID uuid.UUID) error {
	return s.repo.ArchivePlatformUnit(ctx, userID, organizationID, unitID)
}

func (s *OrganizationService) AssignUser(ctx context.Context, actorID, targetUserID int64, req domain.AssignUserToUnitRequest) error {
	return s.repo.AssignUser(ctx, actorID, targetUserID, req)
}

func (s *OrganizationService) AssignPlatformUser(ctx context.Context, userID int64, organizationID uuid.UUID, targetUserID int64, req domain.AssignUserToUnitRequest) error {
	return s.repo.AssignPlatformUser(ctx, userID, organizationID, targetUserID, req)
}

func IsOrganizationValidationError(err error) bool {
	return errors.Is(err, domain.ErrOrganizationUnitTypeRequired) ||
		errors.Is(err, domain.ErrOrganizationUnitParentConflict) ||
		errors.Is(err, domain.ErrOrganizationUnitManagerConflict) ||
		errors.Is(err, domain.ErrOrganizationNameRequired) ||
		errors.Is(err, domain.ErrOrganizationNameTooLong) ||
		errors.Is(err, domain.ErrOrganizationSlugInvalid) ||
		errors.Is(err, domain.ErrOrganizationStatusInvalid) ||
		errors.Is(err, domain.ErrOrganizationUnitNameRequired) ||
		errors.Is(err, domain.ErrOrganizationUnitNameTooLong) ||
		errors.Is(err, domain.ErrOrganizationUnitTypeTooLong) ||
		errors.Is(err, domain.ErrOrganizationUnitOrderInvalid)
}

func (s *OrganizationService) ListContacts(ctx context.Context, userID int64, filter domain.ContactFilter) (*domain.ContactList, error) {
	result, err := s.repo.ListContacts(ctx, userID, filter)
	if err == nil {
		for i := range result.Items {
			s.refreshContactAvatar(&result.Items[i])
		}
	}
	return result, err
}

func (s *OrganizationService) ContactFilters(ctx context.Context, userID int64) (*domain.ContactFilters, error) {
	return s.repo.ContactFilters(ctx, userID)
}

// ListMembersForApplication includes the current company policy only when requested.
func (s *OrganizationService) ListMembersForApplication(ctx context.Context, userID int64, app string) ([]domain.OrganizationMember, error) {
	return s.repo.ListMembersForApplication(ctx, userID, app)
}
