package domain

import (
	"time"

	"github.com/google/uuid"
)

type Event struct {
	AttendanceSummary AttendanceSummary `json:"attendance_summary"`
	Warnings          []string          `json:"warnings,omitempty"`
	DefaultAttendance *string           `json:"-"`
	ID                uuid.UUID         `json:"id" db:"id"`
	OccurrenceID      string            `json:"occurrence_id,omitempty" db:"-"`
	UserID            int64             `json:"user_id" db:"user_id"`
	CalendarID        uuid.UUID         `json:"calendar_id" db:"calendar_id"`
	Username          *string           `json:"username,omitempty" db:"-"`
	FirstName         *string           `json:"first_name,omitempty" db:"-"`
	LastName          *string           `json:"last_name,omitempty" db:"-"`
	Title             string            `json:"title" db:"title"`
	Description       *string           `json:"description,omitempty" db:"description"`
	StartTime         time.Time         `json:"start_time" db:"start_time"`
	EndTime           time.Time         `json:"end_time" db:"end_time"`
	Timezone          string            `json:"timezone" db:"timezone"`
	StartTimeLocal    *string           `json:"start_time_local,omitempty" db:"-"`
	EndTimeLocal      *string           `json:"end_time_local,omitempty" db:"-"`
	Location          *string           `json:"location,omitempty" db:"location"`
	RoomID            *uuid.UUID        `json:"room_id,omitempty" db:"room_id"`
	Room              *MeetingRoom      `json:"room,omitempty" db:"-"`
	IsAllDay          bool              `json:"is_all_day" db:"is_all_day"`
	IsOnline          bool              `json:"is_online" db:"is_online"`
	URL               *string           `json:"url,omitempty" db:"url"`

	RecurrenceRule     *string    `json:"recurrence_rule,omitempty" db:"recurrence_rule"` // DAILY/WEEKLY/MONTHLY
	RecurrenceInterval int        `json:"recurrence_interval,omitempty" db:"recurrence_interval"`
	RecurrenceUntil    *time.Time `json:"recurrence_until,omitempty" db:"recurrence_until"`

	Color     *string    `json:"color,omitempty" db:"color"`
	CreatedAt time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt time.Time  `json:"updated_at" db:"updated_at"`
	DeletedAt *time.Time `json:"-" db:"deleted_at"`

	ReminderMinutes *int            `json:"reminder_minutes,omitempty" db:"-"`
	Attendees       []EventAttendee `json:"attendees,omitempty" db:"-"`
	MeetingProvider string          `json:"meeting_provider,omitempty" db:"meeting_provider"`
	MeetingRoomID   *string         `json:"meeting_room_id,omitempty" db:"meeting_room_id"`
	MeetingURL      *string         `json:"meeting_url,omitempty" db:"meeting_url"`
	LiveKitRoomName *string         `json:"livekit_room_name,omitempty" db:"livekit_room_name"`
	CiscoMeetingID  *string         `json:"cisco_meeting_id,omitempty" db:"cisco_meeting_id"`
	CiscoJoinURL    *string         `json:"cisco_join_url,omitempty" db:"cisco_join_url"`
	MeetingStatus   string          `json:"online_meeting_status,omitempty" db:"online_meeting_status"`

	IsOrganizer bool `json:"is_organizer" db:"-"`
}

type EventAttendee struct {
	Attendance *string    `json:"attendance"`
	EventID    uuid.UUID  `json:"event_id" db:"event_id"`
	UserID     int64      `json:"user_id" db:"user_id"`
	Status     string     `json:"status" db:"status"`
	Comment    *string    `json:"comment,omitempty" db:"comment"`
	CreatedAt  time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt  *time.Time `json:"updated_at,omitempty" db:"updated_at"`

	User *UserResponse `json:"user,omitempty" db:"-"`
}

