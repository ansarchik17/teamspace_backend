package service

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/aiaccess"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/i18n"
	"go.uber.org/zap"
)

func getToolsSchema() []LLMTool {
	tools := []LLMTool{
		{Type: "function", Function: LLMFunction{Name: "search_file_content", Description: "Найти сведения в содержимом доступных пользователю файлов Drive. Возвращает фрагменты и источники; пустой результат при indexing=true не означает отсутствия информации.", Parameters: map[string]interface{}{
			"type": "object", "additionalProperties": false, "properties": map[string]interface{}{"question": map[string]string{"type": "string"}, "file_ids": map[string]interface{}{"type": "array", "maxItems": 32, "items": map[string]string{"type": "string"}}}, "required": []string{"question"},
		}}},
		{
			Type: "function",
			Function: LLMFunction{
				Name:        "search_users",
				Description: "Искать сотрудников по имени.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"query": map[string]string{"type": "string"},
					},
					"required": []string{"query"},
				},
			},
		},
		{
			Type: "function",
			Function: LLMFunction{
				Name:        "get_user_events",
				Description: "Получить события календаря для проверки занятости.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"user_id":   map[string]string{"type": "integer"},
						"from_date": map[string]string{"type": "string", "description": "ISO 8601"},
						"to_date":   map[string]string{"type": "string", "description": "ISO 8601"},
					},
					"required": []string{"user_id", "from_date", "to_date"},
				},
			},
		},
		{
			Type: "function",
			Function: LLMFunction{
				Name:        "create_calendar_event",
				Description: "Создать встречу/событие в календаре.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"title":        map[string]string{"type": "string"},
						"description":  map[string]string{"type": "string"},
						"start_time":   map[string]string{"type": "string", "description": "ISO 8601"},
						"end_time":     map[string]string{"type": "string", "description": "ISO 8601"},
						"attendee_ids": map[string]interface{}{"type": "array", "items": map[string]string{"type": "integer"}},
					},
					"required": []string{"title", "start_time", "end_time"},
				},
			},
		},
		{
			Type: "function",
			Function: LLMFunction{
				Name:        "set_event_reminder",
				Description: "Установить напоминание (пуш) для события календаря.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"event_id":       map[string]string{"type": "string", "description": "ID события (UUID). Если известен - используй его."},
						"event_query":    map[string]string{"type": "string", "description": "Название события (если ID неизвестен)"},
						"minutes_before": map[string]string{"type": "integer", "description": "За сколько минут напомнить"},
					},
					"required": []string{"minutes_before"},
				},
			},
		},
		{
			Type: "function",
			Function: LLMFunction{
				Name:        "cancel_calendar_event",
				Description: "Отменить встречу/событие в календаре.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"event_id":    map[string]string{"type": "string", "description": "ID события (UUID). Если известен - используй его."},
						"event_query": map[string]string{"type": "string", "description": "Название события (если ID неизвестен)"},
					},
				},
			},
		},
		{
			Type: "function",
			Function: LLMFunction{
				Name:        "create_kanban_task",
				Description: "Создать задачу на канбан доске.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"title":          map[string]string{"type": "string"},
						"description":    map[string]string{"type": "string"},
						"priority":       map[string]interface{}{"type": "string", "enum": []string{"low", "medium", "high", "critical"}},
						"estimate_hours": map[string]string{"type": "integer"},
						"assignee_ids":   map[string]interface{}{"type": "array", "items": map[string]string{"type": "integer"}},
						"due_at":         map[string]string{"type": "string", "description": "ISO 8601"},
						"board_id":       map[string]string{"type": "string", "description": "UUID доски. Если не указан - используется доска по умолчанию."},
					},
					"required": []string{"title", "priority"},
				},
			},
		},
		{
			Type: "function",
			Function: LLMFunction{
				Name:        "close_kanban_task",
				Description: "Переместить задачу в статус DONE (закрыть/удалить).",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"task_id":    map[string]string{"type": "string", "description": "ID задачи (UUID). Если известен - используй его."},
						"task_query": map[string]string{"type": "string", "description": "Название задачи для поиска"},
					},
					"required": []string{"task_query"},
				},
			},
		},
		{
			Type: "function",
			Function: LLMFunction{
				Name:        "get_kanban_tasks",
				Description: "Получить список задач. Можно фильтровать по исполнителю, доске или тексту.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"assignee_id": map[string]string{"type": "integer", "description": "ID пользователя, на которого назначена задача."},
						"board_id":    map[string]string{"type": "string", "description": "ID доски (UUID)."},
						"query":       map[string]string{"type": "string", "description": "Поиск по названию/описанию."},
						"status":      map[string]string{"type": "string", "description": "Статус (TODO, IN_PROGRESS, DONE). Можно несколько через запятую."},
						"date_filter": map[string]string{"type": "string", "description": "YYYY-MM-DD optional"},
					},
				},
			},
		},
		{
			Type: "function",
			Function: LLMFunction{
				Name:        "get_kanban_boards",
				Description: "Получить список доступных канбан-досок.",
				Parameters: map[string]interface{}{
					"type":       "object",
					"properties": map[string]interface{}{},
				},
			},
		},
		{
			Type: "function",
			Function: LLMFunction{
				Name:        "send_broadcast",
				Description: "Отправить сообщение нескольким пользователям.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"message":            map[string]string{"type": "string"},
						"recipient_user_ids": map[string]interface{}{"type": "array", "items": map[string]string{"type": "integer"}},
					},
					"required": []string{"message", "recipient_user_ids"},
				},
			},
		},
		{
			Type: "function",
			Function: LLMFunction{
				Name:        "create_online_meeting",
				Description: "Создать онлайн видео-звонок/совещание через внешний планировщик.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"meeting_name": map[string]string{"type": "string", "description": "Название встречи"},
						"from_date":    map[string]string{"type": "string", "description": "Дата и время начала (ISO 8601), например 2026-01-21T18:00:00"},
						"to_date":      map[string]string{"type": "string", "description": "Дата и время окончания (ISO 8601), например 2026-01-21T19:00:00"},
						"attendee_ids": map[string]interface{}{"type": "array", "items": map[string]string{"type": "integer"}, "description": "Список ID участников (используй search_users, чтобы найти ID)"},
					},
					"required": []string{"meeting_name", "from_date", "to_date"},
				},
			},
		},
		{
			Type: "function",
			Function: LLMFunction{
				Name:        "web_search",
				Description: "Искать информацию в интернете для ответов на общие вопросы, новости, поиска биографий, погоды или фактов вне пространства Alem Workspace.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"query": map[string]string{"type": "string"},
					},
					"required": []string{"query"},
				},
			},
		},
	}
	for i := range tools {
		if _, ok := chatActionPartyField(tools[i].Function.Name); ok {
			properties := tools[i].Function.Parameters["properties"].(map[string]interface{})
			properties["participant_query"] = map[string]string{"type": "string", "description": "Точная ссылка на исполнителя, получателя или участников из сообщения пользователя (имя, логин или выбранная группа). Не придумывай. ID разрешай через поиск. Передавай participant_query вместе с ID, даже если получатель найден. Сохраняй логин/имя из предыдущих сообщений пользователя, не заменяй его отображаемым именем из справочника. Для явно личного события можно не указывать."}
		}
	}
	return tools
}

