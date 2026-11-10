package repository

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func (r *CalendarRepository) UpsertEventException(ctx context.Context, item domain.CalendarEventException) (*domain.CalendarEventException, error) {
	return upsertEventException(ctx, r.db, item)
}

type calendarExceptionDB interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func upsertEventException(ctx context.Context, db calendarExceptionDB, item domain.CalendarEventException) (*domain.CalendarEventException, error) {
	var data []byte
	var err error
	if item.EventData != nil {
		data, err = json.Marshal(item.EventData)
		if err != nil {
			return nil, err
		}
	}
	var start, end any
	if item.EventData != nil {
		start, end = item.EventData.StartTime, item.EventData.EndTime
	}
	row := db.QueryRow(ctx, `
		INSERT INTO event_exceptions (event_id, occurrence_date, exception_type, event_data, start_time, end_time)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (event_id, occurrence_date) DO UPDATE SET
			exception_type = EXCLUDED.exception_type, event_data = EXCLUDED.event_data,
			start_time = EXCLUDED.start_time, end_time = EXCLUDED.end_time, updated_at = now()
		RETURNING id, created_at, updated_at`,
		item.EventID, item.OccurrenceDate, item.ExceptionType, data, start, end)
	if err := row.Scan(&item.ID, &item.CreatedAt, &item.UpdatedAt); err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *CalendarRepository) ListEventExceptions(ctx context.Context, eventIDs []uuid.UUID) ([]domain.CalendarEventException, error) {
	return listEventExceptions(ctx, r.db, eventIDs)
}

func listEventExceptions(ctx context.Context, db calendarExceptionDB, eventIDs []uuid.UUID) ([]domain.CalendarEventException, error) {
	if len(eventIDs) == 0 {
		return nil, nil
	}
	rows, err := db.Query(ctx, `SELECT id, event_id, occurrence_date, exception_type, event_data, created_at, updated_at
		FROM event_exceptions WHERE event_id = ANY($1) ORDER BY occurrence_date`, eventIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.CalendarEventException
	for rows.Next() {
		var item domain.CalendarEventException
		var data []byte
		if err := rows.Scan(&item.ID, &item.EventID, &item.OccurrenceDate, &item.ExceptionType, &data, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		if len(data) != 0 {
			item.EventData = new(domain.OccurrenceDetails)
			if err := json.Unmarshal(data, item.EventData); err != nil {
				return nil, err
			}
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (r *CalendarRepository) DeleteEventException(ctx context.Context, eventID, exceptionID uuid.UUID) (bool, error) {
	tag, err := r.db.Exec(ctx, `DELETE FROM event_exceptions WHERE event_id=$1 AND id=$2`, eventID, exceptionID)
	return tag.RowsAffected() != 0, err
}

func (r *CalendarRepository) GetEventException(ctx context.Context, eventID, exceptionID uuid.UUID) (*domain.CalendarEventException, error) {
	var item domain.CalendarEventException
	var data []byte
	err := r.db.QueryRow(ctx, `SELECT id, event_id, occurrence_date, exception_type, event_data, created_at, updated_at
		FROM event_exceptions WHERE event_id=$1 AND id=$2`, eventID, exceptionID).
		Scan(&item.ID, &item.EventID, &item.OccurrenceDate, &item.ExceptionType, &data, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(data) != 0 {
		item.EventData = new(domain.OccurrenceDetails)
		if err := json.Unmarshal(data, item.EventData); err != nil {
			return nil, err
		}
	}
	return &item, nil
}
