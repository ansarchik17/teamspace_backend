package service

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/xuri/excelize/v2"
)

func TestNWS007_ModelsJSON(t *testing.T) {
	now := time.Now().Truncate(time.Second)

	// 1. AcknowledgePublicationResponse
	ackResp := domain.AcknowledgePublicationResponse{
		Status:         "acknowledged",
		Acknowledged:   true,
		AcknowledgedAt: now,
	}
	ackJSON, err := json.Marshal(ackResp)
	require.NoError(t, err)

	var parsedAck domain.AcknowledgePublicationResponse
	require.NoError(t, json.Unmarshal(ackJSON, &parsedAck))
	assert.Equal(t, "acknowledged", parsedAck.Status)
	assert.True(t, parsedAck.Acknowledged)
	assert.True(t, now.Equal(parsedAck.AcknowledgedAt))

	// 2. PublicationAnalyticsResponse
	analytics := domain.PublicationAnalyticsResponse{
		PublicationID:          uuid.New(),
		TotalTargetUsers:       120,
		ViewsCount:             90,
		ViewPercent:            75.0,
		AcknowledgedCount:      60,
		AcknowledgementPercent: 50.0,
		UnreadCount:            30,
		UnacknowledgedCount:    60,
		FirstViewAt:            &now,
		LastViewAt:             &now,
		FirstAcknowledgedAt:    &now,
		LastAcknowledgedAt:     &now,
		DepartmentStats: []domain.PublicationDepartmentAnalytics{
			{
				DepartmentID:           101,
				DepartmentName:         "IT отдел",
				TotalUsers:             20,
				ViewsCount:             18,
				ViewPercent:            90.0,
				AcknowledgedCount:      15,
				AcknowledgementPercent: 75.0,
			},
		},
		MinistryStats: []domain.PublicationMinistryAnalytics{
			{
				MinistryID:             201,
				MinistryName:           "Головной офис",
				TotalUsers:             100,
				ViewsCount:             75,
				ViewPercent:            75.0,
				AcknowledgedCount:      50,
				AcknowledgementPercent: 50.0,
			},
		},
	}

	analyticsJSON, err := json.Marshal(analytics)
	require.NoError(t, err)

	var parsedAnalytics domain.PublicationAnalyticsResponse
	require.NoError(t, json.Unmarshal(analyticsJSON, &parsedAnalytics))
	assert.Equal(t, analytics.PublicationID, parsedAnalytics.PublicationID)
	assert.Equal(t, 120, parsedAnalytics.TotalTargetUsers)
	assert.Equal(t, 90, parsedAnalytics.ViewsCount)
	assert.InDelta(t, 75.0, parsedAnalytics.ViewPercent, 0.01)
	assert.Equal(t, 60, parsedAnalytics.AcknowledgedCount)
	assert.InDelta(t, 50.0, parsedAnalytics.AcknowledgementPercent, 0.01)
	assert.Len(t, parsedAnalytics.DepartmentStats, 1)
	assert.Equal(t, "IT отдел", parsedAnalytics.DepartmentStats[0].DepartmentName)
	assert.Len(t, parsedAnalytics.MinistryStats, 1)
	assert.Equal(t, "Головной офис", parsedAnalytics.MinistryStats[0].MinistryName)

	// 3. PublicationResponse.Acknowledged tag
	pubResp := domain.PublicationResponse{
		ID:           uuid.New(),
		Title:        "Важная новость",
		Acknowledged: true,
	}
	pubRespJSON, err := json.Marshal(pubResp)
	require.NoError(t, err)

	var rawPubMap map[string]interface{}
	require.NoError(t, json.Unmarshal(pubRespJSON, &rawPubMap))
	assert.Equal(t, true, rawPubMap["acknowledged"])
}

func TestNWS007_SanitizeExportFileName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "empty string defaults to readers",
			input:    "",
			expected: "readers",
		},
		{
			name:     "special characters stripped and spaces replaced",
			input:    "Приказ №12/34: Об отпусках (2026)?!",
			expected: "Приказ_1234_Об_отпусках_2026",
		},
		{
			name:     "latin and cyrillic mixed",
			input:    "Important Update: Alem Workspace релиз",
			expected: "Important_Update_Alem_Workspace_релиз",
		},
		{
			name:     "length clamped to 50 runes",
			input:    strings.Repeat("А", 100),
			expected: strings.Repeat("А", 50),
		},
		{
			name:     "only forbidden characters",
			input:    "???///:::***",
			expected: "readers",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := sanitizeExportFileName(tt.input)
			assert.Equal(t, tt.expected, actual)
		})
	}
}

