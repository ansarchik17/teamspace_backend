package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

type ProfileSectionHandler struct {
	service *service.ProfileSectionService
	logger  *zap.Logger
}

func NewProfileSectionHandler(service *service.ProfileSectionService, logger *zap.Logger) *ProfileSectionHandler {
	return &ProfileSectionHandler{
		service: service,
		logger:  logger.With(zap.String("handler", "profile_sections")),
	}
}

// CreateEducation godoc
// @Summary Create education
// @Description Create an education entry for the authenticated user.
// @Tags profile
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body domain.CreateEducationRequest true "Education payload"
// @Success 201 {object} domain.UserEducationResponse
// @Router /users/profile/education [post]
func (h *ProfileSectionHandler) CreateEducation(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	var req domain.CreateEducationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}
	resp, err := h.service.CreateEducation(c.Request.Context(), userID, &req)
	if err != nil {
		h.respondError(c, normalizeProfileError(err))
		return
	}
	respondJSON(c, http.StatusCreated, resp)
}

// ListMyEducations godoc
// @Summary List own education entries
// @Tags profile
// @Security BearerAuth
// @Produce json
// @Success 200 {object} domain.EducationListResponse
// @Router /users/profile/education [get]
func (h *ProfileSectionHandler) ListMyEducations(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	resp, err := h.service.ListEducations(c.Request.Context(), userID, userID)
	if err != nil {
		h.respondError(c, normalizeProfileError(err))
		return
	}
	respondJSON(c, http.StatusOK, resp)
}

// ListUserEducations godoc
// @Summary List education entries by user id
// @Tags profile
// @Security BearerAuth
// @Produce json
// @Param user_id path int true "User ID"
// @Success 200 {object} domain.EducationListResponse
// @Router /users/{user_id}/profile/education [get]
func (h *ProfileSectionHandler) ListUserEducations(c *gin.Context) {
	requesterID := middleware.MustGetUserID(c)
	targetUserID, err := parseInt64Param(c, "user_id")
	if err != nil {
		h.respondError(c, err)
		return
	}
	resp, svcErr := h.service.ListEducations(c.Request.Context(), requesterID, targetUserID)
	if svcErr != nil {
		h.respondError(c, normalizeProfileError(svcErr))
		return
	}
	respondJSON(c, http.StatusOK, resp)
}

// GetMyEducation godoc
// @Summary Get own education entry
// @Tags profile
// @Security BearerAuth
// @Produce json
// @Param education_id path int true "Education ID"
// @Success 200 {object} domain.UserEducationResponse
// @Router /users/profile/education/{education_id} [get]
func (h *ProfileSectionHandler) GetMyEducation(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	educationID, err := parseInt64Param(c, "education_id")
	if err != nil {
		h.respondError(c, err)
		return
	}
	resp, svcErr := h.service.GetEducation(c.Request.Context(), userID, userID, educationID)
	if svcErr != nil {
		h.respondError(c, normalizeProfileError(svcErr))
		return
	}
	respondJSON(c, http.StatusOK, resp)
}

// GetUserEducation godoc
// @Summary Get education entry by user id
// @Tags profile
// @Security BearerAuth
// @Produce json
// @Param user_id path int true "User ID"
// @Param education_id path int true "Education ID"
// @Success 200 {object} domain.UserEducationResponse
// @Router /users/{user_id}/profile/education/{education_id} [get]
func (h *ProfileSectionHandler) GetUserEducation(c *gin.Context) {
	requesterID := middleware.MustGetUserID(c)
	targetUserID, err := parseInt64Param(c, "user_id")
	if err != nil {
		h.respondError(c, err)
		return
	}
	educationID, err := parseInt64Param(c, "education_id")
	if err != nil {
		h.respondError(c, err)
		return
	}
	resp, svcErr := h.service.GetEducation(c.Request.Context(), requesterID, targetUserID, educationID)
	if svcErr != nil {
		h.respondError(c, normalizeProfileError(svcErr))
		return
	}
	respondJSON(c, http.StatusOK, resp)
}

