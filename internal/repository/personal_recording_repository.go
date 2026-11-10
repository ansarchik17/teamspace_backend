package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

// PersonalCallRecordingRepository управляет персональными записями звонков.
type PersonalCallRecordingRepository struct {
	db     *pgxpool.Pool
	logger *zap.Logger
}

func NewPersonalCallRecordingRepository(db *pgxpool.Pool, logger *zap.Logger) *PersonalCallRecordingRepository {
	return &PersonalCallRecordingRepository{
		db:     db,
		logger: logger.With(zap.String("repository", "personal_call_recording")),
	}
}

const personalRecordingSelectCols = `
	id, call_id, room_name, owner_user_id, participant_identity, egress_id, status,
	started_at, stopped_at, failed_at, file_path, duration_sec, failure_reason,
	livekit_error_code, livekit_error_message, end_reason, minio_object_exists,
	created_at, updated_at
`

var (
	personalRecordingLiveStatuses = []string{
		string(domain.PersonalRecordingStatusStarting),
		string(domain.PersonalRecordingStatusActive),
	}
	personalRecordingLockStatuses = []string{
		string(domain.PersonalRecordingStatusStarting),
		string(domain.PersonalRecordingStatusActive),
		string(domain.PersonalRecordingStatusStopping),
	}
)

func personalRecordingStatusList(statuses []string) string {
	items := make([]string, 0, len(statuses))
	for _, status := range statuses {
		items = append(items, "'"+status+"'")
	}
	return strings.Join(items, ", ")
}

// Create сохраняет новую персональную запись сразу после старта Egress.
func (r *PersonalCallRecordingRepository) Create(ctx context.Context, rec *domain.PersonalCallRecording) error {
	err := r.db.QueryRow(ctx, `
		INSERT INTO personal_call_recordings (
			call_id, room_name, owner_user_id, participant_identity, egress_id, status, started_at, file_path
		) VALUES ($1, $2, $3, $4, $5, $6, now(), $7)
		RETURNING id, started_at, created_at, updated_at
	`,
		rec.CallID,
		rec.RoomName,
		rec.OwnerUserID,
		rec.ParticipantIdentity,
		rec.EgressID,
		rec.Status,
		rec.FilePath,
	).Scan(&rec.ID, &rec.StartedAt, &rec.CreatedAt, &rec.UpdatedAt)
	if err != nil {
		return fmt.Errorf("create personal recording: %w", err)
	}
	return nil
}

// GetLockedByCallIDAndOwner возвращает запись с активным lock для данного пользователя и звонка.
// Используется для идемпотентного старта и защиты от повторных stop/start операций.
func (r *PersonalCallRecordingRepository) GetLockedByCallIDAndOwner(
	ctx context.Context, callID uuid.UUID, ownerUserID int64,
) (*domain.PersonalCallRecording, error) {
	var rec domain.PersonalCallRecording
	err := r.db.QueryRow(ctx, `
		SELECT `+personalRecordingSelectCols+`
		FROM personal_call_recordings
		WHERE call_id = $1
		  AND owner_user_id = $2
		  AND status IN (`+personalRecordingStatusList(personalRecordingLockStatuses)+`)
		ORDER BY created_at DESC
		LIMIT 1
		`, callID, ownerUserID).Scan(
		&rec.ID, &rec.CallID, &rec.RoomName, &rec.OwnerUserID, &rec.ParticipantIdentity, &rec.EgressID, &rec.Status,
		&rec.StartedAt, &rec.StoppedAt, &rec.FailedAt, &rec.FilePath, &rec.DurationSec, &rec.FailureReason,
		&rec.LiveKitErrorCode, &rec.LiveKitErrorMessage, &rec.EndReason, &rec.MinIOObjectExists,
		&rec.CreatedAt, &rec.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get locked personal recording by owner: %w", err)
	}
	return &rec, nil
}

// GetByID возвращает запись по её UUID.
func (r *PersonalCallRecordingRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.PersonalCallRecording, error) {
	var rec domain.PersonalCallRecording
	err := r.db.QueryRow(ctx, `
		SELECT `+personalRecordingSelectCols+`
		FROM personal_call_recordings
		WHERE id = $1
		`, id).Scan(
		&rec.ID, &rec.CallID, &rec.RoomName, &rec.OwnerUserID, &rec.ParticipantIdentity, &rec.EgressID, &rec.Status,
		&rec.StartedAt, &rec.StoppedAt, &rec.FailedAt, &rec.FilePath, &rec.DurationSec, &rec.FailureReason,
		&rec.LiveKitErrorCode, &rec.LiveKitErrorMessage, &rec.EndReason, &rec.MinIOObjectExists,
		&rec.CreatedAt, &rec.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get personal recording by id: %w", err)
	}
	return &rec, nil
}