type CreateEventRequest struct {
	ReminderMinutesSet bool `json:"-"`

	DefaultAttendance *string    `json:"default_attendance"`
	CalendarID        *uuid.UUID `json:"calendar_id"`
	Title             string     `json:"title" binding:"required"`
	Description       *string    `json:"description"`
	StartTime         time.Time  `json:"start_time" binding:"required"`
	EndTime           time.Time  `json:"end_time" binding:"required"`
	Timezone          string     `json:"timezone,omitempty"`
	Location          *string    `json:"location"`
	RoomID            *uuid.UUID `json:"room_id,omitempty"`
	IsAllDay          bool       `json:"is_all_day"`
	IsOnline          bool       `json:"is_online"`
	MeetingProvider   *string    `json:"meeting_provider,omitempty"`
	URL               *string    `json:"url"`

	RecurrenceRule     *string    `json:"recurrence_rule"`
	RecurrenceInterval *int       `json:"recurrence_interval"`
	RecurrenceUntil    *time.Time `json:"recurrence_until"`

	Color           *string `json:"color"`
	AttendeeIDs     []int64 `json:"attendee_ids"`
	ReminderMinutes *int    `json:"reminder_minutes"`
}

type CalendarAvailabilityRequest struct {
	StartTime      time.Time  `json:"start_time" binding:"required"`
	EndTime        time.Time  `json:"end_time" binding:"required"`
	AttendeeIDs    []int64    `json:"attendee_ids"`
	ExcludeEventID *uuid.UUID `json:"exclude_event_id"`
	Timezone       string     `json:"timezone"`
}

type CalendarConflictItem struct {
	UserID         int64     `json:"user_id"`
	UserName       string    `json:"user_name"`
	Username       string    `json:"username,omitempty"`
	Email          string    `json:"email,omitempty"`
	AvatarURL      *string   `json:"avatar_url,omitempty"`
	EventID        uuid.UUID `json:"event_id"`
	EventTitle     string    `json:"event_title"`
	StartTime      time.Time `json:"start_time"`
	EndTime        time.Time `json:"end_time"`
	Status         string    `json:"status"`
	OverlapMinutes int       `json:"overlap_minutes"`
}

type CalendarConflictsByUser struct {
	UserID    int64                  `json:"user_id"`
	UserName  string                 `json:"user_name"`
	Username  string                 `json:"username,omitempty"`
	Email     string                 `json:"email,omitempty"`
	AvatarURL *string                `json:"avatar_url,omitempty"`
	Conflicts []CalendarConflictItem `json:"conflicts"`
}

type CalendarSlotUser struct {
	UserID   int64  `json:"user_id"`
	FullName string `json:"full_name"`
}

type CalendarSuggestedSlot struct {
	Type              string                 `json:"type,omitempty"`
	StartTime         time.Time              `json:"start_time"`
	EndTime           time.Time              `json:"end_time"`
	Timezone          string                 `json:"timezone,omitempty"`
	LocalStartTime    time.Time              `json:"local_start_time,omitempty"`
	LocalEndTime      time.Time              `json:"local_end_time,omitempty"`
	Score             int                    `json:"score"`
	Label             string                 `json:"label"`
	AvailableUserIDs  []int64                `json:"available_user_ids,omitempty"`
	ConflictedUserIDs []int64                `json:"conflicted_user_ids,omitempty"`
	AvailableUsers    []CalendarSlotUser     `json:"available_users,omitempty"`
	ConflictedUsers   []CalendarSlotUser     `json:"conflicted_users,omitempty"`
	Conflicts         []CalendarConflictItem `json:"conflicts,omitempty"`
}

type CalendarConflictAlert struct {
	Type           string     `json:"type"`     // overlap | tentative
	Severity       string     `json:"severity"` // error | warning
	UserID         int64      `json:"user_id"`
	UserName       string     `json:"user_name"`
	Username       string     `json:"username,omitempty"`
	EventID        *uuid.UUID `json:"event_id,omitempty"`
	EventTitle     string     `json:"event_title"`
	StartTime      time.Time  `json:"start_time"`
	EndTime        time.Time  `json:"end_time"`
	OverlapMinutes int        `json:"overlap_minutes"`
	Message        string     `json:"message"`
}