func (s *SmartAgentService) executeTool(ctx context.Context, executorUserID int64, name string, argsJSON string) (string, *SmartAgentWidget) {
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return i18n.TextContext(ctx, "error.INTERNAL_ERROR", nil), nil
	}

	switch name {
	case "search_file_content":
		if s.fileSearch == nil {
			return "Поиск по содержимому файлов недоступен.", nil
		}
		query, _ := args["question"].(string)
		ids := []string{}
		for key := range args {
			if key != "question" && key != "file_ids" {
				return "Недопустимые параметры поиска.", nil
			}
		}
		if raw, exists := args["file_ids"]; exists {
			values, ok := raw.([]interface{})
			if !ok {
				return "Недопустимый выбор файлов.", nil
			}
			for _, value := range values {
				id, ok := value.(string)
				if !ok {
					return "Недопустимый выбор файлов.", nil
				}
				ids = append(ids, id)
			}
		}
		result, err := s.fileSearch.Search(ctx, executorUserID, domain.DriveSearchQueryRequest{Question: query, FileIDs: ids, Limit: 6, ChunksPerFile: 4})
		if err != nil {
			return "Поиск по содержимому файлов временно недоступен.", nil
		}
		data, _ := json.Marshal(result)
		return string(data), fileEvidenceWidget(result)

	case "search_users":
		query, _ := args["query"].(string)
		return s.toolSearchUsers(ctx, executorUserID, query)

	case "web_search":
		query, _ := args["query"].(string)
		return s.toolWebSearch(ctx, query)

	case "get_user_events":
		uidFloat, _ := args["user_id"].(float64)
		targetUserID := int64(uidFloat)
		from, _ := args["from_date"].(string)
		to, _ := args["to_date"].(string)
		return s.toolGetUserEvents(ctx, executorUserID, targetUserID, from, to)

	case "create_calendar_event":
		return s.toolCreateEvent(ctx, executorUserID, args)

	case "set_event_reminder":
		query, _ := args["event_query"].(string)
		eventIDStr, _ := args["event_id"].(string)
		mins := 15
		if v, ok := args["minutes_before"].(float64); ok {
			mins = int(v)
		}
		return s.toolSetEventReminder(ctx, executorUserID, eventIDStr, query, mins)

	case "cancel_calendar_event":
		eventIDStr, _ := args["event_id"].(string)
		query, _ := args["event_query"].(string)
		return s.toolCancelCalendarEvent(ctx, executorUserID, eventIDStr, query)

	case "create_online_meeting":
		return s.toolCreateOnlineMeeting(ctx, executorUserID, args)

	case "create_kanban_task":
		return s.toolCreateKanbanTask(ctx, executorUserID, args)

	case "close_kanban_task":
		taskIDStr, _ := args["task_id"].(string)
		query, _ := args["task_query"].(string)
		return s.toolCloseKanbanTask(ctx, executorUserID, taskIDStr, query)

	case "get_kanban_tasks":
		return s.toolGetKanbanTasks(ctx, executorUserID, args)

	case "get_kanban_boards":
		return s.toolGetKanbanBoards(ctx, executorUserID)

	case "send_broadcast":
		return s.toolSendBroadcast(ctx, executorUserID, args)

	default:
		return i18n.UIMessage(ctx, "Unknown tool: %s", name), nil
	}
}

