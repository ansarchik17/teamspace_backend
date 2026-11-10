package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

type OrganizationHandler struct {
	service       *service.OrganizationService
	logger        *zap.Logger
	growySessions growySessionCache
}

func NewOrganizationHandler(svc *service.OrganizationService, logger *zap.Logger) *OrganizationHandler {
	return &OrganizationHandler{service: svc, logger: logger.With(zap.String("handler", "organization"))}
}

// CreateOrganization creates a tenant and assigns the creating global administrator to it.
// @Summary Create a customer organization
// @Tags organizations
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body domain.CreateOrganizationRequest true "Organization"
// @Success 201 {object} domain.Organization
// @Router /organizations [post]
func (h *OrganizationHandler) CreateOrganization(c *gin.Context) {
	var req domain.CreateOrganizationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid organization request"))
		return
	}
	item, err := h.service.CreateOrganization(c.Request.Context(), middleware.MustGetUserID(c), req)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	respondJSON(c, http.StatusCreated, item)
}

// ListPlatformOrganizations lists all customer tenants for a platform
// administrator. The authorization check is performed in the repository and
// does not require the administrator to belong to any tenant.
// @Summary List all customer organizations
// @Tags platform
// @Security BearerAuth
// @Produce json
// @Param q query string false "Search by name, slug or owner email"
// @Param status query string false "active or suspended"
// @Param limit query int false "Page size"
// @Param offset query int false "Page offset"
// @Success 200 {object} domain.PlatformOrganizationList
// @Router /platform/organizations [get]
func (h *OrganizationHandler) ListPlatformOrganizations(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "25"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	items, err := h.service.ListPlatformOrganizations(c.Request.Context(), middleware.MustGetUserID(c), domain.PlatformOrganizationFilter{
		Query:  c.Query("q"),
		Status: c.Query("status"),
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, items)
}

// CreatePlatformOrganization creates a tenant without assigning the
// platform administrator to it.
// @Summary Create a customer organization from the platform console
// @Tags platform
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body domain.CreateOrganizationRequest true "Organization"
// @Success 201 {object} domain.PlatformOrganization
// @Router /platform/organizations [post]
func (h *OrganizationHandler) CreatePlatformOrganization(c *gin.Context) {
	var req domain.CreateOrganizationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid organization request"))
		return
	}
	item, err := h.service.CreatePlatformOrganization(c.Request.Context(), middleware.MustGetUserID(c), req)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	respondJSON(c, http.StatusCreated, item)
}

// ListPlatformOrganizationOwnerCandidates lists active users that can be
// assigned as the owner of a customer organization.
// @Summary List organization owner candidates
// @Tags platform
// @Security BearerAuth
// @Produce json
// @Param q query string false "Search by name, username or email"
// @Param limit query int false "Maximum number of candidates"
// @Param organization_id query string false "Include users already belonging to this organization"
// @Success 200 {array} domain.OrganizationOwnerCandidate
// @Router /platform/organizations/owner-candidates [get]
func (h *OrganizationHandler) ListPlatformOrganizationOwnerCandidates(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	var organizationID *uuid.UUID
	if rawOrganizationID := strings.TrimSpace(c.Query("organization_id")); rawOrganizationID != "" {
		parsedOrganizationID, err := uuid.Parse(rawOrganizationID)
		if err != nil {
			h.respondError(c, apperrors.Validation("invalid organization id"))
			return
		}
		organizationID = &parsedOrganizationID
	}
	items, err := h.service.ListPlatformOrganizationOwnerCandidates(c.Request.Context(), middleware.MustGetUserID(c), organizationID, c.Query("q"), limit)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, items)
}

// AssignPlatformOrganizationOwner assigns an existing Workspace user to a
// customer organization and makes that user its owner atomically.
// @Summary Assign customer organization owner
// @Tags platform
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param organization_id path string true "Organization ID"
// @Param request body domain.AssignOrganizationOwnerRequest true "Owner assignment"
// @Success 200 {object} domain.PlatformOrganization
// @Router /platform/organizations/{organization_id}/owner [put]
func (h *OrganizationHandler) AssignPlatformOrganizationOwner(c *gin.Context) {
	organizationID, err := uuid.Parse(c.Param("organization_id"))
	if err != nil {
		h.respondError(c, apperrors.Validation("invalid organization id"))
		return
	}
	var req domain.AssignOrganizationOwnerRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.UserID <= 0 {
		h.respondError(c, apperrors.Validation("invalid organization owner request"))
		return
	}
	item, err := h.service.AssignPlatformOrganizationOwner(c.Request.Context(), middleware.MustGetUserID(c), organizationID, req.UserID)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, item)
}

