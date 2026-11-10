package domain

import (
	"time"

	"github.com/google/uuid"
)

// ─── Gamification Action Constants ────────────────────────────────────────────
// Each constant maps to a specific user action tracked across the entire platform.
const (
	// ── Kanban / Tasks ────────────────────────────────────────────────────────
	ActionGamifTaskCreated = "TASK_CREATED"       // +2  Создал задачу
	ActionTaskClosed       = "TASK_CLOSED"        // +3  Закрыл задачу
	ActionTaskClosedFast   = "TASK_CLOSED_FAST"   // +5  Закрыл задачу быстро (до дедлайна)
	ActionGamifTaskUpdated = "TASK_UPDATED"       // +1  Обновил задачу
	ActionTaskCommentAdded = "TASK_COMMENT_ADDED" // +1  Оставил комментарий к задаче
	ActionTaskFileUploaded = "TASK_FILE_UPLOADED" // +1  Прикрепил файл к задаче
	ActionSprintCreated    = "SPRINT_CREATED"     // +3  Создал спринт
	ActionBoardCreated     = "BOARD_CREATED"      // +3  Создал доску

	// ── Calendar / Встречи ────────────────────────────────────────────────────
	ActionMeetingCreated  = "MEETING_CREATED"    // +3  Создал встречу
	ActionMeetingAttended = "MEETING_ATTENDED"   // +4  Принял приглашение на встречу
	ActionEventReminder   = "EVENT_REMINDER_SET" // +1  Установил напоминание

	// ── Сообщения / Чат ───────────────────────────────────────────────────────
	ActionMessageSent    = "MESSAGE_SENT"      // +1  Активность в чате за день (раз в день)
	ActionFileSentInChat = "FILE_SENT_IN_CHAT" // +1  Отправил файл в чат (раз в день)
	ActionGroupCreated   = "GROUP_CREATED"     // +2  Создал группу
	ActionBroadcastSent  = "BROADCAST_SENT"    // +3  Отправил массовое сообщение

	// ── Cloud Drive / Документы ───────────────────────────────────────────────
	ActionDocumentCreated = "DOCUMENT_CREATED" // +4  Создал документ / загрузил файл в диск
	ActionFolderCreated   = "FOLDER_CREATED"   // +1  Создал папку в диске
	ActionFileShared      = "FILE_SHARED"      // +2  Поделился файлом
	ActionFileDownloaded  = "FILE_DOWNLOADED"  // +1  Скачал файл (не свой)

	// ── Notebook / AI-заметки ─────────────────────────────────────────────────
	ActionNotebookChatCreated = "NOTEBOOK_CHAT_CREATED" // +2  Создал AI-заметку / чат
	ActionNotebookDocAdded    = "NOTEBOOK_DOC_ADDED"    // +1  Добавил документ в AI-чат

	// ── AI / Alem AI ──────────────────────────────────────────────────────────
	ActionAIChatCreated = "AI_CHAT_CREATED" // +2  Создал AI-чат
	ActionAIMessageSent = "AI_MESSAGE_SENT" // +1  Отправил сообщение в AI

	// ── Звонки ────────────────────────────────────────────────────────────────
	ActionCallStarted = "CALL_STARTED" // +2  Начал звонок
	ActionCallJoined  = "CALL_JOINED"  // +1  Присоединился к звонку

	// ── Профиль / Онбординг ───────────────────────────────────────────────────
	ActionOnboardingCompleted = "ONBOARDING_COMPLETED" // +5  Завершил онбординг
	ActionAvatarUploaded      = "AVATAR_UPLOADED"      // +2  Загрузил аватар
	ActionStatusChanged       = "STATUS_CHANGED"       // +1  Обновил статус

	// ── Ревью / Обратная связь ────────────────────────────────────────────────
	ActionReviewGiven = "REVIEW_GIVEN" // +3  Оставил ревью коллеге

	// ── Онлайн-активность ─────────────────────────────────────────────────────
	ActionDailyLogin = "DAILY_LOGIN" // +2  Вошёл в систему (раз в день)
)

// GamificationPoints maps each action to the number of points awarded.
var GamificationPoints = map[string]int{
	// Kanban / Tasks
	ActionGamifTaskCreated: 2,
	ActionTaskClosed:       3,
	ActionTaskClosedFast:   5,
	ActionGamifTaskUpdated: 1,
	ActionTaskCommentAdded: 1,
	ActionTaskFileUploaded: 1,
	ActionSprintCreated:    3,
	ActionBoardCreated:     3,

	// Calendar
	ActionMeetingCreated:  3,
	ActionMeetingAttended: 4,
	ActionEventReminder:   1,

	// Messaging
	ActionMessageSent:    1,
	ActionFileSentInChat: 1,
	ActionGroupCreated:   2,
	ActionBroadcastSent:  3,

	// Drive
	ActionDocumentCreated: 4,
	ActionFolderCreated:   1,
	ActionFileShared:      2,
	ActionFileDownloaded:  1,

	// Notebook
	ActionNotebookChatCreated: 2,
	ActionNotebookDocAdded:    1,

	// AI
	ActionAIChatCreated: 2,
	ActionAIMessageSent: 1,

	// Calls
	ActionCallStarted: 2,
	ActionCallJoined:  1,

	// Profile
	ActionOnboardingCompleted: 5,
	ActionAvatarUploaded:      2,
	ActionStatusChanged:       1,

	// Reviews
	ActionReviewGiven: 3,

	// Daily login
	ActionDailyLogin: 2,
}

type GamificationScore struct {
	UserID     int64     `json:"user_id" db:"user_id"`
	TotalScore int       `json:"total_score" db:"total_score"`
	Level      int       `json:"level" db:"level"`
	UpdatedAt  time.Time `json:"updated_at" db:"updated_at"`
}

type GamificationActivity struct {
	ID         uuid.UUID `json:"id" db:"id"`
	UserID     int64     `json:"user_id" db:"user_id"`
	ActionType string    `json:"action_type" db:"action_type"`
	Points     int       `json:"points" db:"points"`
	CreatedAt  time.Time `json:"created_at" db:"created_at"`
}

type UserGamificationDashboard struct {
	UserID      int64   `json:"user_id" db:"user_id"`
	FullName    string  `json:"full_name" db:"full_name"`
	Position    *string `json:"position,omitempty" db:"position"`
	AvatarURL   *string `json:"avatar_url,omitempty" db:"avatar_url"`
	TotalScore  int     `json:"total_score" db:"total_score"`
	Level       int     `json:"level" db:"level"`
	PeriodScore *int    `json:"period_score,omitempty" db:"-"` // Omit if not requested

}
