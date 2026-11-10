package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func (r *CalendarRepository) GetUpcomingEvents(ctx context.Context, userID int64, limit int) ([]domain.Event, error) {
	now := time.Now()
	query := `
		SELECT DISTINCT
			e.id, e.user_id, e.title, e.description, e.start_time, e.end_time,
			e.location, e.is_all_day,
			e.recurrence_rule, e.recurrence_interval, e.recurrence_until,
			e.color, e.created_at, e.updated_at,
			e.url
		FROM events e
		LEFT JOIN event_attendees ea ON e.id = ea.event_id
		WHERE (e.user_id = $1 OR ea.user_id = $1)
		  AND e.deleted_at IS NULL
		  AND e.start_time >= $2
		ORDER BY e.start_time ASC
		LIMIT $3
	`

	rows, err := r.db.Query(ctx, query, userID, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := make([]domain.Event, 0, limit)
	ids := make([]uuid.UUID, 0, limit)

	for rows.Next() {
		var e domain.Event
		if err := rows.Scan(
			&e.ID, &e.UserID, &e.Title, &e.Description, &e.StartTime, &e.EndTime,
			&e.Location, &e.IsAllDay,
			&e.RecurrenceRule, &e.RecurrenceInterval, &e.RecurrenceUntil,
			&e.Color, &e.CreatedAt, &e.UpdatedAt,
			&e.URL,
		); err != nil {
			return nil, err
		}
		events = append(events, e)
		ids = append(ids, e.ID)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	if len(ids) > 0 {
		attMap, err := r.getAttendeesByEventIDs(ctx, ids)
		if err != nil {
			return nil, err
		}
		for i := range events {
			events[i].Attendees = attMap[events[i].ID]
		}
	}

	return events, nil
}