// GetByEgressID возвращает запись по egress_id (используется в webhook).
func (r *PersonalCallRecordingRepository) GetByEgressID(ctx context.Context, egressID string) (*domain.PersonalCallRecording, error) {
	var rec domain.PersonalCallRecording
	err := r.db.QueryRow(ctx, `
		SELECT `+personalRecordingSelectCols+`
		FROM personal_call_recordings
		WHERE egress_id = $1
		`, egressID).Scan(
		&rec.ID, &rec.CallID, &rec.RoomName, &rec.OwnerUserID, &rec.ParticipantIdentity, &rec.EgressID, &rec.Status,
		&rec.StartedAt, &rec.StoppedAt, &rec.FailedAt, &rec.FilePath, &rec.DurationSec, &rec.FailureReason,
		&rec.LiveKitErrorCode, &rec.LiveKitErrorMessage, &rec.EndReason, &rec.MinIOObjectExists,
		&rec.CreatedAt, &rec.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get personal recording by egress: %w", err)
	}
	return &rec, nil
}

// CountLiveByCallID возвращает количество live-записей и список owner_user_id для данного call_id.
func (r *PersonalCallRecordingRepository) CountLiveByCallID(
	ctx context.Context, callID uuid.UUID,
) (int, []int64, error) {
	rows, err := r.db.Query(ctx, `
		SELECT owner_user_id
		FROM personal_call_recordings
		WHERE call_id = $1 AND status IN (`+personalRecordingStatusList(personalRecordingLiveStatuses)+`)
	`, callID)
	if err != nil {
		return 0, nil, fmt.Errorf("count live personal recordings: %w", err)
	}
	defer rows.Close()

	var owners []int64
	for rows.Next() {
		var uid int64
		if err := rows.Scan(&uid); err != nil {
			return 0, nil, fmt.Errorf("scan owner_user_id: %w", err)
		}
		owners = append(owners, uid)
	}
	return len(owners), owners, rows.Err()
}

// ListForOwner возвращает все (или paginated) записи текущего пользователя.
func (r *PersonalCallRecordingRepository) ListForOwner(
	ctx context.Context, ownerUserID int64, limit, offset int,
) ([]domain.PersonalCallRecording, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := r.db.Query(ctx, `
		SELECT `+personalRecordingSelectCols+`
		FROM personal_call_recordings
		WHERE owner_user_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`, ownerUserID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list personal recordings for owner: %w", err)
	}
	defer rows.Close()

	var list []domain.PersonalCallRecording
	for rows.Next() {
		var rec domain.PersonalCallRecording
		if err := rows.Scan(
			&rec.ID, &rec.CallID, &rec.RoomName, &rec.OwnerUserID, &rec.ParticipantIdentity, &rec.EgressID, &rec.Status,
			&rec.StartedAt, &rec.StoppedAt, &rec.FailedAt, &rec.FilePath, &rec.DurationSec, &rec.FailureReason,
			&rec.LiveKitErrorCode, &rec.LiveKitErrorMessage, &rec.EndReason, &rec.MinIOObjectExists,
			&rec.CreatedAt, &rec.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan personal recording: %w", err)
		}
		list = append(list, rec)
	}
	return list, rows.Err()
}

// ListLockedByCallID возвращает все записи звонка с активным lock.
func (r *PersonalCallRecordingRepository) ListLockedByCallID(
	ctx context.Context, callID uuid.UUID,
) ([]domain.PersonalCallRecording, error) {
	rows, err := r.db.Query(ctx, `
		SELECT `+personalRecordingSelectCols+`
		FROM personal_call_recordings
		WHERE call_id = $1
		  AND status IN (`+personalRecordingStatusList(personalRecordingLockStatuses)+`)
		ORDER BY created_at DESC
	`, callID)
	if err != nil {
		return nil, fmt.Errorf("list locked personal recordings by call: %w", err)
	}
	defer rows.Close()

	var list []domain.PersonalCallRecording
	for rows.Next() {
		var rec domain.PersonalCallRecording
		if err := rows.Scan(
			&rec.ID, &rec.CallID, &rec.RoomName, &rec.OwnerUserID, &rec.ParticipantIdentity, &rec.EgressID, &rec.Status,
			&rec.StartedAt, &rec.StoppedAt, &rec.FailedAt, &rec.FilePath, &rec.DurationSec, &rec.FailureReason,
			&rec.LiveKitErrorCode, &rec.LiveKitErrorMessage, &rec.EndReason, &rec.MinIOObjectExists,
			&rec.CreatedAt, &rec.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan locked personal recording: %w", err)
		}
		list = append(list, rec)
	}
	return list, rows.Err()
}