type CalendarAvailabilityResponse struct {
	IsAvailable           bool                      `json:"is_available"`
	CheckedUsersCount     int                       `json:"checked_users_count"`
	ConflictedUsersCount  int                       `json:"conflicted_users_count"`
	CheckedUserIDs        []int64                   `json:"checked_user_ids"`
	OrganizerIncluded     bool                      `json:"organizer_included"`
	Conflicts             []CalendarConflictItem    `json:"conflicts"`
	ConflictsByUser       []CalendarConflictsByUser `json:"conflicts_by_user"`
	Alerts                []CalendarConflictAlert   `json:"alerts,omitempty"`
	HasHardConflicts      bool                      `json:"has_hard_conflicts"`
	HasTentativeConflicts bool                      `json:"has_tentative_conflicts"`
	SuggestedSlots        []CalendarSuggestedSlot   `json:"suggested_slots"`
	PartialSuggestedSlots []CalendarSuggestedSlot   `json:"partial_suggested_slots"`
}

type CalendarFreeBusyRequest struct {
	StartTime        time.Time  `json:"start_time" form:"start_time"`
	EndTime          time.Time  `json:"end_time" form:"end_time"`
	AttendeeIDs      []int64    `json:"attendee_ids" form:"attendee_ids"`
	UserIDs          []int64    `json:"user_ids" form:"user_ids"`
	Timezone         string     `json:"timezone" form:"timezone"`
	SlotStepMinutes  int        `json:"slot_step_minutes" form:"slot_step_minutes"`
	IncludeOrganizer *bool      `json:"include_organizer" form:"include_organizer"`
	ExcludeEventID   *uuid.UUID `json:"exclude_event_id" form:"exclude_event_id"`
}

type CalendarBusyIntervalItem struct {
	StartTime   time.Time  `json:"start_time"`
	EndTime     time.Time  `json:"end_time"`
	Status      string     `json:"status"` // busy | tentative
	EventID     *uuid.UUID `json:"event_id,omitempty"`
	EventTitle  string     `json:"event_title"`
	IsRecurring bool       `json:"is_recurring"`
}

type CalendarUserFreeBusy struct {
	UserID                  int64                      `json:"user_id"`
	UserName                string                     `json:"user_name"`
	Username                string                     `json:"username,omitempty"`
	Email                   string                     `json:"email,omitempty"`
	AvatarURL               *string                    `json:"avatar_url,omitempty"`
	TotalBusyMinutes        int                        `json:"total_busy_minutes"`
	BusyIntervalsCount      int                        `json:"busy_intervals_count"`
	TentativeIntervalsCount int                        `json:"tentative_intervals_count"`
	BusyIntervals           []CalendarBusyIntervalItem `json:"busy_intervals"`
}

type CalendarTimeInterval struct {
	StartTime       time.Time `json:"start_time"`
	EndTime         time.Time `json:"end_time"`
	DurationMinutes int       `json:"duration_minutes"`
}

type CalendarTimelineSlot struct {
	StartTime        time.Time          `json:"start_time"`
	EndTime          time.Time          `json:"end_time"`
	Status           string             `json:"status"` // free | busy | tentative
	TotalUsers       int                `json:"total_users"`
	FreeCount        int                `json:"free_count"`
	BusyCount        int                `json:"busy_count"`
	TentativeCount   int                `json:"tentative_count"`
	OccupancyRate    float64            `json:"occupancy_rate"`
	AvailableUserIDs []int64            `json:"available_user_ids"`
	BusyUserIDs      []int64            `json:"busy_user_ids"`
	TentativeUserIDs []int64            `json:"tentative_user_ids"`
	AvailableUsers   []CalendarSlotUser `json:"available_users,omitempty"`
	BusyUsers        []CalendarSlotUser `json:"busy_users,omitempty"`
	TentativeUsers   []CalendarSlotUser `json:"tentative_users,omitempty"`
}

