package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

const (
	ChatCommandCreateKanbanBoard = "kanban.create_board"
	ChatCommandCreateKanbanTask  = "kanban.create_task"
	ChatCommandListKanbanTasks   = "kanban.list_tasks"
	ChatCommandCreateEvent       = "calendar.create_event"
	ChatCommandTodayEvents       = "calendar.today_events"
	ChatCommandWeekEvents        = "calendar.week_events"
	ChatCommandUploadFile        = "drive.upload_file"
	ChatCommandFindFile          = "drive.find_file"
	ChatCommandRecentFiles       = "drive.recent_files"
	ChatCommandShareFile         = "drive.share_file"

	ChatCommandStatusNeedsInput = "needs_input"
	ChatCommandStatusExecuted   = "executed"
)

type ChatCommandService struct {
	kanbanService   *KanbanService
	calendarService *CalendarService
	driveService    *DriveService
	chatService     *ChatService
	groupService    *GroupService
	logger          *zap.Logger
}

func NewChatCommandService(
	kanbanService *KanbanService,
	calendarService *CalendarService,
	driveService *DriveService,
	chatService *ChatService,
	groupService *GroupService,
	logger *zap.Logger,
) *ChatCommandService {
	return &ChatCommandService{
		kanbanService:   kanbanService,
		calendarService: calendarService,
		driveService:    driveService,
		chatService:     chatService,
		groupService:    groupService,
		logger:          logger.With(zap.String("service", "chat_command")),
	}
}

type ChatCommandDefinition struct {
	ID          string                `json:"id"`
	Title       string                `json:"title"`
	Description string                `json:"description"`
	Aliases     []string              `json:"aliases"`
	Scopes      []string              `json:"scopes"`
	Arguments   []ChatCommandArgument `json:"arguments"`
	Examples    []string              `json:"examples,omitempty"`
	Metadata    map[string]any        `json:"metadata,omitempty"`
}

type ChatCommandArgument struct {
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Label    string   `json:"label"`
	Required bool     `json:"required"`
	Values   []string `json:"values,omitempty"`
	Source   string   `json:"source,omitempty"`
}

type ChatCommandExecuteRequest struct {
	CommandID        string              `json:"command_id"`
	RawText          string              `json:"raw_text"`
	ChatID           *uuid.UUID          `json:"chat_id,omitempty"`
	ChatType         string              `json:"chat_type,omitempty"` // conversation or group
	ClientRequestID  string              `json:"client_request_id,omitempty"`
	SendResultToChat *bool               `json:"send_result_to_chat,omitempty"`
	Args             ChatCommandTaskArgs `json:"args"`
}

type ChatCommandTaskArgs struct {
	BoardID           *uuid.UUID  `json:"board_id,omitempty"`
	BoardName         string      `json:"board_name,omitempty"`
	SprintID          *uuid.UUID  `json:"sprint_id,omitempty"`
	Title             string      `json:"title,omitempty"`
	Description       string      `json:"description,omitempty"`
	AssigneeIDs       []int64     `json:"assignee_ids,omitempty"`
	Priority          string      `json:"priority,omitempty"`
	Priorities        []string    `json:"priorities,omitempty"`
	Query             string      `json:"query,omitempty"`
	FileID            *uuid.UUID  `json:"file_id,omitempty"`
	FileName          string      `json:"file_name,omitempty"`
	FileType          string      `json:"file_type,omitempty"`
	Limit             int         `json:"limit,omitempty"`
	Offset            int         `json:"offset,omitempty"`
	ColumnIDs         []string    `json:"column_ids,omitempty"`
	ColumnKeys        []string    `json:"column_keys,omitempty"`
	LabelIDs          []string    `json:"label_ids,omitempty"`
	DateFrom          *time.Time  `json:"date_from,omitempty"`
	DateTo            *time.Time  `json:"date_to,omitempty"`
	DueAt             *time.Time  `json:"due_at,omitempty"`
	StartTime         *time.Time  `json:"start_time,omitempty"`
	EndTime           *time.Time  `json:"end_time,omitempty"`
	Location          string      `json:"location,omitempty"`
	IsAllDay          bool        `json:"is_all_day,omitempty"`
	IsOnline          bool        `json:"is_online,omitempty"`
	AttendeeIDs       []int64     `json:"attendee_ids,omitempty"`
	ReminderMinutes   *int        `json:"reminder_minutes,omitempty"`
	TimezoneOffsetMin *int        `json:"timezone_offset_minutes,omitempty"`
	AttachmentFileIDs []uuid.UUID `json:"attachment_file_ids,omitempty"`
	ParentID          *uuid.UUID  `json:"parent_id,omitempty"`
}

type ChatCommandExecuteResponse struct {
	Status        string              `json:"status"`
	CommandID     string              `json:"command_id"`
	MissingFields []string            `json:"missing_fields,omitempty"`
	Form          *ChatCommandForm    `json:"form,omitempty"`
	Result        any                 `json:"result,omitempty"`
	ChatMessage   *domain.MessageView `json:"chat_message,omitempty"`
}

type ChatCommandForm struct {
	Title       string                 `json:"title"`
	Description string                 `json:"description,omitempty"`
	Fields      []ChatCommandFormField `json:"fields"`
}

type ChatCommandFormField struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Label    string `json:"label"`
	Required bool   `json:"required"`
	Source   string `json:"source,omitempty"`
	Options  any    `json:"options,omitempty"`
}

type ChatCommandKanbanContext struct {
	Commands      []ChatCommandDefinition `json:"commands"`
	Boards        []*domain.Board         `json:"boards"`
	SelectedBoard *domain.BoardResponse   `json:"selected_board,omitempty"`
}

