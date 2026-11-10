package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
	"github.com/west6ide/workspace-backend/internal/service"
)

type staticTool struct {
	name                 string
	description          string
	intents              []domain.AgentIntentName
	actions              []string
	requiresConfirmation bool
	handler              func(ctx context.Context, input ToolRequest) (*ToolResult, *apperrors.AppError)
}

func (t *staticTool) Name() string        { return t.name }
func (t *staticTool) Description() string { return t.description }
func (t *staticTool) Supports(intent domain.AgentIntentName) bool {
	for _, item := range t.intents {
		if item == intent {
			return true
		}
	}
	return false
}
func (t *staticTool) Execute(ctx context.Context, input ToolRequest) (*ToolResult, *apperrors.AppError) {
	return t.handler(ctx, input)
}
func (t *staticTool) Intents() []domain.AgentIntentName { return t.intents }
func (t *staticTool) Actions() []string                 { return t.actions }
func (t *staticTool) RequiresConfirmation() bool        { return t.requiresConfirmation }

type ToolDependencies struct {
	Notifications *repository.NotificationRepository
	UserRepo      service.UserRepository
	Calendar      *service.CalendarService
	Kanban        *service.KanbanService
	Notes         *service.NoteService
	Drive         *service.DriveService
	Chat          *service.ChatService
	BoardRepo     *repository.BoardRepository
	AgentConfig   *config.AgentConfig
}

