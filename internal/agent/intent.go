package agent

import (
	"context"
	"strings"

	"github.com/west6ide/workspace-backend/internal/domain"
)

type keywordIntentResolver struct {
	threshold float64
}

func NewKeywordIntentResolver(threshold float64) IntentResolver {
	return &keywordIntentResolver{threshold: threshold}
}

func (r *keywordIntentResolver) Resolve(_ context.Context, req *domain.AgentChatRequest, _ *domain.AgentSession, _ []domain.AgentMessage) (*domain.AgentIntentResult, error) {
	msg := strings.ToLower(strings.TrimSpace(req.Message))
	intent := &domain.AgentIntentResult{
		Name:       domain.AgentIntentGeneralChat,
		Confidence: 0.55,
	}

	switch {
	case containsAny(msg, "в интернете", "в интернет", "поиск в интернете", "найди в интернете", "поищи в интернете", "web search", "internet search", "search the web"):
		intent.Name = domain.AgentIntentWebSearch
		intent.Confidence = 0.96
	case containsAny(msg, "открой", "open") && containsAny(msg, "задач", "task", "экран", "screen", "уведомлен", "notification"):
		intent.Name = domain.AgentIntentOpenScreen
		intent.Confidence = 0.94
	case containsAny(msg, "доски", "kanban boards", "kanban board", "список досок", "мои доски"):
		intent.Name = domain.AgentIntentGetTaskBoards
		intent.Confidence = 0.94
	case containsAny(msg, "сводка по задачам", "задачи по доскам", "summary by board", "my task summary"):
		intent.Name = domain.AgentIntentGetTaskSummary
		intent.Confidence = 0.95
	case containsAny(msg, "мои задачи", "мои активные задачи", "активные задачи", "задачи на сегодня", "покажи задачи", "show my tasks", "my tasks", "today tasks"):
		intent.Name = domain.AgentIntentGetTasks
		intent.Confidence = 0.96
	case containsAny(msg, "мои встречи", "мой календар", "календарь", "календаре сегодня", "встречи сегодня", "что у меня сегодня", "что у меня в календаре", "calendar", "my meetings", "my calendar", "today meetings"):
		intent.Name = domain.AgentIntentGetCalendar
		intent.Confidence = 0.95
	case containsAny(msg, "свободные слоты", "найди слот", "available slots", "find slot", "окна в календаре"):
		intent.Name = domain.AgentIntentFindSlots
		intent.Confidence = 0.92
	case containsAny(msg, "онлайн-встреч", "онлайн встреч", "online meeting", "meeting link", "ссылка на встречу"):
		intent.Name = domain.AgentIntentCreateMeeting
		intent.Confidence = 0.92
		intent.RequiresConfirmation = true
		intent.RequiredPermissions = []string{"calendar.write"}
	case containsAny(msg, "создай встречу", "создай событие", "добавь в календарь", "create event", "calendar event"):
		intent.Name = domain.AgentIntentCreateEvent
		intent.Confidence = 0.91
		intent.RequiresConfirmation = true
		intent.RequiredPermissions = []string{"calendar.write"}
	case containsAny(msg, "напоминание для события", "напомни о встрече", "set reminder", "event reminder"):
		intent.Name = domain.AgentIntentSetReminder
		intent.Confidence = 0.9
		intent.RequiresConfirmation = true
		intent.RequiredPermissions = []string{"calendar.write"}
	case containsAny(msg, "напомин", "remind"):
		intent.Name = domain.AgentIntentCreateReminder
		intent.Confidence = 0.93
		intent.RequiresConfirmation = true
		intent.RequiredPermissions = []string{"calendar.write"}
	case containsAny(msg, "папки заметок", "note folders", "folders for notes"):
		intent.Name = domain.AgentIntentGetNoteFolders
		intent.Confidence = 0.94
	case containsAny(msg, "создай заметку", "create note", "новая заметка"):
		intent.Name = domain.AgentIntentCreateNote
		intent.Confidence = 0.92
		intent.RequiresConfirmation = true
	case containsAny(msg, "обнови заметку", "измени заметку", "update note", "edit note"):
		intent.Name = domain.AgentIntentUpdateNote
		intent.Confidence = 0.91
		intent.RequiresConfirmation = true
	case containsAny(msg, "удали заметку", "delete note", "remove note"):
		intent.Name = domain.AgentIntentDeleteNote
		intent.Confidence = 0.91
		intent.RequiresConfirmation = true
	case containsAny(msg, "заметк", "notes", "note"):
		intent.Name = domain.AgentIntentGetNotes
		intent.Confidence = 0.88
	case containsAny(msg, "кто имеет доступ", "кто видит файл", "file access", "drive access", "доступ к файлу"):
		intent.Name = domain.AgentIntentGetDriveAccess
		intent.Confidence = 0.93
	case containsAny(msg, "создай ссылку", "share link", "публичную ссылку", "ссылку на файл"):
		intent.Name = domain.AgentIntentCreateDriveLink
		intent.Confidence = 0.91
		intent.RequiresConfirmation = true
	case containsAny(msg, "поделись файлом", "поделись папкой", "share file", "share folder", "выдай доступ"):
		intent.Name = domain.AgentIntentShareDrive
		intent.Confidence = 0.92
		intent.RequiresConfirmation = true
	case containsAny(msg, "drive", "файл", "документ", "pdf", "таблиц", "презентац"):
		intent.Name = domain.AgentIntentGetDriveFiles
		intent.Confidence = 0.82
	case containsAny(msg, "создай задачу", "create task"):
		intent.Name = domain.AgentIntentCreateTask
		intent.Confidence = 0.88
		intent.RequiresConfirmation = true
		intent.RequiredPermissions = []string{"kanban.write"}
	case containsAny(msg, "обнови задачу", "update task", "закрой задачу"):
		intent.Name = domain.AgentIntentUpdateTask
		intent.Confidence = 0.86
		intent.RequiresConfirmation = true
		intent.RequiredPermissions = []string{"kanban.write"}
	case containsAny(msg, "уведомлен", "notification"):
		intent.Name = domain.AgentIntentGetNotify
		intent.Confidence = 0.95
	case containsAny(msg, "рассылк", "broadcast", "отправь всем", "send to many"):
		intent.Name = domain.AgentIntentSendBroadcast
		intent.Confidence = 0.93
		intent.RequiresConfirmation = true
	case containsAny(msg, "позвони", "call"):
		intent.Name = domain.AgentIntentStartCall
		intent.Confidence = 0.89
		intent.RequiresConfirmation = true
	case containsAny(msg, "краткое резюме", "summary", "summarize", "резюме", "итог"):
		intent.Name = domain.AgentIntentSummarize
		intent.Confidence = 0.9
	case containsAny(msg, "заполни форму", "fill form", "форму"):
		intent.Name = domain.AgentIntentFillForm
		intent.Confidence = 0.84
		intent.RequiresConfirmation = true
	case containsAny(msg, "статус сервиса", "service status", "status"):
		intent.Name = domain.AgentIntentGetStatus
		intent.Confidence = 0.92
	case containsAny(msg, "помоги", "help", "навигац"):
		intent.Name = domain.AgentIntentHelp
		intent.Confidence = 0.91
	case containsAny(msg, "найди", "search", "поиск"):
		intent.Name = domain.AgentIntentSearch
		intent.Confidence = 0.8
	default:
		intent.Name = domain.AgentIntentGeneralChat
		if len(strings.Fields(msg)) <= 2 {
			intent.Confidence = 0.4
		}
	}

	if intent.Confidence < r.threshold {
		intent.SuggestedActions = []domain.AgentAction{
			{Type: "clarify", Label: "Уточнить запрос"},
			{Type: "open_capabilities", Label: "Показать возможности"},
		}
	}

	return intent, nil
}

func containsAny(message string, parts ...string) bool {
	for _, part := range parts {
		if strings.Contains(message, part) {
			return true
		}
	}
	return false
}