func (s *ChatCommandService) ListDefinitions() []ChatCommandDefinition {
	return []ChatCommandDefinition{
		{
			ID:          ChatCommandUploadFile,
			Title:       "Загрузить файл",
			Description: "Загружает файл в Team Drive.",
			Aliases:     []string{"/upload_file"},
			Scopes:      []string{"conversation", "group"},
			Arguments:   []ChatCommandArgument{},
			Examples:    []string{"/upload_file"},
			Metadata:    map[string]any{"client_only": true},
		},
		{
			ID:          ChatCommandFindFile,
			Title:       "Найти файл",
			Description: "Ищет файлы по названию в Team Drive.",
			Aliases:     []string{"/find_file"},
			Scopes:      []string{"conversation", "group"},
			Arguments: []ChatCommandArgument{
				{Name: "query", Type: "string", Label: "Название файла", Required: true},
				{Name: "file_type", Type: "string", Label: "Тип файла"},
				{Name: "limit", Type: "integer", Label: "Лимит"},
			},
			Examples: []string{"/find_file report"},
		},
		{
			ID:          ChatCommandRecentFiles,
			Title:       "Последние файлы",
			Description: "Показывает последние файлы из Team Drive.",
			Aliases:     []string{"/recent_files"},
			Scopes:      []string{"conversation", "group"},
			Arguments: []ChatCommandArgument{
				{Name: "limit", Type: "integer", Label: "Лимит"},
			},
			Examples: []string{"/recent_files"},
		},
		{
			ID:          ChatCommandShareFile,
			Title:       "Поделиться файлом",
			Description: "Отправляет файл из Team Drive в текущий чат.",
			Aliases:     []string{"/share_file"},
			Scopes:      []string{"conversation", "group"},
			Arguments: []ChatCommandArgument{
				{Name: "file_name", Type: "string", Label: "Название файла"},
				{Name: "query", Type: "string", Label: "Поиск"},
			},
			Examples: []string{"/share_file report.pdf"},
		},
		{
			ID:          ChatCommandCreateEvent,
			Title:       "Создать событие",
			Description: "Создает событие календаря из чата.",
			Aliases:     []string{"/create_event"},
			Scopes:      []string{"conversation", "group"},
			Arguments: []ChatCommandArgument{
				{Name: "title", Type: "string", Label: "Название", Required: true},
				{Name: "description", Type: "string", Label: "Описание"},
				{Name: "start_time", Type: "datetime", Label: "Начало", Required: true},
				{Name: "end_time", Type: "datetime", Label: "Окончание"},
				{Name: "location", Type: "string", Label: "Место"},
				{Name: "attendee_ids", Type: "user_ids", Label: "Участники"},
				{Name: "reminder_minutes", Type: "integer", Label: "Напомнить за, минут"},
			},
			Examples: []string{"/create_event Team sync"},
		},
		{
			ID:          ChatCommandTodayEvents,
			Title:       "События сегодня",
			Description: "Показывает события календаря на сегодня.",
			Aliases:     []string{"/today_events"},
			Scopes:      []string{"conversation", "group"},
			Arguments: []ChatCommandArgument{
				{Name: "timezone_offset_minutes", Type: "integer", Label: "Смещение часового пояса, минут"},
			},
			Examples: []string{"/today_events"},
		},
		{
			ID:          ChatCommandWeekEvents,
			Title:       "События недели",
			Description: "Показывает события календаря на текущую неделю.",
			Aliases:     []string{"/week_events"},
			Scopes:      []string{"conversation", "group"},
			Arguments: []ChatCommandArgument{
				{Name: "timezone_offset_minutes", Type: "integer", Label: "Смещение часового пояса, минут"},
			},
			Examples: []string{"/week_events"},
		},
		{
			ID:          ChatCommandListKanbanTasks,
			Title:       "Показать задачи",
			Description: "Показывает задачи с фильтрами по доске, исполнителю, приоритету, колонке, метке, дате и тексту.",
			Aliases:     []string{"/list_tasks", "/list_task"},
			Scopes:      []string{"conversation", "group"},
			Arguments: []ChatCommandArgument{
				{Name: "board_id", Type: "uuid", Label: "Доска", Source: "kanban.boards"},
				{Name: "board_name", Type: "string", Label: "Название доски"},
				{Name: "query", Type: "string", Label: "Поиск"},
				{Name: "assignee_ids", Type: "user_ids", Label: "Исполнители", Source: "kanban.board.members"},
				{Name: "priorities", Type: "enum_array", Label: "Приоритеты", Values: []string{"low", "medium", "high", "critical"}},
				{Name: "column_ids", Type: "uuid_array", Label: "Колонки", Source: "kanban.board.columns"},
				{Name: "column_keys", Type: "string_array", Label: "Ключи колонок", Values: []string{domain.ColTODO, domain.ColInProgress, domain.ColTest, domain.ColDone, domain.ColBlock}},
				{Name: "label_ids", Type: "uuid_array", Label: "Метки", Source: "kanban.board.labels"},
				{Name: "date_from", Type: "datetime", Label: "Дата от"},
				{Name: "date_to", Type: "datetime", Label: "Дата до"},
			},
			Examples: []string{"/list_tasks", "/list_tasks report"},
		},
		{
			ID:          ChatCommandCreateKanbanBoard,
			Title:       "Создать доску",
			Description: "Создает kanban-доску с дефолтными колонками.",
			Aliases:     []string{"/create_board"},
			Scopes:      []string{"conversation", "group"},
			Arguments: []ChatCommandArgument{
				{Name: "board_name", Type: "string", Label: "Название доски", Required: true},
			},
			Examples: []string{"/create_board Запуск продукта"},
		},
		{
			ID:          ChatCommandCreateKanbanTask,
			Title:       "Создать задачу",
			Description: "Создает задачу в kanban-доске и может отправить карточку задачи в чат.",
			Aliases:     []string{"/create_task"},
			Scopes:      []string{"conversation", "group"},
			Arguments: []ChatCommandArgument{
				{Name: "board_id", Type: "uuid", Label: "Доска", Required: true, Source: "kanban.boards"},
				{Name: "title", Type: "string", Label: "Название", Required: true},
				{Name: "description", Type: "string", Label: "Описание"},
				{Name: "assignee_ids", Type: "user_ids", Label: "Исполнители", Source: "kanban.board.members"},
				{Name: "sprint_id", Type: "uuid", Label: "Спринт", Source: "kanban.board.sprints"},
				{Name: "priority", Type: "enum", Label: "Приоритет", Values: []string{"low", "medium", "high", "critical"}},
				{Name: "due_at", Type: "datetime", Label: "Дедлайн"},
				{Name: "attachment_file_ids", Type: "uuid_array", Label: "Файлы", Source: "kanban.uploaded_files"},
				{Name: "parent_id", Type: "uuid", Label: "Родительская задача"},
			},
			Examples: []string{"/create_task Подготовить отчет"},
		},
	}
}