func BuildDefaultTools(deps ToolDependencies) []Tool {
	return []Tool{
		&staticTool{
			name:        "open_screen",
			description: "Builds frontend navigation actions and optional task preview.",
			intents:     []domain.AgentIntentName{domain.AgentIntentOpenScreen},
			actions:     []string{"open_screen"},
			handler: func(ctx context.Context, input ToolRequest) (*ToolResult, *apperrors.AppError) {
				screen := firstString(input.Entities["screen"], "home")
				result := &ToolResult{
					Answer: fmt.Sprintf("Открываю раздел `%s`.", screen),
					Actions: []domain.AgentAction{{
						Type:    "open_screen",
						Label:   "Open screen",
						Payload: map[string]interface{}{"screen": screen},
					}},
				}
				if screen == "tasks" && deps.Kanban != nil {
					tasks, err := deps.Kanban.SearchTasks(ctx, input.User.UserID, service.SearchTasksRequest{
						AssigneeIDs: []int64{input.User.UserID},
						ColumnKeys:  []string{domain.ColTODO, domain.ColInProgress},
						Limit:       5,
					})
					if err == nil && len(tasks) > 0 {
						result.Metadata = map[string]interface{}{"tasks_preview_count": len(tasks)}
						result.Suggestions = []string{"Показать мои задачи", "Показать сводку по доскам"}
					}
				}
				return result, nil
			},
		},
		&staticTool{
			name:        "search",
			description: "Ищет сотрудников и рабочие сущности по текстовому запросу.",
			intents:     []domain.AgentIntentName{domain.AgentIntentSearch},
			actions:     []string{"search"},
			handler: func(ctx context.Context, input ToolRequest) (*ToolResult, *apperrors.AppError) {
				query := firstString(input.Entities["query"], input.Request.Message)
				if deps.UserRepo == nil {
					return &ToolResult{
						Answer:      "Поиск доступен, но backend search provider не подключён.",
						Suggestions: []string{"Уточнить поисковый запрос"},
					}, nil
				}
				users, err := deps.UserRepo.SearchVisibleTo(ctx, input.User.UserID, query, 5, 0)
				if err != nil {
					return nil, apperrors.InternalWithErr("search failed", err)
				}
				results := make([]map[string]interface{}, 0, len(users))
				for _, user := range users {
					results = append(results, map[string]interface{}{
						"id":       user.ID,
						"name":     displayName(user.FirstName, user.LastName, user.Username),
						"email":    user.Email,
						"position": user.Position,
						"avatar":   user.AvatarURL,
					})
				}
				return &ToolResult{
					Answer: fmt.Sprintf("Найдено %d результатов.", len(results)),
					Actions: []domain.AgentAction{{
						Type:    "search",
						Label:   "Search results",
						Payload: map[string]interface{}{"query": query, "results": results},
					}},
				}, nil
			},
		},
		&staticTool{
			name:        "web_search",
			description: "Ищет информацию в интернете через adaptive web search backend.",
			intents:     []domain.AgentIntentName{domain.AgentIntentWebSearch},
			actions:     []string{"web_search"},
			handler: func(ctx context.Context, input ToolRequest) (*ToolResult, *apperrors.AppError) {
				query := firstString(input.Entities["query"], input.Request.Message)
				if strings.TrimSpace(query) == "" {
					return &ToolResult{
						Answer:      "Не вижу поискового запроса для интернет-поиска.",
						Suggestions: []string{"Уточнить, что именно искать в интернете"},
					}, nil
				}

				result, err := executeAdaptiveWebSearch(
					ctx,
					deps.AgentConfig,
					query,
					input.User.AuthorizationHeader,
					input.User.CookieHeader,
				)
				if err != nil {
					var accountingError *apperrors.AppError
					if errors.As(err, &accountingError) && accountingError.Code == apperrors.ErrCodeAIAccountingUnavailable {
						return nil, accountingError
					}
					return &ToolResult{
						Answer: fmt.Sprintf("Не удалось выполнить поиск в интернете: %v", err),
						Metadata: map[string]interface{}{
							"query": query,
						},
						Suggestions: []string{"Попробовать запрос ещё раз", "Уточнить формулировку"},
					}, nil
				}

				return &ToolResult{
					Answer: result.Answer,
					Actions: []domain.AgentAction{{
						Type:  "web_search",
						Label: "Web search results",
						Payload: map[string]interface{}{
							"query":          query,
							"answer":         result.Answer,
							"used_web":       result.UsedWeb,
							"search_backend": result.SearchBackend,
							"sources":        result.SourcesAsPayload(),
						},
					}},
					Metadata: map[string]interface{}{
						"query":          query,
						"used_web":       result.UsedWeb,
						"search_backend": result.SearchBackend,
						"route":          result.Route,
						"fast_mode":      result.FastMode,
						"es_chunks_used": result.ESChunksUsed,
						"searxng_hits":   result.SearxNGHits,
						"indexed_chunks": result.IndexedChunks,
						"source_count":   len(result.Sources),
						"gatekeeper":     result.Gatekeeper,
					},
					Suggestions: []string{"Уточнить запрос", "Показать источники"},
				}, nil
			},
		},
		&staticTool{
			name:        "task_boards",
			description: "Показывает доступные канбан-доски пользователя без лишних backend идентификаторов в запросе.",
			intents:     []domain.AgentIntentName{domain.AgentIntentGetTaskBoards},
			actions:     []string{"show_task_boards"},
			handler: func(ctx context.Context, input ToolRequest) (*ToolResult, *apperrors.AppError) {
				if deps.Kanban == nil {
					return &ToolResult{Answer: "Сервис досок сейчас недоступен."}, nil
				}
				boards, err := deps.Kanban.ListUserBoards(ctx, input.User.UserID)
				if err != nil {
					return nil, apperrors.ToAppError(err)
				}
				payload := make([]map[string]interface{}, 0, len(boards))
				for _, board := range boards {
					payload = append(payload, map[string]interface{}{
						"id":                 board.ID.String(),
						"name":               board.Name,
						"total_tasks":        board.Progress.TotalTasks,
						"completed_tasks":    board.Progress.CompletedTasks,
						"completion_percent": board.Progress.CompletionPercent,
					})
				}
				return &ToolResult{
					Answer: buildBoardListAnswer(payload),
					Actions: []domain.AgentAction{{
						Type:    "show_task_boards",
						Label:   "Show task boards",
						Payload: map[string]interface{}{"boards": payload},
					}},
				}, nil
			},
		},
		&staticTool{
			name:        "task_summary",
			description: "Даёт быструю сводку активных задач по всем доскам текущего пользователя.",
			intents:     []domain.AgentIntentName{domain.AgentIntentGetTaskSummary},
			actions:     []string{"show_task_summary"},
			handler: func(ctx context.Context, input ToolRequest) (*ToolResult, *apperrors.AppError) {
				if deps.Kanban == nil {
					return &ToolResult{Answer: "Сервис задач сейчас недоступен."}, nil
				}
				summary, err := deps.Kanban.GetMyTaskBoardsSummary(ctx, input.User.UserID)
				if err != nil {
					return nil, apperrors.ToAppError(err)
				}
				boards := make([]map[string]interface{}, 0, len(summary.Boards))
				totalActive := 0
				for _, board := range summary.Boards {
					active := board.TodoCount + board.InProgressCount + board.ReviewCount + board.BlockedCount
					if active == 0 {
						continue
					}
					totalActive += active
					boards = append(boards, map[string]interface{}{
						"board_id":          board.BoardID.String(),
						"board_name":        board.BoardName,
						"task_count":        active,
						"todo_count":        board.TodoCount,
						"in_progress_count": board.InProgressCount,
						"review_count":      board.ReviewCount,
						"blocked_count":     board.BlockedCount,
						"done_count":        board.DoneCount,
						"overdue_count":     board.OverdueCount,
					})
				}
				return &ToolResult{
					Answer: buildTaskSummaryAnswer(totalActive, boards),
					Actions: []domain.AgentAction{{
						Type:    "show_task_summary",
						Label:   "Show task summary",
						Payload: map[string]interface{}{"total_tasks": totalActive, "boards": boards},
					}},
					Metadata: map[string]interface{}{
						"source":       "kanban_summary",
						"current_user": input.User.UserID,
					},
				}, nil
			},
		},
		&staticTool{
			name:        "tasks_overview",
			description: "Показывает задачи текущего пользователя без запроса внутренних backend ID.",
			intents:     []domain.AgentIntentName{domain.AgentIntentGetTasks},
			actions:     []string{"show_tasks"},
			handler: func(ctx context.Context, input ToolRequest) (*ToolResult, *apperrors.AppError) {
				if deps.Kanban == nil {
					return &ToolResult{Answer: "Сервис задач сейчас недоступен."}, nil
				}
				searchReq := service.SearchTasksRequest{
					AssigneeIDs: []int64{input.User.UserID},
					ColumnKeys:  []string{domain.ColTODO, domain.ColInProgress},
					Limit:       10,
				}
				if query := strings.TrimSpace(firstString(input.Entities["query"])); query != "" {
					searchReq.Query = query
				}
				switch firstString(input.Entities["period"]) {
				case "tomorrow":
					from, to := localDayBounds(input.Entities, 1)
					searchReq.DateFrom = &from
					searchReq.DateTo = &to
				case "today":
					from, to := localDayBounds(input.Entities, 0)
					searchReq.DateFrom = &from
					searchReq.DateTo = &to
				}
				tasks, err := deps.Kanban.SearchTasks(ctx, input.User.UserID, searchReq)
				if err != nil {
					return nil, apperrors.ToAppError(err)
				}
				items := serializeTasks(tasks)
				return &ToolResult{
					Answer: buildTasksOverviewAnswer(items, firstString(input.Entities["period"])),
					Actions: []domain.AgentAction{{
						Type:    "show_tasks",
						Label:   "Show tasks",
						Payload: map[string]interface{}{"tasks": items, "period": input.Entities["period"]},
					}},
					Metadata: map[string]interface{}{
						"source":       "kanban_service",
						"current_user": input.User.UserID,
					},
				}, nil
			},
		},
		&staticTool{
			name:        "calendar_overview",
			description: "Показывает встречи текущего пользователя по его timezone без запроса user_id у модели.",
			intents:     []domain.AgentIntentName{domain.AgentIntentGetCalendar},
			actions:     []string{"show_calendar"},
			handler: func(ctx context.Context, input ToolRequest) (*ToolResult, *apperrors.AppError) {
				if deps.Calendar == nil {
					return &ToolResult{Answer: "Сервис календаря сейчас недоступен."}, nil
				}
				from, to := calendarBounds(input.Entities)
				events, err := deps.Calendar.ListEvents(ctx, input.User.UserID, input.User.UserID, from, to)
				if err != nil {
					return nil, apperrors.ToAppError(err)
				}
				items := serializeEvents(events)
				return &ToolResult{
					Answer: buildCalendarOverviewAnswer(items, firstString(input.Entities["period"])),
					Actions: []domain.AgentAction{{
						Type:    "show_calendar",
						Label:   "Show calendar",
						Payload: map[string]interface{}{"events": items, "period": input.Entities["period"]},
					}},
					Metadata: map[string]interface{}{
						"source":       "calendar_service",
						"timezone":     firstString(input.Entities["timezone"]),
						"current_user": input.User.UserID,
					},
				}, nil
			},
		},
		&staticTool{
			name:                 "create_event",
			description:          "Создаёт календарное событие через существующий calendar service после подтверждения.",
			intents:              []domain.AgentIntentName{domain.AgentIntentCreateEvent},
			actions:              []string{"create_event"},
			requiresConfirmation: true,
			handler: func(ctx context.Context, input ToolRequest) (*ToolResult, *apperrors.AppError) {
				title := firstString(input.Entities["title"])
				startTime := firstString(input.Entities["start_time"])
				endTime := firstString(input.Entities["end_time"])
				if title == "" || startTime == "" || endTime == "" {
					return &ToolResult{
						Answer:      "Для создания события нужны название, время начала и время окончания.",
						Suggestions: []string{"Создай встречу \"Daily\" завтра в 10:00 10:30"},
					}, nil
				}
				action := &domain.AgentAction{
					Type:  "create_event",
					Label: "Create event",
					Payload: map[string]interface{}{
						"title":            title,
						"start_time":       startTime,
						"end_time":         endTime,
						"is_online":        boolValue(input.Entities["is_online"]),
						"reminder_minutes": intValue(input.Entities["reminder_minutes"]),
						"attendee_ids":     contextInt64Slice(input.Request.Context, "attendee_ids"),
					},
				}
				if !input.Confirmed {
					return &ToolResult{
						Answer:               "Подтвердите создание события в календаре.",
						RequiresConfirmation: true,
						ProposedAction:       action,
					}, nil
				}
				if deps.Calendar == nil {
					return &ToolResult{Answer: "Сервис календаря сейчас недоступен."}, nil
				}
				req, appErr := buildCreateEventRequest(action.Payload)
				if appErr != nil {
					return nil, appErr
				}
				event, err := deps.Calendar.CreateEvent(ctx, input.User.UserID, req)
				if err != nil {
					return nil, apperrors.ToAppError(err)
				}
				return &ToolResult{
					Answer: fmt.Sprintf("Событие `%s` создано.", event.Title),
					Actions: []domain.AgentAction{{
						Type:    "event_created",
						Label:   "Event created",
						Payload: serializeEvent(*event),
					}},
				}, nil
			},
		},
		&staticTool{
			name:                 "event_reminder",
			description:          "Ставит напоминание для существующего события через calendar service после подтверждения.",
			intents:              []domain.AgentIntentName{domain.AgentIntentSetReminder},
			actions:              []string{"set_event_reminder"},
			requiresConfirmation: true,
			handler: func(ctx context.Context, input ToolRequest) (*ToolResult, *apperrors.AppError) {
				minutes := intValue(input.Entities["minutes_before"])
				if minutes <= 0 {
					minutes = 15
				}
				eventID, title, appErr := resolveEvent(ctx, deps.Calendar, input.User.UserID, firstString(input.Entities["event_id"]), firstString(input.Entities["event_query"]))
				if appErr != nil {
					return nil, appErr
				}
				action := &domain.AgentAction{
					Type:  "set_event_reminder",
					Label: "Set event reminder",
					Payload: map[string]interface{}{
						"event_id":       eventID.String(),
						"event_title":    title,
						"minutes_before": minutes,
					},
				}
				if !input.Confirmed {
					return &ToolResult{
						Answer:               "Подтвердите установку напоминания для события.",
						RequiresConfirmation: true,
						ProposedAction:       action,
					}, nil
				}
				if deps.Calendar == nil {
					return &ToolResult{Answer: "Сервис календаря сейчас недоступен."}, nil
				}
				if err := deps.Calendar.SetReminder(ctx, input.User.UserID, eventID, minutes); err != nil {
					return nil, apperrors.ToAppError(err)
				}
				return &ToolResult{
					Answer: fmt.Sprintf("Напоминание для `%s` установлено за %d минут.", title, minutes),
					Actions: []domain.AgentAction{{
						Type:    "reminder_set",
						Label:   "Reminder set",
						Payload: action.Payload,
					}},
				}, nil
			},
		},
		&staticTool{
			name:        "find_available_slots",
			description: "Находит свободные слоты по существующему availability service и автоматически включает текущего пользователя.",
			intents:     []domain.AgentIntentName{domain.AgentIntentFindSlots},
			actions:     []string{"show_available_slots"},
			handler: func(ctx context.Context, input ToolRequest) (*ToolResult, *apperrors.AppError) {
				if deps.Calendar == nil {
					return &ToolResult{Answer: "Сервис календаря сейчас недоступен."}, nil
				}
				date := firstString(input.Entities["date"])
				if date == "" {
					return &ToolResult{
						Answer:      "Для поиска свободных слотов нужна дата в формате YYYY-MM-DD или относительная дата вроде 'завтра'.",
						Suggestions: []string{"Найди свободные слоты завтра", "Найди свободные слоты 2026-05-25"},
					}, nil
				}
				day, err := time.ParseInLocation("2006-01-02", date, mustLocation(firstString(input.Entities["timezone"])))
				if err != nil {
					return nil, apperrors.BadRequest("date must be in YYYY-MM-DD format")
				}
				start := time.Date(day.Year(), day.Month(), day.Day(), 9, 0, 0, 0, day.Location())
				attendeeIDs := uniqueInt64(append(contextInt64Slice(input.Request.Context, "user_ids"), input.User.UserID))
				req := domain.CalendarSuggestTimesRequest{
					Date:            day.Format("2006-01-02"),
					DurationMinutes: intValueOrDefault(input.Entities["duration_minutes"], 30),
					AttendeeIDs:     attendeeIDs,
					Timezone:        firstString(input.Entities["timezone"], "Asia/Almaty"),
					WorkdayStart:    start.Format("15:04"),
					WorkdayEnd:      "18:00",
					SlotStepMinutes: 30,
					Limit:           5,
				}
				suggested, err := deps.Calendar.SuggestTimes(ctx, input.User.UserID, req)
				if err != nil {
					return nil, apperrors.ToAppError(err)
				}
				payload := serializeSlots(suggested.SuggestedSlots)
				return &ToolResult{
					Answer: buildSlotsAnswer(payload, date),
					Actions: []domain.AgentAction{{
						Type:    "show_available_slots",
						Label:   "Show available slots",
						Payload: map[string]interface{}{"date": date, "slots": payload},
					}},
					Metadata: map[string]interface{}{
						"checked_users": attendeeIDs,
					},
				}, nil
			},
		},
		&staticTool{
			name:                 "create_reminder",
			description:          "Создаёт напоминание в календаре текущего пользователя после подтверждения.",
			intents:              []domain.AgentIntentName{domain.AgentIntentCreateReminder},
			actions:              []string{"create_reminder"},
			requiresConfirmation: true,
			handler: func(ctx context.Context, input ToolRequest) (*ToolResult, *apperrors.AppError) {
				title := firstString(input.Entities["title"])
				remindAtRaw := firstString(input.Entities["remind_at"])
				action := &domain.AgentAction{
					Type:  "create_reminder",
					Label: "Create reminder",
					Payload: map[string]interface{}{
						"title":     title,
						"remind_at": remindAtRaw,
					},
				}
				if !input.Confirmed {
					return &ToolResult{
						Answer:               "Подтвердите создание напоминания.",
						RequiresConfirmation: true,
						ProposedAction:       action,
					}, nil
				}
				if deps.Calendar == nil {
					return &ToolResult{Answer: "Напоминание подтверждено, но calendar service не подключён."}, nil
				}
				remindAt, err := time.Parse(time.RFC3339, remindAtRaw)
				if err != nil {
					return nil, apperrors.BadRequest("remind_at is required in RFC3339 format")
				}
				endAt := remindAt.Add(15 * time.Minute)
				minutesBefore := 0
				event, err := deps.Calendar.CreateEvent(ctx, input.User.UserID, &domain.CreateEventRequest{
					Title:           title,
					StartTime:       remindAt,
					EndTime:         endAt,
					ReminderMinutes: &minutesBefore,
				})
				if err != nil {
					return nil, apperrors.ToAppError(err)
				}
				return &ToolResult{
					Answer: fmt.Sprintf("Напоминание `%s` создано на %s.", title, remindAt.Format(time.RFC3339)),
					Actions: []domain.AgentAction{{
						Type:    "reminder_created",
						Label:   "Reminder created",
						Payload: map[string]interface{}{"event_id": event.ID.String()},
					}},
				}, nil
			},
		},
		&staticTool{
			name:                 "online_meeting",
			description:          "Создаёт онлайн-встречу как календарное событие с внутренним meeting provider после подтверждения.",
			intents:              []domain.AgentIntentName{domain.AgentIntentCreateMeeting},
			actions:              []string{"create_online_meeting"},
			requiresConfirmation: true,
			handler: func(ctx context.Context, input ToolRequest) (*ToolResult, *apperrors.AppError) {
				title := firstString(input.Entities["title"])
				startTime := firstString(input.Entities["start_time"])
				endTime := firstString(input.Entities["end_time"])
				if title == "" || startTime == "" || endTime == "" {
					return &ToolResult{
						Answer:      "Для онлайн-встречи нужны название, время начала и окончания.",
						Suggestions: []string{"Создай онлайн-встречу \"Синк\" завтра в 15:00 15:30"},
					}, nil
				}
				payload := map[string]interface{}{
					"title":            title,
					"start_time":       startTime,
					"end_time":         endTime,
					"is_online":        true,
					"reminder_minutes": intValue(input.Entities["reminder_minutes"]),
					"attendee_ids":     contextInt64Slice(input.Request.Context, "attendee_ids"),
				}
				action := &domain.AgentAction{Type: "create_online_meeting", Label: "Create online meeting", Payload: payload}
				if !input.Confirmed {
					return &ToolResult{
						Answer:               "Подтвердите создание онлайн-встречи.",
						RequiresConfirmation: true,
						ProposedAction:       action,
					}, nil
				}
				if deps.Calendar == nil {
					return &ToolResult{Answer: "Сервис календаря сейчас недоступен."}, nil
				}
				req, appErr := buildCreateEventRequest(payload)
				if appErr != nil {
					return nil, appErr
				}
				req.IsOnline = true
				event, err := deps.Calendar.CreateEvent(ctx, input.User.UserID, req)
				if err != nil {
					return nil, apperrors.ToAppError(err)
				}
				eventPayload := serializeEvent(*event)
				return &ToolResult{
					Answer: buildMeetingCreatedAnswer(eventPayload),
					Actions: []domain.AgentAction{{
						Type:    "meeting_created",
						Label:   "Meeting created",
						Payload: eventPayload,
					}},
				}, nil
			},
		},
		&staticTool{
			name:        "notes_list",
			description: "Показывает заметки пользователя и извлекает короткие превью без дополнительных API-hop.",
			intents:     []domain.AgentIntentName{domain.AgentIntentGetNotes},
			actions:     []string{"show_notes"},
			handler: func(ctx context.Context, input ToolRequest) (*ToolResult, *apperrors.AppError) {
				if deps.Notes == nil {
					return &ToolResult{Answer: "Сервис заметок сейчас недоступен."}, nil
				}
				filter := domain.NoteListFilter{
					UserID: input.User.UserID,
					Query:  firstString(input.Entities["query"]),
					Limit:  20,
				}
				notes, err := deps.Notes.ListNotes(ctx, filter)
				if err != nil {
					return nil, apperrors.ToAppError(err)
				}
				payload := serializeNotes(notes.Items)
				return &ToolResult{
					Answer: buildNotesAnswer(payload, filter.Query),
					Actions: []domain.AgentAction{{
						Type:    "show_notes",
						Label:   "Show notes",
						Payload: map[string]interface{}{"query": filter.Query, "notes": payload},
					}},
				}, nil
			},
		},
		&staticTool{
			name:        "note_folders",
			description: "Показывает папки заметок пользователя через существующий note service.",
			intents:     []domain.AgentIntentName{domain.AgentIntentGetNoteFolders},
			actions:     []string{"show_note_folders"},
			handler: func(ctx context.Context, input ToolRequest) (*ToolResult, *apperrors.AppError) {
				if deps.Notes == nil {
					return &ToolResult{Answer: "Сервис заметок сейчас недоступен."}, nil
				}
				folders, err := deps.Notes.ListFolders(ctx, input.User.UserID)
				if err != nil {
					return nil, apperrors.ToAppError(err)
				}
				payload := make([]map[string]interface{}, 0, len(folders))
				for _, folder := range folders {
					payload = append(payload, map[string]interface{}{
						"id":         folder.ID.String(),
						"name":       folder.Name,
						"sort_order": folder.SortOrder,
					})
				}
				return &ToolResult{
					Answer: buildNoteFoldersAnswer(payload),
					Actions: []domain.AgentAction{{
						Type:    "show_note_folders",
						Label:   "Show note folders",
						Payload: map[string]interface{}{"folders": payload},
					}},
				}, nil
			},
		},
		&staticTool{
			name:                 "note_create",
			description:          "Создаёт заметку через существующий note service после подтверждения.",
			intents:              []domain.AgentIntentName{domain.AgentIntentCreateNote},
			actions:              []string{"create_note"},
			requiresConfirmation: true,
			handler: func(ctx context.Context, input ToolRequest) (*ToolResult, *apperrors.AppError) {
				title := firstString(input.Entities["title"])
				if title == "" {
					return &ToolResult{Answer: "Для заметки нужно хотя бы название."}, nil
				}
				action := &domain.AgentAction{
					Type:  "create_note",
					Label: "Create note",
					Payload: map[string]interface{}{
						"title":     title,
						"content":   firstString(input.Entities["content"]),
						"is_pinned": contextBool(input.Request.Context, "is_pinned"),
					},
				}
				if !input.Confirmed {
					return &ToolResult{
						Answer:               "Подтвердите создание заметки.",
						RequiresConfirmation: true,
						ProposedAction:       action,
					}, nil
				}
				if deps.Notes == nil {
					return &ToolResult{Answer: "Сервис заметок сейчас недоступен."}, nil
				}
				note, err := deps.Notes.CreateNote(ctx, input.User.UserID, &domain.CreateNoteRequest{
					Title:    title,
					Content:  firstString(action.Payload["content"]),
					Type:     domain.NoteTypeText,
					IsPinned: boolValue(action.Payload["is_pinned"]),
				})
				if err != nil {
					return nil, apperrors.ToAppError(err)
				}
				return &ToolResult{
					Answer: fmt.Sprintf("Заметка `%s` создана.", note.Title),
					Actions: []domain.AgentAction{{
						Type:    "note_created",
						Label:   "Note created",
						Payload: serializeNote(*note),
					}},
				}, nil
			},
		},
		&staticTool{
			name:                 "note_update",
			description:          "Обновляет заметку без изменения note repository logic и сам подтягивает актуальную version перед записью.",
			intents:              []domain.AgentIntentName{domain.AgentIntentUpdateNote},
			actions:              []string{"update_note"},
			requiresConfirmation: true,
			handler: func(ctx context.Context, input ToolRequest) (*ToolResult, *apperrors.AppError) {
				noteID := firstString(input.Entities["note_id"])
				title := firstString(input.Entities["title"])
				content := firstString(input.Entities["content"])
				if noteID == "" {
					return &ToolResult{
						Answer:      "Для изменения заметки нужен `note_id`. Сначала покажите заметки и выберите нужную.",
						Suggestions: []string{"Покажи мои заметки", "Покажи заметки по запросу"},
					}, nil
				}
				action := &domain.AgentAction{
					Type:  "update_note",
					Label: "Update note",
					Payload: map[string]interface{}{
						"note_id": noteID,
						"title":   title,
						"content": content,
					},
				}
				if !input.Confirmed {
					return &ToolResult{
						Answer:               "Подтвердите изменение заметки.",
						RequiresConfirmation: true,
						ProposedAction:       action,
					}, nil
				}
				if deps.Notes == nil {
					return &ToolResult{Answer: "Сервис заметок сейчас недоступен."}, nil
				}
				id, err := uuid.Parse(noteID)
				if err != nil {
					return nil, apperrors.BadRequest("note_id must be a valid UUID")
				}
				current, err := deps.Notes.GetNote(ctx, input.User.UserID, id)
				if err != nil {
					return nil, apperrors.ToAppError(err)
				}
				req := &domain.UpdateNoteRequest{Version: current.Version}
				if title != "" {
					req.Title = &title
				}
				if content != "" {
					req.Content = &content
				}
				updated, err := deps.Notes.UpdateNote(ctx, input.User.UserID, id, req)
				if err != nil {
					return nil, apperrors.ToAppError(err)
				}
				return &ToolResult{
					Answer: fmt.Sprintf("Заметка `%s` обновлена.", updated.Title),
					Actions: []domain.AgentAction{{
						Type:    "note_updated",
						Label:   "Note updated",
						Payload: serializeNote(*updated),
					}},
				}, nil
			},
		},
		&staticTool{
			name:                 "note_delete",
			description:          "Мягко удаляет заметку через существующий soft-delete flow после подтверждения.",
			intents:              []domain.AgentIntentName{domain.AgentIntentDeleteNote},
			actions:              []string{"delete_note"},
			requiresConfirmation: true,
			handler: func(ctx context.Context, input ToolRequest) (*ToolResult, *apperrors.AppError) {
				noteID := firstString(input.Entities["note_id"])
				if noteID == "" {
					return &ToolResult{
						Answer:      "Для удаления заметки нужен `note_id`.",
						Suggestions: []string{"Покажи мои заметки"},
					}, nil
				}
				action := &domain.AgentAction{
					Type:    "delete_note",
					Label:   "Delete note",
					Payload: map[string]interface{}{"note_id": noteID},
				}
				if !input.Confirmed {
					return &ToolResult{
						Answer:               "Подтвердите удаление заметки.",
						RequiresConfirmation: true,
						ProposedAction:       action,
					}, nil
				}
				if deps.Notes == nil {
					return &ToolResult{Answer: "Сервис заметок сейчас недоступен."}, nil
				}
				id, err := uuid.Parse(noteID)
				if err != nil {
					return nil, apperrors.BadRequest("note_id must be a valid UUID")
				}
				current, err := deps.Notes.GetNote(ctx, input.User.UserID, id)
				if err != nil {
					return nil, apperrors.ToAppError(err)
				}
				deleted, err := deps.Notes.SoftDeleteNote(ctx, input.User.UserID, id, current.Version)
				if err != nil {
					return nil, apperrors.ToAppError(err)
				}
				return &ToolResult{
					Answer: fmt.Sprintf("Заметка `%s` перемещена в корзину.", deleted.Title),
					Actions: []domain.AgentAction{{
						Type:    "note_deleted",
						Label:   "Note deleted",
						Payload: map[string]interface{}{"id": noteID, "title": deleted.Title},
					}},
				}, nil
			},
		},
		&staticTool{
			name:        "drive_files",
			description: "Ищет и агрегирует файлы из Team Drive через уже существующий drive service.",
			intents:     []domain.AgentIntentName{domain.AgentIntentGetDriveFiles},
			actions:     []string{"show_drive_files"},
			handler: func(ctx context.Context, input ToolRequest) (*ToolResult, *apperrors.AppError) {
				if deps.Drive == nil {
					return &ToolResult{Answer: "Сервис Team Drive сейчас недоступен."}, nil
				}
				query := firstString(input.Entities["query"])
				scope := firstString(input.Entities["scope"], "all")
				fileType := firstString(input.Entities["file_type"])
				items, appErr := loadDriveItems(ctx, deps.Drive, input.User.UserID, query, scope, fileType)
				if appErr != nil {
					return nil, appErr
				}
				payload := serializeDriveItems(items)
				return &ToolResult{
					Answer: buildDriveFilesAnswer(payload, query, scope),
					Actions: []domain.AgentAction{{
						Type:    "show_drive_files",
						Label:   "Show drive files",
						Payload: map[string]interface{}{"query": query, "scope": scope, "file_type": fileType, "files": payload},
					}},
				}, nil
			},
		},
		&staticTool{
			name:        "drive_access",
			description: "Показывает, у кого уже есть доступ к файлу или папке в Team Drive.",
			intents:     []domain.AgentIntentName{domain.AgentIntentGetDriveAccess},
			actions:     []string{"show_drive_access"},
			handler: func(ctx context.Context, input ToolRequest) (*ToolResult, *apperrors.AppError) {
				if deps.Drive == nil {
					return &ToolResult{Answer: "Сервис Team Drive сейчас недоступен."}, nil
				}
				resourceType, resourceID, appErr := resolveDriveResource(ctx, deps.Drive, input.User.UserID, firstString(input.Entities["resource_type"], "file"), firstString(input.Entities["resource_id"]), firstString(input.Entities["query"]))
				if appErr != nil {
					return nil, appErr
				}
				users, err := deps.Drive.GetUsersWithAccess(ctx, input.User.UserID, resourceType, resourceID)
				if err != nil {
					return nil, apperrors.ToAppError(err)
				}
				payload := make([]map[string]interface{}, 0, len(users))
				for _, user := range users {
					payload = append(payload, map[string]interface{}{
						"id":         user.ID,
						"name":       displayName(user.FirstName, user.LastName, user.Username),
						"email":      user.Email,
						"role":       string(user.Role),
						"avatar_url": user.AvatarURL,
					})
				}
				return &ToolResult{
					Answer: buildDriveAccessAnswer(payload),
					Actions: []domain.AgentAction{{
						Type:    "show_drive_access",
						Label:   "Show drive access",
						Payload: map[string]interface{}{"resource_type": string(resourceType), "resource_id": resourceID.String(), "users": payload},
					}},
				}, nil
			},
		},
		&staticTool{
			name:                 "drive_share",
			description:          "Выдаёт доступ к ресурсу в Team Drive через существующий permission flow после подтверждения.",
			intents:              []domain.AgentIntentName{domain.AgentIntentShareDrive},
			actions:              []string{"share_drive_resource"},
			requiresConfirmation: true,
			handler: func(ctx context.Context, input ToolRequest) (*ToolResult, *apperrors.AppError) {
				if deps.Drive == nil {
					return &ToolResult{Answer: "Сервис Team Drive сейчас недоступен."}, nil
				}
				targetUserIDs := contextInt64Slice(input.Request.Context, "target_user_ids")
				if len(targetUserIDs) == 0 {
					targetUserIDs = contextInt64Slice(input.Request.Context, "recipient_user_ids")
				}
				if len(targetUserIDs) == 0 {
					return &ToolResult{
						Answer:      "Для выдачи доступа нужны `target_user_ids` в контексте запроса.",
						Suggestions: []string{"Сначала найдите пользователя", "Уточните, кому открыть доступ"},
					}, nil
				}
				resourceType, resourceID, appErr := resolveDriveResource(ctx, deps.Drive, input.User.UserID, firstString(input.Entities["resource_type"], "file"), firstString(input.Entities["resource_id"]), firstString(input.Entities["query"]))
				if appErr != nil {
					return nil, appErr
				}
				role := normalizeDriveRole(firstString(input.Entities["role"], "viewer"))
				action := &domain.AgentAction{
					Type:  "share_drive_resource",
					Label: "Share drive resource",
					Payload: map[string]interface{}{
						"resource_type":   string(resourceType),
						"resource_id":     resourceID.String(),
						"target_user_ids": targetUserIDs,
						"role":            string(role),
					},
				}
				if !input.Confirmed {
					return &ToolResult{
						Answer:               "Подтвердите выдачу доступа к ресурсу Team Drive.",
						RequiresConfirmation: true,
						ProposedAction:       action,
					}, nil
				}
				for _, targetUserID := range targetUserIDs {
					if err := deps.Drive.GrantPermission(ctx, input.User.UserID, resourceType, resourceID, targetUserID, role); err != nil {
						return nil, apperrors.ToAppError(err)
					}
				}
				return &ToolResult{
					Answer: fmt.Sprintf("Доступ `%s` выдан %d пользователям.", role, len(targetUserIDs)),
					Actions: []domain.AgentAction{{
						Type:    "drive_shared",
						Label:   "Drive shared",
						Payload: action.Payload,
					}},
				}, nil
			},
		},
		&staticTool{
			name:                 "drive_share_link",
			description:          "Создаёт публичную ссылку на ресурс Team Drive через существующий share-link flow.",
			intents:              []domain.AgentIntentName{domain.AgentIntentCreateDriveLink},
			actions:              []string{"create_drive_share_link"},
			requiresConfirmation: true,
			handler: func(ctx context.Context, input ToolRequest) (*ToolResult, *apperrors.AppError) {
				if deps.Drive == nil {
					return &ToolResult{Answer: "Сервис Team Drive сейчас недоступен."}, nil
				}
				resourceType, resourceID, appErr := resolveDriveResource(ctx, deps.Drive, input.User.UserID, firstString(input.Entities["resource_type"], "file"), firstString(input.Entities["resource_id"]), firstString(input.Entities["query"]))
				if appErr != nil {
					return nil, appErr
				}
				role := string(normalizeDriveRole(firstString(input.Entities["role"], "viewer")))
				action := &domain.AgentAction{
					Type:  "create_drive_share_link",
					Label: "Create drive share link",
					Payload: map[string]interface{}{
						"resource_type": string(resourceType),
						"resource_id":   resourceID.String(),
						"role":          role,
						"ttl_hours":     intValueOrDefault(contextValue(input.Request.Context, "ttl_hours"), 24),
					},
				}
				if !input.Confirmed {
					return &ToolResult{
						Answer:               "Подтвердите создание публичной ссылки.",
						RequiresConfirmation: true,
						ProposedAction:       action,
					}, nil
				}
				ttlHours := intValueOrDefault(action.Payload["ttl_hours"], 24)
				link, err := deps.Drive.CreatePublicLink(ctx, input.User.UserID, string(resourceType), resourceID, role, time.Duration(ttlHours)*time.Hour)
				if err != nil {
					return nil, apperrors.ToAppError(err)
				}
				return &ToolResult{
					Answer: "Публичная ссылка создана.",
					Actions: []domain.AgentAction{{
						Type:    "drive_share_link_created",
						Label:   "Drive share link created",
						Payload: map[string]interface{}{"token": link.Token.String(), "resource_id": resourceID.String(), "role": role},
					}},
				}, nil
			},
		},
		&staticTool{
			name:        "notifications",
			description: "Показывает последние уведомления текущего пользователя.",
			intents:     []domain.AgentIntentName{domain.AgentIntentGetNotify},
			actions:     []string{"get_notifications"},
			handler: func(ctx context.Context, input ToolRequest) (*ToolResult, *apperrors.AppError) {
				if deps.Notifications == nil {
					return &ToolResult{Answer: "Сервис уведомлений недоступен."}, nil
				}
				items, err := deps.Notifications.GetUserNotifications(ctx, input.User.UserID, 5, 0)
				if err != nil {
					return nil, apperrors.InternalWithErr("failed to load notifications", err)
				}
				payload := make([]map[string]interface{}, 0, len(items))
				for _, item := range items {
					payload = append(payload, map[string]interface{}{
						"id":         item.ID.String(),
						"title":      item.Title,
						"type":       item.Type,
						"created_at": item.CreatedAt,
						"is_read":    item.IsRead,
					})
				}
				return &ToolResult{
					Answer: buildNotificationsAnswer(payload),
					Actions: []domain.AgentAction{{
						Type:    "show_notifications",
						Label:   "Show notifications",
						Payload: map[string]interface{}{"notifications": payload},
					}},
				}, nil
			},
		},
		&staticTool{
			name:                 "broadcast",
			description:          "Отправляет широковещательное сообщение через существующий chat broadcast flow после подтверждения.",
			intents:              []domain.AgentIntentName{domain.AgentIntentSendBroadcast},
			actions:              []string{"send_broadcast"},
			requiresConfirmation: true,
			handler: func(ctx context.Context, input ToolRequest) (*ToolResult, *apperrors.AppError) {
				if deps.Chat == nil {
					return &ToolResult{Answer: "Сервис чатов сейчас недоступен."}, nil
				}
				message := firstString(input.Entities["message"])
				recipientIDs := contextInt64Slice(input.Request.Context, "recipient_user_ids")
				if message == "" || len(recipientIDs) == 0 {
					return &ToolResult{
						Answer:      "Для рассылки нужны текст сообщения и `recipient_user_ids` в контексте запроса.",
						Suggestions: []string{"Уточните получателей", "Сначала найдите сотрудников"},
					}, nil
				}
				action := &domain.AgentAction{
					Type:  "send_broadcast",
					Label: "Send broadcast",
					Payload: map[string]interface{}{
						"message":            message,
						"recipient_user_ids": recipientIDs,
					},
				}
				if !input.Confirmed {
					return &ToolResult{
						Answer:               "Подтвердите отправку рассылки.",
						RequiresConfirmation: true,
						ProposedAction:       action,
					}, nil
				}
				resp, err := deps.Chat.BroadcastMessage(ctx, input.User.UserID, &service.BroadcastMessageRequest{
					RecipientUserIDs: recipientIDs,
					Content:          message,
					MessageType:      "text",
				})
				if err != nil {
					return nil, apperrors.ToAppError(err)
				}
				return &ToolResult{
					Answer: fmt.Sprintf("Рассылка отправлена. Успешно: %d, ошибок: %d.", resp.SuccessCount, resp.FailureCount),
					Actions: []domain.AgentAction{{
						Type:    "broadcast_sent",
						Label:   "Broadcast sent",
						Payload: map[string]interface{}{"success_count": resp.SuccessCount, "failure_count": resp.FailureCount},
					}},
				}, nil
			},
		},
		&staticTool{
			name:                 "call",
			description:          "Готовит frontend action для звонка после подтверждения.",
			intents:              []domain.AgentIntentName{domain.AgentIntentStartCall},
			actions:              []string{"start_call"},
			requiresConfirmation: true,
			handler: func(_ context.Context, input ToolRequest) (*ToolResult, *apperrors.AppError) {
				target := firstString(input.Entities["target"])
				action := &domain.AgentAction{
					Type:    "start_call",
					Label:   "Start call",
					Payload: map[string]interface{}{"target": target, "callee_id": contextValue(input.Request.Context, "callee_id")},
				}
				if !input.Confirmed {
					return &ToolResult{
						Answer:               "Подтвердите запуск звонка.",
						RequiresConfirmation: true,
						ProposedAction:       action,
					}, nil
				}
				return &ToolResult{
					Answer:  fmt.Sprintf("Запускаю звонок с `%s`.", target),
					Actions: []domain.AgentAction{*action},
				}, nil
			},
		},
		&staticTool{
			name:        "summary",
			description: "Делает краткое резюме последних сообщений текущей сессии.",
			intents:     []domain.AgentIntentName{domain.AgentIntentSummarize},
			actions:     []string{"summarize"},
			handler: func(_ context.Context, input ToolRequest) (*ToolResult, *apperrors.AppError) {
				if len(input.History) == 0 {
					return &ToolResult{Answer: "В текущей сессии пока нет истории для резюме."}, nil
				}
				last := input.History
				if len(last) > 6 {
					last = last[len(last)-6:]
				}
				lines := make([]string, 0, len(last))
				for _, item := range last {
					lines = append(lines, fmt.Sprintf("%s: %s", item.Role, truncate(item.Content, 80)))
				}
				return &ToolResult{
					Answer: "Краткое резюме последних сообщений:\n" + strings.Join(lines, "\n"),
				}, nil
			},
		},
		&staticTool{
			name:                 "form_fill",
			description:          "Готовит frontend payload для автозаполнения формы после подтверждения.",
			intents:              []domain.AgentIntentName{domain.AgentIntentFillForm},
			actions:              []string{"fill_form"},
			requiresConfirmation: true,
			handler: func(_ context.Context, input ToolRequest) (*ToolResult, *apperrors.AppError) {
				formName := firstString(input.Entities["form_name"])
				action := &domain.AgentAction{
					Type:    "fill_form",
					Label:   "Fill form",
					Payload: map[string]interface{}{"form_name": formName, "context": input.Request.Context},
				}
				if !input.Confirmed {
					return &ToolResult{
						Answer:               "Подтвердите автозаполнение формы.",
						RequiresConfirmation: true,
						ProposedAction:       action,
					}, nil
				}
				return &ToolResult{
					Answer:  fmt.Sprintf("Подготовил данные для формы `%s`.", formName),
					Actions: []domain.AgentAction{*action},
				}, nil
			},
		},
		&staticTool{
			name:        "status",
			description: "Возвращает статус ассистента и текущую конфигурацию моделей orchestration слоя.",
			intents:     []domain.AgentIntentName{domain.AgentIntentGetStatus},
			actions:     []string{"get_status"},
			handler: func(_ context.Context, input ToolRequest) (*ToolResult, *apperrors.AppError) {
				metadata := map[string]interface{}{
					"assistant_status": "ok",
					"session_id":       input.Session.ID,
				}
				if deps.AgentConfig != nil {
					metadata["router_model"] = deps.AgentConfig.RouterModel
					metadata["executor_model"] = deps.AgentConfig.ExecutorModel
					metadata["fallback_model"] = deps.AgentConfig.FallbackModel
				}
				return &ToolResult{
					Answer:   "Сервис ассистента доступен.",
					Metadata: metadata,
				}, nil
			},
		},
		&staticTool{
			name:        "help",
			description: "Показывает основные возможности smart agent для фронта и пользователя.",
			intents:     []domain.AgentIntentName{domain.AgentIntentHelp, domain.AgentIntentUnknown},
			actions:     []string{"help"},
			handler: func(_ context.Context, _ ToolRequest) (*ToolResult, *apperrors.AppError) {
				return &ToolResult{
					Answer: "Я могу помочь с задачами, календарём, онлайн-встречами, заметками, файлами Drive, уведомлениями, рассылками, поиском и навигацией по приложению.",
					Suggestions: []string{
						"Покажи мои задачи на сегодня",
						"Покажи сводку задач по доскам",
						"Что у меня в календаре сегодня",
						"Создай онлайн-встречу завтра в 15:00 15:30",
						"Покажи мои заметки",
						"Найди pdf в Team Drive",
					},
				}, nil
			},
		},
		&staticTool{
			name:                 "task_create",
			description:          "Создаёт задачу в канбане после подтверждения через существующий backend service.",
			intents:              []domain.AgentIntentName{domain.AgentIntentCreateTask},
			actions:              []string{"create_task"},
			requiresConfirmation: true,
			handler: func(ctx context.Context, input ToolRequest) (*ToolResult, *apperrors.AppError) {
				title := firstString(input.Entities["title"])
				action := &domain.AgentAction{
					Type:    "create_task",
					Label:   "Create task",
					Payload: map[string]interface{}{"title": title},
				}
				if !input.Confirmed {
					return &ToolResult{
						Answer:               "Подтвердите создание задачи.",
						RequiresConfirmation: true,
						ProposedAction:       action,
					}, nil
				}
				if deps.Kanban == nil || deps.BoardRepo == nil {
					return &ToolResult{Answer: "Kanban service не подключён."}, nil
				}
				board, err := deps.BoardRepo.GetBoardByOwnerID(ctx, input.User.UserID)
				if err != nil || board == nil {
					if ensureErr := deps.BoardRepo.EnsureDefaultBoard(ctx, input.User.UserID); ensureErr == nil {
						board, _ = deps.BoardRepo.GetBoardByOwnerID(ctx, input.User.UserID)
					}
				}
				if board == nil {
					return nil, apperrors.BadRequest("default board not found")
				}
				task, err := deps.Kanban.CreateTask(ctx, input.User.UserID, service.CreateTaskRequest{
					BoardID:         board.ID,
					Title:           title,
					AssigneeUserIDs: []int64{input.User.UserID},
				})
				if err != nil {
					return nil, apperrors.ToAppError(err)
				}
				return &ToolResult{
					Answer: fmt.Sprintf("Задача `%s` создана.", title),
					Actions: []domain.AgentAction{{
						Type:    "task_created",
						Label:   "Task created",
						Payload: map[string]interface{}{"task_id": task.ID.String()},
					}},
				}, nil
			},
		},
		&staticTool{
			name:                 "task_update",
			description:          "Закрывает задачу через существующий kanban flow и сам находит DONE-колонку после подтверждения.",
			intents:              []domain.AgentIntentName{domain.AgentIntentUpdateTask},
			actions:              []string{"update_task"},
			requiresConfirmation: true,
			handler: func(ctx context.Context, input ToolRequest) (*ToolResult, *apperrors.AppError) {
				taskIDRaw := firstString(input.Entities["task_id"])
				titleOrQuery := firstString(input.Entities["title"])
				action := &domain.AgentAction{
					Type:    "update_task",
					Label:   "Update task",
					Payload: map[string]interface{}{"task_id": taskIDRaw, "title": titleOrQuery},
				}
				if !input.Confirmed {
					return &ToolResult{
						Answer:               "Подтвердите изменение задачи.",
						RequiresConfirmation: true,
						ProposedAction:       action,
					}, nil
				}
				if deps.Kanban == nil {
					return &ToolResult{Answer: "Сервис задач сейчас недоступен."}, nil
				}
				taskID, taskTitle, boardID, appErr := resolveTask(ctx, deps.Kanban, input.User.UserID, taskIDRaw, titleOrQuery)
				if appErr != nil {
					return nil, appErr
				}
				board, err := deps.Kanban.GetBoard(ctx, input.User.UserID, boardID)
				if err != nil {
					return nil, apperrors.ToAppError(err)
				}
				doneColumnID := findDoneColumn(board)
				if doneColumnID == uuid.Nil {
					return nil, apperrors.BadRequest("DONE column not found on board")
				}
				if err := deps.Kanban.MoveTask(ctx, input.User.UserID, taskID, doneColumnID); err != nil {
					return nil, apperrors.ToAppError(err)
				}
				return &ToolResult{
					Answer: fmt.Sprintf("Задача `%s` перенесена в DONE.", taskTitle),
					Actions: []domain.AgentAction{{
						Type:    "task_updated",
						Label:   "Task updated",
						Payload: map[string]interface{}{"task_id": taskID.String(), "status": "DONE"},
					}},
				}, nil
			},
		},
	}
}

