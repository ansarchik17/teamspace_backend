package domain

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	CallTypePrivate = "private"
	CallTypeGroup   = "group"

	CallStatusCreated    = "created"
	CallStatusRinging    = "ringing"
	CallStatusConnecting = "connecting"
	CallStatusInitiated  = "initiated"
	CallStatusAccepted   = "accepted"
	CallStatusOngoing    = "ongoing"
	CallStatusDeclined   = "declined"
	CallStatusCanceled   = "canceled"
	CallStatusCancelled  = "cancelled"
	CallStatusRejected   = "rejected"
	CallStatusBusy       = "busy"
	CallStatusEnded      = "ended"
	CallStatusMissed     = "missed"
	CallStatusExpired    = "expired"
	CallStatusNoAnswer   = "no_answer"
	CallStatusFailed     = "failed"
)

type CallHistory struct {
	ID                  uuid.UUID  `json:"id" db:"id"`
	CallID              uuid.UUID  `json:"call_id" db:"call_id"`
	FromUserID          int64      `json:"from_user_id" db:"from_user_id"`
	ToUserID            int64      `json:"to_user_id" db:"to_user_id"`
	RoomID              string     `json:"room_id" db:"room_id"`
	RoomName            string     `json:"room_name" db:"-"`
	CallType            string     `json:"call_type" db:"call_type"`
	ParticipantsCount   int        `json:"participants_count" db:"-"`
	ChatID              *string    `json:"chat_id,omitempty" db:"chat_id"`
	FromName            *string    `json:"from_name,omitempty" db:"from_name"`
	IsVideo             bool       `json:"is_video" db:"is_video"`
	Status              string     `json:"status" db:"status"`
	AcceptedByUserID    *int64     `json:"accepted_by_user_id,omitempty" db:"accepted_by_user_id"`
	AcceptedByDeviceID  *string    `json:"accepted_by_device_id,omitempty" db:"accepted_by_device_id"`
	AcceptedBySessionID *string    `json:"accepted_by_session_id,omitempty" db:"accepted_by_session_id"`
	AcceptedAt          *time.Time `json:"accepted_at,omitempty" db:"accepted_at"`
	CreatedAt           time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at" db:"updated_at"`
	EndedAt             *time.Time `json:"ended_at,omitempty" db:"ended_at"`
}

type CallParticipant struct {
	ID       uuid.UUID  `json:"id" db:"id"`
	CallID   uuid.UUID  `json:"call_id" db:"call_id"`
	UserID   int64      `json:"user_id" db:"user_id"`
	Status   string     `json:"status" db:"status"` // joined, left
	Role     string     `json:"role" db:"role"`
	JoinedAt time.Time  `json:"joined_at" db:"joined_at"`
	LeftAt   *time.Time `json:"left_at,omitempty" db:"left_at"`

	// Enriched fields
	FullName  string `json:"full_name" db:"full_name"`
	Username  string `json:"username" db:"username"`
	AvatarURL string `json:"avatar_url" db:"avatar_url"`
}

type ListCallHistoryQuery struct {
	Limit  int
	Offset int
	Kind   string
}

type CancelCallRequest struct {
	CallID   string `json:"call_id" binding:"required"`
	ToUserID int64  `json:"to_user_id" binding:"required"` // ID Организатора (кому сообщить об отмене)
	Reason   string `json:"reason"`                        // "declined", "busy", "timeout"
}

func NormalizeCallStatus(status string) string {
	switch strings.TrimSpace(strings.ToLower(status)) {
	case "", CallStatusCreated, CallStatusInitiated:
		return CallStatusRinging
	case CallStatusConnecting:
		return CallStatusConnecting
	case CallStatusCanceled, CallStatusCancelled:
		return CallStatusCancelled
	case CallStatusRejected:
		return CallStatusDeclined
	default:
		return strings.TrimSpace(strings.ToLower(status))
	}
}

func IsCallRingingStatus(status string) bool {
	switch NormalizeCallStatus(status) {
	case CallStatusRinging, CallStatusConnecting:
		return true
	default:
		return false
	}
}

func IsCallAcceptedStatus(status string) bool {
	switch NormalizeCallStatus(status) {
	case CallStatusAccepted, CallStatusOngoing:
		return true
	default:
		return false
	}
}

func IsCallFinalStatus(status string) bool {
	switch NormalizeCallStatus(status) {
	case CallStatusDeclined, CallStatusCancelled, CallStatusBusy, CallStatusEnded, CallStatusMissed, CallStatusExpired, CallStatusNoAnswer, CallStatusFailed:
		return true
	default:
		return false
	}
}
