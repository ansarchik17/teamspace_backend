package domain

import (
	"time"

	"github.com/google/uuid"
)

// RecordingStatus — возможные статусы Egress-записи.
type RecordingStatus string

const (
	RecordingStatusRecording RecordingStatus = "recording"
	RecordingStatusCompleted RecordingStatus = "completed"
	RecordingStatusFailed    RecordingStatus = "failed"
	RecordingStatusStopped   RecordingStatus = "stopped"
)

// CallRecording хранит метаданные аудио-записи одного звонка.
type CallRecording struct {
	ID        uuid.UUID       `json:"id"         db:"id"`
	CallID    uuid.UUID       `json:"call_id"    db:"call_id"`
	RoomID    string          `json:"room_id"    db:"room_id"`
	EgressID  string          `json:"egress_id"  db:"egress_id"`
	Status    RecordingStatus `json:"status"     db:"status"`
	FilePath  *string         `json:"file_path"  db:"file_path"`
	FileURL   *string         `json:"file_url"   db:"file_url"`
	StartedBy int64           `json:"started_by" db:"started_by"`
	StartedAt time.Time       `json:"started_at" db:"started_at"`
	StoppedAt *time.Time      `json:"stopped_at" db:"stopped_at"`
	DurationS *int            `json:"duration_s" db:"duration_s"`
	SizeBytes *int64          `json:"size_bytes" db:"size_bytes"`
	CreatedAt time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt time.Time       `json:"updated_at" db:"updated_at"`
}
