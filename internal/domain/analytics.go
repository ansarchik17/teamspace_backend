package domain

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

const analyticsDefaultLimit = 20

var allowedAnalyticsSections = map[string]struct{}{
	"workspace":            {},
	"alemstore":            {},
	"calendar":             {},
	"calls_calendar":       {},
	"board":                {},
	"alemai":               {},
	"alem_rag":             {},
	"drive":                {},
	"notes":                {},
	"alem_contact":         {},
	"alem_contact_org_map": {},
	"eps":                  {},
	"profile":              {},
	"messenger":            {},
}

type SectionVisitTrackRequest struct {
	Section string `json:"section" binding:"required"`
	Path    string `json:"path,omitempty"`
}

type GrantMinistryAnalyticsAccessRequest struct {
	UserID int64 `json:"user_id" binding:"required"`
}

type MinistryAnalyticsRequest struct {
	StartDate    *time.Time `form:"start_date"`
	EndDate      *time.Time `form:"end_date"`
	StartDay     *string    `form:"start_day"`
	EndDay       *string    `form:"end_day"`
	MinistryID   *int64     `form:"ministry_id"`
	StateBodyID  *int64     `form:"state_body_id"`
	DepartmentID *int64     `form:"department_id"`
	DivisionID   *int64     `form:"division_id"`
	Limit        int        `form:"limit"`
	Offset       int        `form:"offset"`
}

type AnalyticsSectionTotal struct {
	Section     string `json:"section"`
	Visits      int64  `json:"visits"`
	UniqueUsers int64  `json:"unique_users"`
}

type EmployeeVisitedSection struct {
	Section       string     `json:"section"`
	RoutePath     string     `json:"route_path,omitempty"`
	Visits        int64      `json:"visits"`
	LastVisitedAt *time.Time `json:"last_visited_at,omitempty"`
}

type MinistryRatingItem struct {
	MinistryID         int64  `json:"ministry_id"`
	MinistryName       string `json:"ministry_name"`
	ActiveUsers        int64  `json:"active_users"`
	TotalLogins        int64  `json:"total_logins"`
	TotalSectionVisits int64  `json:"total_section_visits"`
	DistinctSections   int64  `json:"distinct_sections"`
	ActivityScore      int64  `json:"activity_score"`
}

type EmployeeAnalyticsItem struct {
	UserID             int64                    `json:"user_id"`
	FullName           string                   `json:"full_name"`
	Position           *string                  `json:"position,omitempty"`
	MinistryID         *int64                   `json:"ministry_id,omitempty"`
	MinistryName       *string                  `json:"ministry_name,omitempty"`
	StateBodyID        *int64                   `json:"state_body_id,omitempty"`
	StateBodyName      *string                  `json:"state_body_name,omitempty"`
	DepartmentID       *int64                   `json:"department_id,omitempty"`
	DepartmentName     *string                  `json:"department_name,omitempty"`
	DivisionID         *int64                   `json:"division_id,omitempty"`
	DivisionName       *string                  `json:"division_name,omitempty"`
	TotalLogins        int64                    `json:"total_logins"`
	TotalSectionVisits int64                    `json:"total_section_visits"`
	DistinctSections   int64                    `json:"distinct_sections"`
	ActivityScore      int64                    `json:"activity_score"`
	LastLoginAt        *time.Time               `json:"last_login_at,omitempty"`
	LastSeenAt         *time.Time               `json:"last_seen_at,omitempty"`
	VisitedSections    []EmployeeVisitedSection `json:"visited_sections,omitempty"`
}

type MinistryAnalyticsSummary struct {
	TotalLogins        int64 `json:"total_logins"`
	UniqueUsers        int64 `json:"unique_users"`
	TotalSectionVisits int64 `json:"total_section_visits"`
}

type MinistryAnalyticsResponse struct {
	Scope               string                   `json:"scope"`
	EffectiveMinistryID *int64                   `json:"effective_ministry_id,omitempty"`
	Summary             MinistryAnalyticsSummary `json:"summary"`
	SectionTotals       []AnalyticsSectionTotal  `json:"section_totals"`
	MinistryRanking     []MinistryRatingItem     `json:"ministry_ranking"`
	EmployeeRanking     []EmployeeAnalyticsItem  `json:"employee_ranking"`
	EmployeeTotal       int64                    `json:"employee_total"`
	EmployeeLimit       int                      `json:"employee_limit"`
	EmployeeOffset      int                      `json:"employee_offset"`
}