// GetPlatformOrganization returns tenant metadata for a platform administrator.
// @Summary Get customer organization
// @Tags platform
// @Security BearerAuth
// @Produce json
// @Param organization_id path string true "Organization ID"
// @Success 200 {object} domain.PlatformOrganization
// @Router /platform/organizations/{organization_id} [get]
func (h *OrganizationHandler) GetPlatformOrganization(c *gin.Context) {
	organizationID, err := uuid.Parse(c.Param("organization_id"))
	if err != nil {
		h.respondError(c, apperrors.Validation("invalid organization id"))
		return
	}
	item, err := h.service.GetPlatformOrganization(c.Request.Context(), middleware.MustGetUserID(c), organizationID)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, item)
}

// UpdatePlatformOrganization changes tenant metadata or lifecycle status.
// @Summary Update customer organization
// @Tags platform
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param organization_id path string true "Organization ID"
// @Param request body domain.UpdatePlatformOrganizationRequest true "Organization update"
// @Success 200 {object} domain.PlatformOrganization
// @Router /platform/organizations/{organization_id} [patch]
func (h *OrganizationHandler) UpdatePlatformOrganization(c *gin.Context) {
	organizationID, err := uuid.Parse(c.Param("organization_id"))
	if err != nil {
		h.respondError(c, apperrors.Validation("invalid organization id"))
		return
	}
	var req domain.UpdatePlatformOrganizationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid organization update request"))
		return
	}
	item, err := h.service.UpdatePlatformOrganization(c.Request.Context(), middleware.MustGetUserID(c), organizationID, req)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, item)
}

// ListPlatformOrganizationUnits lists the structure of a selected tenant.
func (h *OrganizationHandler) ListPlatformOrganizationUnits(c *gin.Context) {
	organizationID, err := uuid.Parse(c.Param("organization_id"))
	if err != nil {
		h.respondError(c, apperrors.Validation("invalid organization id"))
		return
	}
	items, err := h.service.ListPlatformUnits(c.Request.Context(), middleware.MustGetUserID(c), organizationID)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, items)
}

// ListPlatformOrganizationMembers lists users belonging to a selected tenant.
func (h *OrganizationHandler) ListPlatformOrganizationMembers(c *gin.Context) {
	organizationID, err := uuid.Parse(c.Param("organization_id"))
	if err != nil {
		h.respondError(c, apperrors.Validation("invalid organization id"))
		return
	}
	items, err := h.service.ListPlatformMembers(c.Request.Context(), middleware.MustGetUserID(c), organizationID)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, items)
}

func (h *OrganizationHandler) ListGrowyPositionRoles(c *gin.Context) {
	items, err := h.service.ListGrowyPositionRoles(c.Request.Context(), middleware.MustGetUserID(c))
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, items)
}

func (h *OrganizationHandler) SaveGrowyPositionRoles(c *gin.Context) {
	var req domain.UpdateGrowyPositionRolesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid Growy role mappings request"))
		return
	}
	if err := h.service.SaveGrowyPositionRoles(c.Request.Context(), middleware.MustGetUserID(c), req); err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	items, err := h.service.ListGrowyPositionRoles(c.Request.Context(), middleware.MustGetUserID(c))
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, items)
}

func (h *OrganizationHandler) ListPlatformGrowyPositionRoles(c *gin.Context) {
	organizationID, err := uuid.Parse(c.Param("organization_id"))
	if err != nil {
		h.respondError(c, apperrors.Validation("invalid organization id"))
		return
	}
	items, err := h.service.ListPlatformGrowyPositionRoles(c.Request.Context(), middleware.MustGetUserID(c), organizationID)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, items)
}

func (h *OrganizationHandler) SavePlatformGrowyPositionRoles(c *gin.Context) {
	organizationID, err := uuid.Parse(c.Param("organization_id"))
	if err != nil {
		h.respondError(c, apperrors.Validation("invalid organization id"))
		return
	}
	var req domain.UpdateGrowyPositionRolesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid Growy role mappings request"))
		return
	}
	if err := h.service.SavePlatformGrowyPositionRoles(c.Request.Context(), middleware.MustGetUserID(c), organizationID, req); err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	items, err := h.service.ListPlatformGrowyPositionRoles(c.Request.Context(), middleware.MustGetUserID(c), organizationID)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, items)
}

// CreatePlatformOrganizationUnit creates a unit in a selected tenant.
func (h *OrganizationHandler) CreatePlatformOrganizationUnit(c *gin.Context) {
	organizationID, err := uuid.Parse(c.Param("organization_id"))
	if err != nil {
		h.respondError(c, apperrors.Validation("invalid organization id"))
		return
	}
	var req domain.CreateOrganizationUnitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid organization unit request"))
		return
	}
	item, err := h.service.CreatePlatformUnit(c.Request.Context(), middleware.MustGetUserID(c), organizationID, req)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	respondJSON(c, http.StatusCreated, item)
}