// UpdateEducation godoc
// @Summary Update education entry
// @Tags profile
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param education_id path int true "Education ID"
// @Param request body domain.UpdateEducationRequest true "Education update payload"
// @Success 200 {object} domain.UserEducationResponse
// @Router /users/profile/education/{education_id} [put]
func (h *ProfileSectionHandler) UpdateEducation(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	educationID, err := parseInt64Param(c, "education_id")
	if err != nil {
		h.respondError(c, err)
		return
	}
	var req domain.UpdateEducationRequest
	if bindErr := c.ShouldBindJSON(&req); bindErr != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}
	resp, svcErr := h.service.UpdateEducation(c.Request.Context(), userID, educationID, &req)
	if svcErr != nil {
		h.respondError(c, normalizeProfileError(svcErr))
		return
	}
	respondJSON(c, http.StatusOK, resp)
}

// DeleteEducation godoc
// @Summary Delete education entry
// @Tags profile
// @Security BearerAuth
// @Produce json
// @Param education_id path int true "Education ID"
// @Success 200 {object} map[string]string
// @Router /users/profile/education/{education_id} [delete]
func (h *ProfileSectionHandler) DeleteEducation(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	educationID, err := parseInt64Param(c, "education_id")
	if err != nil {
		h.respondError(c, err)
		return
	}
	if svcErr := h.service.DeleteEducation(c.Request.Context(), userID, educationID); svcErr != nil {
		h.respondError(c, normalizeProfileError(svcErr))
		return
	}
	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "education deleted successfully")})
}

// CreateLicenseCertification godoc
// @Summary Create license or certification
// @Description Create a license/certification entry for the authenticated user. Upload the attachment first via /files/upload and pass object_name + metadata here.
// @Tags profile
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body domain.CreateLicenseCertificationRequest true "License/certification payload"
// @Success 201 {object} domain.UserLicenseCertificationResponse
// @Router /users/profile/licenses-certifications [post]
func (h *ProfileSectionHandler) CreateLicenseCertification(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	var req domain.CreateLicenseCertificationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}
	resp, err := h.service.CreateLicenseCertification(c.Request.Context(), userID, &req)
	if err != nil {
		h.respondError(c, normalizeProfileError(err))
		return
	}
	respondJSON(c, http.StatusCreated, resp)
}

// ListMyLicenseCertifications godoc
// @Summary List own licenses and certifications
// @Tags profile
// @Security BearerAuth
// @Produce json
// @Success 200 {object} domain.LicenseCertificationListResponse
// @Router /users/profile/licenses-certifications [get]
func (h *ProfileSectionHandler) ListMyLicenseCertifications(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	resp, err := h.service.ListLicenseCertifications(c.Request.Context(), userID, userID)
	if err != nil {
		h.respondError(c, normalizeProfileError(err))
		return
	}
	respondJSON(c, http.StatusOK, resp)
}

// ListUserLicenseCertifications godoc
// @Summary List licenses and certifications by user id
// @Tags profile
// @Security BearerAuth
// @Produce json
// @Param user_id path int true "User ID"
// @Success 200 {object} domain.LicenseCertificationListResponse
// @Router /users/{user_id}/profile/licenses-certifications [get]
func (h *ProfileSectionHandler) ListUserLicenseCertifications(c *gin.Context) {
	requesterID := middleware.MustGetUserID(c)
	targetUserID, err := parseInt64Param(c, "user_id")
	if err != nil {
		h.respondError(c, err)
		return
	}
	resp, svcErr := h.service.ListLicenseCertifications(c.Request.Context(), requesterID, targetUserID)
	if svcErr != nil {
		h.respondError(c, normalizeProfileError(svcErr))
		return
	}
	respondJSON(c, http.StatusOK, resp)
}

