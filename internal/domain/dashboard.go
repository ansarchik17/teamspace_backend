package domain

type DashboardSummaryResponse struct {
	Summary            DashboardSummary            `json:"summary"`
	EfficiencyDynamics DashboardEfficiencyDynamics `json:"efficiency_dynamics"`
	TaskStructure      DashboardTaskStructure      `json:"task_structure"`
	CompletionProgress DashboardCompletionProgress `json:"completion_progress"`
}

type DashboardSummary struct {
	ActiveTasks   DashboardMetric `json:"active_tasks"`
	MeetingsToday DashboardMetric `json:"meetings_today"`
	Documents     DashboardMetric `json:"documents"`
	WeeklyPoints  DashboardPoints `json:"weekly_points"`
}

type DashboardMetric struct {
	Count           int   `json:"count"`
	TrendPercentage int   `json:"trend_percentage"`
	Sparkline       []int `json:"sparkline,omitempty"`
}

type DashboardPoints struct {
	Count           int `json:"count"`
	TrendPercentage int `json:"trend_percentage"`
	TodayPoints     int `json:"today_points"`
}

type DashboardEfficiencyDynamics struct {
	AverageLoad float64            `json:"average_load"`
	Labels      []string           `json:"labels"`
	Datasets    []DashboardDataset `json:"datasets"`
}

type DashboardDataset struct {
	Name string `json:"name"`
	Data []int  `json:"data"`
}

type DashboardTaskStructure struct {
	TotalCount int                 `json:"total_count"`
	Stats      []DashboardTaskStat `json:"stats"`
}

type DashboardTaskStat struct {
	Label string `json:"label"`
	Count int    `json:"count"`
}

type DashboardCompletionProgress struct {
	CompletedCount int `json:"completed_count"`
	TotalCount     int `json:"total_count"`
	Percentage     int `json:"percentage"`
}