type MinistryAnalyticsAccessEntry struct {
	UserID    int64     `json:"user_id"`
	FullName  string    `json:"full_name"`
	GrantedBy int64     `json:"granted_by"`
	CreatedAt time.Time `json:"created_at"`
}

type MinistryAnalyticsAccessCheckResponse struct {
	MinistryID int64  `json:"ministry_id"`
	Allowed    bool   `json:"allowed"`
	Reason     string `json:"reason,omitempty"`
}

type StateBodyUsersAnalyticsRequest struct {
	StartDate    *time.Time `form:"start_date"`
	EndDate      *time.Time `form:"end_date"`
	StartDay     *string    `form:"start_day"`
	EndDay       *string    `form:"end_day"`
	StateBodyID  *int64     `form:"state_body_id"`
	Query        string     `form:"q"`
	OnlyLoggedIn *bool      `form:"only_logged_in"`
	Limit        int        `form:"limit"`
	Offset       int        `form:"offset"`
}

type StateBodyUsersAnalyticsSummary struct {
	TotalUsers  int64      `json:"total_users"`
	LoggedUsers int64      `json:"logged_users"`
	TotalLogins int64      `json:"total_logins"`
	PeriodStart *time.Time `json:"period_start,omitempty"`
	PeriodEnd   *time.Time `json:"period_end,omitempty"`
}

type StateBodyUserAnalyticsItem struct {
	UserID             int64       `json:"user_id"`
	Username           string      `json:"username"`
	Email              string      `json:"email"`
	FullName           string      `json:"full_name"`
	FirstName          string      `json:"first_name"`
	LastName           string      `json:"last_name"`
	Position           string      `json:"position"`
	OrganizationName   string      `json:"organization_name"`
	DepartmentName     *string     `json:"department_name,omitempty"`
	DivisionName       *string     `json:"division_name,omitempty"`
	LastLoginAt        *time.Time  `json:"last_login_at,omitempty"`
	LastSeenAt         *time.Time  `json:"last_seen_at,omitempty"`
	TotalLogins        int64       `json:"total_logins"`
	FirstLoginInPeriod *time.Time  `json:"first_login_in_period,omitempty"`
	LastLoginInPeriod  *time.Time  `json:"last_login_in_period,omitempty"`
	LoginEvents        []time.Time `json:"login_events"`
}

type StateBodyUsersAnalyticsResponse struct {
	StateBodyID   *int64                         `json:"state_body_id,omitempty"`
	StateBodyName string                         `json:"state_body_name"`
	Summary       StateBodyUsersAnalyticsSummary `json:"summary"`
	Items         []StateBodyUserAnalyticsItem   `json:"items"`
	Limit         int                            `json:"limit"`
	Offset        int                            `json:"offset"`
}

func NormalizeAnalyticsSection(value string) (string, error) {
	normalized := strings.TrimSpace(strings.ToLower(value))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	if _, ok := allowedAnalyticsSections[normalized]; !ok {
		return "", fmt.Errorf("unsupported analytics section")
	}
	return normalized, nil
}

func NormalizeAnalyticsPath(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return ""
	}

	path := strings.TrimSpace(parsed.Path)
	if path == "" {
		return ""
	}

	path = strings.ToLower(path)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if len(path) > 255 {
		path = path[:255]
	}
	return path
}

func (r *MinistryAnalyticsRequest) Normalize() {
	if r.Limit <= 0 {
		r.Limit = analyticsDefaultLimit
	}
	if r.Limit > 100 {
		r.Limit = 100
	}
}

func (r MinistryAnalyticsRequest) Validate() error {
	return nil
}

func (r *StateBodyUsersAnalyticsRequest) Normalize() {
	if r.Limit <= 0 {
		r.Limit = analyticsDefaultLimit
	}
	if r.Limit > 100 {
		r.Limit = 100
	}
	if r.Offset < 0 {
		r.Offset = 0
	}
	r.Query = strings.TrimSpace(r.Query)
}

func (r StateBodyUsersAnalyticsRequest) Validate() error {
	if r.StateBodyID != nil && *r.StateBodyID <= 0 {
		return fmt.Errorf("state_body_id must be greater than 0")
	}
	return nil
}
