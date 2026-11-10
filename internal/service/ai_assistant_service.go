package service

import (
	"context"
	"fmt"
	"github.com/west6ide/workspace-backend/internal/i18n"
	"strings"
	"time"

	"go.uber.org/zap"
)

// AIAssistantService предоставляет специализированный AI-ready слой поверх
// существующих сервисов. Возвращает компактные ответы, оптимизированные для
// голосового ассистента: минимум данных, готовый speech_text.
type AIAssistantService struct {
	calendar *CalendarService
	kanban   *KanbanService
	userRepo UserRepository
	cache    *AICache
	logger   *zap.Logger
}

func NewAIAssistantService(
	calendar *CalendarService,
	kanban *KanbanService,
	userRepo UserRepository,
	cache *AICache,
	logger *zap.Logger,
) *AIAssistantService {
	return &AIAssistantService{
		calendar: calendar,
		kanban:   kanban,
		userRepo: userRepo,
		cache:    cache,
		logger:   logger.With(zap.String("service", "ai_assistant")),
	}
}

// --------------------------------------------------------------------------
// Response types — компактные, AI-ready структуры
// --------------------------------------------------------------------------

// AICalendarItem — минимальная информация о событии для голосового ответа.
type AICalendarItem struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	StartTime time.Time `json:"start_time"`
	EndTime   time.Time `json:"end_time"`
}

// AICalendarSummary — ответ на запрос "какие встречи сегодня".
type AICalendarSummary struct {
	SpeechText string           `json:"speech_text"`
	Count      int              `json:"count"`
	Items      []AICalendarItem `json:"items"`
	Date       string           `json:"date"`
}

// AITaskItem — минимальная информация о задаче.
type AITaskItem struct {
	ID       string     `json:"id"`
	Title    string     `json:"title"`
	Priority string     `json:"priority"`
	Status   string     `json:"status"`
	DueAt    *time.Time `json:"due_at,omitempty"`
}

// AITasksSummary — ответ на запрос "покажи мои задачи".
type AITasksSummary struct {
	SpeechText string       `json:"speech_text"`
	Count      int          `json:"count"`
	Items      []AITaskItem `json:"items"`
}

// AIContactItem — минимальная информация о контакте.
type AIContactItem struct {
	ID       int64  `json:"id"`
	FullName string `json:"full_name"`
	Email    string `json:"email"`
	Position string `json:"position,omitempty"`
}

// AIContactsResult — ответ на поиск контактов.
type AIContactsResult struct {
	SpeechText string          `json:"speech_text"`
	Count      int             `json:"count"`
	Items      []AIContactItem `json:"items"`
}

type AIExecuteRequest struct {
	Intent   string            `json:"intent"`
	Query    string            `json:"query,omitempty"`
	Timezone string            `json:"timezone,omitempty"`
	Locale   string            `json:"locale,omitempty"`
	Slots    map[string]string `json:"slots,omitempty"`
}

type AIExecuteResponse struct {
	Intent      string      `json:"intent"`
	SpeechText  string      `json:"speech_text"`
	DisplayText string      `json:"display_text"`
	Data        interface{} `json:"data,omitempty"`
	Actions     []string    `json:"actions"`
}

// --------------------------------------------------------------------------
// GetCalendarToday — сводка событий на сегодня
// --------------------------------------------------------------------------

// GetCalendarToday возвращает компактный список событий пользователя на сегодня.
// Генерирует готовый speech_text для TTS.
// Использует Redis cache (TTL=60сек) — повторные вызовы < 5 мс.
func (s *AIAssistantService) GetCalendarToday(ctx context.Context, userID int64, timezone string) (*AICalendarSummary, error) {
	loc, err := s.resolveTimezone(timezone)
	if err != nil {
		loc = time.FixedZone("UTC+5", 5*60*60)
	}

	now := time.Now().In(loc)
	dateStr := now.Format("2006-01-02")

	// Проверяем кэш
	cacheKey := AILocalizedKey(ctx, AICalendarTodayKey(userID, dateStr))
	var cached AICalendarSummary
	if hit, _ := s.cache.Get(ctx, cacheKey, &cached); hit {
		s.logger.Debug("ai_calendar_today_cache_hit", zap.Int64("user_id", userID))
		return &cached, nil
	}

	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	endOfDay := startOfDay.Add(24*time.Hour - time.Second)

	events, err := s.calendar.ListEvents(ctx, userID, userID, startOfDay, endOfDay)
	if err != nil {
		return nil, fmt.Errorf("calendar list events: %w", err)
	}

	items := make([]AICalendarItem, 0, len(events))
	for _, e := range events {
		items = append(items, AICalendarItem{
			ID:        e.ID.String(),
			Title:     e.Title,
			StartTime: e.StartTime,
			EndTime:   e.EndTime,
		})
	}

	speechText := s.buildCalendarSpeech(items, now.In(loc), ctx)

	result := &AICalendarSummary{
		SpeechText: speechText,
		Count:      len(items),
		Items:      items,
		Date:       dateStr,
	}

	// Кэшируем результат
	s.cache.Set(ctx, cacheKey, result, AICalendarTodayTTL())

	return result, nil
}