func getInt64Array(v interface{}) []int64 {
	var result []int64
	if arr, ok := v.([]interface{}); ok {
		for _, item := range arr {
			if num, ok := item.(float64); ok {
				result = append(result, int64(num))
			}
		}
	}
	return result
}

func parseTime(s string) (time.Time, error) {
	formats := []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unable to parse time: %s", s)
}

func (s *SmartAgentService) toolSearchUsers(ctx context.Context, userID int64, query string) (string, *SmartAgentWidget) {
	users, err := s.userRepo.SearchVisibleTo(ctx, userID, query, 10, 0)
	if err != nil {
		return i18n.TextContext(ctx, "error.INTERNAL_ERROR", nil), nil
	}
	if len(users) == 0 {
		return i18n.UIMessage(ctx, "No users found"), nil
	}

	var output []map[string]interface{}
	for _, u := range users {
		output = append(output, map[string]interface{}{
			"id":       u.ID,
			"name":     u.FirstName + " " + u.LastName,
			"email":    u.Email,
			"position": u.Position,
			"avatar":   u.AvatarURL,
		})
	}
	b, _ := json.Marshal(output)

	widget := &SmartAgentWidget{
		Type:  "user_list",
		Title: i18n.UIMessage(ctx, "Found %d users for '%s'", len(users), query),
		Data:  output,
	}

	return string(b), widget
}

func (s *SmartAgentService) toolGetUserEvents(ctx context.Context, viewerID, targetUserID int64, fromStr, toStr string) (string, *SmartAgentWidget) {
	from, err := parseTime(fromStr)
	if err != nil {
		return i18n.UIMessage(ctx, "Invalid from_date format (use ISO 8601 or YYYY-MM-DD)"), nil
	}
	to, err := parseTime(toStr)
	if err != nil {
		return i18n.UIMessage(ctx, "Invalid to_date format"), nil
	}

	events, err := s.calendarService.ListEvents(ctx, viewerID, targetUserID, from, to)
	if err != nil {
		return i18n.TextContext(ctx, "error.INTERNAL_ERROR", nil), nil
	}

	var output []map[string]interface{}
	for _, e := range events {
		output = append(output, map[string]interface{}{
			"id":    e.ID,
			"title": e.Title,
			"start": e.StartTime,
			"end":   e.EndTime,
			"color": e.Color,
		})
	}
	b, _ := json.Marshal(output)

	widget := &SmartAgentWidget{
		Type:  "calendar_events",
		Title: i18n.UIMessage(ctx, "Calendar events"),
		Data:  output,
	}

	return string(b), widget
}

