package handler

import (
	"encoding/json"
	"github.com/west6ide/workspace-backend/internal/service"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/middleware"
)

// ListContacts lists active employees of the caller's organization.
// @Summary Search employee directory
// @Tags contacts
// @Security BearerAuth
// @Produce json
// @Param q query string false "Literal case-insensitive substring of name, email or username (max 200 characters)"
// @Param unit_id query string false "Exact organization unit UUID (without descendants)"
// @Param position query string false "Exact case-insensitive job title (max 255 characters)"
// @Param limit query int false "Page size, 1 to 100; default 50"
// @Param offset query int false "Non-negative page offset"
// @Success 200 {object} domain.ContactList
// @Failure 400 {object} apperrors.AppError
// @Failure 404 {object} apperrors.AppError
// @Router /contacts [get]
func (h *OrganizationHandler) ListContacts(c *gin.Context) {
	f, err := parseContactFilter(c)
	if err != nil {
		h.respondError(c, apperrors.Validation(err.Error()))
		return
	}
	result, err := h.service.ListContacts(c.Request.Context(), middleware.MustGetUserID(c), f)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, result)
}

func parseContactFilter(c *gin.Context) (domain.ContactFilter, error) {
	f := domain.ContactFilter{Query: strings.TrimSpace(c.Query("q")), Position: strings.TrimSpace(c.Query("position")), Limit: 50}
	invalid := func(message string) (domain.ContactFilter, error) { return f, apperrors.Validation(message) }
	if len([]rune(f.Query)) > 200 || len([]rune(f.Position)) > 255 {
		return invalid("contact search is too long")
	}
	if raw, exists := c.GetQuery("unit_id"); exists {
		id, err := uuid.Parse(raw)
		if err != nil || id == uuid.Nil {
			return invalid("invalid unit_id")
		}
		f.UnitID = &id
	}
	if raw, exists := c.GetQuery("limit"); exists {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 100 {
			return invalid("limit must be between 1 and 100")
		}
		f.Limit = n
	}
	if raw, exists := c.GetQuery("offset"); exists {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			return invalid("offset must be non-negative")
		}
		f.Offset = n
	}
	return f, nil
}

// ContactFilters returns company units and distinct active employee job titles.
// @Summary Get employee directory filters
// @Tags contacts
// @Security BearerAuth
// @Produce json
// @Success 200 {object} domain.ContactFilters
// @Failure 404 {object} apperrors.AppError
// @Router /contacts/filters [get]
func (h *OrganizationHandler) ContactFilters(c *gin.Context) {
	result, err := h.service.ContactFilters(c.Request.Context(), middleware.MustGetUserID(c))
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, result)
}

func contactTarget(c *gin.Context) (int64, error) {
	id, err := strconv.ParseInt(c.Param("user_id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, apperrors.Validation("invalid user_id")
	}
	return id, nil
}

// GetContactProfile returns a company employee card and management chain.
// @Summary Get employee profile
// @Tags contacts
// @Security BearerAuth
// @Produce json
// @Param user_id path int true "Employee ID"
// @Success 200 {object} domain.ContactProfile
// @Failure 400 {object} apperrors.ErrorResponse
// @Failure 404 {object} apperrors.ErrorResponse
// @Router /contacts/{user_id} [get]
func (h *OrganizationHandler) GetContactProfile(c *gin.Context) {
	id, err := contactTarget(c)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	result, err := h.service.GetContactProfile(c.Request.Context(), middleware.MustGetUserID(c), id)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, result)
}

// UpdateContact updates the caller's work and personal contacts and visibility (empty string clears a field).
// @Summary Update own employee contacts
// @Tags contacts
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param user_id path int true "Own employee ID"
// @Param request body domain.UpdateContactRequest true "Contact details"
// @Success 204
// @Failure 400 {object} apperrors.ErrorResponse
// @Failure 403 {object} apperrors.ErrorResponse
// @Failure 404 {object} apperrors.ErrorResponse
// @Router /contacts/{user_id} [patch]
func (h *OrganizationHandler) UpdateContact(c *gin.Context) {
	id, err := contactTarget(c)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	var req domain.UpdateContactRequest
	decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 4096))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid contact request"))
		return
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		h.respondError(c, apperrors.Validation("invalid contact request"))
		return
	}
	if err = h.service.UpdateContact(c.Request.Context(), middleware.MustGetUserID(c), id, req); err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	c.Status(http.StatusNoContent)
}

// UploadContactAvatar validates image bytes and updates the caller's photo.
// @Summary Upload own employee avatar
// @Tags contacts
// @Security BearerAuth
// @Accept multipart/form-data
// @Produce json
// @Param user_id path int true "Own employee ID"
// @Param file formData file true "JPEG/PNG, at most 5 MiB and 2048x2048"
// @Success 200 {object} domain.ContactProfile
// @Failure 400 {object} apperrors.ErrorResponse
// @Failure 403 {object} apperrors.ErrorResponse
// @Router /contacts/{user_id}/avatar [post]
func (h *OrganizationHandler) UploadContactAvatar(c *gin.Context) {
	id, err := contactTarget(c)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	if id != middleware.MustGetUserID(c) {
		h.respondError(c, apperrors.Forbidden("only your own avatar can be updated"))
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, service.MaxContactAvatarBytes+(1<<20))
	if err = c.Request.ParseMultipartForm(service.MaxContactAvatarBytes); err != nil {
		h.respondError(c, apperrors.Validation("invalid or oversized avatar upload"))
		return
	}
	if c.Request.MultipartForm != nil {
		defer c.Request.MultipartForm.RemoveAll()
	}
	file, _, err := c.Request.FormFile("file")
	if err != nil {
		h.respondError(c, apperrors.Validation("file is required"))
		return
	}
	defer file.Close()
	result, err := h.service.UploadContactAvatar(c.Request.Context(), middleware.MustGetUserID(c), id, file)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, result)
}

// DirectReports returns a paginated list of direct subordinates.
// @Summary List employee direct reports
// @Tags contacts
// @Security BearerAuth
// @Produce json
// @Param user_id path int true "Manager employee ID"
// @Param limit query int false "Page size 1 to 100, default 50"
// @Param offset query int false "Non-negative offset"
// @Success 200 {object} domain.ContactList
// @Router /contacts/{user_id}/reports [get]
func (h *OrganizationHandler) DirectReports(c *gin.Context) {
	id, err := contactTarget(c)
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}
	f, err := parseContactFilter(c)
	if err != nil {
		h.respondError(c, apperrors.Validation(err.Error()))
		return
	}
	result, err := h.service.DirectReports(c.Request.Context(), middleware.MustGetUserID(c), id, f)
	if err != nil {
		h.respondError(c, organizationAppError(err))
		return
	}
	respondJSON(c, http.StatusOK, result)
}