func (s *AIAssistantService) buildCalendarSpeech(items []AICalendarItem, now time.Time, contexts ...context.Context) string {
	ctx := context.Background()
	if len(contexts) > 0 {
		ctx = contexts[0]
	}

	if len(items) == 0 {
		return i18n.UIMessage(ctx, "Сегодня встреч нет. Ваш день свободен.")
	}

	count := len(items)
	countWord := i18n.UIMessage(ctx, countRu(count, "встреча", "встречи", "встреч"))

	if count == 1 {
		t := items[0].StartTime.Format("15:04")
		return i18n.UIMessage(ctx, "Сегодня %d %s: в %s — %s.", count, countWord, t, items[0].Title)
	}

	// Ближайшее событие
	var nearest *AICalendarItem
	for i := range items {
		if items[i].StartTime.After(now) {
			nearest = &items[i]
			break
		}
	}

	sb := i18n.UIMessage(ctx, "Сегодня %d %s.", count, countWord)
	if nearest != nil {
		t := nearest.StartTime.Format("15:04")
		sb += i18n.UIMessage(ctx, " Ближайшая в %s — %s.", t, nearest.Title)
	} else {
		// Все уже прошли
		last := items[len(items)-1]
		t := last.StartTime.Format("15:04")
		sb += i18n.UIMessage(ctx, " Последняя была в %s — %s.", t, last.Title)
	}
	return sb
}

// --------------------------------------------------------------------------
// GetTasksSummary — сводка задач пользователя
// --------------------------------------------------------------------------

// GetTasksSummary возвращает компактный список активных задач пользователя.
// Использует Redis cache (TTL=30сек).
func (s *AIAssistantService) GetTasksSummary(ctx context.Context, userID int64) (*AITasksSummary, error) {
	// Проверяем кэш
	cacheKey := AILocalizedKey(ctx, AITasksSummaryKey(userID))
	var cached AITasksSummary
	if hit, _ := s.cache.Get(ctx, cacheKey, &cached); hit {
		s.logger.Debug("ai_tasks_summary_cache_hit", zap.Int64("user_id", userID))
		return &cached, nil
	}

	searchReq := SearchTasksRequest{
		AssigneeIDs: []int64{userID},
		ColumnKeys:  []string{"TODO", "IN_PROGRESS"},
	}

	tasks, err := s.kanban.SearchTasks(ctx, userID, searchReq)
	if err != nil {
		return nil, fmt.Errorf("kanban search tasks: %w", err)
	}

	items := make([]AITaskItem, 0, len(tasks))
	for _, t := range tasks {
		item := AITaskItem{
			ID:       t.ID.String(),
			Title:    t.Title,
			Priority: t.Priority,
			Status:   t.Status,
		}
		if t.DueAt != nil {
			item.DueAt = t.DueAt
		}
		items = append(items, item)
	}

	speechText := s.buildTasksSpeech(items, ctx)

	result := &AITasksSummary{
		SpeechText: speechText,
		Count:      len(items),
		Items:      items,
	}

	// Кэшируем результат
	s.cache.Set(ctx, cacheKey, result, AITasksSummaryTTL())

	return result, nil
}

func (s *AIAssistantService) buildTasksSpeech(items []AITaskItem, contexts ...context.Context) string {
	ctx := context.Background()
	if len(contexts) > 0 {
		ctx = contexts[0]
	}
	if len(items) == 0 {
		return i18n.UIMessage(ctx, "Активных задач нет. Все задачи выполнены.")
	}

	count := len(items)
	countWord := i18n.UIMessage(ctx, countRu(count, "активная задача", "активные задачи", "активных задач"))
	sb := i18n.UIMessage(ctx, "У вас %d %s.", count, countWord)

	// Найдём критические
	var critical []string
	for _, t := range items {
		if strings.EqualFold(t.Priority, "critical") || strings.EqualFold(t.Priority, "high") {
			critical = append(critical, t.Title)
			if len(critical) >= 2 {
				break
			}
		}
	}

	if len(critical) > 0 {
		sb += i18n.UIMessage(ctx, " Срочных: %s.", strings.Join(critical, ", "))
	} else if count <= 3 {
		titles := make([]string, 0, count)
		for _, t := range items {
			titles = append(titles, t.Title)
		}
		sb += i18n.UIMessage(ctx, " Задачи: %s.", strings.Join(titles, "; "))
	}

	return sb
}

// --------------------------------------------------------------------------
// SearchContacts — поиск контактов
// --------------------------------------------------------------------------