func (s *ChatCommandService) GetKanbanContext(ctx context.Context, userID int64, boardID *uuid.UUID) (*ChatCommandKanbanContext, error) {
	boards, err := s.kanbanService.ListUserBoards(ctx, userID)
	if err != nil {
		return nil, err
	}

	resp := &ChatCommandKanbanContext{
		Commands: s.ListDefinitions(),
		Boards:   boards,
	}

	if boardID != nil {
		board, err := s.kanbanService.GetBoard(ctx, userID, *boardID)
		if err != nil {
			return nil, err
		}
		resp.SelectedBoard = board
	}

	return resp, nil
}

func (s *ChatCommandService) Execute(ctx context.Context, userID int64, req ChatCommandExecuteRequest, senderSessionKey string) (*ChatCommandExecuteResponse, error) {
	commandID, rawRemainder := s.resolveCommand(req.CommandID, req.RawText)
	if commandID == "" {
		return nil, apperrors.BadRequest("Неизвестная команда")
	}

	if strings.TrimSpace(req.Args.Title) == "" && commandID == ChatCommandCreateKanbanTask {
		req.Args.Title = rawRemainder
	}
	if strings.TrimSpace(req.Args.BoardName) == "" && commandID == ChatCommandCreateKanbanBoard {
		req.Args.BoardName = rawRemainder
	}
	if strings.TrimSpace(req.Args.Query) == "" && commandID == ChatCommandListKanbanTasks {
		req.Args.Query = rawRemainder
	}
	if strings.TrimSpace(req.Args.Title) == "" && commandID == ChatCommandCreateEvent {
		req.Args.Title = rawRemainder
	}
	if strings.TrimSpace(req.Args.Query) == "" && commandID == ChatCommandFindFile {
		req.Args.Query = rawRemainder
	}
	if strings.TrimSpace(req.Args.FileName) == "" && strings.TrimSpace(req.Args.Query) == "" && commandID == ChatCommandShareFile {
		req.Args.FileName = rawRemainder
		req.Args.Query = rawRemainder
	}

	switch commandID {
	case ChatCommandUploadFile:
		return s.executeUploadFile()
	case ChatCommandFindFile:
		return s.executeFindFiles(ctx, userID, req)
	case ChatCommandRecentFiles:
		return s.executeRecentFiles(ctx, userID, req)
	case ChatCommandShareFile:
		return s.executeShareFile(ctx, userID, req, senderSessionKey)
	case ChatCommandCreateEvent:
		return s.executeCreateEvent(ctx, userID, req, senderSessionKey)
	case ChatCommandTodayEvents:
		return s.executeEventsForRange(ctx, userID, req, ChatCommandTodayEvents)
	case ChatCommandWeekEvents:
		return s.executeEventsForRange(ctx, userID, req, ChatCommandWeekEvents)
	case ChatCommandCreateKanbanBoard:
		return s.executeCreateBoard(ctx, userID, req)
	case ChatCommandCreateKanbanTask:
		return s.executeCreateTask(ctx, userID, req, senderSessionKey)
	case ChatCommandListKanbanTasks:
		return s.executeListTasks(ctx, userID, req)
	default:
		return nil, apperrors.BadRequest("Команда пока не поддерживается")
	}
}

func (s *ChatCommandService) executeCreateBoard(ctx context.Context, userID int64, req ChatCommandExecuteRequest) (*ChatCommandExecuteResponse, error) {
	boardName := strings.TrimSpace(req.Args.BoardName)
	if boardName == "" {
		return &ChatCommandExecuteResponse{
			Status:        ChatCommandStatusNeedsInput,
			CommandID:     ChatCommandCreateKanbanBoard,
			MissingFields: []string{"board_name"},
			Form: &ChatCommandForm{
				Title: "Создать доску",
				Fields: []ChatCommandFormField{
					{Name: "board_name", Type: "text", Label: "Название доски", Required: true},
				},
			},
		}, nil
	}

	board, err := s.kanbanService.CreateBoard(ctx, userID, CreateBoardRequest{Name: boardName})
	if err != nil {
		return nil, err
	}

	return &ChatCommandExecuteResponse{
		Status:    ChatCommandStatusExecuted,
		CommandID: ChatCommandCreateKanbanBoard,
		Result: map[string]any{
			"type":  "kanban_board_created",
			"board": board,
		},
	}, nil
}

func (s *ChatCommandService) executeCreateTask(ctx context.Context, userID int64, req ChatCommandExecuteRequest, senderSessionKey string) (*ChatCommandExecuteResponse, error) {
	req.Args.Title = strings.TrimSpace(req.Args.Title)
	if req.Args.Title == "" {
		return s.needsTaskInput(ctx, userID, []string{"title"}, nil)
	}

	boardID, boardResp, needsBoardSelection, err := s.resolveBoard(ctx, userID, req.Args)
	if err != nil {
		return nil, err
	}
	if needsBoardSelection {
		return s.needsTaskInput(ctx, userID, []string{"board_id"}, nil)
	}

	var desc *string
	if strings.TrimSpace(req.Args.Description) != "" {
		v := strings.TrimSpace(req.Args.Description)
		desc = &v
	}
	priority := strings.TrimSpace(req.Args.Priority)
	if priority == "" {
		priority = "medium"
	}

	task, err := s.kanbanService.CreateTask(ctx, userID, CreateTaskRequest{
		BoardID:           boardID,
		SprintID:          req.Args.SprintID,
		Title:             req.Args.Title,
		Description:       desc,
		AssigneeUserIDs:   req.Args.AssigneeIDs,
		Priority:          priority,
		DueAt:             req.Args.DueAt,
		AttachmentFileIDs: req.Args.AttachmentFileIDs,
		ParentID:          req.Args.ParentID,
	})
	if err != nil {
		return nil, err
	}

	result := map[string]any{
		"type": "kanban_task_created",
		"task": task,
	}
	if boardResp != nil && boardResp.Board != nil {
		result["board"] = boardResp.Board
	}

	var chatMessage *domain.MessageView
	if shouldSendResultToChat(req) && req.ChatID != nil {
		chatMessage, err = s.sendTaskCardToChat(ctx, userID, *req.ChatID, req.ChatType, task, senderSessionKey)
		if err != nil {
			return nil, err
		}
	}

	return &ChatCommandExecuteResponse{
		Status:      ChatCommandStatusExecuted,
		CommandID:   ChatCommandCreateKanbanTask,
		Result:      result,
		ChatMessage: chatMessage,
	}, nil
}