func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max] + "..."
}

func stringValue(value interface{}) string {
	if s, ok := value.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

func firstString(values ...interface{}) string {
	for _, value := range values {
		if s, ok := value.(string); ok {
			s = strings.TrimSpace(s)
			if s != "" {
				return s
			}
		}
	}
	return ""
}

func boolValue(value interface{}) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		typed = strings.TrimSpace(strings.ToLower(typed))
		return typed == "true" || typed == "1" || typed == "yes"
	default:
		return false
	}
}

func intValue(value interface{}) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case string:
		var parsed int
		if _, err := fmt.Sscanf(strings.TrimSpace(typed), "%d", &parsed); err == nil {
			return parsed
		}
	}
	return 0
}

func intValueOrDefault(value interface{}, fallback int) int {
	if parsed := intValue(value); parsed != 0 {
		return parsed
	}
	return fallback
}

func contextValue(ctx map[string]interface{}, key string) interface{} {
	if ctx == nil {
		return nil
	}
	return ctx[key]
}

func contextBool(ctx map[string]interface{}, key string) bool {
	return boolValue(contextValue(ctx, key))
}

func contextInt64Slice(ctx map[string]interface{}, key string) []int64 {
	value := contextValue(ctx, key)
	switch typed := value.(type) {
	case []int64:
		return uniqueInt64(typed)
	case []interface{}:
		result := make([]int64, 0, len(typed))
		for _, item := range typed {
			switch num := item.(type) {
			case int:
				result = append(result, int64(num))
			case int64:
				result = append(result, num)
			case float64:
				result = append(result, int64(num))
			}
		}
		return uniqueInt64(result)
	default:
		return nil
	}
}