func (s *SmartAgentService) toolCreateEvent(ctx context.Context, userID int64, args map[string]interface{}) (string, *SmartAgentWidget) {
	title, _ := args["title"].(string)
	desc, _ := args["description"].(string)
	startStr, _ := args["start_time"].(string)
	endStr, _ := args["end_time"].(string)
	attendeeIDs := getInt64Array(args["attendee_ids"])

	start, err := parseTime(startStr)
	if err != nil {
		return i18n.TextContext(ctx, "error.INTERNAL_ERROR", nil), nil
	}
	end, err := parseTime(endStr)
	if err != nil {
		return i18n.TextContext(ctx, "error.INTERNAL_ERROR", nil), nil
	}

	defaultColor := "#3B82F6"
	req := domain.CreateEventRequest{
		Title:       title,
		Description: &desc,
		StartTime:   start,
		EndTime:     end,
		AttendeeIDs: attendeeIDs,
		Color:       &defaultColor,
	}

	event, err := s.calendarService.CreateEvent(ctx, userID, &req)
	if err != nil {
		return i18n.TextContext(ctx, "error.INTERNAL_ERROR", nil), nil
	}

	return i18n.UIMessage(ctx, "Event created successfully. ID: %s", event.ID), nil
}

func (s *SmartAgentService) toolSetEventReminder(ctx context.Context, userID int64, eventIDStr string, query string, minutes int) (string, *SmartAgentWidget) {
	var eventID uuid.UUID

	if eventIDStr != "" {
		if id, err := uuid.Parse(eventIDStr); err == nil {
			eventID = id
		}
	}

	if eventID == uuid.Nil {
		if query == "" {
			return i18n.UIMessage(ctx, "Neither event_id nor event_query provided"), nil
		}
		now := time.Now()
		to := now.AddDate(0, 0, 30)

		events, err := s.calendarService.ListEvents(ctx, userID, userID, now, to)
		if err != nil {
			return i18n.TextContext(ctx, "error.INTERNAL_ERROR", nil), nil
		}

		for i := range events {
			if strings.Contains(strings.ToLower(events[i].Title), strings.ToLower(query)) {
				eventID = events[i].ID
				break
			}
		}
	}

	if eventID == uuid.Nil {
		return i18n.UIMessage(ctx, "Event '%s' not found to set reminder", query), nil
	}

	event, readErr := s.calendarService.GetEvent(ctx, userID, eventID)
	if readErr != nil || event == nil {
		return i18n.TextContext(ctx, "error.INTERNAL_ERROR", nil), nil
	}
	eventTitle := event.Title

	err := s.calendarService.SetReminder(ctx, userID, eventID, minutes)
	if err != nil {
		return i18n.TextContext(ctx, "error.INTERNAL_ERROR", nil), nil
	}

	widget := &SmartAgentWidget{
		Type: "reminder_set",
		Data: map[string]interface{}{
			"event_title":    eventTitle,
			"minutes_before": minutes,
			"status":         "active",
		},
	}

	return i18n.UIMessage(ctx, "Reminder set for '%s' (%d min before start).", eventTitle, minutes), widget
}