func (s *ChatCommandService) executeListTasks(ctx context.Context, userID int64, req ChatCommandExecuteRequest) (*ChatCommandExecuteResponse, error) {
	var boardID *string
	var selectedBoard *domain.Board

	if req.Args.BoardID != nil && *req.Args.BoardID != uuid.Nil {
		board, err := s.kanbanService.GetBoard(ctx, userID, *req.Args.BoardID)
		if err != nil {
			return nil, err
		}
		boardIDValue := req.Args.BoardID.String()
		boardID = &boardIDValue
		if board != nil {
			selectedBoard = board.Board
		}
	} else if strings.TrimSpace(req.Args.BoardName) != "" {
		board, err := s.findBoardByName(ctx, userID, req.Args.BoardName)
		if err != nil {
			return nil, err
		}
		if board == nil {
			return s.needsListTasksInput(ctx, userID, []string{"board_id"})
		}
		boardIDValue := board.ID.String()
		boardID = &boardIDValue
		selectedBoard = board
	}

	priorities := append([]string{}, req.Args.Priorities...)
	if strings.TrimSpace(req.Args.Priority) != "" {
		priorities = append(priorities, req.Args.Priority)
	}

	tasks, err := s.kanbanService.SearchTasks(ctx, userID, SearchTasksRequest{
		Query:       strings.TrimSpace(req.Args.Query),
		AssigneeIDs: req.Args.AssigneeIDs,
		Priorities:  priorities,
		ColumnIDs:   req.Args.ColumnIDs,
		ColumnKeys:  req.Args.ColumnKeys,
		DateFrom:    req.Args.DateFrom,
		DateTo:      req.Args.DateTo,
		BoardID:     boardID,
		LabelIDs:    req.Args.LabelIDs,
	})
	if err != nil {
		return nil, err
	}

	result := map[string]any{
		"type":  "kanban_tasks_list",
		"count": len(tasks),
		"tasks": tasks,
		"filters": map[string]any{
			"board_id":     boardID,
			"query":        strings.TrimSpace(req.Args.Query),
			"assignee_ids": req.Args.AssigneeIDs,
			"priorities":   priorities,
			"column_ids":   req.Args.ColumnIDs,
			"column_keys":  req.Args.ColumnKeys,
			"label_ids":    req.Args.LabelIDs,
			"date_from":    req.Args.DateFrom,
			"date_to":      req.Args.DateTo,
		},
	}
	if selectedBoard != nil {
		result["board"] = selectedBoard
	}

	return &ChatCommandExecuteResponse{
		Status:    ChatCommandStatusExecuted,
		CommandID: ChatCommandListKanbanTasks,
		Result:    result,
	}, nil
}

func (s *ChatCommandService) executeCreateEvent(ctx context.Context, userID int64, req ChatCommandExecuteRequest, senderSessionKey string) (*ChatCommandExecuteResponse, error) {
	if s.calendarService == nil {
		return nil, apperrors.BadRequest("calendar service is not available")
	}

	req.Args.Title = strings.TrimSpace(req.Args.Title)
	if req.Args.Title == "" || req.Args.StartTime == nil {
		missing := make([]string, 0, 2)
		if req.Args.Title == "" {
			missing = append(missing, "title")
		}
		if req.Args.StartTime == nil {
			missing = append(missing, "start_time")
		}
		return s.needsEventInput(missing), nil
	}

	endTime := req.Args.EndTime
	if endTime == nil {
		v := req.Args.StartTime.Add(time.Hour)
		endTime = &v
	}

	var desc *string
	if strings.TrimSpace(req.Args.Description) != "" {
		v := strings.TrimSpace(req.Args.Description)
		desc = &v
	}
	var location *string
	if strings.TrimSpace(req.Args.Location) != "" {
		v := strings.TrimSpace(req.Args.Location)
		location = &v
	}
	attendeeIDs, err := s.resolveEventAttendees(ctx, userID, req)
	if err != nil {
		return nil, err
	}

	event, err := s.calendarService.CreateEvent(ctx, userID, &domain.CreateEventRequest{
		Title:           req.Args.Title,
		Description:     desc,
		StartTime:       *req.Args.StartTime,
		EndTime:         *endTime,
		Location:        location,
		IsAllDay:        req.Args.IsAllDay,
		IsOnline:        req.Args.IsOnline,
		AttendeeIDs:     attendeeIDs,
		ReminderMinutes: req.Args.ReminderMinutes,
	})
	if err != nil {
		return nil, err
	}

	result := map[string]any{
		"type":  "calendar_event_created",
		"event": event,
	}

	var chatMessage *domain.MessageView
	if shouldSendResultToChat(req) && req.ChatID != nil {
		chatMessage, err = s.sendEventCardToChat(ctx, userID, *req.ChatID, req.ChatType, event, senderSessionKey)
		if err != nil {
			return nil, err
		}
	}

	return &ChatCommandExecuteResponse{
		Status:      ChatCommandStatusExecuted,
		CommandID:   ChatCommandCreateEvent,
		Result:      result,
		ChatMessage: chatMessage,
	}, nil
}

func (s *ChatCommandService) executeUploadFile() (*ChatCommandExecuteResponse, error) {
	return &ChatCommandExecuteResponse{
		Status:    ChatCommandStatusExecuted,
		CommandID: ChatCommandUploadFile,
		Result: map[string]any{
			"type":   "drive_upload_file_requested",
			"action": "upload_drive_file",
		},
	}, nil
}