func uniqueInt64(items []int64) []int64 {
	seen := make(map[int64]struct{}, len(items))
	result := make([]int64, 0, len(items))
	for _, item := range items {
		if item <= 0 {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		result = append(result, item)
	}
	return result
}

func localDayBounds(entities map[string]interface{}, dayOffset int) (time.Time, time.Time) {
	loc := mustLocation(firstString(entities["timezone"]))
	now := time.Now().In(loc).AddDate(0, 0, dayOffset)
	startLocal := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	endLocal := startLocal.Add(24*time.Hour - time.Second)
	return startLocal.UTC(), endLocal.UTC()
}

func calendarBounds(entities map[string]interface{}) (time.Time, time.Time) {
	switch firstString(entities["period"]) {
	case "tomorrow":
		return localDayBounds(entities, 1)
	case "week":
		loc := mustLocation(firstString(entities["timezone"]))
		now := time.Now().In(loc)
		startLocal := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
		endLocal := startLocal.Add(7*24*time.Hour - time.Second)
		return startLocal.UTC(), endLocal.UTC()
	default:
		return localDayBounds(entities, 0)
	}
}

func mustLocation(name string) *time.Location {
	if strings.TrimSpace(name) == "" {
		name = "Asia/Almaty"
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.FixedZone("UTC+5", 5*60*60)
	}
	return loc
}

func displayName(firstName, lastName, fallback string) string {
	fullName := strings.TrimSpace(strings.TrimSpace(firstName) + " " + strings.TrimSpace(lastName))
	if fullName != "" {
		return fullName
	}
	return strings.TrimSpace(fallback)
}

func serializeTasks(tasks []*domain.Task) []map[string]interface{} {
	items := make([]map[string]interface{}, 0, len(tasks))
	for _, task := range tasks {
		item := map[string]interface{}{
			"id":       task.ID.String(),
			"title":    task.Title,
			"priority": task.Priority,
			"status":   task.Status,
			"board_id": task.BoardID.String(),
		}
		if task.DueAt != nil {
			item["due_at"] = task.DueAt.Format(time.RFC3339)
		}
		items = append(items, item)
	}
	return items
}

func serializeEvent(event domain.Event) map[string]interface{} {
	payload := map[string]interface{}{
		"id":            event.ID.String(),
		"title":         event.Title,
		"start_time":    event.StartTime.Format(time.RFC3339),
		"end_time":      event.EndTime.Format(time.RFC3339),
		"is_online":     event.IsOnline,
		"meeting_url":   "",
		"meeting_room":  "",
		"meeting_state": event.MeetingStatus,
	}
	if event.MeetingURL != nil {
		payload["meeting_url"] = *event.MeetingURL
	}
	if event.LiveKitRoomName != nil {
		payload["meeting_room"] = *event.LiveKitRoomName
	}
	return payload
}

func serializeEvents(events []domain.Event) []map[string]interface{} {
	items := make([]map[string]interface{}, 0, len(events))
	for _, event := range events {
		items = append(items, serializeEvent(event))
	}
	return items
}

func serializeNote(note domain.Note) map[string]interface{} {
	text := strings.TrimSpace(note.Content)
	if text == "" && note.ContentDoc != nil {
		text = strings.TrimSpace(note.ContentDoc.ExtractPlainText())
	}
	return map[string]interface{}{
		"id":          note.ID.String(),
		"title":       note.Title,
		"content":     text,
		"is_pinned":   note.IsPinned,
		"is_archived": note.IsArchived,
		"version":     note.Version,
		"updated_at":  note.UpdatedAt.Format(time.RFC3339),
	}
}

func serializeNotes(notes []domain.Note) []map[string]interface{} {
	items := make([]map[string]interface{}, 0, len(notes))
	for _, note := range notes {
		items = append(items, serializeNote(note))
	}
	return items
}

func serializeDriveItems(items []domain.DriveItem) []map[string]interface{} {
	payload := make([]map[string]interface{}, 0, len(items))
	for _, item := range items {
		serialized := map[string]interface{}{
			"type": string(item.Type),
		}
		if item.File != nil {
			serialized["id"] = item.File.ID.String()
			serialized["name"] = item.File.RealName
			serialized["owner_fio"] = item.File.OwnerFio
			serialized["size"] = item.File.Size
			serialized["mime_type"] = item.File.MimeType
			serialized["download_url"] = item.File.DownloadURL
		}
		if item.Folder != nil {
			serialized["id"] = item.Folder.ID.String()
			serialized["name"] = item.Folder.Name
			serialized["owner_fio"] = item.Folder.OwnerFio
		}
		payload = append(payload, serialized)
	}
	return payload
}

func serializeSlots(slots []domain.CalendarSuggestedSlot) []map[string]interface{} {
	payload := make([]map[string]interface{}, 0, len(slots))
	for _, slot := range slots {
		payload = append(payload, map[string]interface{}{
			"type":                slot.Type,
			"start_time":          slot.StartTime.Format(time.RFC3339),
			"end_time":            slot.EndTime.Format(time.RFC3339),
			"label":               slot.Label,
			"score":               slot.Score,
			"available_user_ids":  slot.AvailableUserIDs,
			"conflicted_user_ids": slot.ConflictedUserIDs,
			"available_users":     slot.AvailableUsers,
			"conflicted_users":    slot.ConflictedUsers,
		})
	}
	return payload
}

func buildCreateEventRequest(payload map[string]interface{}) (*domain.CreateEventRequest, *apperrors.AppError) {
	start, err := time.Parse(time.RFC3339, firstString(payload["start_time"]))
	if err != nil {
		return nil, apperrors.BadRequest("start_time must be in RFC3339 format")
	}
	end, err := time.Parse(time.RFC3339, firstString(payload["end_time"]))
	if err != nil {
		return nil, apperrors.BadRequest("end_time must be in RFC3339 format")
	}
	var reminder *int
	if minutes := intValue(payload["reminder_minutes"]); minutes > 0 {
		reminder = &minutes
	}
	return &domain.CreateEventRequest{
		Title:           firstString(payload["title"]),
		StartTime:       start,
		EndTime:         end,
		IsOnline:        boolValue(payload["is_online"]),
		AttendeeIDs:     toInt64Slice(payload["attendee_ids"]),
		ReminderMinutes: reminder,
	}, nil
}

func toInt64Slice(value interface{}) []int64 {
	switch typed := value.(type) {
	case []int64:
		return uniqueInt64(typed)
	case []interface{}:
		result := make([]int64, 0, len(typed))
		for _, item := range typed {
			switch num := item.(type) {
			case int:
				result = append(result, int64(num))
			case int64:
				result = append(result, num)
			case float64:
				result = append(result, int64(num))
			}
		}
		return uniqueInt64(result)
	default:
		return nil
	}
}

func loadDriveItems(ctx context.Context, drive *service.DriveService, userID int64, query, scope, fileType string) ([]domain.DriveItem, *apperrors.AppError) {
	var (
		items []domain.DriveItem
		err   error
	)
	if strings.TrimSpace(query) != "" {
		items, err = drive.SearchAndFilter(ctx, userID, query, fileType, 20, 0, nil, "")
		return items, apperrors.ToAppError(err)
	}
	switch scope {
	case "own":
		items, err = drive.ListOwnItems(ctx, userID, 20, 0)
	case "shared":
		items, err = drive.ListSharedItems(ctx, userID, 20, 0)
	default:
		var own, shared []domain.DriveItem
		own, shared, err = drive.ListAllDriveItems(ctx, userID, 20, 0)
		if err == nil {
			items = append(items, own...)
			items = append(items, shared...)
		}
	}
	return items, apperrors.ToAppError(err)
}

func resolveDriveResource(ctx context.Context, drive *service.DriveService, userID int64, rawType, rawID, query string) (domain.ResourceType, uuid.UUID, *apperrors.AppError) {
	resourceType := domain.ResourceFile
	if strings.EqualFold(strings.TrimSpace(rawType), string(domain.ResourceFolder)) {
		resourceType = domain.ResourceFolder
	}
	if strings.TrimSpace(rawID) != "" {
		id, err := uuid.Parse(strings.TrimSpace(rawID))
		if err != nil {
			return resourceType, uuid.Nil, apperrors.BadRequest("resource_id must be a valid UUID")
		}
		return resourceType, id, nil
	}
	if strings.TrimSpace(query) == "" {
		return resourceType, uuid.Nil, apperrors.BadRequest("resource_id or query is required")
	}
	items, appErr := loadDriveItems(ctx, drive, userID, query, "all", "")
	if appErr != nil {
		return resourceType, uuid.Nil, appErr
	}
	for _, item := range items {
		if resourceType == domain.ResourceFolder && item.Folder != nil {
			return resourceType, item.Folder.ID, nil
		}
		if resourceType == domain.ResourceFile && item.File != nil {
			return resourceType, item.File.ID, nil
		}
	}
	return resourceType, uuid.Nil, apperrors.NotFound("drive resource")
}

func resolveEvent(ctx context.Context, calendar *service.CalendarService, userID int64, eventIDRaw, query string) (uuid.UUID, string, *apperrors.AppError) {
	if calendar == nil {
		return uuid.Nil, "", apperrors.Internal("calendar service is not configured")
	}
	if strings.TrimSpace(eventIDRaw) != "" {
		id, err := uuid.Parse(strings.TrimSpace(eventIDRaw))
		if err != nil {
			return uuid.Nil, "", apperrors.BadRequest("event_id must be a valid UUID")
		}
		event, err := calendar.GetEvent(ctx, userID, id)
		if err != nil {
			return uuid.Nil, "", apperrors.ToAppError(err)
		}
		return id, event.Title, nil
	}
	from := time.Now()
	to := from.AddDate(0, 0, 30)
	events, err := calendar.ListEvents(ctx, userID, userID, from, to)
	if err != nil {
		return uuid.Nil, "", apperrors.ToAppError(err)
	}
	for _, event := range events {
		if strings.Contains(strings.ToLower(event.Title), strings.ToLower(query)) {
			return event.ID, event.Title, nil
		}
	}
	return uuid.Nil, "", apperrors.NotFound("event")
}

func resolveTask(ctx context.Context, kanban *service.KanbanService, userID int64, taskIDRaw, query string) (uuid.UUID, string, uuid.UUID, *apperrors.AppError) {
	if strings.TrimSpace(taskIDRaw) != "" {
		id, err := uuid.Parse(strings.TrimSpace(taskIDRaw))
		if err != nil {
			return uuid.Nil, "", uuid.Nil, apperrors.BadRequest("task_id must be a valid UUID")
		}
		task, err := kanban.GetTask(ctx, userID, id)
		if err != nil {
			return uuid.Nil, "", uuid.Nil, apperrors.ToAppError(err)
		}
		return id, task.Title, task.BoardID, nil
	}
	tasks, err := kanban.SearchTasks(ctx, userID, service.SearchTasksRequest{Query: query, Limit: 10})
	if err != nil {
		return uuid.Nil, "", uuid.Nil, apperrors.ToAppError(err)
	}
	if len(tasks) == 0 {
		return uuid.Nil, "", uuid.Nil, apperrors.NotFound("task")
	}
	return tasks[0].ID, tasks[0].Title, tasks[0].BoardID, nil
}

func findDoneColumn(board *domain.BoardResponse) uuid.UUID {
	if board == nil {
		return uuid.Nil
	}
	for _, column := range board.Columns {
		if column == nil || column.Column == nil {
			continue
		}
		key := strings.ToUpper(strings.TrimSpace(column.Column.Key))
		name := strings.ToUpper(strings.TrimSpace(column.Column.Name))
		if key == "DONE" || name == "DONE" {
			return column.Column.ID
		}
	}
	return uuid.Nil
}

func normalizeDriveRole(raw string) domain.PermissionRole {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "owner":
		return domain.RoleOwner
	case "editor":
		return domain.RoleEditor
	default:
		return domain.RoleViewer
	}
}