type CalendarFreeBusySummary struct {
	TotalUsers           int  `json:"total_users"`
	AllFree              bool `json:"all_free"`
	ConflictedUsersCount int  `json:"conflicted_users_count"`
	TotalSlotsCount      int  `json:"total_slots_count"`
	FreeSlotsCount       int  `json:"free_slots_count"`
	BusySlotsCount       int  `json:"busy_slots_count"`
}

type CalendarFreeBusyResponse struct {
	StartTime       time.Time               `json:"start_time"`
	EndTime         time.Time               `json:"end_time"`
	Timezone        string                  `json:"timezone"`
	SlotStepMinutes int                     `json:"slot_step_minutes"`
	Users           []CalendarUserFreeBusy  `json:"users"`
	MutualFreeSlots []CalendarTimeInterval  `json:"mutual_free_slots"`
	TimelineGrid    []CalendarTimelineSlot  `json:"timeline_grid"`
	Alerts          []CalendarConflictAlert `json:"alerts,omitempty"`
	Summary         CalendarFreeBusySummary `json:"summary"`
}

type CalendarSuggestTimesRequest struct {
	Date             string     `json:"date"`
	Timezone         string     `json:"timezone"`
	AttendeeIDs      []int64    `json:"attendee_ids"`
	DurationMinutes  int        `json:"duration_minutes"`
	WorkdayStart     string     `json:"workday_start"`
	WorkdayEnd       string     `json:"workday_end"`
	SlotStepMinutes  int        `json:"slot_step_minutes"`
	IncludeOrganizer *bool      `json:"include_organizer"`
	ExcludeEventID   *uuid.UUID `json:"exclude_event_id"`
	Limit            int        `json:"limit"`
	AllowPartial     *bool      `json:"allow_partial"`
	PreferredStart   *time.Time `json:"preferred_start,omitempty"`
	PreferredEnd     *time.Time `json:"preferred_end,omitempty"`
}

type CalendarSuggestTimesResponse struct {
	Date                  string                  `json:"date"`
	Timezone              string                  `json:"timezone,omitempty"`
	DurationMinutes       int                     `json:"duration_minutes,omitempty"`
	CheckedUsersCount     int                     `json:"checked_users_count,omitempty"`
	CheckedUserIDs        []int64                 `json:"checked_user_ids,omitempty"`
	OrganizerIncluded     bool                    `json:"organizer_included,omitempty"`
	WorkdayStart          string                  `json:"workday_start,omitempty"`
	WorkdayEnd            string                  `json:"workday_end,omitempty"`
	SlotStepMinutes       int                     `json:"slot_step_minutes,omitempty"`
	Slots                 []CalendarSuggestedSlot `json:"slots,omitempty"`
	SuggestedSlots        []CalendarSuggestedSlot `json:"suggested_slots,omitempty"`
	PartialSuggestedSlots []CalendarSuggestedSlot `json:"partial_suggested_slots,omitempty"`
	ConflictsBySlot       []CalendarSuggestedSlot `json:"conflicts_by_slot,omitempty"`
	Message               string                  `json:"message,omitempty"`
}

type CalendarMeetingInfo struct {
	Provider        string  `json:"provider"`
	MeetingRoomID   *string `json:"meeting_room_id,omitempty"`
	MeetingURL      *string `json:"meeting_url,omitempty"`
	LiveKitRoomName *string `json:"livekit_room_name,omitempty"`
	CiscoMeetingID  *string `json:"cisco_meeting_id,omitempty"`
	CiscoJoinURL    *string `json:"cisco_join_url,omitempty"`
	Status          string  `json:"status"`
}

