package service

import (
	"fmt"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

type timezoneDef struct {
	ID        string
	Name      string
	City      string
	Country   string
	IsDefault bool
}

func ValidateCalendarTimezone(tz string) error {
	tz = strings.TrimSpace(tz)
	if tz == "" || tz == "Local" || len(tz) > 100 {
		return apperrors.BadRequest("Некорректный часовой пояс")
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return apperrors.BadRequest("Некорректный часовой пояс: " + tz)
	}
	return nil
}

var supportedTimezonesDef = []timezoneDef{
	{ID: "Asia/Almaty", Name: "Алматы, Астана (UTC+5)", City: "Алматы", Country: "Казахстан", IsDefault: true},
	{ID: "Asia/Aqtau", Name: "Актау, Мангистау (UTC+5)", City: "Актау", Country: "Казахстан"},
	{ID: "Asia/Aqtobe", Name: "Актобе (UTC+5)", City: "Актобе", Country: "Казахстан"},
	{ID: "Asia/Oral", Name: "Уральск (UTC+5)", City: "Уральск", Country: "Казахстан"},
	{ID: "Asia/Qostanay", Name: "Костанай (UTC+5)", City: "Костанай", Country: "Казахстан"},
	{ID: "Asia/Qyzylorda", Name: "Кызылорда (UTC+5)", City: "Кызылорда", Country: "Казахстан"},
	{ID: "Asia/Tashkent", Name: "Ташкент (UTC+5)", City: "Ташкент", Country: "Узбекистан"},
	{ID: "Asia/Bishkek", Name: "Бишкек (UTC+6)", City: "Бишкек", Country: "Кыргызстан"},
	{ID: "Asia/Dushanbe", Name: "Душанбе (UTC+5)", City: "Душанбе", Country: "Таджикистан"},
	{ID: "Asia/Ashgabat", Name: "Ашхабад (UTC+5)", City: "Ашхабад", Country: "Туркменистан"},
	{ID: "Asia/Baku", Name: "Баку (UTC+4)", City: "Баку", Country: "Азербайджан"},
	{ID: "Asia/Tbilisi", Name: "Тбилиси (UTC+4)", City: "Тбилиси", Country: "Грузия"},
	{ID: "Asia/Yerevan", Name: "Ереван (UTC+4)", City: "Ереван", Country: "Армения"},
	{ID: "Europe/Moscow", Name: "Москва, Санкт-Петербург (UTC+3)", City: "Москва", Country: "Россия"},
	{ID: "Europe/Samara", Name: "Самара (UTC+4)", City: "Самара", Country: "Россия"},
	{ID: "Asia/Yekaterinburg", Name: "Екатеринбург (UTC+5)", City: "Екатеринбург", Country: "Россия"},
	{ID: "Asia/Omsk", Name: "Омск (UTC+6)", City: "Омск", Country: "Россия"},
	{ID: "Asia/Novosibirsk", Name: "Новосибирск (UTC+7)", City: "Новосибирск", Country: "Россия"},
	{ID: "Asia/Dubai", Name: "Дубай (UTC+4)", City: "Дубай", Country: "ОАЭ"},
	{ID: "Europe/Istanbul", Name: "Стамбул (UTC+3)", City: "Стамбул", Country: "Турция"},
	{ID: "UTC", Name: "UTC (Всемирное координированное время)", City: "UTC", Country: "Международное"},
	{ID: "Europe/London", Name: "Лондон (GMT/BST)", City: "Лондон", Country: "Великобритания"},
	{ID: "Europe/Berlin", Name: "Берлин, Париж (CET/CEST)", City: "Берлин", Country: "Германия"},
	{ID: "America/New_York", Name: "Нью-Йорк (EST/EDT)", City: "Нью-Йорк", Country: "США"},
	{ID: "America/Los_Angeles", Name: "Лос-Анджелес (PST/PDT)", City: "Лос-Анджелес", Country: "США"},
	{ID: "Asia/Singapore", Name: "Сингапур (UTC+8)", City: "Сингапур", Country: "Сингапур"},
	{ID: "Asia/Tokyo", Name: "Токио (UTC+9)", City: "Токио", Country: "Япония"},
}

// GetSupportedTimezones returns list of corporate and international timezones with dynamic offsets
func GetSupportedTimezones() []domain.CalendarTimezoneItem {
	now := time.Now().UTC()
	result := make([]domain.CalendarTimezoneItem, 0, len(supportedTimezonesDef))

	for _, def := range supportedTimezonesDef {
		loc, err := time.LoadLocation(def.ID)
		if err != nil {
			loc = time.UTC
		}
		inLoc := now.In(loc)
		_, offsetSec := inLoc.Zone()
		offsetMin := offsetSec / 60

		sign := "+"
		if offsetMin < 0 {
			sign = "-"
			offsetMin = -offsetMin
		}
		hours := offsetMin / 60
		mins := offsetMin % 60
		offsetStr := fmt.Sprintf("%s%02d:%02d", sign, hours, mins)

		result = append(result, domain.CalendarTimezoneItem{
			ID:            def.ID,
			Name:          def.Name,
			City:          def.City,
			Country:       def.Country,
			Offset:        offsetStr,
			OffsetMinutes: offsetSec / 60,
			IsDefault:     def.IsDefault,
		})
	}

	return result
}

// ConvertTimezoneItem converts start and end time to the target timezone
func ConvertTimezoneItem(startTime, endTime time.Time, tzID string) (*domain.TimezoneConvertedItem, error) {
	if err := ValidateCalendarTimezone(tzID); err != nil {
		return nil, err
	}
	loc, err := time.LoadLocation(strings.TrimSpace(tzID))
	if err != nil {
		return nil, apperrors.BadRequest(fmt.Sprintf("Некорректный часовой пояс: %s", tzID))
	}

	startInLoc := startTime.In(loc)
	endInLoc := endTime.In(loc)

	_, offsetSec := startInLoc.Zone()
	offsetMin := offsetSec / 60
	sign := "+"
	absMin := offsetMin
	if absMin < 0 {
		sign = "-"
		absMin = -absMin
	}
	offsetStr := fmt.Sprintf("%s%02d:%02d", sign, absMin/60, absMin%60)

	displayName := tzID
	for _, def := range supportedTimezonesDef {
		if strings.EqualFold(def.ID, tzID) {
			displayName = def.Name
			break
		}
	}

	return &domain.TimezoneConvertedItem{
		Timezone:           tzID,
		DisplayName:        displayName,
		Offset:             offsetStr,
		OffsetMinutes:      offsetMin,
		StartTime:          startInLoc,
		EndTime:            endInLoc,
		StartTimeFormatted: startInLoc.Format("2006-01-02 15:04"),
		EndTimeFormatted:   endInLoc.Format("2006-01-02 15:04"),
		StartTimeISO:       startInLoc.Format(time.RFC3339),
		EndTimeISO:         endInLoc.Format(time.RFC3339),
	}, nil
}

// FormatEventLocalTimes populates StartTimeLocal and EndTimeLocal on domain.Event
func FormatEventLocalTimes(event *domain.Event, requestedTimezone string) {
	if event == nil {
		return
	}
	tz := strings.TrimSpace(requestedTimezone)
	if tz == "" {
		tz = strings.TrimSpace(event.Timezone)
	}
	if tz == "" {
		tz = "Asia/Almaty"
	}

	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}

	startStr := event.StartTime.In(loc).Format(time.RFC3339)
	endStr := event.EndTime.In(loc).Format(time.RFC3339)
	event.StartTimeLocal = &startStr
	event.EndTimeLocal = &endStr
}