func buildBoardListAnswer(boards []map[string]interface{}) string {
	if len(boards) == 0 {
		return "У вас пока нет доступных канбан-досок."
	}
	lines := make([]string, 0, len(boards)+1)
	lines = append(lines, fmt.Sprintf("Доступно %d канбан-досок:", len(boards)))
	for _, board := range boards {
		lines = append(lines, fmt.Sprintf("- %s", firstString(board["name"])))
	}
	return strings.Join(lines, "\n")
}

func buildTaskSummaryAnswer(totalActive int, boards []map[string]interface{}) string {
	if len(boards) == 0 {
		return "Сейчас у вас нет активных задач."
	}
	lines := []string{fmt.Sprintf("У вас %d активных задач в %d досках.", totalActive, len(boards))}
	for _, board := range boards {
		lines = append(lines, fmt.Sprintf("- %s: %d активных", firstString(board["board_name"]), intValue(board["task_count"])))
	}
	return strings.Join(lines, "\n")
}

func buildTasksOverviewAnswer(items []map[string]interface{}, period string) string {
	if len(items) == 0 {
		switch period {
		case "tomorrow":
			return "На завтра активных задач не найдено."
		default:
			return "Активных задач не найдено."
		}
	}
	firstTitle := firstString(items[0]["title"])
	if len(items) == 1 {
		return fmt.Sprintf("У вас 1 активная задача: %s.", firstTitle)
	}
	return fmt.Sprintf("У вас %d активные задачи. Первая: %s.", len(items), firstTitle)
}

