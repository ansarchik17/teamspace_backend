package domain

import (
	"time"

	"github.com/google/uuid"
)

// MeetingRoom represents a physical room or resource available for booking.
type MeetingRoom struct {
	OrganizationID *uuid.UUID `json:"organization_id,omitempty"`
	SiteID         *uuid.UUID `json:"site_id,omitempty"`
	SiteName       *string    `json:"site_name,omitempty"`
	Address        *string    `json:"address,omitempty"`
	PhotoURL       *string    `json:"photo_url,omitempty"`
	ID             uuid.UUID  `json:"id" db:"id"`
	Name           string     `json:"name" db:"name"`
	Description    *string    `json:"description,omitempty" db:"description"`
	Capacity       int        `json:"capacity" db:"capacity"`
	Floor          *int       `json:"floor,omitempty" db:"floor"`
	Location       *string    `json:"location,omitempty" db:"location"`
	Color          *string    `json:"color,omitempty" db:"color"`
	Equipment      []string   `json:"equipment" db:"equipment"`
	IsActive       bool       `json:"is_active" db:"is_active"`
	CreatedAt      time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at" db:"updated_at"`
	DeletedAt      *time.Time `json:"-" db:"deleted_at"`
}

// RoomBooking represents an atomic reserved time slot for a meeting room.
type RoomBooking struct {
	ID          uuid.UUID  `json:"id" db:"id"`
	RoomID      uuid.UUID  `json:"room_id" db:"room_id"`
	EventID     *uuid.UUID `json:"event_id,omitempty" db:"event_id"`
	UserID      int64      `json:"user_id" db:"user_id"`
	Title       string     `json:"title" db:"title"`
	Description *string    `json:"description,omitempty" db:"description"`
	StartTime   time.Time  `json:"start_time" db:"start_time"`
	EndTime     time.Time  `json:"end_time" db:"end_time"`
	Status      string     `json:"status" db:"status"` // 'confirmed', 'cancelled'
	CreatedAt   time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at" db:"updated_at"`
	DeletedAt   *time.Time `json:"-" db:"deleted_at"`

	// Enriched fields for responses
	Room *MeetingRoom  `json:"room,omitempty" db:"-"`
	User *UserResponse `json:"user,omitempty" db:"-"`
}

type CreateMeetingRoomRequest struct {
	SiteID      *uuid.UUID `json:"site_id"`
	PhotoURL    *string    `json:"photo_url"`
	Name        string     `json:"name" binding:"required"`
	Description *string    `json:"description"`
	Capacity    int        `json:"capacity" binding:"required,min=1"`
	Floor       *int       `json:"floor"`
	Location    *string    `json:"location"`
	Color       *string    `json:"color"`
	Equipment   []string   `json:"equipment"`
	IsActive    *bool      `json:"is_active"`
}

type UpdateMeetingRoomRequest struct {
	SiteID      *uuid.UUID `json:"site_id"`
	PhotoURL    *string    `json:"photo_url"`
	Name        *string    `json:"name"`
	Description *string    `json:"description"`
	Capacity    *int       `json:"capacity"`
	Floor       *int       `json:"floor"`
	Location    *string    `json:"location"`
	Color       *string    `json:"color"`
	Equipment   *[]string  `json:"equipment"`
	IsActive    *bool      `json:"is_active"`
}

type BookRoomRequest struct {
	EventID             *uuid.UUID `json:"event_id,omitempty"`
	Title               string     `json:"title" binding:"required"`
	Description         *string    `json:"description"`
	StartTime           time.Time  `json:"start_time" binding:"required"`
	EndTime             time.Time  `json:"end_time" binding:"required"`
	CreateCalendarEvent bool       `json:"create_calendar_event"`
}

type RoomAvailabilityQuery struct {
	StartTime        time.Time  `json:"start_time" form:"start_time"`
	EndTime          time.Time  `json:"end_time" form:"end_time"`
	Capacity         *int       `json:"capacity" form:"capacity"`
	Equipment        []string   `json:"equipment" form:"equipment"`
	ExcludeBookingID *uuid.UUID `json:"exclude_booking_id" form:"exclude_booking_id"`
	ExcludeEventID   *uuid.UUID `json:"exclude_event_id" form:"exclude_event_id"`
	SlotStepMinutes  int        `json:"slot_step_minutes" form:"slot_step_minutes"`
}

type RoomSlot struct {
	StartTime time.Time  `json:"start_time"`
	EndTime   time.Time  `json:"end_time"`
	IsBusy    bool       `json:"is_busy"`
	BookingID *uuid.UUID `json:"booking_id,omitempty"`
	EventID   *uuid.UUID `json:"event_id,omitempty"`
	Title     *string    `json:"title,omitempty"`
	UserID    *int64     `json:"user_id,omitempty"`
	UserName  *string    `json:"user_name,omitempty"`
}

type RoomFreeSlot struct {
	StartTime       time.Time `json:"start_time"`
	EndTime         time.Time `json:"end_time"`
	DurationMinutes int       `json:"duration_minutes"`
}

type RoomAvailabilityResponse struct {
	Room        MeetingRoom    `json:"room"`
	StartTime   time.Time      `json:"start_time"`
	EndTime     time.Time      `json:"end_time"`
	IsAvailable bool           `json:"is_available"`
	Bookings    []RoomBooking  `json:"bookings"`
	FreeSlots   []RoomFreeSlot `json:"free_slots"`
	Slots       []RoomSlot     `json:"slots,omitempty"`
}

type RoomFilter struct {
	OrganizationID *uuid.UUID
	SiteID         *uuid.UUID
	RequesterID    int64
	Search         string
	MinCapacity    *int
	Floor          *int
	Equipment      []string
	OnlyActive     bool
	IncludeDeleted bool
}

type RoomConflictDetail struct {
	RoomID          uuid.UUID            `json:"room_id"`
	RoomName        string               `json:"room_name"`
	ConflictingSlot CalendarTimeInterval `json:"conflicting_slot"`
	ExistingBooking *RoomBooking         `json:"existing_booking,omitempty"`
	Message         string               `json:"message"`
}

// Room occupancy is public to authenticated users; meeting details are not.
func RedactRoomBooking(booking RoomBooking, requesterID int64) RoomBooking {
	if booking.UserID == requesterID {
		return booking
	}
	booking.ID = uuid.Nil
	booking.EventID = nil
	booking.Title = "Занято"
	booking.Description = nil
	booking.User = nil
	booking.UserID = 0
	return booking
}
