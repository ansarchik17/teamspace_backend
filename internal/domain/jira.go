package domain

import (
	"time"

	"github.com/google/uuid"
)

const (
	ColTODO       = "TODO"
	ColInProgress = "IN_PROGRESS"
	ColTest       = "TEST"
	ColDone       = "DONE"
	ColBlock      = "BLOCK"

	SprintStatusPlanned = "planned"
	SprintStatusActive  = "active"
	SprintStatusClosed  = "closed"

	StatusTodo    = "К ВЫПОЛНЕНИЮ"
	StatusWorking = "В РАБОТЕ"
	StatusReview  = "В ПРОЦЕССЕ ПРОВЕРКИ"
	StatusDone    = "ГОТОВО"

	BoardRoleOwner  = "owner" // Board creator; retains administrator permissions.
	BoardRoleAdmin  = "admin"
	BoardRoleViewer = "viewer"
	BoardRoleEditor = "editor"
	BoardRoleMember = "member"
)

var DefaultColumns = []struct {
	Key      string
	Name     string
	Position int
}{
	{ColTODO, "To Do", 10},
	{ColInProgress, "In Progress", 20},
	{ColTest, "Test", 30},
	{ColDone, "Done", 40},
	{ColBlock, "Block", 50},
}

// TaskFilters moved here to be accessible by repositories and services
type TaskFilters struct {
	AuthorIDs   []int64     `json:"author_ids"`
	Statuses    []string    `json:"statuses"`
	BoardID     *uuid.UUID  `json:"board_id"`
	Query       string      `json:"query"`
	AssigneeIDs []int64     `json:"assignee_ids"`
	Priorities  []string    `json:"priorities"`
	ColumnIDs   []uuid.UUID `json:"column_ids"`
	ColumnKeys  []string    `json:"column_keys"`
	DateFrom    *time.Time  `json:"date_from"`
	DateTo      *time.Time  `json:"date_to"`
	LabelIDs    []uuid.UUID `json:"label_ids"`
	SprintIDs   []uuid.UUID `json:"sprint_ids"`
	Limit       int         `json:"limit"`
	Offset      int         `json:"offset"`
}

type KanbanLabel struct {
	ID        uuid.UUID `json:"id" db:"id"`
	BoardID   uuid.UUID `json:"board_id" db:"board_id"`
	Name      string    `json:"name" db:"name"`
	Color     string    `json:"color" db:"color"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}

type Board struct {
	ID                      uuid.UUID     `json:"id" db:"id"`
	OwnerID                 int64         `json:"owner_id" db:"owner_id"`
	OwnerFirstName          string        `json:"owner_first_name,omitempty" db:"owner_first_name"`
	OwnerLastName           string        `json:"owner_last_name,omitempty" db:"owner_last_name"`
	OwnerFullName           string        `json:"owner_full_name,omitempty" db:"owner_full_name"`
	Name                    string        `json:"name" db:"name"`
	NextDueAt               *time.Time    `json:"next_due_at,omitempty" db:"next_due_at"`
	SystemKey               string        `json:"-" db:"system_key"`
	TaskKeyPrefix           string        `json:"task_key_prefix" db:"task_key_prefix"`
	ManualCompletionPercent *int          `json:"-" db:"manual_completion_percent"`
	Progress                BoardProgress `json:"progress" db:"-"`
	TaskSequence            int           `json:"-" db:"task_sequence"`
	BgMode                  string        `json:"-" db:"background_mode"`
	BgPreset                string        `json:"-" db:"background_preset_id"`
	BgURL                   *string       `json:"-" db:"background_image_url"`
	CreatedAt               time.Time     `json:"created_at" db:"created_at"`
	UpdatedAt               time.Time     `json:"updated_at" db:"updated_at"`
	DeletedAt               *time.Time    `json:"-" db:"deleted_at"`
}

type BoardProgress struct {
	TotalTasks        int `json:"total_tasks"`
	CompletedTasks    int `json:"completed_tasks"`
	CompletionPercent int `json:"completion_percent"`
}

type BoardMember struct {
	BoardID  uuid.UUID `json:"board_id" db:"board_id"`
	UserID   int64     `json:"user_id" db:"user_id"`
	FullName string    `json:"full_name" db:"full_name"`
	Avatar   *string   `json:"avatar,omitempty" db:"avatar"`
	Role     string    `json:"role" db:"role"`
	TeamRole *string   `json:"team_role,omitempty" db:"team_role"`
	JoinedAt time.Time `json:"joined_at" db:"joined_at"`
}

type Sprint struct {
	ID        uuid.UUID  `json:"id" db:"id"`
	BoardID   uuid.UUID  `json:"board_id" db:"board_id"`
	GoalID    *uuid.UUID `json:"goal_id,omitempty" db:"goal_id"`
	ProjectID *uuid.UUID `json:"project_id,omitempty" db:"project_id"`
	Name      string     `json:"name" db:"name"`
	Status    string     `json:"status" db:"status"`
	StartDate *time.Time `json:"start_date,omitempty" db:"start_date"`
	EndDate   *time.Time `json:"end_date,omitempty" db:"end_date"`
	CreatedAt time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt time.Time  `json:"updated_at" db:"updated_at"`
}

type Goal struct {
	ID                uuid.UUID  `json:"id" db:"id"`
	BoardID           uuid.UUID  `json:"board_id" db:"board_id"`
	Name              string     `json:"name" db:"name"`
	Description       *string    `json:"description,omitempty" db:"description"`
	TaskCount         int        `json:"task_count" db:"-"`
	CompletedTasks    int        `json:"completed_tasks" db:"-"`
	ActiveTasks       int        `json:"active_tasks" db:"-"`
	OverdueTasks      int        `json:"overdue_tasks" db:"-"`
	ProjectCount      int        `json:"project_count" db:"-"`
	SprintCount       int        `json:"sprint_count" db:"-"`
	CompletionPercent int        `json:"completion_percent" db:"-"`
	TopProjectName    string     `json:"top_project_name,omitempty" db:"-"`
	PredictionLabel   string     `json:"prediction_label,omitempty" db:"-"`
	NextActionLabel   string     `json:"next_action_label,omitempty" db:"-"`
	CreatedBy         int64      `json:"created_by" db:"created_by"`
	CreatedAt         time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at" db:"updated_at"`
	DeletedAt         *time.Time `json:"-" db:"deleted_at"`
}