func (s *SmartAgentService) toolCancelCalendarEvent(ctx context.Context, userID int64, eventIDStr string, query string) (string, *SmartAgentWidget) {
	var eventID uuid.UUID

	if eventIDStr != "" {
		if id, err := uuid.Parse(eventIDStr); err == nil {
			eventID = id
		}
	}

	if eventID == uuid.Nil {
		if query == "" {
			return i18n.UIMessage(ctx, "Не указан ID или название встречи для отмены."), nil
		}
		now := time.Now()
		to := now.AddDate(0, 0, 30)

		events, err := s.calendarService.ListEvents(ctx, userID, userID, now, to)
		if err != nil {
			return i18n.TextContext(ctx, "error.INTERNAL_ERROR", nil), nil
		}

		var candidateEvents []domain.Event
		for i := range events {
			if strings.Contains(strings.ToLower(events[i].Title), strings.ToLower(query)) {
				candidateEvents = append(candidateEvents, events[i])
			}
		}

		if len(candidateEvents) > 1 {
			var selectionEvents []map[string]interface{}
			for _, event := range candidateEvents {
				selectionEvents = append(selectionEvents, map[string]interface{}{
					"id":              event.ID.String(),
					"title":           event.Title,
					"start":           event.StartTime,
					"end":             event.EndTime,
					"color":           event.Color,
					"selection_value": fmt.Sprintf("событие %s (ID: %s)", event.Title, event.ID.String()),
				})
			}
			widget := &SmartAgentWidget{
				Type:  "event_selection",
				Title: i18n.UIMessage(ctx, "Выберите событие"),
				Data: map[string]interface{}{
					"message": i18n.UIMessage(ctx, "Найдено несколько событий. Выберите нужное событие."),
					"query":   query,
					"events":  selectionEvents,
				},
			}
			b, _ := json.Marshal(selectionEvents)
			return string(b), widget
		}

		if len(candidateEvents) == 1 {
			eventID = candidateEvents[0].ID
		}
	}

	if eventID == uuid.Nil {
		return i18n.UIMessage(ctx, "Событие '%s' не найдено для отмены.", query), nil
	}

	event, err := cancelCalendarActionEvent(ctx, s.calendarService, userID, eventID)
	if err != nil {
		return i18n.TextContext(ctx, "error.INTERNAL_ERROR", nil), nil
	}

	eventTitle := event.Title

	return i18n.UIMessage(ctx, "Встреча '%s' успешно отменена.", eventTitle), &SmartAgentWidget{
		Type:  "event_cancelled",
		Title: eventTitle,
		Data: map[string]interface{}{
			"id":    eventID.String(),
			"title": eventTitle,
		},
	}
}

func (s *SmartAgentService) toolCreateOnlineMeeting(ctx context.Context, userID int64, args map[string]interface{}) (string, *SmartAgentWidget) {
	meetingName, _ := args["meeting_name"].(string)
	fromDate, _ := args["from_date"].(string)
	toDate, _ := args["to_date"].(string)
	attendeeIDs := getInt64Array(args["attendee_ids"])

	s.logger.Info("Executing toolCreateOnlineMeeting",
		zap.Int64("user_id", userID),
		zap.String("name", meetingName),
		zap.Int("attendees_count", len(attendeeIDs)))

	// 1. Получаем инфо об организаторе (Host)
	hostUser, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return i18n.TextContext(ctx, "error.INTERNAL_ERROR", nil), nil
	}
	hostName := hostUser.FirstName + " " + hostUser.LastName
	if strings.TrimSpace(hostName) == "" {
		hostName = hostUser.Username
	}

	// 2. Получаем инфо об участниках (Participants)
	type Participant struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	var participants []Participant

	if len(attendeeIDs) > 0 {
		for _, pid := range attendeeIDs {
			pUser, err := s.userRepo.GetByID(ctx, pid)
			if err != nil {
				continue
			}
			pName := pUser.FirstName + " " + pUser.LastName
			if strings.TrimSpace(pName) == "" {
				pName = pUser.Username
			}
			participants = append(participants, Participant{
				Name:  pName,
				Email: pUser.Email,
			})
		}
	}

	// 3. Формируем тело запроса
	reqBody := map[string]interface{}{
		"meeting_name": meetingName,
		"from_date":    fromDate,
		"to_date":      toDate,
		"host": map[string]string{
			"name":  hostName,
			"email": hostUser.Email,
		},
		"participants": participants,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return i18n.TextContext(ctx, "error.INTERNAL_ERROR", nil), nil
	}

	// 4. Отправляем запрос
	// ВАЖНО: Используем доменное имя, если возможно, или проверяем доступность.
	// Для предотвращения фризов UI уменьшаем таймаут.
	url := "https://100.72.193.178/scheduler/api/create_meeting"

	// Отключаем проверку SSL сертификата (так как это внутренний IP)
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	client := &http.Client{Transport: tr, Timeout: 15 * time.Second} // Уменьшен таймаут со 120 до 15 секунд

	s.logger.Info("Calling external meeting API", zap.String("url", url))

	// Используем context из запроса, чтобы прерваться, если клиент ушел
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return i18n.TextContext(ctx, "error.INTERNAL_ERROR", nil), nil
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(httpReq)
	if err != nil {
		if ctx.Err() != nil {
			s.logger.Warn("Client closed connection before meeting API responded", zap.Error(ctx.Err()))
			return i18n.UIMessage(ctx, "The request was cancelled by the client (probably timeout)."), nil
		}
		s.logger.Error("Failed to call meeting API", zap.Error(err), zap.String("url", url))
		errMsg := i18n.TextContext(ctx, "error.INTERNAL_ERROR", nil)
		if strings.Contains(err.Error(), "deadline exceeded") || strings.Contains(err.Error(), "Timeout exceeded") || strings.Contains(err.Error(), "context deadline exceeded") {
			errMsg = i18n.UIMessage(ctx, "Сервис планирования совещаний временно недоступен. Попробуйте позже.")
		}
		return errMsg, nil
	}
	defer resp.Body.Close()

	var apiResp map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		s.logger.Warn("Failed to decode meeting API response", zap.Error(err))
	}

	if resp.StatusCode >= 400 {
		s.logger.Error("External Meeting API returned error",
			zap.Int("status", resp.StatusCode),
			zap.Any("body", apiResp))
		return i18n.TextContext(ctx, "error.INTERNAL_ERROR", nil), nil
	}

	s.logger.Info("External Meeting created successfully", zap.Any("response", apiResp))

	// Пытаемся вытащить ссылку на встречу, если она есть в ответе
	meetingURL, _ := apiResp["url"].(string)
	if meetingURL == "" {
		meetingURL, _ = apiResp["meeting_url"].(string)
	}

	widget := &SmartAgentWidget{
		Type: "meeting_created",
		Data: map[string]interface{}{
			"title":        meetingName,
			"from":         fromDate,
			"to":           toDate,
			"organizer":    hostName,
			"url":          meetingURL,
			"participants": len(participants),
		},
	}

	infoMsg := i18n.UIMessage(ctx, "Совещание '%s' успешно создано в планировщике.", meetingName)
	if meetingURL != "" {
		infoMsg += i18n.UIMessage(ctx, "\nСсылка: %s", meetingURL)
	}

	return infoMsg, widget
}

