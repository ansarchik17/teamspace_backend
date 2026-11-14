package agent

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/domain"
)

type simpleEntityExtractor struct{}

func NewSimpleEntityExtractor() EntityExtractor {
	return &simpleEntityExtractor{}
}

func (e *simpleEntityExtractor) Extract(_ context.Context, req *domain.AgentChatRequest, intent *domain.AgentIntentResult, _ []domain.AgentMessage) (map[string]interface{}, error) {
	loc, timezoneName := resolveRequestLocation(req.Timezone)
	entities := map[string]interface{}{
		"raw_message":             req.Message,
		"timezone":                timezoneName,
		"timezone_offset_minutes": timezoneOffsetMinutes(loc),
		"period":                  detectRelativePeriod(strings.ToLower(strings.TrimSpace(req.Message))),
	}

	message := strings.TrimSpace(req.Message)
	lower := strings.ToLower(message)

	switch intent.Name {
	case domain.AgentIntentOpenScreen:
		switch {
		case containsAny(lower, "задач", "task"):
			entities["screen"] = "tasks"
		case containsAny(lower, "уведомлен", "notification"):
			entities["screen"] = "notifications"
		default:
			entities["screen"] = "home"
		}
	case domain.AgentIntentSearch:
		entities["query"] = normalizeSearchQuery(message)
	case domain.AgentIntentWebSearch:
		entities["query"] = normalizeInternetSearchQuery(message)
	case domain.AgentIntentGetTaskBoards, domain.AgentIntentGetTaskSummary, domain.AgentIntentGetNotes, domain.AgentIntentGetDriveFiles, domain.AgentIntentGetDriveAccess:
		entities["query"] = normalizeLookupQuery(message)
		if resourceID, ok := extractUUID(message); ok {
			entities["resource_id"] = resourceID
			entities["note_id"] = resourceID
			entities["task_id"] = resourceID
		}
		entities["scope"] = detectDriveScope(lower)
		entities["file_type"] = detectDriveFileType(lower)
	case domain.AgentIntentGetTasks, domain.AgentIntentGetCalendar:
		if containsAny(lower, "завтра", "tomorrow") {
			entities["period"] = "tomorrow"
		} else if containsAny(lower, "недел", "this week", "week") {
			entities["period"] = "week"
		} else {
			entities["period"] = "today"
		}
	case domain.AgentIntentCreateReminder:
		title := extractQuoted(message)
		if title == "" {
			title = message
		}
		entities["title"] = title
		if when, ok := parseReminderTime(lower, loc); ok {
			entities["remind_at"] = when.Format(time.RFC3339)
		}
	case domain.AgentIntentCreateEvent, domain.AgentIntentCreateMeeting:
		entities["title"] = firstNonEmpty(extractQuoted(message), normalizeEventTitle(message))
		entities["is_online"] = intent.Name == domain.AgentIntentCreateMeeting || containsAny(lower, "онлайн", "video", "zoom", "meet")
		startAt, endAt, ok := parseDateTimeRange(lower, loc)
		if ok {
			entities["start_time"] = startAt.Format(time.RFC3339)
			entities["end_time"] = endAt.Format(time.RFC3339)
		}
		if minutes, ok := parseReminderMinutes(lower); ok {
			entities["reminder_minutes"] = minutes
		}
	case domain.AgentIntentSetReminder:
		if eventID, ok := extractUUID(message); ok {
			entities["event_id"] = eventID
		}
		entities["event_query"] = normalizeReminderTarget(message)
		if minutes, ok := parseReminderMinutes(lower); ok {
			entities["minutes_before"] = minutes
		}
	case domain.AgentIntentFindSlots:
		if date, ok := parseDateOnly(lower, loc); ok {
			entities["date"] = date.Format("2006-01-02")
		}
		entities["duration_minutes"] = parseDurationMinutes(lower, 30)
	case domain.AgentIntentCreateTask, domain.AgentIntentUpdateTask:
		entities["title"] = normalizeTaskTitle(message)
		if taskID, ok := extractUUID(message); ok {
			entities["task_id"] = taskID
		}
	case domain.AgentIntentStartCall:
		entities["target"] = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(message, "Позвони"), "Call"))
	case domain.AgentIntentCreateNote:
		entities["title"] = firstNonEmpty(extractQuoted(message), normalizeNoteTitle(message))
		entities["content"] = normalizeNoteContent(message, entities["title"])
	case domain.AgentIntentUpdateNote, domain.AgentIntentDeleteNote:
		if noteID, ok := extractUUID(message); ok {
			entities["note_id"] = noteID
		}
		entities["title"] = firstNonEmpty(extractQuoted(message), normalizeNoteTitle(message))
		entities["content"] = normalizeNoteContent(message, entities["title"])
	case domain.AgentIntentShareDrive, domain.AgentIntentCreateDriveLink:
		if resourceID, ok := extractUUID(message); ok {
			entities["resource_id"] = resourceID
		}
		entities["query"] = normalizeLookupQuery(message)
		entities["resource_type"] = detectDriveResourceType(lower)
		entities["role"] = detectDriveRole(lower)
	case domain.AgentIntentFillForm:
		entities["form_name"] = extractQuoted(message)
	}

	return entities, nil
}