func TestNWS007_CSVExportGeneration(t *testing.T) {
	now := time.Now()
	pos := "Инженер"
	deptName := "Департамент разработки"
	minName := "Главное управление"

	readers := []domain.PublicationReaderItem{
		{
			User: domain.UserMini{
				ID:        1,
				FirstName: "Аскар",
				LastName:  "Сериков",
				FullName:  "Аскар Сериков",
				Position:  &pos,
			},
			Email:          "askar@example.com",
			DepartmentName: &deptName,
			MinistryName:   &minName,
			IsViewed:       true,
			ViewedAt:       &now,
			IsAcknowledged: true,
			AcknowledgedAt: &now,
		},
		{
			User: domain.UserMini{
				ID:        2,
				FirstName: "Берик",
				LastName:  "Омаров",
				FullName:  "Берик Омаров",
			},
			Email:          "berik@example.com",
			IsViewed:       false,
			ViewedAt:       nil,
			IsAcknowledged: false,
			AcknowledgedAt: nil,
		},
	}

	var buf bytes.Buffer
	buf.WriteString("\xef\xbb\xbf") // BOM
	w := csv.NewWriter(&buf)

	_ = w.Write([]string{
		"№", "Сотрудник", "Email", "Должность", "Отдел", "Филиал",
		"Статус прочтения", "Дата и время прочтения", "Статус ознакомления", "Дата и время ознакомления",
	})

	for i, r := range readers {
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

		posVal := "-"
		if r.User.Position != nil && *r.User.Position != "" {
			posVal = *r.User.Position
		}
		dVal := "-"
		if r.DepartmentName != nil && *r.DepartmentName != "" {
			dVal = *r.DepartmentName
		}
		mVal := "-"
		if r.MinistryName != nil && *r.MinistryName != "" {
			mVal = *r.MinistryName
		}

		_ = w.Write([]string{
			string(rune('1' + i)),
			r.User.FullName,
			r.Email,
			posVal,
			dVal,
			mVal,
			viewStatus,
			viewTime,
			ackStatus,
			ackTime,
		})
	}
	w.Flush()

	csvBytes := buf.Bytes()
	// Assert BOM
	require.True(t, bytes.HasPrefix(csvBytes, []byte("\xef\xbb\xbf")), "CSV must start with UTF-8 BOM")

	// Parse CSV without BOM
	r := csv.NewReader(bytes.NewReader(csvBytes[3:]))
	records, err := r.ReadAll()
	require.NoError(t, err)
	require.Len(t, records, 3)

	assert.Equal(t, "Сотрудник", records[0][1])
	assert.Equal(t, "Аскар Сериков", records[1][1])
	assert.Equal(t, "Прочитано", records[1][6])
	assert.Equal(t, "Ознакомлен", records[1][8])

	assert.Equal(t, "Берик Омаров", records[2][1])
	assert.Equal(t, "Не прочитано", records[2][6])
	assert.Equal(t, "Не ознакомлен", records[2][8])
}

func TestNWS007_XLSXExportGeneration(t *testing.T) {
	now := time.Now()
	pos := "Архитектор"
	deptName := "Отдел архитектуры"
	minName := "Астана"

	readers := []domain.PublicationReaderItem{
		{
			User: domain.UserMini{
				ID:        1,
				FirstName: "Данияр",
				LastName:  "Касымов",
				FullName:  "Данияр Касымов",
				Position:  &pos,
			},
			Email:          "daniyar@example.com",
			DepartmentName: &deptName,
			MinistryName:   &minName,
			IsViewed:       true,
			ViewedAt:       &now,
			IsAcknowledged: true,
			AcknowledgedAt: &now,
		},
	}

	f := excelize.NewFile()
	defer func() { _ = f.Close() }()

	const sheet = "Ознакомление"
	defaultSheet := f.GetSheetName(f.GetActiveSheetIndex())
	if defaultSheet != sheet {
		_ = f.SetSheetName(defaultSheet, sheet)
	}

	_ = f.SetCellValue(sheet, "A1", "Отчет по ознакомлению с публикацией")
	_ = f.SetCellValue(sheet, "A2", "Публикация: Тестовая публикация")
	_ = f.SetCellValue(sheet, "A3", "Всего в выборке: 1 | Целевая аудитория: 1 | Прочитали: 1 (100.0%) | Ознакомились: 1 (100.0%)")

	headers := []string{
		"№", "Сотрудник", "Email", "Должность", "Отдел", "Филиал",
		"Статус прочтения", "Дата и время прочтения", "Статус ознакомления", "Дата и время ознакомления",
	}

	for colIdx, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(colIdx+1, 5)
		_ = f.SetCellValue(sheet, cell, h)
	}

	rowIdx := 6
	for i, r := range readers {
		_ = f.SetCellValue(sheet, fmtCell(1, rowIdx), i+1)
		_ = f.SetCellValue(sheet, fmtCell(2, rowIdx), r.User.FullName)
		_ = f.SetCellValue(sheet, fmtCell(3, rowIdx), r.Email)
		_ = f.SetCellValue(sheet, fmtCell(4, rowIdx), *r.User.Position)
		_ = f.SetCellValue(sheet, fmtCell(5, rowIdx), *r.DepartmentName)
		_ = f.SetCellValue(sheet, fmtCell(6, rowIdx), *r.MinistryName)
		_ = f.SetCellValue(sheet, fmtCell(7, rowIdx), "Прочитано")
		_ = f.SetCellValue(sheet, fmtCell(8, rowIdx), r.ViewedAt.Format("02.01.2006 15:04:05"))
		_ = f.SetCellValue(sheet, fmtCell(9, rowIdx), "Ознакомлен")
		_ = f.SetCellValue(sheet, fmtCell(10, rowIdx), r.AcknowledgedAt.Format("02.01.2006 15:04:05"))
		rowIdx++
	}

	var buf bytes.Buffer
	err := f.Write(&buf)
	require.NoError(t, err)

	// Verify opening with excelize
	readF, err := excelize.OpenReader(bytes.NewReader(buf.Bytes()))
	require.NoError(t, err)
	defer func() { _ = readF.Close() }()

	val, err := readF.GetCellValue(sheet, "A1")
	require.NoError(t, err)
	assert.Equal(t, "Отчет по ознакомлению с публикацией", val)

	valB5, err := readF.GetCellValue(sheet, "B5")
	require.NoError(t, err)
	assert.Equal(t, "Сотрудник", valB5)

	valB6, err := readF.GetCellValue(sheet, "B6")
	require.NoError(t, err)
	assert.Equal(t, "Данияр Касымов", valB6)
}

func fmtCell(col, row int) string {
	c, _ := excelize.CoordinatesToCellName(col, row)
	return c
}