func (s *SmartAgentService) toolCreateKanbanTask(ctx context.Context, userID int64, args map[string]interface{}) (string, *SmartAgentWidget) {
	title, _ := args["title"].(string)
	desc, _ := args["description"].(string)
	priority, _ := args["priority"].(string)
	estimateHours := 0
	if v, ok := args["estimate_hours"].(float64); ok {
		estimateHours = int(v)
	}
	assigneeIDs := getInt64Array(args["assignee_ids"])
	dueAtStr, _ := args["due_at"].(string)
	boardIDStr, _ := args["board_id"].(string)

	var boardID uuid.UUID
	if boardIDStr != "" {
		boardID, _ = uuid.Parse(boardIDStr)
	}

	if boardID == uuid.Nil {
		b, err := s.boardRepo.GetBoardByOwnerID(ctx, userID)
		if err != nil || b == nil {
			if err := s.boardRepo.EnsureDefaultBoard(ctx, userID); err == nil {
				b, _ = s.boardRepo.GetBoardByOwnerID(ctx, userID)
			}
		}
		if b != nil {
			boardID = b.ID
		}
	}

	if boardID == uuid.Nil {
		return i18n.TextContext(ctx, "error.INTERNAL_ERROR", nil), nil
	}

	req := CreateTaskRequest{
		BoardID:             boardID,
		Title:               title,
		Description:         &desc,
		Priority:            priority,
		OriginalEstimateSec: estimateHours * 3600,
		AssigneeUserIDs:     assigneeIDs,
	}

	if dueAtStr != "" {
		if t, err := time.Parse(time.RFC3339, dueAtStr); err == nil {
			req.DueAt = &t
		}
	}

	task, err := s.kanbanService.CreateTask(ctx, userID, req)
	if err != nil {
		return i18n.TextContext(ctx, "error.INTERNAL_ERROR", nil), nil
	}

	widget := &SmartAgentWidget{
		Type: "task_created",
		Data: map[string]interface{}{
			"id":       task.ID,
			"title":    task.Title,
			"view_url": fmt.Sprintf("/kanban?task=%s", task.ID),
		},
	}

	return i18n.UIMessage(ctx, "Task created successfully in board %s. ID: %s", boardID, task.ID), widget
}