// UpdatePlatformOrganizationUnit updates a unit in a selected tenant.
func (h *OrganizationHandler) UpdatePlatformOrganizationUnit(c *gin.Context) {
	organizationID, err := uuid.Parse(c.Param("organization_id"))
	if err != nil {
		h.respondError(c, apperrors.Validation("invalid organization id"))
		return
	}
	unitID, err := uuid.Parse(c.Param("unit_id"))
	if err != nil {
		h.respondError(c, apperrors.Validation("invalid unit id"))
		return
	}
	var req domain.UpdateOrganizationUnitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid organization unit request"))
		return
	}
	item, err := h.service.UpdatePlatformUnit(c.Request.Context(), middleware.MustGetUserID(c), organizationID, unitID, req)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, item)
}

// ArchivePlatformOrganizationUnit archives a unit in a selected tenant.
func (h *OrganizationHandler) ArchivePlatformOrganizationUnit(c *gin.Context) {
	organizationID, err := uuid.Parse(c.Param("organization_id"))
	if err != nil {
		h.respondError(c, apperrors.Validation("invalid organization id"))
		return
	}
	unitID, err := uuid.Parse(c.Param("unit_id"))
	if err != nil {
		h.respondError(c, apperrors.Validation("invalid unit id"))
		return
	}
	if err := h.service.ArchivePlatformUnit(c.Request.Context(), middleware.MustGetUserID(c), organizationID, unitID); err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	c.Status(http.StatusNoContent)
}

// AssignPlatformOrganizationUser assigns a tenant member to a unit.
func (h *OrganizationHandler) AssignPlatformOrganizationUser(c *gin.Context) {
	organizationID, err := uuid.Parse(c.Param("organization_id"))
	if err != nil {
		h.respondError(c, apperrors.Validation("invalid organization id"))
		return
	}
	targetUserID, err := strconv.ParseInt(c.Param("user_id"), 10, 64)
	if err != nil || targetUserID <= 0 {
		h.respondError(c, apperrors.Validation("invalid user id"))
		return
	}
	var req domain.AssignUserToUnitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid assignment request"))
		return
	}
	if err := h.service.AssignPlatformUser(c.Request.Context(), middleware.MustGetUserID(c), organizationID, targetUserID, req); err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	c.Status(http.StatusNoContent)
}

// GetCurrentOrganization returns the tenant of the authenticated user.
// @Summary Get current organization
// @Tags organizations
// @Security BearerAuth
// @Produce json
// @Success 200 {object} domain.Organization
// @Router /organizations/current [get]
func (h *OrganizationHandler) GetCurrentOrganization(c *gin.Context) {
	item, err := h.service.CurrentOrganization(c.Request.Context(), middleware.MustGetUserID(c))
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, item)
}

// ListUnits returns the configurable organization tree as a flat list.
// @Summary List organization units
// @Tags organizations
// @Security BearerAuth
// @Produce json
// @Success 200 {array} domain.OrganizationUnit
// @Router /organizations/current/units [get]
func (h *OrganizationHandler) ListUnits(c *gin.Context) {
	items, err := h.service.ListUnits(c.Request.Context(), middleware.MustGetUserID(c))
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, items)
}

// ListMembers returns employees available for manager assignment in the current company.
// @Summary List organization members
// @Tags organizations
// @Security BearerAuth
// @Produce json
// @Param application query string false "Include current company position policy for this application"
// @Success 200 {array} domain.OrganizationMember
// @Router /organizations/current/users [get]
func (h *OrganizationHandler) ListMembers(c *gin.Context) {
	items, err := h.service.ListMembersForApplication(c.Request.Context(), middleware.MustGetUserID(c), c.Query("application"))
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, items)
}

// CreateUnit adds a unit at any depth in the customer-defined tree.
// @Summary Create organization unit
// @Tags organizations
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body domain.CreateOrganizationUnitRequest true "Organization unit"
// @Success 201 {object} domain.OrganizationUnit
// @Router /organizations/current/units [post]
func (h *OrganizationHandler) CreateUnit(c *gin.Context) {
	var req domain.CreateOrganizationUnitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid organization unit request"))
		return
	}
	item, err := h.service.CreateUnit(c.Request.Context(), middleware.MustGetUserID(c), req)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	respondJSON(c, http.StatusCreated, item)
}