var quotePattern = regexp.MustCompile(`["«](.+?)["»]`)
var uuidPattern = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)

func extractQuoted(message string) string {
	match := quotePattern.FindStringSubmatch(message)
	if len(match) == 2 {
		return strings.TrimSpace(match[1])
	}
	return ""
}

func parseReminderTime(message string, loc *time.Location) (time.Time, bool) {
	now := time.Now().In(loc)
	hourMinute := regexp.MustCompile(`(\d{1,2}):(\d{2})`)
	match := hourMinute.FindStringSubmatch(message)
	if len(match) != 3 {
		return time.Time{}, false
	}

	var hour, minute int
	_, _ = fmt.Sscanf(match[1], "%d", &hour)
	_, _ = fmt.Sscanf(match[2], "%d", &minute)

	target := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
	if strings.Contains(message, "завтра") || strings.Contains(message, "tomorrow") {
		target = target.Add(24 * time.Hour)
	}
	return target, true
}

func normalizeSearchQuery(message string) string {
	trimmed := strings.TrimSpace(message)
	lower := strings.ToLower(trimmed)
	prefixes := []string{"найди", "поиск", "search", "find"}
	for _, prefix := range prefixes {
		if strings.HasPrefix(lower, prefix) {
			return strings.TrimSpace(trimmed[len(prefix):])
		}
	}
	return trimmed
}

func normalizeInternetSearchQuery(message string) string {
	trimmed := strings.TrimSpace(message)
	lower := strings.ToLower(trimmed)
	prefixes := []string{
		"найди в интернете",
		"поищи в интернете",
		"поиск в интернете",
		"найди в интернет",
		"поищи в интернет",
		"web search",
		"internet search",
		"search the web",
		"search web for",
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(lower, prefix) {
			candidate := strings.TrimSpace(trimmed[len(prefix):])
			candidate = strings.TrimLeft(candidate, ":,- ")
			if candidate != "" {
				return candidate
			}
		}
	}
	return normalizeSearchQuery(message)
}

func normalizeTaskTitle(message string) string {
	trimmed := strings.TrimSpace(message)
	lower := strings.ToLower(trimmed)
	prefixes := []string{"создай задачу", "create task", "обнови задачу", "update task", "закрой задачу"}
	for _, prefix := range prefixes {
		if strings.HasPrefix(lower, prefix) {
			return strings.TrimSpace(trimmed[len(prefix):])
		}
	}
	return trimmed
}

func normalizeLookupQuery(message string) string {
	trimmed := strings.TrimSpace(message)
	lower := strings.ToLower(trimmed)
	prefixes := []string{
		"покажи", "найди", "найди мне", "список", "show", "find", "list", "открой",
		"заметки", "файлы", "доступ к файлу", "кто имеет доступ к",
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(lower, prefix) {
			candidate := strings.TrimSpace(trimmed[len(prefix):])
			if candidate != "" {
				return candidate
			}
		}
	}
	return trimmed
}

func normalizeEventTitle(message string) string {
	trimmed := strings.TrimSpace(message)
	prefixes := []string{
		"создай встречу", "создай событие", "добавь в календарь", "create event",
		"calendar event", "создай онлайн-встречу", "create online meeting",
	}
	lower := strings.ToLower(trimmed)
	for _, prefix := range prefixes {
		if strings.HasPrefix(lower, prefix) {
			candidate := strings.TrimSpace(trimmed[len(prefix):])
			if candidate != "" {
				return candidate
			}
		}
	}
	return trimmed
}

func normalizeReminderTarget(message string) string {
	trimmed := strings.TrimSpace(message)
	prefixes := []string{
		"напоминание для события", "напомни о встрече", "set reminder", "event reminder",
	}
	lower := strings.ToLower(trimmed)
	for _, prefix := range prefixes {
		if strings.HasPrefix(lower, prefix) {
			candidate := strings.TrimSpace(trimmed[len(prefix):])
			if candidate != "" {
				return candidate
			}
		}
	}
	return trimmed
}

func normalizeNoteTitle(message string) string {
	trimmed := strings.TrimSpace(message)
	prefixes := []string{"создай заметку", "create note", "обнови заметку", "update note", "удали заметку", "delete note"}
	lower := strings.ToLower(trimmed)
	for _, prefix := range prefixes {
		if strings.HasPrefix(lower, prefix) {
			return strings.TrimSpace(trimmed[len(prefix):])
		}
	}
	return trimmed
}

func normalizeNoteContent(message string, title interface{}) string {
	trimmed := strings.TrimSpace(message)
	titleStr := stringValue(title)
	if titleStr == "" {
		return ""
	}
	idx := strings.Index(trimmed, titleStr)
	if idx == -1 {
		return ""
	}
	content := strings.TrimSpace(trimmed[idx+len(titleStr):])
	content = strings.TrimLeft(content, ":,- ")
	if strings.EqualFold(content, titleStr) {
		return ""
	}
	return content
}