func (s *ChatCommandService) executeFindFiles(ctx context.Context, userID int64, req ChatCommandExecuteRequest) (*ChatCommandExecuteResponse, error) {
	if s.driveService == nil {
		return nil, apperrors.BadRequest("drive service is not available")
	}
	query := strings.TrimSpace(req.Args.Query)
	if query == "" {
		return s.needsFindFileInput([]string{"query"}), nil
	}
	limit := normalizeChatCommandLimit(req.Args.Limit, 20, 100)
	items, err := s.driveService.SearchAndFilter(ctx, userID, query, req.Args.FileType, limit, req.Args.Offset, nil, "")
	if err != nil {
		return nil, err
	}
	files := driveItemsToFiles(items)
	for _, file := range files {
		s.driveService.fillFileUrl(file)
	}

	return &ChatCommandExecuteResponse{
		Status:    ChatCommandStatusExecuted,
		CommandID: ChatCommandFindFile,
		Result: map[string]any{
			"type":   "drive_files_list",
			"action": "open_alem_drive",
			"scope":  ChatCommandFindFile,
			"count":  len(files),
			"files":  files,
			"filters": map[string]any{
				"query":     query,
				"file_type": req.Args.FileType,
				"limit":     limit,
				"offset":    req.Args.Offset,
			},
		},
	}, nil
}

func (s *ChatCommandService) executeRecentFiles(ctx context.Context, userID int64, req ChatCommandExecuteRequest) (*ChatCommandExecuteResponse, error) {
	if s.driveService == nil {
		return nil, apperrors.BadRequest("drive service is not available")
	}
	limit := normalizeChatCommandLimit(req.Args.Limit, 20, 100)
	items, err := s.driveService.ListRecent(ctx, userID, limit, req.Args.Offset)
	if err != nil {
		return nil, err
	}
	files := recentItemsToFiles(items)

	return &ChatCommandExecuteResponse{
		Status:    ChatCommandStatusExecuted,
		CommandID: ChatCommandRecentFiles,
		Result: map[string]any{
			"type":   "drive_files_list",
			"action": "open_alem_drive",
			"scope":  ChatCommandRecentFiles,
			"count":  len(files),
			"files":  files,
			"filters": map[string]any{
				"limit":  limit,
				"offset": req.Args.Offset,
			},
		},
	}, nil
}

func (s *ChatCommandService) executeShareFile(ctx context.Context, userID int64, req ChatCommandExecuteRequest, senderSessionKey string) (*ChatCommandExecuteResponse, error) {
	if s.driveService == nil {
		return nil, apperrors.BadRequest("drive service is not available")
	}
	if req.ChatID == nil {
		return nil, apperrors.BadRequest("chat_id is required")
	}

	file, needsInput, err := s.resolveDriveFileForShare(ctx, userID, req.Args)
	if err != nil {
		return nil, err
	}
	if needsInput {
		return s.needsShareFileInput(ctx, userID, req.Args, []string{"query"})
	}

	if err := s.grantDriveFileAccessToChat(ctx, userID, req, file.ID); err != nil {
		return nil, err
	}

	chatMessage, err := s.sendDriveFileCardToChat(ctx, userID, *req.ChatID, req.ChatType, file, senderSessionKey)
	if err != nil {
		return nil, err
	}

	return &ChatCommandExecuteResponse{
		Status:    ChatCommandStatusExecuted,
		CommandID: ChatCommandShareFile,
		Result: map[string]any{
			"type": "drive_file_shared",
			"file": file,
		},
		ChatMessage: chatMessage,
	}, nil
}

func (s *ChatCommandService) executeEventsForRange(ctx context.Context, userID int64, req ChatCommandExecuteRequest, commandID string) (*ChatCommandExecuteResponse, error) {
	if s.calendarService == nil {
		return nil, apperrors.BadRequest("calendar service is not available")
	}

	offset := 300
	if req.Args.TimezoneOffsetMin != nil {
		offset = *req.Args.TimezoneOffsetMin
	}

	from, to := chatCommandCalendarRange(time.Now().UTC(), offset, commandID)
	events, err := s.calendarService.ListEvents(ctx, userID, userID, from, to)
	if err != nil {
		return nil, err
	}

	return &ChatCommandExecuteResponse{
		Status:    ChatCommandStatusExecuted,
		CommandID: commandID,
		Result: map[string]any{
			"type":   "calendar_events_list",
			"scope":  commandID,
			"count":  len(events),
			"events": events,
			"range": map[string]any{
				"from":                    from,
				"to":                      to,
				"timezone_offset_minutes": offset,
			},
		},
	}, nil
}

func chatCommandCalendarRange(nowUTC time.Time, timezoneOffsetMinutes int, commandID string) (time.Time, time.Time) {
	userNow := nowUTC.Add(time.Duration(timezoneOffsetMinutes) * time.Minute)
	y, m, d := userNow.Date()
	startOfDay := time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Add(-time.Duration(timezoneOffsetMinutes) * time.Minute)
	if commandID == ChatCommandWeekEvents {
		weekday := int(userNow.Weekday())
		if weekday == 0 {
			weekday = 7
		}
		monday := userNow.AddDate(0, 0, -weekday+1)
		startOfWeek := time.Date(monday.Year(), monday.Month(), monday.Day(), 0, 0, 0, 0, time.UTC).Add(-time.Duration(timezoneOffsetMinutes) * time.Minute)
		return startOfWeek, startOfWeek.AddDate(0, 0, 7)
	}
	return startOfDay, startOfDay.Add(24 * time.Hour)
}

