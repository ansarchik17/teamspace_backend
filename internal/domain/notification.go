package domain

import (
	"time"

	"github.com/google/uuid"
)

type Notification struct {
	ID                   uuid.UUID         `json:"id" db:"id"`
	UserID               int64             `json:"user_id" db:"user_id"`
	OrganizerID          *int64            `json:"organizer_id,omitempty" db:"organizer_id"` // Создатель события/уведомления
	Type                 string            `json:"type" db:"type"`
	Title                string            `json:"title" db:"title"`
	Content              string            `json:"content" db:"content"`
	ShortText            *string           `json:"short_text,omitempty" db:"short_text"`
	SourceSystemName     *string           `json:"source_system_name,omitempty" db:"source_system_name"`
	NotificationType     *string           `json:"notification_type,omitempty" db:"notification_type"`
	ApplicationNumber    *string           `json:"application_number,omitempty" db:"application_number"`
	ActionURL            *string           `json:"action_url,omitempty" db:"action_url"`
	IsRead               bool              `json:"is_read" db:"is_read"`
	Metadata             map[string]string `json:"metadata,omitempty" db:"metadata"` // JSONB в базе
	SourceSystemCode     *string           `json:"source_system_code,omitempty" db:"source_system_code"`
	ExternalMessageID    *string           `json:"external_message_id,omitempty" db:"external_message_id"`
	Status               string            `json:"status,omitempty" db:"notification_status"`
	PushStatus           string            `json:"push_status,omitempty" db:"push_status"`
	ErrorMessage         *string           `json:"error_message,omitempty" db:"error_message"`
	SentAt               *time.Time        `json:"sent_at,omitempty" db:"sent_at"`
	CreatedAt            time.Time         `json:"created_at" db:"created_at"`
	ProcessedAt          *time.Time        `json:"processed_at,omitempty" db:"processed_at"`
	UpdatedAt            *time.Time        `json:"updated_at,omitempty" db:"updated_at"`
	InviteResponseStatus *string           `json:"invite_response_status,omitempty" db:"-"`
}

type CreateNotificationRequest struct {
	// Server templates are rendered for UserID before persistence and delivery.
	TitleTemplate   *NotificationTemplate `json:"-"`
	ContentTemplate *NotificationTemplate `json:"-"`
	UserID          int64                 `json:"user_id"`
	OrganizerID     *int64                `json:"organizer_id,omitempty"`
	Type            string                `json:"type"`
	Title           string                `json:"title"`
	Content         string                `json:"content"`
	Metadata        map[string]string     `json:"metadata"`
}

type NotificationTemplate struct {
	Source string
	Args   []any
}

// NotificationLabel marks a server-owned parameter, such as a role or resource type.
type NotificationLabel string
type NotificationCountdown int

const (
	NotificationStatusReceived  = "RECEIVED"
	NotificationStatusSaved     = "SAVED"
	NotificationStatusProcessed = "PROCESSED"
	NotificationStatusFailed    = "FAILED"

	PushStatusPending = "PENDING"
	PushStatusSent    = "SENT"
	PushStatusFailed  = "FAILED"
	PushStatusSkipped = "SKIPPED"
)

type InternalNotificationRequest struct {
	IIN                string `json:"iin"`
	Title              string `json:"title,omitempty"`
	MessageText        string `json:"messageText"`
	ShortText          string `json:"shortText,omitempty"`
	SourceSystemCode   string `json:"sourceSystemCode"`
	SourceSystemName   string `json:"sourceSystemName,omitempty"`
	NotificationType   string `json:"notificationType,omitempty"`
	Status             string `json:"status,omitempty"`
	ExternalMessageID  string `json:"externalMessageId,omitempty"`
	ApplicationNumber  string `json:"applicationNumber,omitempty"`
	ActionURL          string `json:"actionUrl,omitempty"`
	Language           string `json:"language,omitempty"`
	SentAt             string `json:"sentAt,omitempty"`
	AttachmentURL      string `json:"attachmentUrl,omitempty"`
	AttachmentName     string `json:"attachmentName,omitempty"`
	AttachmentMimeType string `json:"attachmentMimeType,omitempty"`
	AttachmentContent  string `json:"attachmentContent,omitempty"`
	ImportToDrive      *bool  `json:"importToDrive,omitempty"`
}

type InternalNotificationResponse struct {
	Status         string     `json:"status"`
	NotificationID uuid.UUID  `json:"notificationId"`
	Message        string     `json:"message"`
	PushStatus     string     `json:"pushStatus,omitempty"`
	ProcessedAt    time.Time  `json:"processedAt"`
	DriveFileID    *uuid.UUID `json:"driveFileId,omitempty"`
}

type InternalDriveImportRequest struct {
	IIN                string `json:"iin"`
	AttachmentName     string `json:"attachmentName"`
	AttachmentMimeType string `json:"attachmentMimeType,omitempty"`
	AttachmentURL      string `json:"attachmentUrl,omitempty"`
	AttachmentContent  string `json:"attachmentContent,omitempty"`
	SourceSystemCode   string `json:"sourceSystemCode"`
	SourceSystemName   string `json:"sourceSystemName,omitempty"`
	ExternalMessageID  string `json:"externalMessageId,omitempty"`
}

type InternalDriveImportResponse struct {
	Status      string    `json:"status"`
	Message     string    `json:"message"`
	DriveFileID uuid.UUID `json:"driveFileId"`
	ImportedAt  time.Time `json:"importedAt"`
}