func extractUUID(message string) (string, bool) {
	match := uuidPattern.FindString(message)
	if match == "" {
		return "", false
	}
	if _, err := uuid.Parse(match); err != nil {
		return "", false
	}
	return match, true
}

func detectDriveScope(message string) string {
	switch {
	case containsAny(message, "recent", "недавн", "последн"):
		return "recent"
	case containsAny(message, "shared", "расшар", "поделил"):
		return "shared"
	case containsAny(message, "own", "мои файлы", "свои файлы"):
		return "own"
	default:
		return "all"
	}
}

func detectDriveFileType(message string) string {
	switch {
	case containsAny(message, "pdf"):
		return "pdf"
	case containsAny(message, "doc", "word"):
		return "doc"
	case containsAny(message, "xls", "excel", "таблиц"):
		return "xls"
	case containsAny(message, "ppt", "presentation", "презентац"):
		return "ppt"
	case containsAny(message, "image", "png", "jpg", "jpeg", "картин"):
		return "image"
	default:
		return ""
	}
}

func detectDriveRole(message string) string {
	switch {
	case containsAny(message, "owner", "владелец"):
		return "owner"
	case containsAny(message, "editor", "редактор", "edit"):
		return "editor"
	default:
		return "viewer"
	}
}

func detectDriveResourceType(message string) string {
	if containsAny(message, "папк", "folder") {
		return "folder"
	}
	return "file"
}

func parseReminderMinutes(message string) (int, bool) {
	pattern := regexp.MustCompile(`(\d{1,3})\s*(мин|min|minutes?)`)
	match := pattern.FindStringSubmatch(message)
	if len(match) != 2 {
		return 0, false
	}
	var minutes int
	_, _ = fmt.Sscanf(match[1], "%d", &minutes)
	if minutes < 0 {
		return 0, false
	}
	return minutes, true
}

func parseDurationMinutes(message string, fallback int) int {
	if minutes, ok := parseReminderMinutes(message); ok {
		return minutes
	}
	return fallback
}

func parseDateOnly(message string, loc *time.Location) (time.Time, bool) {
	now := time.Now().In(loc)
	switch {
	case containsAny(message, "завтра", "tomorrow"):
		return now.AddDate(0, 0, 1), true
	case containsAny(message, "сегодня", "today"):
		return now, true
	}
	re := regexp.MustCompile(`(\d{4}-\d{2}-\d{2})`)
	match := re.FindStringSubmatch(message)
	if len(match) != 2 {
		return time.Time{}, false
	}
	parsed, err := time.ParseInLocation("2006-01-02", match[1], loc)
	if err != nil {
		return time.Time{}, false
	}
	return parsed, true
}

func parseDateTimeRange(message string, loc *time.Location) (time.Time, time.Time, bool) {
	baseDate, ok := parseDateOnly(message, loc)
	if !ok {
		baseDate = time.Now().In(loc)
		if containsAny(message, "завтра", "tomorrow") {
			baseDate = baseDate.AddDate(0, 0, 1)
		}
	}
	re := regexp.MustCompile(`(\d{1,2}):(\d{2})`)
	matches := re.FindAllStringSubmatch(message, -1)
	if len(matches) == 0 {
		return time.Time{}, time.Time{}, false
	}
	startHour, startMinute := parseHourMinute(matches[0])
	start := time.Date(baseDate.Year(), baseDate.Month(), baseDate.Day(), startHour, startMinute, 0, 0, loc)
	end := start.Add(30 * time.Minute)
	if len(matches) > 1 {
		endHour, endMinute := parseHourMinute(matches[1])
		end = time.Date(baseDate.Year(), baseDate.Month(), baseDate.Day(), endHour, endMinute, 0, 0, loc)
	}
	if !end.After(start) {
		end = start.Add(30 * time.Minute)
	}
	return start, end, true
}

func parseHourMinute(match []string) (int, int) {
	var hour, minute int
	if len(match) >= 3 {
		_, _ = fmt.Sscanf(match[1], "%d", &hour)
		_, _ = fmt.Sscanf(match[2], "%d", &minute)
	}
	return hour, minute
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func resolveRequestLocation(raw string) (*time.Location, string) {
	name := strings.TrimSpace(raw)
	if name == "" {
		name = "Asia/Almaty"
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.FixedZone("UTC+5", 5*60*60), "Asia/Almaty"
	}
	return loc, name
}

func timezoneOffsetMinutes(loc *time.Location) int {
	_, offsetSeconds := time.Now().In(loc).Zone()
	return offsetSeconds / 60
}

func detectRelativePeriod(message string) string {
	switch {
	case containsAny(message, "завтра", "tomorrow"):
		return "tomorrow"
	case containsAny(message, "недел", "this week", "week"):
		return "week"
	default:
		return "today"
	}
}