func (s *ChatCommandService) needsTaskInput(ctx context.Context, userID int64, missing []string, boardID *uuid.UUID) (*ChatCommandExecuteResponse, error) {
	ctxResp, err := s.GetKanbanContext(ctx, userID, boardID)
	if err != nil {
		return nil, err
	}

	fields := []ChatCommandFormField{
		{Name: "board_id", Type: "select", Label: "Доска", Required: true, Source: "kanban.boards", Options: ctxResp.Boards},
		{Name: "title", Type: "text", Label: "Название задачи", Required: true},
		{Name: "description", Type: "textarea", Label: "Описание"},
		{Name: "priority", Type: "select", Label: "Приоритет", Options: []string{"low", "medium", "high", "critical"}},
		{Name: "due_at", Type: "datetime", Label: "Дедлайн"},
	}
	if ctxResp.SelectedBoard != nil {
		fields = append(fields,
			ChatCommandFormField{Name: "assignee_ids", Type: "multi_user_select", Label: "Исполнители", Source: "kanban.board.members", Options: ctxResp.SelectedBoard.Members},
			ChatCommandFormField{Name: "sprint_id", Type: "select", Label: "Спринт", Source: "kanban.board.sprints", Options: ctxResp.SelectedBoard.Sprints},
		)
	}

	return &ChatCommandExecuteResponse{
		Status:        ChatCommandStatusNeedsInput,
		CommandID:     ChatCommandCreateKanbanTask,
		MissingFields: missing,
		Result: map[string]any{
			"type":    "kanban_task_form_context",
			"context": ctxResp,
		},
		Form: &ChatCommandForm{
			Title:       "Создать задачу",
			Description: "Заполните недостающие поля и выполните команду снова.",
			Fields:      fields,
		},
	}, nil
}

func (s *ChatCommandService) needsListTasksInput(ctx context.Context, userID int64, missing []string) (*ChatCommandExecuteResponse, error) {
	ctxResp, err := s.GetKanbanContext(ctx, userID, nil)
	if err != nil {
		return nil, err
	}

	return &ChatCommandExecuteResponse{
		Status:        ChatCommandStatusNeedsInput,
		CommandID:     ChatCommandListKanbanTasks,
		MissingFields: missing,
		Result: map[string]any{
			"type":    "kanban_tasks_list_form_context",
			"context": ctxResp,
		},
		Form: &ChatCommandForm{
			Title:       "Список задач",
			Description: "Выберите доску и выполните команду снова.",
			Fields: []ChatCommandFormField{
				{Name: "board_id", Type: "select", Label: "Доска", Required: true, Source: "kanban.boards", Options: ctxResp.Boards},
				{Name: "query", Type: "text", Label: "Поиск"},
				{Name: "priorities", Type: "multi_select", Label: "Приоритеты", Options: []string{"low", "medium", "high", "critical"}},
				{Name: "column_keys", Type: "multi_select", Label: "Колонки", Options: []string{domain.ColTODO, domain.ColInProgress, domain.ColTest, domain.ColDone, domain.ColBlock}},
			},
		},
	}, nil
}

func (s *ChatCommandService) needsEventInput(missing []string) *ChatCommandExecuteResponse {
	return &ChatCommandExecuteResponse{
		Status:        ChatCommandStatusNeedsInput,
		CommandID:     ChatCommandCreateEvent,
		MissingFields: missing,
		Result: map[string]any{
			"type": "calendar_event_form_context",
		},
		Form: &ChatCommandForm{
			Title:       "Создать событие",
			Description: "Заполните обязательные поля и выполните команду снова.",
			Fields: []ChatCommandFormField{
				{Name: "title", Type: "text", Label: "Название", Required: true},
				{Name: "description", Type: "textarea", Label: "Описание"},
				{Name: "start_time", Type: "datetime", Label: "Начало", Required: true},
				{Name: "end_time", Type: "datetime", Label: "Окончание"},
				{Name: "location", Type: "text", Label: "Место"},
				{Name: "reminder_minutes", Type: "integer", Label: "Напомнить за, минут"},
			},
		},
	}
}

func (s *ChatCommandService) needsFindFileInput(missing []string) *ChatCommandExecuteResponse {
	return &ChatCommandExecuteResponse{
		Status:        ChatCommandStatusNeedsInput,
		CommandID:     ChatCommandFindFile,
		MissingFields: missing,
		Result: map[string]any{
			"type": "drive_find_file_form_context",
		},
		Form: &ChatCommandForm{
			Title:       "Найти файл",
			Description: "Введите название файла и выполните команду снова.",
			Fields: []ChatCommandFormField{
				{Name: "query", Type: "text", Label: "Название файла", Required: true},
				{Name: "file_type", Type: "text", Label: "Тип файла"},
			},
		},
	}
}

func (s *ChatCommandService) needsShareFileInput(ctx context.Context, userID int64, args ChatCommandTaskArgs, missing []string) (*ChatCommandExecuteResponse, error) {
	var files []*domain.DriveFile
	query := strings.TrimSpace(args.Query)
	if query == "" {
		query = strings.TrimSpace(args.FileName)
	}
	if query != "" && s.driveService != nil {
		items, err := s.driveService.SearchAndFilter(ctx, userID, query, args.FileType, 20, 0, nil, "")
		if err != nil {
			return nil, err
		}
		files = driveItemsToFiles(items)
		for _, file := range files {
			s.driveService.fillFileUrl(file)
		}
	}

	return &ChatCommandExecuteResponse{
		Status:        ChatCommandStatusNeedsInput,
		CommandID:     ChatCommandShareFile,
		MissingFields: missing,
		Result: map[string]any{
			"type":  "drive_share_file_form_context",
			"files": files,
		},
		Form: &ChatCommandForm{
			Title:       "Поделиться файлом",
			Description: "Введите название файла или выберите файл из результатов.",
			Fields: []ChatCommandFormField{
				{Name: "query", Type: "text", Label: "Название файла", Required: true},
			},
		},
	}, nil
}

func (s *ChatCommandService) resolveEventAttendees(ctx context.Context, userID int64, req ChatCommandExecuteRequest) ([]int64, error) {
	if len(req.Args.AttendeeIDs) > 0 {
		return uniqueUserIDsExcept(req.Args.AttendeeIDs, userID), nil
	}

	if req.ChatID == nil {
		return nil, nil
	}

	var participantIDs []int64
	var err error
	switch strings.ToLower(strings.TrimSpace(req.ChatType)) {
	case "group":
		participantIDs, err = s.groupService.GetGroupMemberIDs(ctx, *req.ChatID, userID)
	default:
		participantIDs, err = s.chatService.GetConversationParticipantIDs(ctx, *req.ChatID, userID)
	}
	if err != nil {
		return nil, err
	}

	return uniqueUserIDsExcept(participantIDs, userID), nil
}

