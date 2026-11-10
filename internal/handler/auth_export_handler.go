package handler

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/auth"
	"github.com/west6ide/workspace-backend/internal/middleware"
)

// ExportMinistryUsersExcel godoc
// @Summary Export ministry users to Excel
// @Description Exports active users of a ministry, including users assigned inside its structure, with optional exclusion filters by state body, department, and division IDs.
// @Tags users
// @Security BearerAuth
// @Produce application/vnd.openxmlformats-officedocument.spreadsheetml.sheet
// @Param ministry_id query int false "Ministry ID. For non-global roles, defaults to the requester's ministry."
// @Param exclude_unit_ids query string false "Comma-separated unit IDs alias applied to state bodies, departments and divisions"
// @Param exclude_state_body_ids query string false "Comma-separated state body IDs to exclude"
// @Param exclude_department_ids query string false "Comma-separated department IDs to exclude"
// @Param exclude_division_ids query string false "Comma-separated division IDs to exclude"
// @Success 200 {file} binary
// @Failure 400 {object} map[string]interface{}
// @Failure 403 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /users/export/ministry [get]
func (h *AuthHandler) ExportMinistryUsersExcel(c *gin.Context) {
	requesterID := middleware.MustGetUserID(c)

	var ministryID int64
	if raw := strings.TrimSpace(c.Query("ministry_id")); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed <= 0 {
			h.respondError(c, apperrors.Validation("invalid ministry_id"))
			return
		}
		ministryID = parsed
	}

	excludeUnitIDs, err := parseInt64ListQuery(c.Query("exclude_unit_ids"))
	if err != nil {
		h.respondError(c, apperrors.Validation("invalid exclude_unit_ids"))
		return
	}
	excludeStateBodyIDs, err := parseInt64ListQuery(c.Query("exclude_state_body_ids"))
	if err != nil {
		h.respondError(c, apperrors.Validation("invalid exclude_state_body_ids"))
		return
	}
	excludeDepartmentIDs, err := parseInt64ListQuery(c.Query("exclude_department_ids"))
	if err != nil {
		h.respondError(c, apperrors.Validation("invalid exclude_department_ids"))
		return
	}
	excludeDivisionIDs, err := parseInt64ListQuery(c.Query("exclude_division_ids"))
	if err != nil {
		h.respondError(c, apperrors.Validation("invalid exclude_division_ids"))
		return
	}

	excludeStateBodyIDs = append(excludeStateBodyIDs, excludeUnitIDs...)
	excludeDepartmentIDs = append(excludeDepartmentIDs, excludeUnitIDs...)
	excludeDivisionIDs = append(excludeDivisionIDs, excludeUnitIDs...)

	reportBytes, fileName, err := h.authService.ExportMinistryUsersExcel(c.Request.Context(), requesterID, auth.MinistryUsersExportParams{
		MinistryID:           ministryID,
		ExcludeStateBodyIDs:  uniqueInt64s(excludeStateBodyIDs),
		ExcludeDepartmentIDs: uniqueInt64s(excludeDepartmentIDs),
		ExcludeDivisionIDs:   uniqueInt64s(excludeDivisionIDs),
	})
	if err != nil {
		h.respondError(c, apperrors.ToAppError(err))
		return
	}

	c.Header("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", fileName))
	c.Header("Content-Length", fmt.Sprintf("%d", len(reportBytes)))
	c.Data(http.StatusOK, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", reportBytes)
}

func parseInt64ListQuery(raw string) ([]int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}

	parts := strings.Split(raw, ",")
	result := make([]int64, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		value, err := strconv.ParseInt(part, 10, 64)
		if err != nil || value <= 0 {
			return nil, fmt.Errorf("invalid int64 list")
		}
		result = append(result, value)
	}

	return result, nil
}

func uniqueInt64s(values []int64) []int64 {
	if len(values) == 0 {
		return nil
	}

	seen := make(map[int64]struct{}, len(values))
	result := make([]int64, 0, len(values))
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}

	return result
}
