package domain

import "time"

type AgentIntentName string

const (
	AgentIntentGeneralChat     AgentIntentName = "general_chat"
	AgentIntentOpenScreen      AgentIntentName = "open_screen"
	AgentIntentSearch          AgentIntentName = "search"
	AgentIntentWebSearch       AgentIntentName = "web_search"
	AgentIntentGetTasks        AgentIntentName = "get_tasks"
	AgentIntentGetTaskBoards   AgentIntentName = "get_task_boards"
	AgentIntentGetTaskSummary  AgentIntentName = "get_task_summary"
	AgentIntentGetCalendar     AgentIntentName = "get_calendar"
	AgentIntentCreateEvent     AgentIntentName = "create_event"
	AgentIntentSetReminder     AgentIntentName = "set_event_reminder"
	AgentIntentFindSlots       AgentIntentName = "find_available_slots"
	AgentIntentCreateReminder  AgentIntentName = "create_reminder"
	AgentIntentCreateTask      AgentIntentName = "create_task"
	AgentIntentUpdateTask      AgentIntentName = "update_task"
	AgentIntentGetNotes        AgentIntentName = "get_notes"
	AgentIntentGetNoteFolders  AgentIntentName = "get_note_folders"
	AgentIntentCreateNote      AgentIntentName = "create_note"
	AgentIntentUpdateNote      AgentIntentName = "update_note"
	AgentIntentDeleteNote      AgentIntentName = "delete_note"
	AgentIntentGetDriveFiles   AgentIntentName = "get_drive_files"
	AgentIntentGetDriveAccess  AgentIntentName = "get_drive_access"
	AgentIntentShareDrive      AgentIntentName = "share_drive_resource"
	AgentIntentCreateDriveLink AgentIntentName = "create_drive_share_link"
	AgentIntentCreateMeeting   AgentIntentName = "create_online_meeting"
	AgentIntentGetNotify       AgentIntentName = "get_notifications"
	AgentIntentStartCall       AgentIntentName = "start_call"
	AgentIntentSummarize       AgentIntentName = "summarize"
	AgentIntentFillForm        AgentIntentName = "fill_form"
	AgentIntentGetStatus       AgentIntentName = "get_status"
	AgentIntentHelp            AgentIntentName = "help"
	AgentIntentUnknown         AgentIntentName = "unknown"
)

type AgentSessionStatus string

const (
	AgentSessionActive AgentSessionStatus = "active"
	AgentSessionClosed AgentSessionStatus = "closed"
)

type AgentConfirmationStatus string

const (
	AgentConfirmationPending  AgentConfirmationStatus = "pending"
	AgentConfirmationExecuted AgentConfirmationStatus = "executed"
	AgentConfirmationExpired  AgentConfirmationStatus = "expired"
	AgentConfirmationCanceled AgentConfirmationStatus = "canceled"
)

type AgentRole string

const (
	AgentRoleUser      AgentRole = "user"
	AgentRoleAssistant AgentRole = "assistant"
	AgentRoleTool      AgentRole = "tool"
	AgentRoleSystem    AgentRole = "system"
)

type AgentUserContext struct {
	UserID              int64
	Role                string
	RequestID           string
	IP                  string
	UserAgent           string
	AuthorizationHeader string
	CookieHeader        string
}

type AgentOptions struct {
	ExecuteActions bool `json:"execute_actions,omitempty"`
	DryRun         bool `json:"dry_run,omitempty"`
	Stream         bool `json:"stream,omitempty"`
}

type AgentChatRequest struct {
	SessionID string                 `json:"session_id,omitempty"`
	Message   string                 `json:"message"`
	InputType string                 `json:"input_type,omitempty"`
	Locale    string                 `json:"locale,omitempty"`
	Timezone  string                 `json:"timezone,omitempty"`
	Context   map[string]interface{} `json:"context,omitempty"`
	Options   AgentOptions           `json:"options,omitempty"`
}

type AgentIntentResult struct {
	Name                 AgentIntentName        `json:"name"`
	Confidence           float64                `json:"confidence"`
	Entities             map[string]interface{} `json:"entities,omitempty"`
	RequiresConfirmation bool                   `json:"requires_confirmation"`
	RequiredPermissions  []string               `json:"required_permissions,omitempty"`
	SuggestedActions     []AgentAction          `json:"suggested_actions,omitempty"`
}

type AgentAction struct {
	Type     string                 `json:"type"`
	Label    string                 `json:"label,omitempty"`
	Payload  map[string]interface{} `json:"payload,omitempty"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

type AgentChatResponse struct {
	Answer               string                 `json:"answer"`
	Message              string                 `json:"message,omitempty"`
	SessionID            string                 `json:"session_id"`
	Intent               *AgentIntentResult     `json:"intent,omitempty"`
	Entities             map[string]interface{} `json:"entities,omitempty"`
	Actions              []AgentAction          `json:"actions,omitempty"`
	Suggestions          []string               `json:"suggestions,omitempty"`
	RequiresConfirmation bool                   `json:"requires_confirmation"`
	ConfirmationToken    string                 `json:"confirmation_token,omitempty"`
	ProposedAction       *AgentAction           `json:"proposed_action,omitempty"`
	Metadata             map[string]interface{} `json:"metadata,omitempty"`
}

type AgentSession struct {
	ID        string                 `json:"id"`
	UserID    int64                  `json:"user_id"`
	Status    AgentSessionStatus     `json:"status"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt time.Time              `json:"created_at"`
	UpdatedAt time.Time              `json:"updated_at"`
	ExpiresAt time.Time              `json:"expires_at"`
}

type AgentMessage struct {
	ID        string                 `json:"id"`
	SessionID string                 `json:"session_id"`
	UserID    int64                  `json:"user_id"`
	Role      AgentRole              `json:"role"`
	Content   string                 `json:"content"`
	Intent    string                 `json:"intent,omitempty"`
	Entities  map[string]interface{} `json:"entities,omitempty"`
	Actions   []AgentAction          `json:"actions,omitempty"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt time.Time              `json:"created_at"`
}

type AgentActionConfirmation struct {
	ID         string                  `json:"id"`
	SessionID  string                  `json:"session_id"`
	UserID     int64                   `json:"user_id"`
	TokenHash  string                  `json:"-"`
	ActionType string                  `json:"action_type"`
	Payload    map[string]interface{}  `json:"payload"`
	Status     AgentConfirmationStatus `json:"status"`
	ExpiresAt  time.Time               `json:"expires_at"`
	ExecutedAt *time.Time              `json:"executed_at,omitempty"`
	CreatedAt  time.Time               `json:"created_at"`
}

type AgentFeedback struct {
	ID        string                 `json:"id"`
	UserID    int64                  `json:"user_id"`
	SessionID string                 `json:"session_id"`
	MessageID *string                `json:"message_id,omitempty"`
	Rating    int                    `json:"rating"`
	Comment   string                 `json:"comment,omitempty"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt time.Time              `json:"created_at"`
}

type AgentAuditLog struct {
	ID        string                 `json:"id"`
	UserID    int64                  `json:"user_id"`
	SessionID string                 `json:"session_id"`
	EventType string                 `json:"event_type"`
	Payload   map[string]interface{} `json:"payload,omitempty"`
	IP        string                 `json:"ip,omitempty"`
	UserAgent string                 `json:"user_agent,omitempty"`
	CreatedAt time.Time              `json:"created_at"`
}

type AgentCapability struct {
	Name                 string   `json:"name"`
	Description          string   `json:"description"`
	RequiresConfirmation bool     `json:"requires_confirmation"`
	SupportedInputTypes  []string `json:"supported_input_types,omitempty"`
}