type GoalBoardItem struct {
	Goal
	BoardName string `json:"board_name" db:"board_name"`
}

type Project struct {
	OwnerID     int64      `json:"owner_id" db:"owner_id"`
	ID          uuid.UUID  `json:"id" db:"id"`
	BoardID     uuid.UUID  `json:"board_id" db:"board_id"`
	GoalID      *uuid.UUID `json:"goal_id,omitempty" db:"goal_id"`
	Name        string     `json:"name" db:"name"`
	Description *string    `json:"description,omitempty" db:"description"`
	Status      string     `json:"status" db:"status"`
	CreatedBy   int64      `json:"created_by" db:"created_by"`
	CreatedAt   time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at" db:"updated_at"`
	DeletedAt   *time.Time `json:"-" db:"deleted_at"`
}

type KanbanColumn struct {
	ID        uuid.UUID `json:"id" db:"id"`
	BoardID   uuid.UUID `json:"board_id" db:"board_id"`
	UserID    int64     `json:"user_id,omitempty" db:"user_id"`
	Key       string    `json:"key" db:"key"`
	Name      string    `json:"name" db:"name"`
	Position  int       `json:"position" db:"position"`
	IsSystem  bool      `json:"is_system" db:"is_system"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

type Task struct {
	ID                   uuid.UUID         `json:"id" db:"id"`
	BoardID              uuid.UUID         `json:"board_id" db:"board_id"`
	GoalID               *uuid.UUID        `json:"goal_id,omitempty" db:"goal_id"`
	ProjectID            *uuid.UUID        `json:"project_id,omitempty" db:"project_id"`
	SprintID             *uuid.UUID        `json:"sprint_id,omitempty" db:"sprint_id"`
	ColumnID             uuid.UUID         `json:"column_id" db:"column_id"`
	CreatedBy            int64             `json:"created_by" db:"created_by"`
	TaskNumber           int               `json:"task_number" db:"task_number"`
	TaskKey              string            `json:"task_key" db:"task_key"`
	Link                 string            `json:"link" db:"-"`
	AssigneeIDs          []int64           `json:"assignee_ids" db:"-"`
	Title                string            `json:"title" db:"title"`
	Description          *string           `json:"description,omitempty" db:"description"`
	Priority             string            `json:"priority" db:"priority"`
	DueAt                *time.Time        `json:"due_at,omitempty" db:"due_at"`
	OriginalEstimateSec  int               `json:"original_estimate_sec" db:"original_estimate_sec"`
	TimeSpentSec         int               `json:"time_spent_sec" db:"time_spent_sec"`
	CreatedAt            time.Time         `json:"created_at" db:"created_at"`
	UpdatedAt            time.Time         `json:"updated_at" db:"updated_at"`
	DeletedAt            *time.Time        `json:"-" db:"deleted_at"`
	Attachments          []*TaskAttachment `json:"attachments,omitempty" db:"-"`
	ParentID             *uuid.UUID        `json:"parent_id,omitempty" db:"parent_id"`
	Status               string            `json:"status" db:"status"`
	SourceSystem         string            `json:"source_system,omitempty" db:"source_system"`
	SourceAssignmentID   string            `json:"source_assignment_id,omitempty" db:"source_assignment_id"`
	SourceDocumentID     string            `json:"source_document_id,omitempty" db:"source_document_id"`
	SourceURL            string            `json:"source_url,omitempty" db:"source_url"`
	SourceAuthorSub      string            `json:"source_author_sub,omitempty" db:"source_author_sub"`
	SourceDepartmentID   string            `json:"source_department_id,omitempty" db:"source_department_id"`
	SourceOrganizationID *uuid.UUID        `json:"source_organization_id,omitempty"`
	SourceRevision       int64             `json:"source_revision,omitempty"`
	SourceStatus         string            `json:"source_status,omitempty"`
	SourceDueDate        *string           `json:"source_due_date"`
	SourceTimezone       string            `json:"source_timezone,omitempty"`
	SourceCompletedAt    *time.Time        `json:"source_completed_at"`
	SourceClosedAt       *time.Time        `json:"source_closed_at"`
	ColumnKey            string            `json:"column_key,omitempty" db:"-"`
	Subtasks             []*Task           `json:"subtasks,omitempty" db:"-"`
	Labels               []*KanbanLabel    `json:"labels,omitempty" db:"-"`
}

type TaskAttachment struct {
	ID         uuid.UUID `json:"id" db:"id"`
	TaskID     uuid.UUID `json:"task_id" db:"task_id"`
	UploadedBy int64     `json:"uploaded_by" db:"uploaded_by"`
	CreatedAt  time.Time `json:"created_at" db:"created_at"`

	FileName   string `json:"file_name" db:"file_name"`
	ObjectName string `json:"-" db:"object_name"`
	FileSize   int64  `json:"file_size" db:"file_size"`
	MimeType   string `json:"mime_type" db:"mime_type"`

	DownloadURL string `json:"download_url" db:"-"`
}

type TaskWorklog struct {
	ID           uuid.UUID `json:"id" db:"id"`
	TaskID       uuid.UUID `json:"task_id" db:"task_id"`
	UserID       int64     `json:"user_id" db:"user_id"`
	TimeSpentSec int       `json:"time_spent_sec" db:"time_spent_sec"`
	StartedAt    time.Time `json:"started_at" db:"started_at"`
	Comment      string    `json:"comment" db:"comment"`
	CreatedAt    time.Time `json:"created_at" db:"created_at"`
}

type ColumnWithTasks struct {
	Column     *KanbanColumn `json:"column"`
	Tasks      []*Task       `json:"tasks"`
	TotalTasks int           `json:"total_tasks"`
}

type BoardBackground struct {
	Mode      string  `json:"mode"`
	PresetID  string  `json:"preset_id"`
	PresetHex string  `json:"preset_hex"`
	ImageURL  *string `json:"image_url"`
}

type BoardResponse struct {
	Board      *Board             `json:"board"`
	Background *BoardBackground   `json:"background"`
	Goals      []*Goal            `json:"goals,omitempty"`
	Projects   []*Project         `json:"projects,omitempty"`
	Sprints    []*Sprint          `json:"sprints,omitempty"`
	Columns    []*ColumnWithTasks `json:"columns"`
	Members    []*BoardMember     `json:"members,omitempty"`
}

type BoardTasksByUserResponse struct {
	Data []BoardTasksByUserItem `json:"data"`
}

type UserTaskBoardsSummaryResponse struct {
	UserID      int64                       `json:"user_id"`
	TotalBoards int                         `json:"total_boards"`
	TotalTasks  int                         `json:"total_tasks"`
	Boards      []UserTaskBoardsSummaryItem `json:"boards"`
}

type MyTasksByStatusResponse struct {
	UserID          int64   `json:"user_id"`
	TotalTasks      int     `json:"total_tasks"`
	TodoCount       int     `json:"todo_count"`
	InProgressCount int     `json:"in_progress_count"`
	TestCount       int     `json:"test_count"`
	Todo            []*Task `json:"todo"`
	InProgress      []*Task `json:"in_progress"`
	Test            []*Task `json:"test"`
}

type UserTaskBoardsSummaryItem struct {
	BoardID         uuid.UUID                     `json:"board_id"`
	BoardName       string                        `json:"board_name"`
	TaskCount       int                           `json:"task_count"`
	TodoCount       int                           `json:"todo_count"`
	InProgressCount int                           `json:"in_progress_count"`
	ReviewCount     int                           `json:"review_count"`
	DoneCount       int                           `json:"done_count"`
	BlockedCount    int                           `json:"blocked_count"`
	OverdueCount    int                           `json:"overdue_count"`
	Columns         []UserTaskBoardsSummaryColumn `json:"columns,omitempty"`
}

type UserTaskBoardsSummaryColumn struct {
	ColumnID     uuid.UUID `json:"column_id"`
	ColumnKey    string    `json:"column_key"`
	ColumnName   string    `json:"column_name"`
	TaskCount    int       `json:"task_count"`
	OverdueCount int       `json:"overdue_count"`
}

type BoardTasksByUserItem struct {
	User  BoardTasksByUserUser   `json:"user"`
	Tasks []BoardTasksByUserTask `json:"tasks"`
}

type BoardTasksByUserUser struct {
	ID       int64   `json:"id"`
	FIO      string  `json:"fio"`
	Role     string  `json:"role"`
	TeamRole *string `json:"team_role,omitempty"`
}

type BoardTasksByUserTask struct {
	ID          uuid.UUID  `json:"id"`
	Title       string     `json:"title"`
	Number      int        `json:"number"`
	Description *string    `json:"description"`
	StartDate   *time.Time `json:"start_date"`
	EndDate     *time.Time `json:"end_date"`
	Status      string     `json:"status"`
}

type AssistantBoardTasksResponse struct {
	Data []AssistantBoardUserTasksItem `json:"data"`
}

type AssistantBoardUserTasksItem struct {
	User  AssistantBoardUser `json:"user"`
	Tasks []AssistantTask    `json:"tasks"`
}

type AssistantBoardUser struct {
	ID       int64   `json:"id"`
	FIO      string  `json:"fio"`
	Role     string  `json:"role"`
	TeamRole *string `json:"team_role,omitempty"`
}

type AssistantTask struct {
	ID        uuid.UUID  `json:"id"`
	Number    int        `json:"number"`
	Title     string     `json:"title"`
	Status    string     `json:"status"`
	CreatedAt time.Time  `json:"created_at"`
	ClosedAt  *time.Time `json:"closed_at,omitempty"`
}

type BoardStatsPeriod struct {
	DateFrom *time.Time `json:"date_from,omitempty"`
	DateTo   *time.Time `json:"date_to,omitempty"`
}

type BoardStatsSummary struct {
	TotalTasks           int     `json:"total_tasks"`
	CompletedTasks       int     `json:"completed_tasks"`
	ActiveTasks          int     `json:"active_tasks"`
	OverdueTasks         int     `json:"overdue_tasks"`
	UnassignedTasks      int     `json:"unassigned_tasks"`
	CompletionPercent    float64 `json:"completion_percent"`
	TotalEstimateSeconds int64   `json:"total_estimate_seconds"`
	TotalSpentSeconds    int64   `json:"total_spent_seconds"`
	AvgCompletionSeconds int64   `json:"avg_completion_seconds"`
}

type BoardStatsCountItem struct {
	Key     string  `json:"key"`
	Name    string  `json:"name"`
	Count   int     `json:"count"`
	Percent float64 `json:"percent"`
}

type BoardStatsColumnItem struct {
	ColumnID uuid.UUID `json:"column_id"`
	Key      string    `json:"key"`
	Name     string    `json:"name"`
	Position int       `json:"position"`
	Count    int       `json:"count"`
	Percent  float64   `json:"percent"`
}

type BoardStatsEmployee struct {
	UserID               int64   `json:"user_id"`
	FullName             string  `json:"full_name"`
	AssignedTasks        int     `json:"assigned_tasks"`
	CreatedTasks         int     `json:"created_tasks"`
	CompletedTasks       int     `json:"completed_tasks"`
	ActiveTasks          int     `json:"active_tasks"`
	OverdueTasks         int     `json:"overdue_tasks"`
	CompletionPercent    float64 `json:"completion_percent"`
	OverduePercent       float64 `json:"overdue_percent"`
	TotalEstimateSeconds int64   `json:"total_estimate_seconds"`
	TotalSpentSeconds    int64   `json:"total_spent_seconds"`
	WorklogSeconds       int64   `json:"worklog_seconds"`
	ProductivityScore    float64 `json:"productivity_score"`
}

type BoardStatsActivityItem struct {
	Action string `json:"action"`
	Count  int    `json:"count"`
}

type BoardStatsWorklogDay struct {
	Date         string `json:"date"`
	TotalSeconds int64  `json:"total_seconds"`
}

type BoardStatsResponse struct {
	BoardID    uuid.UUID                `json:"board_id"`
	BoardName  string                   `json:"board_name"`
	Period     BoardStatsPeriod         `json:"period"`
	Summary    BoardStatsSummary        `json:"summary"`
	ByStatus   []BoardStatsCountItem    `json:"by_status"`
	ByColumn   []BoardStatsColumnItem   `json:"by_column"`
	ByPriority []BoardStatsCountItem    `json:"by_priority"`
	ByDeadline []BoardStatsCountItem    `json:"by_deadline"`
	Employees  []BoardStatsEmployee     `json:"employees"`
	Activity   []BoardStatsActivityItem `json:"activity"`
	Worklogs   []BoardStatsWorklogDay   `json:"worklogs"`
}

type OrgTaskStatusStat struct {
	Status string `json:"status"`
	Count  int    `json:"count"`
}

type OrgProjectStats struct {
	BoardID    uuid.UUID           `json:"board_id"`
	BoardName  string              `json:"board_name"`
	TotalTasks int                 `json:"total_tasks"`
	Statuses   []OrgTaskStatusStat `json:"statuses"`
}

type OrgClosedTaskItem struct {
	TaskID    uuid.UUID `json:"task_id"`
	Title     string    `json:"title"`
	BoardID   uuid.UUID `json:"board_id"`
	BoardName string    `json:"board_name"`
	ClosedAt  time.Time `json:"closed_at"`
}

type OrgClosedTasksByUser struct {
	UserID      int64               `json:"user_id"`
	FullName    string              `json:"full_name"`
	ClosedCount int                 `json:"closed_count"`
	Tasks       []OrgClosedTaskItem `json:"tasks"`
}

type OrgKanbanStatsResponse struct {
	StateBodyID int64                  `json:"state_body_id"`
	Summary     []OrgTaskStatusStat    `json:"summary"`
	Projects    []OrgProjectStats      `json:"projects"`
	ClosedBy    []OrgClosedTasksByUser `json:"closed_by"`
}

type StateBodyStatsAccessEntry struct {
	UserID    int64     `json:"user_id"`
	FullName  string    `json:"full_name"`
	GrantedBy *int64    `json:"granted_by,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type StateBodyKanbanAccessEntry struct {
	UserID    int64     `json:"user_id"`
	FullName  string    `json:"full_name"`
	GrantedBy *int64    `json:"granted_by,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type GrantStateBodyStatsAccessRequest struct {
	UserID int64 `json:"user_id" binding:"required"`
}

type GrantStateBodyKanbanAccessRequest struct {
	UserID int64 `json:"user_id" binding:"required"`
}

type OrgStatsAccessCheckResponse struct {
	StateBodyID int64  `json:"state_body_id"`
	Allowed     bool   `json:"allowed"`
	Reason      string `json:"reason,omitempty"`
}

const (
	BgModePresetColor = "preset_color"
	BgModeCustomImage = "custom_image"

	BgPresetDefault = "preset_default"
	BgPresetMint    = "preset_mint"
	BgPresetSand    = "preset_sand"
	BgPresetSky     = "preset_sky"
)

func GetPresetHex(presetID string) string {
	switch presetID {
	case BgPresetMint:
		return "#EAF7F2"
	case BgPresetSand:
		return "#F8F2E8"
	case BgPresetSky:
		return "#EAF1FB"
	default:
		return "#F4F5F7"
	}
}

type UpdateTaskPatch struct {
	ColumnID             *uuid.UUID  `json:"column_id,omitempty"`
	Title                *string     `json:"title,omitempty"`
	Description          *string     `json:"description,omitempty"`
	Priority             *string     `json:"priority,omitempty"`
	DueAt                *time.Time  `json:"due_at,omitempty"`
	ClearDueAt           bool        `json:"clear_due_at,omitempty"`
	ClearDescription     bool        `json:"clear_description,omitempty"`
	GoalID               *uuid.UUID  `json:"goal_id,omitempty"`
	ProjectID            *uuid.UUID  `json:"project_id,omitempty"`
	SprintID             *uuid.UUID  `json:"sprint_id,omitempty"`
	OriginalEstimateSec  *int        `json:"original_estimate_sec,omitempty"`
	AssigneeIDs          []int64     `json:"assignee_ids,omitempty"`
	AssigneeID           *int64      `json:"assignee_id,omitempty"`
	AddAttachmentFileIDs []uuid.UUID `json:"add_attachment_file_ids,omitempty"`
	ParentID             *uuid.UUID  `json:"parent_id,omitempty"`
	ClearParentID        bool        `json:"clear_parent_id,omitempty"`
	Status               *string     `json:"status,omitempty"`
	LabelIDs             []uuid.UUID `json:"label_ids,omitempty"`
}

// Task History Constants
const (
	ActionTaskCreated    = "created"
	ActionTaskUpdated    = "updated"
	ActionTaskMoved      = "moved"
	ActionTaskAssigned   = "assigned"
	ActionTaskUnassigned = "unassigned"
	ActionTaskDeleted    = "deleted"
	ActionFileAttached   = "file_attached"
	ActionFileRemoved    = "file_removed"
	ActionWorklogAdded   = "worklog_added"
	ActionCommentAdded   = "comment_added"
	ActionSubtaskCreated = "subtask_created"
)

// TaskHistory представляет запись в истории изменений задачи (как в Jira)
type TaskHistory struct {
	ID        uuid.UUID              `json:"id" db:"id"`
	TaskID    uuid.UUID              `json:"task_id" db:"task_id"`
	UserID    int64                  `json:"user_id" db:"user_id"`
	Action    string                 `json:"action" db:"action"`
	FieldName *string                `json:"field_name,omitempty" db:"field_name"`
	OldValue  *string                `json:"old_value,omitempty" db:"old_value"`
	NewValue  *string                `json:"new_value,omitempty" db:"new_value"`
	Metadata  map[string]interface{} `json:"metadata,omitempty" db:"metadata"`
	CreatedAt time.Time              `json:"created_at" db:"created_at"`
}

// TaskHistoryResponse включает информацию о пользователе для удобства фронтенда
type TaskHistoryResponse struct {
	ID        uuid.UUID              `json:"id"`
	TaskID    uuid.UUID              `json:"task_id"`
	UserID    int64                  `json:"user_id"`
	UserName  string                 `json:"user_name,omitempty"`
	Action    string                 `json:"action"`
	FieldName *string                `json:"field_name,omitempty"`
	OldValue  *string                `json:"old_value,omitempty"`
	NewValue  *string                `json:"new_value,omitempty"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt time.Time              `json:"created_at"`
}

type TaskComment struct {
	ID            uuid.UUID  `json:"id" db:"id"`
	TaskID        uuid.UUID  `json:"task_id" db:"task_id"`
	UserID        int64      `json:"user_id" db:"user_id"`
	Content       string     `json:"content" db:"content"`
	AttachmentURL *string    `json:"attachment_url,omitempty" db:"attachment_url"`
	CreatedAt     time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at" db:"updated_at"`
	DeletedAt     *time.Time `json:"-" db:"deleted_at"`

	Author *UserResponse `json:"author,omitempty" db:"-"`
}