type UpdateEventRequest struct {
	ReminderMinutesSet bool `json:"-"`

	DefaultAttendance *string    `json:"default_attendance"`
	RoomIDSet         bool       `json:"-"`
	LocationSet       bool       `json:"-"`
	CalendarID        *uuid.UUID `json:"calendar_id"`
	Title             *string    `json:"title"`
	Description       *string    `json:"description"`
	StartTime         *time.Time `json:"start_time"`
	EndTime           *time.Time `json:"end_time"`
	Timezone          *string    `json:"timezone,omitempty"`
	Location          *string    `json:"location"`
	RoomID            *uuid.UUID `json:"room_id,omitempty"`
	IsAllDay          *bool      `json:"is_all_day"`
	IsOnline          *bool      `json:"is_online"`
	MeetingProvider   *string    `json:"meeting_provider,omitempty"`
	URL               *string    `json:"url"`

	RecurrenceRule     *string    `json:"recurrence_rule"`
	RecurrenceInterval *int       `json:"recurrence_interval"`
	RecurrenceUntil    *time.Time `json:"recurrence_until"`

	Color           *string `json:"color"`
	AttendeeIDs     []int64 `json:"attendee_ids"`
	ReminderMinutes *int    `json:"reminder_minutes"`
	Force           *bool   `json:"force,omitempty"`
}