func (s *SmartAgentService) toolCloseKanbanTask(ctx context.Context, executorID int64, taskIDStr string, query string) (string, *SmartAgentWidget) {
	var taskID uuid.UUID
	if taskIDStr != "" {
		taskID, _ = uuid.Parse(taskIDStr)
	}

	if taskID == uuid.Nil {
		// Search for task by query
		tasks, err := s.kanbanService.SearchTasks(ctx, executorID, SearchTasksRequest{Query: query})
		if err != nil || len(tasks) == 0 {
			return i18n.UIMessage(ctx, "Task '%s' not found.", query), nil
		}
		taskID = tasks[0].ID
	}

	// Find the DONE column for the board this task belongs to
	task, err := s.kanbanService.GetTask(ctx, executorID, taskID)
	if err != nil {
		return i18n.TextContext(ctx, "error.INTERNAL_ERROR", nil), nil
	}

	boardResp, err := s.kanbanService.GetBoard(ctx, executorID, task.BoardID)
	if err != nil {
		return i18n.TextContext(ctx, "error.INTERNAL_ERROR", nil), nil
	}

	var doneColID uuid.UUID
	for _, col := range boardResp.Columns {
		if strings.ToUpper(col.Column.Key) == "DONE" || strings.ToUpper(col.Column.Name) == "DONE" {
			doneColID = col.Column.ID
			break
		}
	}

	if doneColID == uuid.Nil {
		return i18n.TextContext(ctx, "error.INTERNAL_ERROR", nil), nil
	}

	err = s.kanbanService.MoveTask(ctx, executorID, taskID, doneColID)
	if err != nil {
		return i18n.TextContext(ctx, "error.INTERNAL_ERROR", nil), nil
	}

	return i18n.UIMessage(ctx, "Task '%s' moved to DONE.", task.Title), nil
}

func (s *SmartAgentService) toolGetKanbanTasks(ctx context.Context, executorID int64, args map[string]interface{}) (string, *SmartAgentWidget) {
	query, _ := args["query"].(string)
	boardIDStr, _ := args["board_id"].(string)
	dateFilter, _ := args["date_filter"].(string)
	statusStr, _ := args["status"].(string)

	searchReq := SearchTasksRequest{
		Query: query,
	}

	if boardIDStr != "" {
		searchReq.BoardID = &boardIDStr
	}

	var columnKeys []string
	if statusStr != "" {
		parts := strings.Split(statusStr, ",")
		for _, p := range parts {
			columnKeys = append(columnKeys, strings.TrimSpace(p))
		}
	} else if query == "" && boardIDStr == "" {
		// Default behavior: tasks assigned to me AND (TODO or IN_PROGRESS)
		columnKeys = []string{"IN_PROGRESS", "TODO"}
	}
	searchReq.ColumnKeys = columnKeys

	if uidFloat, ok := args["assignee_id"].(float64); ok {
		searchReq.AssigneeIDs = []int64{int64(uidFloat)}
	} else if query == "" && boardIDStr == "" {
		// If no filters provided, default to tasks assigned to me
		searchReq.AssigneeIDs = []int64{executorID}
	}

	if dateFilter != "" {
		if t, err := time.Parse("2006-01-02", dateFilter); err == nil {
			searchReq.DateFrom = &t
			tEnd := t.Add(23*time.Hour + 59*time.Minute)
			searchReq.DateTo = &tEnd
		}
	}

	tasks, err := s.kanbanService.SearchTasks(ctx, executorID, searchReq)
	if err != nil {
		return i18n.TextContext(ctx, "error.INTERNAL_ERROR", nil), nil
	}

	if len(tasks) == 0 {
		return i18n.UIMessage(ctx, "No tasks found."), nil
	}

	var results []string
	var widgetData []map[string]interface{}

	for _, t := range tasks {
		dueStr := "No due date"
		if t.DueAt != nil {
			dueStr = t.DueAt.Format("2006-01-02")
		}
		results = append(results, fmt.Sprintf("- %s (Due: %s)", t.Title, dueStr))
		widgetData = append(widgetData, map[string]interface{}{
			"id":       t.ID,
			"title":    t.Title,
			"priority": t.Priority,
			"due_at":   t.DueAt,
		})
	}

	widget := &SmartAgentWidget{
		Type:  "task_list",
		Title: i18n.UIMessage(ctx, "Tasks Found"),
		Data:  widgetData,
	}

	return strings.Join(results, "\n"), widget
}