func buildCalendarOverviewAnswer(items []map[string]interface{}, period string) string {
	if len(items) == 0 {
		switch period {
		case "tomorrow":
			return "На завтра встреч в календаре нет."
		default:
			return "На сегодня встреч в календаре нет."
		}
	}
	firstTitle := firstString(items[0]["title"])
	if len(items) == 1 {
		return fmt.Sprintf("У вас 1 встреча: %s.", firstTitle)
	}
	return fmt.Sprintf("У вас %d встреч. Ближайшая: %s.", len(items), firstTitle)
}

func buildMeetingCreatedAnswer(event map[string]interface{}) string {
	answer := fmt.Sprintf("Онлайн-встреча `%s` создана.", firstString(event["title"]))
	if meetingURL := firstString(event["meeting_url"]); meetingURL != "" {
		answer += "\nСсылка: " + meetingURL
	}
	return answer
}

func buildNoteFoldersAnswer(folders []map[string]interface{}) string {
	if len(folders) == 0 {
		return "У вас нет папок с заметками."
	}
	lines := []string{fmt.Sprintf("Папки заметок (%d):", len(folders))}
	for _, folder := range folders {
		lines = append(lines, fmt.Sprintf("- %s", firstString(folder["name"])))
	}
	return strings.Join(lines, "\n")
}

