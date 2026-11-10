package domain

import (
	"time"

	"github.com/google/uuid"
)

// PersonalRecordingStatus — статусы персональной аудио-записи.
type PersonalRecordingStatus string

const (
	PersonalRecordingStatusStarting  PersonalRecordingStatus = "starting"
	PersonalRecordingStatusActive    PersonalRecordingStatus = "active"
	PersonalRecordingStatusStopping  PersonalRecordingStatus = "stopping"
	PersonalRecordingStatusStopped   PersonalRecordingStatus = "stopped"
	PersonalRecordingStatusCompleted PersonalRecordingStatus = "completed"
	PersonalRecordingStatusFailed    PersonalRecordingStatus = "failed"
	PersonalRecordingStatusAborted   PersonalRecordingStatus = "aborted"
)

func (s PersonalRecordingStatus) IsTerminal() bool {
	switch s {
	case PersonalRecordingStatusStopped,
		PersonalRecordingStatusCompleted,
		PersonalRecordingStatusFailed,
		PersonalRecordingStatusAborted:
		return true
	default:
		return false
	}
}

func (s PersonalRecordingStatus) HasActiveLock() bool {
	switch s {
	case PersonalRecordingStatusStarting,
		PersonalRecordingStatusActive,
		PersonalRecordingStatusStopping:
		return true
	default:
		return false
	}
}

func (s PersonalRecordingStatus) CountsAsLive() bool {
	switch s {
	case PersonalRecordingStatusStarting,
		PersonalRecordingStatusActive:
		return true
	default:
		return false
	}
}

// PersonalCallRecording хранит метаданные персональной записи одного участника звонка.
// Правила:
//   - Любой участник звонка может запустить запись.
//   - Останавливает запись только её owner_user_id.
//   - Одна запись с активным lock (starting|active|stopping) на пару (call_id, owner_user_id).
//   - Active ставится только после подтверждения LiveKit.
//   - После StopEgress запись переходит в stopping до webhook egress_ended.
//   - Разные пользователи могут писать параллельно в одном звонке.
type PersonalCallRecording struct {
	ID                  uuid.UUID               `json:"id"                   db:"id"`
	CallID              uuid.UUID               `json:"call_id"              db:"call_id"`
	RoomName            string                  `json:"room_name"            db:"room_name"`
	OwnerUserID         int64                   `json:"owner_user_id"        db:"owner_user_id"`
	ParticipantIdentity *string                 `json:"participant_identity" db:"participant_identity"`
	EgressID            string                  `json:"egress_id"            db:"egress_id"`
	Status              PersonalRecordingStatus `json:"status"               db:"status"`
	StartedAt           time.Time               `json:"started_at"           db:"started_at"`
	StoppedAt           *time.Time              `json:"stopped_at"           db:"stopped_at"`
	FailedAt            *time.Time              `json:"failed_at"            db:"failed_at"`
	FilePath            *string                 `json:"file_path"            db:"file_path"`
	DurationSec         *int                    `json:"duration_sec"         db:"duration_sec"`
	FailureReason       *string                 `json:"failure_reason"       db:"failure_reason"`
	LiveKitErrorCode    *int                    `json:"livekit_error_code"   db:"livekit_error_code"`
	LiveKitErrorMessage *string                 `json:"livekit_error_message" db:"livekit_error_message"`
	EndReason           *string                 `json:"end_reason"           db:"end_reason"`
	MinIOObjectExists   *bool                   `json:"minio_object_exists"  db:"minio_object_exists"`
	CreatedAt           time.Time               `json:"created_at"           db:"created_at"`
	UpdatedAt           time.Time               `json:"updated_at"           db:"updated_at"`
}

func (r PersonalCallRecording) IsDownloadReady() bool {
	return r.Status == PersonalRecordingStatusCompleted &&
		r.FilePath != nil &&
		*r.FilePath != "" &&
		r.MinIOObjectExists != nil &&
		*r.MinIOObjectExists
}

// ActiveRecordingsStatus — ответ для GET /calls/{call_id}/recordings/personal/active.
type ActiveRecordingsStatus struct {
	ActiveCount          int     `json:"active_count"`
	ActiveRecorders      []int64 `json:"active_recorders"`
	IsAnyRecordingActive bool    `json:"is_any_recording_active"`
}

// RecordingStatusChangedEvent — payload WS события call_recording_status_changed.
type RecordingStatusChangedEvent struct {
	CallID               string `json:"call_id"`
	Action               string `json:"action"` // "started" | "stopped"
	OwnerUserID          int64  `json:"owner_user_id"`
	ActiveCount          int    `json:"active_count"`
	IsAnyRecordingActive bool   `json:"is_any_recording_active"`
	Ts                   int64  `json:"ts"`
}