type UserCalendar struct {
	ID        uuid.UUID `json:"id" db:"id"`
	UserID    int64     `json:"user_id" db:"user_id"`
	Name      string    `json:"name" db:"name"`
	Color     string    `json:"color" db:"color"`
	IsDefault bool      `json:"is_default" db:"is_default"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

type CreateUserCalendarRequest struct {
	Name  string `json:"name" binding:"required"`
	Color string `json:"color"`
}

type BatchEventItem struct {
	Mail               string `json:"mail" binding:"required,email"`
	MeetingName        string `json:"meeting_name" binding:"required"`
	MeetingDescription string `json:"meeting_description"`
}

type BatchCreateEventRequest struct {
	DateFrom string           `json:"date_from" binding:"required"`
	DateTo   string           `json:"date_to" binding:"required"`
	Items    []BatchEventItem `json:"items" binding:"required,dive"`
}

type RespondToInviteRequest struct {
	Attendance    *string `json:"attendance"`
	AttendanceSet bool    `json:"-"`
	Status        string  `json:"status" binding:"required"` // accepted | declined | tentative
	Comment       *string `json:"comment"`                   // optional
}

type TodaySummaryResponse struct {
	TasksCount  int     `json:"tasks_count"`
	EventsCount int     `json:"events_count"`
	Tasks       []*Task `json:"tasks"`
	Events      []Event `json:"events"`
}

type GroupEventsResponse struct {
	Count  int     `json:"count"`
	Events []Event `json:"events"`
}

type EventReminder struct {
	ID                      uuid.UUID
	EventID                 uuid.UUID
	UserID                  int64
	MinutesBefore           int
	CreatedAt               time.Time
	LastSentOccurrenceStart *time.Time
	LastSentAt              *time.Time
}

type SetReminderRequest struct {
	MinutesBefore int `json:"minutes_before" binding:"required"`
}

type ReminderPresetItem struct {
	MinutesBefore int    `json:"minutes_before"`
	Label         string `json:"label"`
	Description   string `json:"description"`
}

type EventReminderInfo struct {
	EventID       uuid.UUID  `json:"event_id"`
	UserID        int64      `json:"user_id"`
	MinutesBefore int        `json:"minutes_before"`
	CreatedAt     time.Time  `json:"created_at"`
	LastSentAt    *time.Time `json:"last_sent_at,omitempty"`
}

type CreateQMeetLinkResponse struct {
	EventID         uuid.UUID `json:"event_id"`
	MeetingProvider string    `json:"meeting_provider"`
	RoomName        string    `json:"room_name"`
	MeetingURL      string    `json:"meeting_url"`
	Status          string    `json:"status"`
	IsOnline        bool      `json:"is_online"`
}

type CalendarTimezoneItem struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	City          string `json:"city"`
	Country       string `json:"country"`
	Offset        string `json:"offset"`
	OffsetMinutes int    `json:"offset_minutes"`
	IsDefault     bool   `json:"is_default"`
}

type TimezoneConvertedItem struct {
	Timezone           string    `json:"timezone"`
	DisplayName        string    `json:"display_name"`
	Offset             string    `json:"offset"`
	OffsetMinutes      int       `json:"offset_minutes"`
	StartTime          time.Time `json:"start_time"`
	EndTime            time.Time `json:"end_time"`
	StartTimeFormatted string    `json:"start_time_formatted"`
	EndTimeFormatted   string    `json:"end_time_formatted"`
	StartTimeISO       string    `json:"start_time_iso"`
	EndTimeISO         string    `json:"end_time_iso"`
}

type TimezoneConvertRequest struct {
	EventID         *uuid.UUID `json:"event_id,omitempty"`
	StartTime       *time.Time `json:"start_time,omitempty"`
	EndTime         *time.Time `json:"end_time,omitempty"`
	SourceTimezone  string     `json:"source_timezone,omitempty"`
	TargetTimezones []string   `json:"target_timezones" binding:"required"`
}

type TimezoneConvertResponse struct {
	Source  TimezoneConvertedItem   `json:"source"`
	Targets []TimezoneConvertedItem `json:"targets"`
}

type SearchEventsResponse struct {
	Events []Event `json:"events"`
}

type SearchEventsQuery struct {
	Title string    `json:"-"`
	From  time.Time `json:"-"`
	To    time.Time `json:"-"`
	Limit int       `json:"-"`
}

// Calendar visibility settings
type UserCalendarSetting struct {
	UserID       int64     `json:"user_id" db:"user_id"`
	CalendarID   uuid.UUID `json:"calendar_id" db:"calendar_id"`
	IsVisible    bool      `json:"is_visible" db:"is_visible"`
	DisplayOrder int       `json:"display_order" db:"display_order"`
	CreatedAt    time.Time `json:"created_at" db:"created_at"`
	UpdatedAt    time.Time `json:"updated_at" db:"updated_at"`
}

// Calendar with visibility settings
type UserCalendarWithSettings struct {
	UserCalendar
	IsVisible    bool `json:"is_visible"`
	DisplayOrder int  `json:"display_order"`
}

// GroupCalendarWithSettings represents an active team calendar layer.
type GroupCalendarWithSettings struct {
	GroupID      uuid.UUID `json:"group_id" db:"group_id"`
	GroupName    string    `json:"group_name" db:"group_name"`
	IsVisible    bool      `json:"is_visible" db:"is_visible"`
	DisplayOrder int       `json:"display_order" db:"display_order"`
}

// Update calendar visibility request
type UpdateCalendarVisibilityRequest struct {
	IsVisible    *bool `json:"is_visible"`
	DisplayOrder *int  `json:"display_order"`
}

// Calendar grid view types
type CalendarViewType string

const (
	CalendarViewDay   CalendarViewType = "day"
	CalendarViewWeek  CalendarViewType = "week"
	CalendarViewMonth CalendarViewType = "month"
)

// Calendar grid request
type CalendarGridRequest struct {
	View         CalendarViewType `json:"view" binding:"required,oneof=day week month"`
	Date         string           `json:"date" binding:"required"` // YYYY-MM-DD format
	Timezone     string           `json:"timezone"`
	CalendarIDs  []uuid.UUID      `json:"calendar_ids"` // Filter by specific calendars
	IncludeGroup bool             `json:"include_group"`
}

// Calendar grid response
type CalendarGridResponse struct {
	View           CalendarViewType            `json:"view"`
	Date           string                      `json:"date"`
	StartDate      time.Time                   `json:"start_date"`
	EndDate        time.Time                   `json:"end_date"`
	Timezone       string                      `json:"timezone"`
	Events         []Event                     `json:"events"`
	Calendars      []UserCalendar              `json:"calendars"`
	GroupCalendars []GroupCalendarWithSettings `json:"group_calendars,omitempty"`
	GroupEvents    []GroupEventInfo            `json:"group_events,omitempty"`
}

// Group event info
type GroupEventInfo struct {
	Event
	GroupID   uuid.UUID `json:"group_id" db:"group_id"`
	GroupName string    `json:"group_name" db:"group_name"`
	CreatedBy int64     `json:"created_by" db:"created_by"`
}