func (r *PersonalCallRecordingRepository) ListStaleLocked(
	ctx context.Context, updatedBefore time.Time, limit int,
) ([]domain.PersonalCallRecording, error) {
	if limit <= 0 {
		limit = 100
	}

	rows, err := r.db.Query(ctx, `
		SELECT `+personalRecordingSelectCols+`
		FROM personal_call_recordings
		WHERE status IN (`+personalRecordingStatusList(personalRecordingLockStatuses)+`)
		  AND updated_at < $1
		ORDER BY updated_at ASC
		LIMIT $2
	`, updatedBefore, limit)
	if err != nil {
		return nil, fmt.Errorf("list stale locked personal recordings: %w", err)
	}
	defer rows.Close()

	var list []domain.PersonalCallRecording
	for rows.Next() {
		var rec domain.PersonalCallRecording
		if err := rows.Scan(
			&rec.ID, &rec.CallID, &rec.RoomName, &rec.OwnerUserID, &rec.ParticipantIdentity, &rec.EgressID, &rec.Status,
			&rec.StartedAt, &rec.StoppedAt, &rec.FailedAt, &rec.FilePath, &rec.DurationSec, &rec.FailureReason,
			&rec.LiveKitErrorCode, &rec.LiveKitErrorMessage, &rec.EndReason, &rec.MinIOObjectExists,
			&rec.CreatedAt, &rec.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan stale personal recording: %w", err)
		}
		list = append(list, rec)
	}
	return list, rows.Err()
}

func (r *PersonalCallRecordingRepository) UpdateResult(
	ctx context.Context,
	id uuid.UUID,
	status domain.PersonalRecordingStatus,
	filePath *string,
	durationSec *int,
	failureReason *string,
	liveKitErrorCode *int,
	liveKitErrorMessage *string,
	endReason *string,
	minioObjectExists *bool,
) error {
	_, err := r.db.Exec(ctx, `
		UPDATE personal_call_recordings
		SET status                = $1,
		    stopped_at            = CASE
		        WHEN $1 IN ('completed', 'failed', 'aborted', 'stopped') AND stopped_at IS NULL THEN now()
		        ELSE stopped_at
		    END,
		    failed_at             = CASE
		        WHEN $1 IN ('failed', 'aborted') AND failed_at IS NULL THEN now()
		        WHEN $1 NOT IN ('failed', 'aborted') THEN failed_at
		        ELSE failed_at
		    END,
		    file_path             = COALESCE($2, file_path),
		    duration_sec          = COALESCE(
		        $3,
		        duration_sec,
		        CASE
		            WHEN $1 IN ('completed', 'failed', 'aborted', 'stopped')
		                THEN GREATEST(0, EXTRACT(EPOCH FROM (COALESCE(stopped_at, now()) - started_at))::int)
		            ELSE duration_sec
		        END
		    ),
		    failure_reason        = COALESCE($4, failure_reason),
		    livekit_error_code    = COALESCE($5, livekit_error_code),
		    livekit_error_message = COALESCE($6, livekit_error_message),
		    end_reason            = COALESCE($7, end_reason),
		    minio_object_exists   = COALESCE($8, minio_object_exists),
		    updated_at            = now()
		WHERE id = $9
	`, status, filePath, durationSec, failureReason, liveKitErrorCode, liveKitErrorMessage, endReason, minioObjectExists, id)
	if err != nil {
		return fmt.Errorf("update personal recording result: %w", err)
	}
	return nil
}

