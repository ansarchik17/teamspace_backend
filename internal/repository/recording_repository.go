package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

// CallRecordingRepository управляет персистентностью аудио-записей звонков.
type CallRecordingRepository struct {
	db     *pgxpool.Pool
	logger *zap.Logger
}

func NewCallRecordingRepository(db *pgxpool.Pool, logger *zap.Logger) *CallRecordingRepository {
	return &CallRecordingRepository{
		db:     db,
		logger: logger.With(zap.String("repository", "call_recording")),
	}
}

// Create сохраняет новую запись сразу после старта Egress.
func (r *CallRecordingRepository) Create(ctx context.Context, rec *domain.CallRecording) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO call_recordings (
			call_id, room_id, egress_id, status, file_path, started_by
		) VALUES ($1, $2, $3, $4, $5, $6)
	`,
		rec.CallID,
		rec.RoomID,
		rec.EgressID,
		rec.Status,
		rec.FilePath,
		rec.StartedBy,
	)
	if err != nil {
		return fmt.Errorf("create call recording: %w", err)
	}
	return nil
}

// GetByEgressID возвращает запись по egress_id.
func (r *CallRecordingRepository) GetByEgressID(ctx context.Context, egressID string) (*domain.CallRecording, error) {
	var rec domain.CallRecording
	err := r.db.QueryRow(ctx, `
		SELECT id, call_id, room_id, egress_id, status, file_path, file_url,
		       started_by, started_at, stopped_at, duration_s, size_bytes, created_at, updated_at
		FROM call_recordings
		WHERE egress_id = $1
	`, egressID).Scan(
		&rec.ID, &rec.CallID, &rec.RoomID, &rec.EgressID, &rec.Status,
		&rec.FilePath, &rec.FileURL,
		&rec.StartedBy, &rec.StartedAt, &rec.StoppedAt,
		&rec.DurationS, &rec.SizeBytes,
		&rec.CreatedAt, &rec.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get recording by egress: %w", err)
	}
	return &rec, nil
}

// GetActiveByCallID возвращает активную запись (status=recording) для данного call_id.
// Нужно чтобы не стартовать вторую запись параллельно.
func (r *CallRecordingRepository) GetActiveByCallID(ctx context.Context, callID uuid.UUID) (*domain.CallRecording, error) {
	var rec domain.CallRecording
	err := r.db.QueryRow(ctx, `
		SELECT id, call_id, room_id, egress_id, status, file_path, file_url,
		       started_by, started_at, stopped_at, duration_s, size_bytes, created_at, updated_at
		FROM call_recordings
		WHERE call_id = $1 AND status = 'recording'
		ORDER BY started_at DESC
		LIMIT 1
	`, callID).Scan(
		&rec.ID, &rec.CallID, &rec.RoomID, &rec.EgressID, &rec.Status,
		&rec.FilePath, &rec.FileURL,
		&rec.StartedBy, &rec.StartedAt, &rec.StoppedAt,
		&rec.DurationS, &rec.SizeBytes,
		&rec.CreatedAt, &rec.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get active recording: %w", err)
	}
	return &rec, nil
}

// ListByCallID возвращает все записи для данного call_id (для истории).
func (r *CallRecordingRepository) ListByCallID(ctx context.Context, callID uuid.UUID) ([]domain.CallRecording, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, call_id, room_id, egress_id, status, file_path, file_url,
		       started_by, started_at, stopped_at, duration_s, size_bytes, created_at, updated_at
		FROM call_recordings
		WHERE call_id = $1
		ORDER BY started_at DESC
	`, callID)
	if err != nil {
		return nil, fmt.Errorf("list recordings: %w", err)
	}
	defer rows.Close()

	var list []domain.CallRecording
	for rows.Next() {
		var rec domain.CallRecording
		if err := rows.Scan(
			&rec.ID, &rec.CallID, &rec.RoomID, &rec.EgressID, &rec.Status,
			&rec.FilePath, &rec.FileURL,
			&rec.StartedBy, &rec.StartedAt, &rec.StoppedAt,
			&rec.DurationS, &rec.SizeBytes,
			&rec.CreatedAt, &rec.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan recording: %w", err)
		}
		list = append(list, rec)
	}
	return list, rows.Err()
}

// ListForUser возвращает все аудио-записи для текущего пользователя (где он звонил или ему звонили).
func (r *CallRecordingRepository) ListForUser(ctx context.Context, userID int64, limit, offset int) ([]domain.CallRecording, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}

	query := `
		SELECT cr.id, cr.call_id, cr.room_id, cr.egress_id, cr.status, cr.file_path, cr.file_url,
		       cr.started_by, cr.started_at, cr.stopped_at, cr.duration_s, cr.size_bytes, cr.created_at, cr.updated_at
		FROM call_recordings cr
		JOIN call_history ch ON cr.call_id = ch.call_id
		WHERE ch.from_user_id = $1 OR ch.to_user_id = $1 OR EXISTS (
			SELECT 1 FROM call_participants cp WHERE cp.call_id = cr.call_id AND cp.user_id = $1
		)
		ORDER BY cr.started_at DESC
		LIMIT $2 OFFSET $3
	`

	rows, err := r.db.Query(ctx, query, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list user recordings: %w", err)
	}
	defer rows.Close()

	var list []domain.CallRecording
	for rows.Next() {
		var rec domain.CallRecording
		if err := rows.Scan(
			&rec.ID, &rec.CallID, &rec.RoomID, &rec.EgressID, &rec.Status,
			&rec.FilePath, &rec.FileURL,
			&rec.StartedBy, &rec.StartedAt, &rec.StoppedAt,
			&rec.DurationS, &rec.SizeBytes,
			&rec.CreatedAt, &rec.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan user recording: %w", err)
		}
		list = append(list, rec)
	}
	return list, rows.Err()
}

// UpdateStatus обновляет статус записи при завершении/остановке.
func (r *CallRecordingRepository) UpdateStatus(
	ctx context.Context,
	egressID string,
	status domain.RecordingStatus,
	filePath *string,
	durationS *int,
	sizeBytes *int64,
) error {
	_, err := r.db.Exec(ctx, `
		UPDATE call_recordings
		SET status      = $1,
			file_path   = COALESCE($2, file_path),
			stopped_at  = CASE WHEN $1 IN ('completed','stopped','failed') THEN NOW() ELSE stopped_at END,
			duration_s  = COALESCE($3, duration_s),
			size_bytes  = COALESCE($4, size_bytes),
			updated_at  = NOW()
		WHERE egress_id = $5
	`, status, filePath, durationS, sizeBytes, egressID)
	if err != nil {
		return fmt.Errorf("update recording status: %w", err)
	}
	return nil
}