func buildNotesAnswer(notes []map[string]interface{}, query string) string {
	if len(notes) == 0 {
		if query != "" {
			return fmt.Sprintf("Заметок по запросу `%s` не найдено.", query)
		}
		return "У вас пока нет заметок."
	}
	lines := make([]string, 0, len(notes)+1)
	if query != "" {
		lines = append(lines, fmt.Sprintf("Найдено заметок по запросу `%s`: %d.", query, len(notes)))
	} else {
		lines = append(lines, fmt.Sprintf("Всего заметок: %d.", len(notes)))
	}
	for _, note := range notes {
		title := firstString(note["title"])
		preview := truncate(firstString(note["content"]), 80)
		if preview != "" {
			lines = append(lines, fmt.Sprintf("- %s — %s", title, preview))
		} else {
			lines = append(lines, fmt.Sprintf("- %s", title))
		}
	}
	return strings.Join(lines, "\n")
}

func buildDriveFilesAnswer(items []map[string]interface{}, query, scope string) string {
	if len(items) == 0 {
		if query != "" {
			return fmt.Sprintf("В Team Drive не найдено файлов по запросу `%s`.", query)
		}
		return "В Team Drive не найдено доступных файлов."
	}
	scopeLabel := map[string]string{
		"all":    "доступных",
		"own":    "ваших",
		"shared": "расшаренных вам",
		"recent": "недавних",
	}[scope]
	if scopeLabel == "" {
		scopeLabel = "доступных"
	}
	lines := make([]string, 0, len(items)+1)
	if query != "" {
		lines = append(lines, fmt.Sprintf("Найдено файлов по запросу `%s`: %d.", query, len(items)))
	} else {
		lines = append(lines, fmt.Sprintf("Найдено %s файлов: %d.", scopeLabel, len(items)))
	}
	for _, item := range items {
		lines = append(lines, fmt.Sprintf("- %s", firstString(item["name"])))
	}
	return strings.Join(lines, "\n")
}

