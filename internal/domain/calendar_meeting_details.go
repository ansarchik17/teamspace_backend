package domain

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type MeetingSite struct {
	ID             uuid.UUID `json:"id"`
	OrganizationID uuid.UUID `json:"organization_id"`
	Name           string    `json:"name"`
	Address        string    `json:"address"`
	City           string    `json:"city"`
	Timezone       string    `json:"timezone"`
	WorkingHours   string    `json:"working_hours"`
	IsActive       bool      `json:"is_active"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type CreateMeetingSiteRequest struct {
	Name         string `json:"name" binding:"required"`
	Address      string `json:"address"`
	City         string `json:"city"`
	Timezone     string `json:"timezone"`
	WorkingHours string `json:"working_hours"`
	IsActive     *bool  `json:"is_active"`
}

type UpdateMeetingSiteRequest struct {
	Name         *string `json:"name"`
	Address      *string `json:"address"`
	City         *string `json:"city"`
	Timezone     *string `json:"timezone"`
	WorkingHours *string `json:"working_hours"`
	IsActive     *bool   `json:"is_active"`
}

type AttendanceSummary struct {
	Offline int `json:"offline"`
	Online  int `json:"online"`
	Unknown int `json:"unknown"`
}

func (e Event) HasPlace() bool {
	return e.RoomID != nil || e.Room != nil || strings.TrimSpace(stringValue(e.Location)) != ""
}
func (e Event) HasMeetingLink() bool {
	return e.IsOnline && strings.TrimSpace(stringValue(e.MeetingURL)) != ""
}
func (e Event) DefaultAttendanceMode() *string {
	mode := ""
	if e.HasPlace() && !e.HasMeetingLink() {
		mode = "offline"
	}
	if e.HasMeetingLink() && !e.HasPlace() {
		mode = "online"
	}
	if mode == "" {
		return e.DefaultAttendance
	}
	return &mode
}
func (e Event) AttendanceAllowed(mode *string) bool {
	return mode == nil || (*mode == "offline" && e.HasPlace()) || (*mode == "online" && e.HasMeetingLink())
}
func (e *Event) UpdateAttendanceSummary() {
	e.AttendanceSummary = AttendanceSummary{}
	e.Warnings = nil
	for _, attendee := range e.Attendees {
		if attendee.Status == "declined" {
			continue
		}
		switch stringValue(attendee.Attendance) {
		case "offline":
			e.AttendanceSummary.Offline++
		case "online":
			e.AttendanceSummary.Online++
		default:
			e.AttendanceSummary.Unknown++
		}
	}
	if e.Room != nil && e.Room.Capacity > 0 && e.AttendanceSummary.Offline > e.Room.Capacity {
		e.Warnings = []string{"ROOM_CAPACITY_EXCEEDED"}
	}
}
func (e Event) MeetingLocation() string {
	if e.Room == nil {
		return strings.TrimSpace(stringValue(e.Location))
	}
	parts := []string{e.Room.Name, stringValue(e.Room.SiteName), stringValue(e.Room.Address)}
	if e.Room.Floor != nil {
		parts = append(parts, fmt.Sprintf("%d этаж", *e.Room.Floor))
	}
	parts = append(parts, stringValue(e.Room.Location))
	var nonempty []string
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			nonempty = append(nonempty, part)
		}
	}
	return strings.Join(nonempty, ", ")
}
func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (r *CreateEventRequest) UnmarshalJSON(data []byte) error {
	type alias CreateEventRequest
	var value alias
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	*r = CreateEventRequest(value)
	_, r.ReminderMinutesSet = fields["reminder_minutes"]
	return nil
}

func (r *UpdateEventRequest) UnmarshalJSON(data []byte) error {
	type alias UpdateEventRequest
	var value alias
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	*r = UpdateEventRequest(value)
	_, r.RoomIDSet = fields["room_id"]
	_, r.LocationSet = fields["location"]
	_, r.ReminderMinutesSet = fields["reminder_minutes"]
	return nil
}
func (r *RespondToInviteRequest) UnmarshalJSON(data []byte) error {
	type alias RespondToInviteRequest
	var value alias
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	*r = RespondToInviteRequest(value)
	_, r.AttendanceSet = fields["attendance"]
	return nil
}