func (r *PersonalCallRecordingRepository) UpdateResultByEgressID(
	ctx context.Context,
	egressID string,
	status domain.PersonalRecordingStatus,
	filePath *string,
	durationSec *int,
	failureReason *string,
	liveKitErrorCode *int,
	liveKitErrorMessage *string,
	endReason *string,
	minioObjectExists *bool,
) error {
	_, err := r.db.Exec(ctx, `
		UPDATE personal_call_recordings
		SET status                = $1,
		    stopped_at            = CASE
		        WHEN $1 IN ('completed', 'failed', 'aborted', 'stopped') AND stopped_at IS NULL THEN now()
		        ELSE stopped_at
		    END,
		    failed_at             = CASE
		        WHEN $1 IN ('failed', 'aborted') AND failed_at IS NULL THEN now()
		        WHEN $1 NOT IN ('failed', 'aborted') THEN failed_at
		        ELSE failed_at
		    END,
		    file_path             = COALESCE($2, file_path),
		    duration_sec          = COALESCE(
		        $3,
		        duration_sec,
		        CASE
		            WHEN $1 IN ('completed', 'failed', 'aborted', 'stopped')
		                THEN GREATEST(0, EXTRACT(EPOCH FROM (COALESCE(stopped_at, now()) - started_at))::int)
		            ELSE duration_sec
		        END
		    ),
		    failure_reason        = COALESCE($4, failure_reason),
		    livekit_error_code    = COALESCE($5, livekit_error_code),
		    livekit_error_message = COALESCE($6, livekit_error_message),
		    end_reason            = COALESCE($7, end_reason),
		    minio_object_exists   = COALESCE($8, minio_object_exists),
		    updated_at            = now()
		WHERE egress_id = $9
	`, status, filePath, durationSec, failureReason, liveKitErrorCode, liveKitErrorMessage, endReason, minioObjectExists, egressID)
	if err != nil {
		return fmt.Errorf("update personal recording result by egress: %w", err)
	}
	return nil
}

// UpdateStatus обновляет статус записи и связанные поля.
func (r *PersonalCallRecordingRepository) UpdateStatus(
	ctx context.Context,
	id uuid.UUID,
	status domain.PersonalRecordingStatus,
	filePath *string,
	durationSec *int,
	failureReason *string,
) error {
	return r.UpdateResult(ctx, id, status, filePath, durationSec, failureReason, nil, nil, nil, nil)
}

// UpdateStatusByEgressID — вариант UpdateStatus по egress_id (для webhook финализации).
func (r *PersonalCallRecordingRepository) UpdateStatusByEgressID(
	ctx context.Context,
	egressID string,
	status domain.PersonalRecordingStatus,
	filePath *string,
	durationSec *int,
	failureReason *string,
) error {
	return r.UpdateResultByEgressID(ctx, egressID, status, filePath, durationSec, failureReason, nil, nil, nil, nil)
}

// MarkStoppingIfLive переводит запись из starting|active в stopping один раз.
func (r *PersonalCallRecordingRepository) MarkStoppingIfLive(
	ctx context.Context, id uuid.UUID,
) (bool, error) {
	tag, err := r.db.Exec(ctx, `
		UPDATE personal_call_recordings
		SET status = $1,
		    stopped_at = CASE WHEN stopped_at IS NULL THEN now() ELSE stopped_at END,
		    updated_at = now()
		WHERE id = $2
		  AND status IN ($3, $4)
	`,
		domain.PersonalRecordingStatusStopping,
		id,
		domain.PersonalRecordingStatusStarting,
		domain.PersonalRecordingStatusActive,
	)
	if err != nil {
		return false, fmt.Errorf("mark personal recording stopping: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// FinalizeCallRecordings завершает все записи звонка с активным lock.
func (r *PersonalCallRecordingRepository) FinalizeCallRecordings(
	ctx context.Context, callID uuid.UUID, status domain.PersonalRecordingStatus, failureReason string,
) error {
	_, err := r.db.Exec(ctx, `
		UPDATE personal_call_recordings
		SET status     = $2,
		    failure_reason = COALESCE(failure_reason, $3),
		    stopped_at = CASE WHEN stopped_at IS NULL THEN now() ELSE stopped_at END,
		    duration_sec = COALESCE(
		        duration_sec,
		        GREATEST(0, EXTRACT(EPOCH FROM (COALESCE(stopped_at, now()) - started_at))::int)
		    ),
		    updated_at = now()
		WHERE call_id = $1
		  AND status IN (`+personalRecordingStatusList(personalRecordingLockStatuses)+`)
	`, callID, status, failureReason)
	if err != nil {
		return fmt.Errorf("finalize call personal recordings: %w", err)
	}
	return nil
}
