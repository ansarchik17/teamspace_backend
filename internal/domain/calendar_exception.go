package domain

import (
	"time"

	"github.com/google/uuid"
)

// CalendarEventException identifies the original start of one occurrence. EventData
// is a complete snapshot of its editable fields for modified occurrences.
type CalendarEventException struct {
	ID             uuid.UUID          `json:"id"`
	EventID        uuid.UUID          `json:"event_id"`
	OccurrenceDate time.Time          `json:"occurrence_date"`
	ExceptionType  string             `json:"exception_type"`
	EventData      *OccurrenceDetails `json:"event_data,omitempty"`
	CreatedAt      time.Time          `json:"created_at"`
	UpdatedAt      time.Time          `json:"updated_at"`
}

type OccurrenceDetails struct {
	Title       string       `json:"title"`
	Description *string      `json:"description"`
	StartTime   time.Time    `json:"start_time"`
	EndTime     time.Time    `json:"end_time"`
	Location    *string      `json:"location"`
	RoomID      *uuid.UUID   `json:"room_id,omitempty"`
	Room        *MeetingRoom `json:"room,omitempty"`
	IsAllDay    bool         `json:"is_all_day"`
}

type UpdateOccurrenceRequest struct {
	Title       *string    `json:"title"`
	Description *string    `json:"description"`
	StartTime   *time.Time `json:"start_time"`
	EndTime     *time.Time `json:"end_time"`
	Location    *string    `json:"location"`
	RoomID      *uuid.UUID `json:"room_id,omitempty"`
	IsAllDay    *bool      `json:"is_all_day"`
	Force       *bool      `json:"force,omitempty"`
}