// UpdateUnit changes a unit while preserving organization boundaries.
// @Summary Update organization unit
// @Tags organizations
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param unit_id path string true "Unit ID"
// @Param request body domain.UpdateOrganizationUnitRequest true "Organization unit"
// @Success 200 {object} domain.OrganizationUnit
// @Router /organizations/current/units/{unit_id} [patch]
func (h *OrganizationHandler) UpdateUnit(c *gin.Context) {
	unitID, err := uuid.Parse(c.Param("unit_id"))
	if err != nil {
		h.respondError(c, apperrors.Validation("invalid unit id"))
		return
	}
	var req domain.UpdateOrganizationUnitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid organization unit request"))
		return
	}
	item, err := h.service.UpdateUnit(c.Request.Context(), middleware.MustGetUserID(c), unitID, req)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, item)
}

// ArchiveUnit safely removes an empty unit from the active tree.
// @Summary Archive organization unit
// @Tags organizations
// @Security BearerAuth
// @Param unit_id path string true "Unit ID"
// @Success 204
// @Router /organizations/current/units/{unit_id} [delete]
func (h *OrganizationHandler) ArchiveUnit(c *gin.Context) {
	unitID, err := uuid.Parse(c.Param("unit_id"))
	if err != nil {
		h.respondError(c, apperrors.Validation("invalid unit id"))
		return
	}
	if err := h.service.ArchiveUnit(c.Request.Context(), middleware.MustGetUserID(c), unitID); err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	c.Status(http.StatusNoContent)
}

// AssignUser places an existing employee into a unit of the current company.
// @Summary Assign user to organization unit
// @Tags organizations
// @Security BearerAuth
// @Accept json
// @Param user_id path int true "User ID"
// @Param request body domain.AssignUserToUnitRequest true "Assignment"
// @Success 204
// @Router /organizations/current/users/{user_id}/assignment [put]
func (h *OrganizationHandler) AssignUser(c *gin.Context) {
	targetUserID, err := strconv.ParseInt(c.Param("user_id"), 10, 64)
	if err != nil || targetUserID <= 0 {
		h.respondError(c, apperrors.Validation("invalid user id"))
		return
	}
	var req domain.AssignUserToUnitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid assignment request"))
		return
	}
	if err := h.service.AssignUser(c.Request.Context(), middleware.MustGetUserID(c), targetUserID, req); err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	c.Status(http.StatusNoContent)
}

func organizationAppError(err error) *apperrors.AppError {
	var app *apperrors.AppError
	if errors.As(err, &app) {
		return app
	}
	if service.IsOrganizationValidationError(err) {
		return apperrors.Validation(err.Error())
	}
	if errors.Is(err, domain.ErrOrganizationNotFound) {
		return apperrors.NotFound("organization")
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "organization owner user not found"):
		return apperrors.NotFound("organization owner user")
	case strings.Contains(message, "organization owner user id is invalid"), strings.Contains(message, "invalid organization owner request"):
		return apperrors.Validation(err.Error())
	case strings.Contains(message, "organization owner user must be active"), strings.Contains(message, "already assigned"):
		return apperrors.Conflict(err.Error())
	case strings.Contains(message, "identity provider type is invalid"),
		strings.Contains(message, "identity provider display name is required"),
		strings.Contains(message, "identity provider settings are invalid"),
		strings.Contains(message, "identity provider client secret is required"),
		strings.Contains(message, "invalid identity provider"),
		strings.Contains(message, "job title"),
		strings.Contains(message, "growy role"):
		return apperrors.Validation(err.Error())
	case strings.Contains(message, "identity provider not found"):
		return apperrors.NotFound("identity provider")
	case strings.Contains(message, "keycloak identity provider"), strings.Contains(message, "keycloak organization mapper"):
		return apperrors.Conflict("Не удалось применить настройки корпоративного входа. Проверьте параметры провайдера и попробуйте снова.")
	case strings.Contains(message, "only a global administrator"), strings.Contains(message, "outside the current organization"), strings.Contains(message, "only the organization owner"), strings.Contains(message, "platform administrator cannot be a customer organization owner"), strings.Contains(message, "administrator"):
		return apperrors.Forbidden(err.Error())
	case strings.Contains(message, "platform organization endpoint"), strings.Contains(message, "must be active"):
		return apperrors.Conflict(err.Error())
	case strings.Contains(message, "user id is invalid"), strings.Contains(message, "invalid"):
		return apperrors.Validation(err.Error())
	case strings.Contains(message, "must be empty"), strings.Contains(message, "cannot be moved"), strings.Contains(message, "cannot be its own"):
		return apperrors.Conflict(err.Error())
	case strings.Contains(message, "duplicate key"), strings.Contains(message, "unique constraint"):
		return apperrors.Conflict("organization slug is already in use")
	case strings.Contains(message, "not found"):
		return apperrors.NotFound("organization resource")
	default:
		return apperrors.ToAppError(err)
	}
}

func (h *OrganizationHandler) respondError(c *gin.Context, err *apperrors.AppError) {
	respondAppError(c, err)
}