func (s *SmartAgentService) toolGetKanbanBoards(ctx context.Context, userID int64) (string, *SmartAgentWidget) {
	boards, err := s.kanbanService.ListUserBoards(ctx, userID)
	if err != nil {
		return i18n.TextContext(ctx, "error.INTERNAL_ERROR", nil), nil
	}

	if len(boards) == 0 {
		return i18n.UIMessage(ctx, "You don't have any boards yet."), nil
	}

	var results []string
	var widgetData []map[string]interface{}
	for _, b := range boards {
		results = append(results, fmt.Sprintf("- %s (ID: %s)", b.Name, b.ID))
		widgetData = append(widgetData, map[string]interface{}{
			"id":   b.ID,
			"name": b.Name,
		})
	}

	widget := &SmartAgentWidget{
		Type:  "board_list",
		Title: i18n.UIMessage(ctx, "Your Kanban Boards"),
		Data:  widgetData,
	}

	return strings.Join(results, "\n"), widget
}

func (s *SmartAgentService) toolSendBroadcast(ctx context.Context, senderID int64, args map[string]interface{}) (string, *SmartAgentWidget) {
	message, _ := args["message"].(string)
	recipientIDs := getInt64Array(args["recipient_user_ids"])

	req := BroadcastMessageRequest{
		RecipientUserIDs: recipientIDs,
		Content:          message,
		MessageType:      "text",
	}

	resp, err := s.chatService.BroadcastMessage(ctx, senderID, &req)
	if err != nil {
		return i18n.TextContext(ctx, "error.INTERNAL_ERROR", nil), nil
	}

	return i18n.UIMessage(ctx, "Broadcast sent. Success: %d, Fail: %d", resp.SuccessCount, resp.FailureCount), nil
}

func (s *SmartAgentService) toolWebSearch(ctx context.Context, query string) (string, *SmartAgentWidget) {
	if strings.TrimSpace(query) == "" {
		return i18n.TextContext(ctx, "error.INTERNAL_ERROR", nil), nil
	}

	baseURL := s.cfg.App.PublicURL
	if baseURL == "" {
		baseURL = "http://localhost:8080"
	}
	endpoint := s.cfg.Agent.AdaptiveWebSearchURL
	if strings.TrimSpace(endpoint) == "" {
		endpoint = baseURL + "/v1/chat/ask_adaptive"
	}

	body, err := json.Marshal(map[string]interface{}{
		"question":                 query,
		"chat_id":                  uuid.NewString(),
		"top_k":                    20,
		"temperature":              0.15,
		"learn":                    true,
		"web_cache_project":        nil,
		"include_web_cache_in_rag": true,
		"fast":                     true,
	})
	if err != nil {
		return i18n.TextContext(ctx, "error.INTERNAL_ERROR", nil), nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return i18n.TextContext(ctx, "error.INTERNAL_ERROR", nil), nil
	}
	req.Header.Set("Accept", "application/json")
	authHeader := s.cfg.Alem.APIKey
	if authHeader != "" {
		if !strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
			authHeader = "Bearer " + authHeader
		}
		req.Header.Set("Authorization", authHeader)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", baseURL)
	req.Header.Set("Referer", baseURL+"/web/team-rag")
	req.Header.Set("User-Agent", "smart-agent/1.0")

	// Legacy tool has a separate RAG transport from agent v2; neither may
	// invoke an unaccounted upstream through the model's paid chat step.
	client := &http.Client{Timeout: 15 * time.Second, Transport: aiaccess.BlockedTransport{}}
	resp, err := client.Do(req)
	if err != nil {
		return i18n.TextContext(ctx, "error.AI_ACCOUNTING_UNAVAILABLE", nil), nil
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return i18n.TextContext(ctx, "error.INTERNAL_ERROR", nil), nil
	}

	var payload struct {
		Answer string `json:"answer"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return i18n.TextContext(ctx, "error.INTERNAL_ERROR", nil), nil
	}

	ans := strings.TrimSpace(payload.Answer)
	if ans == "" {
		ans = i18n.UIMessage(ctx, "Интернет-поиск выполнен, но не удалось получить текстовый ответ.")
	}
	return ans, nil
}