// FormatCountdownRU formats reminder countdown into natural Russian text
func FormatCountdownRU(minutes int) string {
	if minutes <= 0 {
		return "прямо сейчас"
	}
	if minutes < 60 {
		return fmt.Sprintf("через %d %s", minutes, pluralMinutesRU(minutes))
	}
	hours := minutes / 60
	remMinutes := minutes % 60
	if remMinutes == 0 {
		if hours == 24 {
			return "через 1 день (24 часа)"
		}
		return fmt.Sprintf("через %d %s", hours, pluralHoursRU(hours))
	}
	return fmt.Sprintf("через %d %s %d %s", hours, pluralHoursRU(hours), remMinutes, pluralMinutesRU(remMinutes))
}

func pluralMinutesRU(n int) string {
	n10 := n % 10
	n100 := n % 100
	if n100 >= 11 && n100 <= 19 {
		return "минут"
	}
	switch n10 {
	case 1:
		return "минуту"
	case 2, 3, 4:
		return "минуты"
	default:
		return "минут"
	}
}

func pluralHoursRU(n int) string {
	n10 := n % 10
	n100 := n % 100
	if n100 >= 11 && n100 <= 19 {
		return "часов"
	}
	switch n10 {
	case 1:
		return "час"
	case 2, 3, 4:
		return "часа"
	default:
		return "часов"
	}
}

// GetReminderPresets returns recommended and standard reminder presets
func GetReminderPresets() []domain.ReminderPresetItem {
	return []domain.ReminderPresetItem{
		{MinutesBefore: 0, Label: "В момент начала", Description: "Уведомление отправляется ровно в момент начала встречи"},
		{MinutesBefore: 5, Label: "За 5 минут", Description: "Краткое напоминание перед началом"},
		{MinutesBefore: 10, Label: "За 10 минут", Description: "Время подготовиться к созвону"},
		{MinutesBefore: 15, Label: "За 15 минут (рекомендуется)", Description: "Стандартное корпоративное напоминание"},
		{MinutesBefore: 30, Label: "За 30 минут", Description: "Заблаговременное напоминание"},
		{MinutesBefore: 60, Label: "За 1 час", Description: "За 1 час до начала встречи"},
		{MinutesBefore: 120, Label: "За 2 часа", Description: "За 2 часа до начала встречи"},
		{MinutesBefore: 1440, Label: "За 1 день", Description: "За 24 часа до запланированного события"},
	}
}