// GetMyLicenseCertification godoc
// @Summary Get own license or certification
// @Tags profile
// @Security BearerAuth
// @Produce json
// @Param license_id path int true "License/Certification ID"
// @Success 200 {object} domain.UserLicenseCertificationResponse
// @Router /users/profile/licenses-certifications/{license_id} [get]
func (h *ProfileSectionHandler) GetMyLicenseCertification(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	itemID, err := parseInt64Param(c, "license_id")
	if err != nil {
		h.respondError(c, err)
		return
	}
	resp, svcErr := h.service.GetLicenseCertification(c.Request.Context(), userID, userID, itemID)
	if svcErr != nil {
		h.respondError(c, normalizeProfileError(svcErr))
		return
	}
	respondJSON(c, http.StatusOK, resp)
}

// GetUserLicenseCertification godoc
// @Summary Get license or certification by user id
// @Tags profile
// @Security BearerAuth
// @Produce json
// @Param user_id path int true "User ID"
// @Param license_id path int true "License/Certification ID"
// @Success 200 {object} domain.UserLicenseCertificationResponse
// @Router /users/{user_id}/profile/licenses-certifications/{license_id} [get]
func (h *ProfileSectionHandler) GetUserLicenseCertification(c *gin.Context) {
	requesterID := middleware.MustGetUserID(c)
	targetUserID, err := parseInt64Param(c, "user_id")
	if err != nil {
		h.respondError(c, err)
		return
	}
	itemID, err := parseInt64Param(c, "license_id")
	if err != nil {
		h.respondError(c, err)
		return
	}
	resp, svcErr := h.service.GetLicenseCertification(c.Request.Context(), requesterID, targetUserID, itemID)
	if svcErr != nil {
		h.respondError(c, normalizeProfileError(svcErr))
		return
	}
	respondJSON(c, http.StatusOK, resp)
}

// UpdateLicenseCertification godoc
// @Summary Update license or certification
// @Tags profile
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param license_id path int true "License/Certification ID"
// @Param request body domain.UpdateLicenseCertificationRequest true "License/certification update payload"
// @Success 200 {object} domain.UserLicenseCertificationResponse
// @Router /users/profile/licenses-certifications/{license_id} [put]
func (h *ProfileSectionHandler) UpdateLicenseCertification(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	itemID, err := parseInt64Param(c, "license_id")
	if err != nil {
		h.respondError(c, err)
		return
	}
	var req domain.UpdateLicenseCertificationRequest
	if bindErr := c.ShouldBindJSON(&req); bindErr != nil {
		h.respondError(c, apperrors.Validation("invalid request body"))
		return
	}
	resp, svcErr := h.service.UpdateLicenseCertification(c.Request.Context(), userID, itemID, &req)
	if svcErr != nil {
		h.respondError(c, normalizeProfileError(svcErr))
		return
	}
	respondJSON(c, http.StatusOK, resp)
}

// DeleteLicenseCertification godoc
// @Summary Delete license or certification
// @Tags profile
// @Security BearerAuth
// @Produce json
// @Param license_id path int true "License/Certification ID"
// @Success 200 {object} map[string]string
// @Router /users/profile/licenses-certifications/{license_id} [delete]
func (h *ProfileSectionHandler) DeleteLicenseCertification(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	itemID, err := parseInt64Param(c, "license_id")
	if err != nil {
		h.respondError(c, err)
		return
	}
	if svcErr := h.service.DeleteLicenseCertification(c.Request.Context(), userID, itemID); svcErr != nil {
		h.respondError(c, normalizeProfileError(svcErr))
		return
	}
	respondJSON(c, http.StatusOK, gin.H{"message": apiMessage(c, "license certification deleted successfully")})
}

func parseInt64Param(c *gin.Context, name string) (int64, *apperrors.AppError) {
	value, err := strconv.ParseInt(c.Param(name), 10, 64)
	if err != nil || value <= 0 {
		return 0, apperrors.BadRequest("invalid " + name)
	}
	return value, nil
}

func normalizeProfileError(err error) *apperrors.AppError {
	if appErr, ok := err.(*apperrors.AppError); ok {
		return appErr
	}
	return apperrors.ToAppError(err)
}

func (h *ProfileSectionHandler) respondError(c *gin.Context, err *apperrors.AppError) {
	respondAppError(c, err)
}
