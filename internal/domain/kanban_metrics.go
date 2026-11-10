package domain

import "time"

// ─── Группа 1: Исполнительность и SLA ───────────────────────────────────────

// SLAComplianceResponse — процент задач, закрытых вовремя.
type SLAComplianceResponse struct {
	BoardID           string  `json:"board_id,omitempty"`
	Period            string  `json:"period"` // day | week | month
	DateFrom          string  `json:"date_from"`
	DateTo            string  `json:"date_to"`
	TotalClosed       int     `json:"total_closed"`
	ClosedOnTime      int     `json:"closed_on_time"`
	ClosedOverdue     int     `json:"closed_overdue"`
	CompliancePercent float64 `json:"compliance_percent"`
}

// CycleTimeResponse — среднее время выполнения задачи.
type CycleTimeResponse struct {
	BoardID          string  `json:"board_id,omitempty"`
	Period           string  `json:"period"`
	DateFrom         string  `json:"date_from"`
	DateTo           string  `json:"date_to"`
	AvgCycleHours    float64 `json:"avg_cycle_hours"`
	AvgCycleDays     float64 `json:"avg_cycle_days"`
	MedianCycleHours float64 `json:"median_cycle_hours"`
	SampleSize       int     `json:"sample_size"`
}

// ─── Группа 2: Качество и Проверка ───────────────────────────────────────────

// ReopenRateResponse — задачи, возвращённые с «Проверки» в «В работе».
type ReopenRateResponse struct {
	BoardID       string  `json:"board_id,omitempty"`
	Period        string  `json:"period"`
	DateFrom      string  `json:"date_from"`
	DateTo        string  `json:"date_to"`
	TotalTasks    int     `json:"total_tasks"`
	ReopenedCount int     `json:"reopened_count"`
	ReopenRate    float64 `json:"reopen_rate_percent"`
}

// TimeInStatusItem — время задачи в одном статусе.
type TimeInStatusItem struct {
	Status    string  `json:"status"`
	AvgHours  float64 `json:"avg_hours"`
	AvgDays   float64 `json:"avg_days"`
	TaskCount int     `json:"task_count"`
}

// TimeInStatusResponse — распределение времени по статусам (воронка).
type TimeInStatusResponse struct {
	BoardID  string             `json:"board_id,omitempty"`
	Period   string             `json:"period"`
	DateFrom string             `json:"date_from"`
	DateTo   string             `json:"date_to"`
	Statuses []TimeInStatusItem `json:"statuses"`
}

// ─── Группа 3: Нагрузка и Ресурсы ────────────────────────────────────────────

// WorkloadDistributionItem — одна «долька» pie chart.
type WorkloadDistributionItem struct {
	Key     string  `json:"key"`
	Name    string  `json:"name"`
	Count   int     `json:"count"`
	Percent float64 `json:"percent"`
}

// WorkloadDistributionResponse — данные для Pie Chart.
type WorkloadDistributionResponse struct {
	BoardID string                     `json:"board_id,omitempty"`
	GroupBy string                     `json:"group_by"` // priority | type | status
	Items   []WorkloadDistributionItem `json:"items"`
}

// CapacityResponse — текущая загрузка vs лимит.
type CapacityResponse struct {
	BoardID         string         `json:"board_id,omitempty"`
	WIPLimit        int            `json:"wip_limit"`
	ActiveTaskCount int            `json:"active_task_count"`
	UsagePercent    float64        `json:"usage_percent"`
	ByAssignee      []AssigneeLoad `json:"by_assignee"`
}

// AssigneeLoad — нагрузка одного исполнителя.
type AssigneeLoad struct {
	UserID      int64   `json:"user_id"`
	FullName    string  `json:"full_name"`
	ActiveTasks int     `json:"active_tasks"`
	WIPLimit    int     `json:"wip_limit"`
	Usage       float64 `json:"usage_percent"`
}

// StaleTask — «зависшая» задача без активности.
type StaleTask struct {
	ID           string     `json:"id"`
	TaskKey      string     `json:"task_key"`
	Title        string     `json:"title"`
	BoardID      string     `json:"board_id"`
	BoardName    string     `json:"board_name"`
	Status       string     `json:"status"`
	Priority     string     `json:"priority"`
	AssigneeIDs  []int64    `json:"assignee_ids"`
	DaysSinceAct int        `json:"days_since_last_activity"`
	LastActivity *time.Time `json:"last_activity"`
	DueAt        *time.Time `json:"due_at,omitempty"`
}

// StaleTasksResponse — список зависших задач.
type StaleTasksResponse struct {
	ThresholdDays int         `json:"threshold_days"`
	Count         int         `json:"count"`
	Tasks         []StaleTask `json:"tasks"`
}

// OverdueTask — одна просроченная задача.
type OverdueTask struct {
	ID          string     `json:"id"`
	TaskKey     string     `json:"task_key"`
	Title       string     `json:"title"`
	BoardID     string     `json:"board_id"`
	BoardName   string     `json:"board_name"`
	Status      string     `json:"status"`
	Priority    string     `json:"priority"`
	AssigneeIDs []int64    `json:"assignee_ids"`
	DueAt       *time.Time `json:"due_at"`
	DaysOverdue int        `json:"days_overdue"`
}

// OverdueTasksResponse — список просроченных задач.
type OverdueTasksResponse struct {
	Count int           `json:"count"`
	Tasks []OverdueTask `json:"tasks"`
}

// ─── Группа 4: Динамика и Тренды ─────────────────────────────────────────────

// BurnDownPoint — одна точка на графике сгорания.
type BurnDownPoint struct {
	Date                string `json:"date"`
	TotalTasksRemaining int    `json:"total_tasks_remaining"`
}

// BurnDownResponse — данные для Burn-down chart.
type BurnDownResponse struct {
	BoardID    string          `json:"board_id,omitempty"`
	SprintID   string          `json:"sprint_id,omitempty"`
	DateFrom   string          `json:"date_from"`
	DateTo     string          `json:"date_to"`
	DataPoints []BurnDownPoint `json:"data_points"`
}

// ThroughputPoint — созданные vs закрытые задачи за день.
type ThroughputPoint struct {
	Date    string `json:"date"`
	Created int    `json:"created"`
	Closed  int    `json:"closed"`
}

// ThroughputResponse — данные для throughput-графика.
type ThroughputResponse struct {
	BoardID    string            `json:"board_id,omitempty"`
	DateFrom   string            `json:"date_from"`
	DateTo     string            `json:"date_to"`
	DataPoints []ThroughputPoint `json:"data_points"`
}

// EfficiencyPoint — одна точка на линейном графике эффективности.
type EfficiencyPoint struct {
	Date              string  `json:"date"`
	CompletedTasks    int     `json:"completed_tasks"`
	TotalActive       int     `json:"total_active"`
	EfficiencyPercent float64 `json:"efficiency_percent"`
}

// EfficiencyTrendResponse — данные для линейного графика «Эффективность».
type EfficiencyTrendResponse struct {
	BoardID    string            `json:"board_id,omitempty"`
	Period     string            `json:"period"`
	DateFrom   string            `json:"date_from"`
	DateTo     string            `json:"date_to"`
	DataPoints []EfficiencyPoint `json:"data_points"`
}
