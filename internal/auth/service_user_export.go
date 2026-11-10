package auth

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/xuri/excelize/v2"
)

type MinistryUsersExportParams struct {
	MinistryID           int64
	ExcludeStateBodyIDs  []int64
	ExcludeDepartmentIDs []int64
	ExcludeDivisionIDs   []int64
}

func (s *Service) ExportMinistryUsersExcel(ctx context.Context, requesterID int64, params MinistryUsersExportParams) ([]byte, string, error) {
	requester, err := s.userRepo.GetByID(ctx, requesterID)
	if err != nil || requester == nil {
		return nil, "", apperrors.NotFound("Requester not found")
	}

	ministryID := params.MinistryID
	if ministryID == 0 {
		if requester.MinistryID == nil {
			return nil, "", apperrors.Validation("ministry_id is required")
		}
		ministryID = *requester.MinistryID
	}

	if !domain.HasGlobalUserScope(requester) {
		if requester.MinistryID == nil {
			return nil, "", apperrors.Forbidden("Requester is not assigned to a ministry")
		}
		if *requester.MinistryID != ministryID {
			return nil, "", apperrors.Forbidden("Access denied for the requested ministry")
		}
	}

	rows, err := s.userRepo.ExportMinistryUsers(
		ctx,
		ministryID,
		params.ExcludeStateBodyIDs,
		params.ExcludeDepartmentIDs,
		params.ExcludeDivisionIDs,
	)
	if err != nil {
		return nil, "", apperrors.InternalWithErr("Failed to export ministry users", err)
	}

	ministryName, err := s.userRepo.GetMinistryName(ctx, ministryID)
	if err != nil {
		return nil, "", apperrors.InternalWithErr("Failed to resolve ministry name", err)
	}

	file := excelize.NewFile()
	const sheetName = "Users"
	defaultSheet := file.GetSheetName(file.GetActiveSheetIndex())
	if defaultSheet != sheetName {
		if err := file.SetSheetName(defaultSheet, sheetName); err != nil {
			return nil, "", apperrors.InternalWithErr("failed to set ministry users export sheet name", err)
		}
	}

	headers := []string{
		"fio",
		"last_login_utc_plus_5",
	}
	for i, header := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		if err := file.SetCellValue(sheetName, cell, header); err != nil {
			return nil, "", apperrors.InternalWithErr("Failed to write export header", err)
		}
	}

	headerStyle, err := file.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true},
	})
	if err == nil {
		_ = file.SetCellStyle(sheetName, "A1", "B1", headerStyle)
	}

	for i, row := range rows {
		rowIndex := i + 2
		lastLogin := ""
		if row.LastLoginAt != nil {
			lastLogin = row.LastLoginAt.In(time.FixedZone("UTC+5", 5*60*60)).Format("2006-01-02 15:04:05")
		}
		values := []interface{}{
			row.FIO,
			lastLogin,
		}
		cell, _ := excelize.CoordinatesToCellName(1, rowIndex)
		if err := file.SetSheetRow(sheetName, cell, &values); err != nil {
			return nil, "", apperrors.InternalWithErr("Failed to write export row", err)
		}
	}

	_ = file.SetPanes(sheetName, &excelize.Panes{
		Freeze:      true,
		Split:       false,
		XSplit:      0,
		YSplit:      1,
		TopLeftCell: "A2",
		ActivePane:  "bottomLeft",
	})
	_ = file.SetColWidth(sheetName, "A", "A", 32)
	_ = file.SetColWidth(sheetName, "B", "B", 22)

	buf := bytes.NewBuffer(nil)
	if err := file.Write(buf); err != nil {
		return nil, "", apperrors.InternalWithErr("Failed to finalize export file", err)
	}

	fileName := fmt.Sprintf("%s-users.xlsx", sanitizeExportFileName(ministryName))
	return buf.Bytes(), fileName, nil
}

func sanitizeExportFileName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "ministry"
	}

	replacer := strings.NewReplacer(
		"/", "-",
		"\\", "-",
		":", "-",
		"*", "",
		"?", "",
		"\"", "",
		"<", "",
		">", "",
		"|", "",
	)
	value = replacer.Replace(value)
	value = strings.TrimSpace(value)
	if value == "" {
		return "ministry"
	}
	return value
}