func buildDriveAccessAnswer(users []map[string]interface{}) string {
	if len(users) == 0 {
		return "Я не нашёл пользователей с явным доступом к этому ресурсу."
	}
	lines := []string{fmt.Sprintf("Доступ есть у %d пользователей:", len(users))}
	for _, user := range users {
		lines = append(lines, fmt.Sprintf("- %s (%s)", firstString(user["name"]), firstString(user["role"])))
	}
	return strings.Join(lines, "\n")
}

func buildNotificationsAnswer(items []map[string]interface{}) string {
	if len(items) == 0 {
		return "Новых уведомлений нет."
	}
	lines := []string{fmt.Sprintf("Последние уведомления: %d.", len(items))}
	for _, item := range items {
		lines = append(lines, fmt.Sprintf("- %s", firstString(item["title"])))
	}
	return strings.Join(lines, "\n")
}

func buildSlotsAnswer(slots []map[string]interface{}, date string) string {
	if len(slots) == 0 {
		return fmt.Sprintf("На %s свободных слотов не найдено.", date)
	}
	lines := []string{fmt.Sprintf("Нашёл %d свободных слотов на %s:", len(slots), date)}
	for _, slot := range slots {
		lines = append(lines, fmt.Sprintf("- %s", firstString(slot["label"])))
	}
	return strings.Join(lines, "\n")
}