// SearchContacts возвращает компактный список контактов по запросу.
func (s *AIAssistantService) SearchContacts(ctx context.Context, userID int64, query string) (*AIContactsResult, error) {
	// Resolve current visibility on every read; cached contacts can outlive a company transfer.
	users, err := s.userRepo.SearchVisibleTo(ctx, userID, query, 10, 0)
	if err != nil {
		return nil, fmt.Errorf("user search: %w", err)
	}

	items := make([]AIContactItem, 0, len(users))
	for _, u := range users {
		fullName := strings.TrimSpace(u.FirstName + " " + u.LastName)
		if fullName == "" {
			fullName = u.Username
		}
		position := ""
		if u.Position != nil {
			position = *u.Position
		}
		items = append(items, AIContactItem{
			ID:       u.ID,
			FullName: fullName,
			Email:    u.Email,
			Position: position,
		})
	}

	var speechText string
	if len(items) == 0 {
		speechText = i18n.UIMessage(ctx, "Сотрудники по запросу «%s» не найдены.", query)
	} else if len(items) == 1 {
		u := items[0]
		speechText = i18n.UIMessage(ctx, "Найден сотрудник: %s", u.FullName)
		if u.Email != "" {
			speechText += i18n.UIMessage(ctx, ", почта: %s", u.Email)
		}
		speechText += "."
	} else {
		names := make([]string, 0, min(3, len(items)))
		for i, u := range items {
			if i >= 3 {
				break
			}
			names = append(names, u.FullName)
		}
		countWord := i18n.UIMessage(ctx, countRu(len(items), "сотрудник", "сотрудника", "сотрудников"))
		speechText = i18n.UIMessage(ctx, "Найдено %d %s: %s.", len(items), countWord, strings.Join(names, ", "))
	}

	result := &AIContactsResult{
		SpeechText: speechText,
		Count:      len(items),
		Items:      items,
	}
	return result, nil
}

func (s *AIAssistantService) Execute(
	ctx context.Context,
	userID int64,
	req AIExecuteRequest,
) (*AIExecuteResponse, error) {
	intent := strings.TrimSpace(req.Intent)
	if intent == "" {
		intent = "unknown"
	}

	switch intent {
	case "get_calendar_today":
		timezone := strings.TrimSpace(req.Timezone)
		if timezone == "" {
			timezone = "Asia/Almaty"
		}
		result, err := s.GetCalendarToday(ctx, userID, timezone)
		if err != nil {
			return nil, err
		}
		return &AIExecuteResponse{
			Intent:      intent,
			SpeechText:  result.SpeechText,
			DisplayText: i18n.UIMessage(ctx, "Сегодня: %d %s", result.Count, i18n.UIMessage(ctx, countRu(result.Count, "встреча", "встречи", "встреч"))),
			Data:        result,
			Actions:     []string{},
		}, nil
	case "get_kanban_tasks":
		result, err := s.GetTasksSummary(ctx, userID)
		if err != nil {
			return nil, err
		}
		return &AIExecuteResponse{
			Intent:      intent,
			SpeechText:  result.SpeechText,
			DisplayText: i18n.UIMessage(ctx, "Активных задач: %d", result.Count),
			Data:        result,
			Actions:     []string{},
		}, nil
	case "search_contacts":
		query := strings.TrimSpace(req.Query)
		if query == "" {
			query = strings.TrimSpace(req.Slots["query"])
		}
		if query == "" {
			return &AIExecuteResponse{
				Intent:      intent,
				SpeechText:  i18n.UIMessage(ctx, "Не удалось определить, кого нужно найти."),
				DisplayText: i18n.UIMessage(ctx, "Нужен запрос для поиска контакта"),
				Actions:     []string{},
			}, nil
		}
		result, err := s.SearchContacts(ctx, userID, query)
		if err != nil {
			return nil, err
		}
		return &AIExecuteResponse{
			Intent:      intent,
			SpeechText:  result.SpeechText,
			DisplayText: i18n.UIMessage(ctx, "Найдено контактов: %d", result.Count),
			Data:        result,
			Actions:     []string{},
		}, nil
	case "greeting":
		return &AIExecuteResponse{
			Intent:      intent,
			SpeechText:  i18n.UIMessage(ctx, "Здравствуйте! Чем могу помочь?"),
			DisplayText: i18n.UIMessage(ctx, "Голосовой помощник готов"),
			Actions:     []string{},
		}, nil
	default:
		return &AIExecuteResponse{
			Intent:      intent,
			SpeechText:  i18n.UIMessage(ctx, "Для этого запроса нужен расширенный AI-разбор."),
			DisplayText: i18n.UIMessage(ctx, "Требуется полный AI pipeline"),
			Actions:     []string{"fallback_to_llm"},
		}, nil
	}
}

// --------------------------------------------------------------------------
// Helpers
// --------------------------------------------------------------------------

func (s *AIAssistantService) resolveTimezone(tz string) (*time.Location, error) {
	if tz == "" {
		return nil, fmt.Errorf("empty timezone")
	}
	return time.LoadLocation(tz)
}

// countRu — склонение существительного по числу (русский язык).
func countRu(n int, one, few, many string) string {
	mod10 := n % 10
	mod100 := n % 100
	if mod100 >= 11 && mod100 <= 14 {
		return many
	}
	switch mod10 {
	case 1:
		return one
	case 2, 3, 4:
		return few
	default:
		return many
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