func uniqueUserIDsExcept(userIDs []int64, excludeID int64) []int64 {
	out := make([]int64, 0, len(userIDs))
	seen := make(map[int64]struct{}, len(userIDs))
	for _, id := range userIDs {
		if id == 0 || id == excludeID {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func (s *ChatCommandService) resolveBoard(ctx context.Context, userID int64, args ChatCommandTaskArgs) (uuid.UUID, *domain.BoardResponse, bool, error) {
	if args.BoardID != nil && *args.BoardID != uuid.Nil {
		board, err := s.kanbanService.GetBoard(ctx, userID, *args.BoardID)
		if err != nil {
			return uuid.Nil, nil, false, err
		}
		return *args.BoardID, board, false, nil
	}

	boards, err := s.kanbanService.ListUserBoards(ctx, userID)
	if err != nil {
		return uuid.Nil, nil, false, err
	}

	boardName := strings.TrimSpace(args.BoardName)
	if boardName != "" {
		for _, board := range boards {
			if strings.EqualFold(strings.TrimSpace(board.Name), boardName) {
				boardResp, err := s.kanbanService.GetBoard(ctx, userID, board.ID)
				if err != nil {
					return uuid.Nil, nil, false, err
				}
				return board.ID, boardResp, false, nil
			}
		}
		board, err := s.kanbanService.CreateBoard(ctx, userID, CreateBoardRequest{Name: boardName})
		if err != nil {
			return uuid.Nil, nil, false, err
		}
		boardResp, err := s.kanbanService.GetBoard(ctx, userID, board.ID)
		if err != nil {
			return uuid.Nil, nil, false, err
		}
		return board.ID, boardResp, false, nil
	}

	if len(boards) == 1 {
		boardResp, err := s.kanbanService.GetBoard(ctx, userID, boards[0].ID)
		if err != nil {
			return uuid.Nil, nil, false, err
		}
		return boards[0].ID, boardResp, false, nil
	}

	return uuid.Nil, nil, true, nil
}

func (s *ChatCommandService) findBoardByName(ctx context.Context, userID int64, boardName string) (*domain.Board, error) {
	boards, err := s.kanbanService.ListUserBoards(ctx, userID)
	if err != nil {
		return nil, err
	}
	boardName = strings.TrimSpace(boardName)
	for _, board := range boards {
		if strings.EqualFold(strings.TrimSpace(board.Name), boardName) {
			return board, nil
		}
	}
	return nil, nil
}

func (s *ChatCommandService) sendTaskCardToChat(ctx context.Context, userID int64, chatID uuid.UUID, chatType string, task *domain.Task, senderSessionKey string) (*domain.MessageView, error) {
	if task == nil {
		return nil, nil
	}

	metadataBytes, err := json.Marshal(map[string]any{
		"kind":                 "task",
		"task_id":              task.ID.String(),
		"title":                task.Title,
		"board_id":             task.BoardID.String(),
		"created_from_command": true,
	})
	if err != nil {
		return nil, apperrors.InternalWithErr("Не удалось сформировать metadata карточки задачи", err)
	}
	metadata := string(metadataBytes)
	content := "Создана задача: " + task.Title

	if chatType == "group" {
		return s.groupService.SendMessage(ctx, chatID, userID, content, "", "share_task", &metadata, nil, nil, senderSessionKey)
	}
	return s.chatService.SendMessage(ctx, chatID, userID, content, "", nil, "share_task", &metadata, nil, false, nil, nil, senderSessionKey)
}

func (s *ChatCommandService) sendEventCardToChat(ctx context.Context, userID int64, chatID uuid.UUID, chatType string, event *domain.Event, senderSessionKey string) (*domain.MessageView, error) {
	if event == nil {
		return nil, nil
	}

	location := ""
	if event.Location != nil {
		location = *event.Location
	}
	metadataBytes, err := json.Marshal(map[string]any{
		"kind":                 "event",
		"event_id":             event.ID.String(),
		"title":                event.Title,
		"start_time":           event.StartTime.Format(time.RFC3339),
		"end_time":             event.EndTime.Format(time.RFC3339),
		"location":             location,
		"created_from_command": true,
	})
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to build event card metadata", err)
	}
	metadata := string(metadataBytes)
	content := "Создано событие: " + event.Title

	if chatType == "group" {
		return s.groupService.SendMessage(ctx, chatID, userID, content, "", "share_event", &metadata, nil, nil, senderSessionKey)
	}
	return s.chatService.SendMessage(ctx, chatID, userID, content, "", nil, "share_event", &metadata, nil, false, nil, nil, senderSessionKey)
}

func (s *ChatCommandService) resolveDriveFileForShare(ctx context.Context, userID int64, args ChatCommandTaskArgs) (*domain.DriveFile, bool, error) {
	if args.FileID != nil && *args.FileID != uuid.Nil {
		file, err := s.driveService.GetFileMeta(ctx, userID, *args.FileID)
		return file, false, err
	}

	query := strings.TrimSpace(args.FileName)
	if query == "" {
		query = strings.TrimSpace(args.Query)
	}
	if query == "" {
		return nil, true, nil
	}

	items, err := s.driveService.SearchAndFilter(ctx, userID, query, args.FileType, 2, 0, nil, "")
	if err != nil {
		return nil, false, err
	}
	files := driveItemsToFiles(items)
	if len(files) == 1 {
		file, err := s.driveService.GetFileMeta(ctx, userID, files[0].ID)
		return file, false, err
	}
	return nil, true, nil
}

func (s *ChatCommandService) grantDriveFileAccessToChat(ctx context.Context, userID int64, req ChatCommandExecuteRequest, fileID uuid.UUID) error {
	if req.ChatID == nil {
		return nil
	}
	participantIDs, err := s.resolveChatParticipantIDs(ctx, userID, *req.ChatID, req.ChatType)
	if err != nil {
		return err
	}
	for _, participantID := range uniqueUserIDsExcept(participantIDs, userID) {
		if err := s.driveService.GrantPermission(ctx, userID, domain.ResourceFile, fileID, participantID, domain.RoleViewer); err != nil {
			if appErr, ok := err.(*apperrors.AppError); ok && appErr.Code == apperrors.ErrCodeForbidden {
				continue
			}
			return err
		}
	}
	return nil
}

func (s *ChatCommandService) resolveChatParticipantIDs(ctx context.Context, userID int64, chatID uuid.UUID, chatType string) ([]int64, error) {
	switch strings.ToLower(strings.TrimSpace(chatType)) {
	case "group":
		return s.groupService.GetGroupMemberIDs(ctx, chatID, userID)
	default:
		return s.chatService.GetConversationParticipantIDs(ctx, chatID, userID)
	}
}

func (s *ChatCommandService) sendDriveFileCardToChat(ctx context.Context, userID int64, chatID uuid.UUID, chatType string, file *domain.DriveFile, senderSessionKey string) (*domain.MessageView, error) {
	if file == nil {
		return nil, nil
	}
	s.driveService.fillFileUrl(file)
	downloadURL := strings.TrimSpace(file.DownloadURL)
	if downloadURL == "" && strings.TrimSpace(file.ObjectName) != "" {
		downloadURL = fmt.Sprintf("/api/v1/files/download?object_name=%s", url.QueryEscape(file.ObjectName))
	}
	metadataBytes, err := json.Marshal(map[string]any{
		"kind":         "file",
		"file_id":      file.ID.String(),
		"title":        file.RealName,
		"download_url": downloadURL,
		"mime_type":    file.MimeType,
		"size":         file.Size,
		"attachment": map[string]any{
			"object_name": file.ObjectName,
			"filename":    file.RealName,
			"size":        file.Size,
			"mime":        file.MimeType,
			"ext":         filepath.Ext(file.RealName),
			"upload_type": "file",
		},
	})
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to build file card metadata", err)
	}
	metadata := string(metadataBytes)
	content := downloadURL
	messageType := driveFileMessageType(file.MimeType)
	if chatType == "group" {
		return s.groupService.SendMessage(ctx, chatID, userID, content, "", messageType, &metadata, nil, nil, senderSessionKey)
	}
	return s.chatService.SendMessage(ctx, chatID, userID, content, "", nil, messageType, &metadata, nil, false, nil, nil, senderSessionKey)
}

func driveFileMessageType(mimeType string) string {
	mimeType = strings.ToLower(strings.TrimSpace(mimeType))
	switch {
	case strings.HasPrefix(mimeType, "image/"):
		return "image"
	case strings.HasPrefix(mimeType, "video/"):
		return "video"
	case strings.HasPrefix(mimeType, "audio/"):
		return "audio"
	default:
		return "file"
	}
}

func driveItemsToFiles(items []domain.DriveItem) []*domain.DriveFile {
	files := make([]*domain.DriveFile, 0, len(items))
	for _, item := range items {
		if item.Type == domain.ResourceFile && item.File != nil {
			files = append(files, item.File)
		}
	}
	return files
}

func recentItemsToFiles(items []domain.RecentItem) []*domain.DriveFile {
	files := make([]*domain.DriveFile, 0, len(items))
	for _, item := range items {
		if item.Type == domain.ResourceFile && item.File != nil {
			files = append(files, item.File)
		}
	}
	return files
}

func normalizeChatCommandLimit(value, fallback, max int) int {
	if value <= 0 {
		return fallback
	}
	if value > max {
		return max
	}
	return value
}

func shouldSendResultToChat(req ChatCommandExecuteRequest) bool {
	if req.SendResultToChat != nil {
		return *req.SendResultToChat
	}
	return req.ChatID != nil
}

func (s *ChatCommandService) resolveCommand(commandID, rawText string) (string, string) {
	commandID = strings.TrimSpace(commandID)
	if commandID != "" {
		return commandID, stripKnownAlias(commandID, rawText)
	}

	rawText = strings.TrimSpace(rawText)
	if rawText == "" {
		return "", ""
	}

	for _, def := range s.ListDefinitions() {
		for _, alias := range def.Aliases {
			if remainder, ok := trimAlias(rawText, alias); ok {
				return def.ID, remainder
			}
		}
	}

	return "", ""
}

func stripKnownAlias(commandID, rawText string) string {
	rawText = strings.TrimSpace(rawText)
	if rawText == "" {
		return ""
	}

	var aliases []string
	switch commandID {
	case ChatCommandUploadFile:
		aliases = []string{"/upload_file"}
	case ChatCommandFindFile:
		aliases = []string{"/find_file"}
	case ChatCommandRecentFiles:
		aliases = []string{"/recent_files"}
	case ChatCommandShareFile:
		aliases = []string{"/share_file"}
	case ChatCommandCreateEvent:
		aliases = []string{"/create_event"}
	case ChatCommandTodayEvents:
		aliases = []string{"/today_events"}
	case ChatCommandWeekEvents:
		aliases = []string{"/week_events"}
	case ChatCommandCreateKanbanBoard:
		aliases = []string{"/create_board"}
	case ChatCommandCreateKanbanTask:
		aliases = []string{"/create_task"}
	case ChatCommandListKanbanTasks:
		aliases = []string{"/list_tasks", "/list_task"}
	}
	for _, alias := range aliases {
		if remainder, ok := trimAlias(rawText, alias); ok {
			return remainder
		}
	}
	return rawText
}

func trimAlias(rawText, alias string) (string, bool) {
	rawText = strings.TrimSpace(rawText)
	alias = strings.TrimSpace(alias)
	if !strings.HasPrefix(strings.ToLower(rawText), strings.ToLower(alias)) {
		return "", false
	}
	if len(rawText) > len(alias) {
		next := rawText[len(alias)]
		if next != ' ' && next != '\t' && next != '\n' && next != '\r' {
			return "", false
		}
	}
	return strings.TrimSpace(rawText[len(alias):]), true
}
