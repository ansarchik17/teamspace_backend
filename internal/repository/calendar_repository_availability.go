package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/domain"
)

type CalendarConflictRow struct {
	UserID             int64
	UserName           string
	Username           string
	Email              string
	AvatarURL          *string
	EventID            uuid.UUID
	EventTitle         string
	StartTime          time.Time
	EndTime            time.Time
	Status             string
	RecurrenceRule     *string
	RecurrenceInterval int
	RecurrenceUntil    *time.Time
	IsAllDay           bool
}

func (r *CalendarRepository) ListAvailabilityConflicts(
	ctx context.Context,
	userIDs []int64,
	startTime, endTime time.Time,
	excludeEventID *uuid.UUID,
) ([]domain.CalendarConflictItem, error) {
	if len(userIDs) == 0 {
		return []domain.CalendarConflictItem{}, nil
	}

	q := `
		SELECT
			ea.user_id,
			COALESCE(NULLIF(TRIM(CONCAT_WS(' ', u.first_name, u.last_name)), ''), u.username, u.email, 'Пользователь') AS user_name,
			u.username,
			u.email,
			u.avatar_url,
			e.id,
			e.title,
			e.start_time,
			e.end_time,
			ea.status
		FROM event_attendees ea
		JOIN events e ON e.id = ea.event_id
		JOIN users u ON u.id = ea.user_id
		WHERE ea.user_id = ANY($1)
		  AND e.deleted_at IS NULL
		  AND ea.status IN ('accepted', 'pending', 'tentative')
		  AND e.start_time < $3
		  AND e.end_time > $2
		  AND ($4::uuid IS NULL OR e.id <> $4)
		ORDER BY e.start_time ASC, ea.user_id ASC
	`

	rows, err := r.db.Query(ctx, q, userIDs, startTime, endTime, excludeEventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	conflicts := make([]domain.CalendarConflictItem, 0)
	for rows.Next() {
		var item domain.CalendarConflictItem
		if err := rows.Scan(
			&item.UserID,
			&item.UserName,
			&item.Username,
			&item.Email,
			&item.AvatarURL,
			&item.EventID,
			&item.EventTitle,
			&item.StartTime,
			&item.EndTime,
			&item.Status,
		); err != nil {
			return nil, err
		}
		conflicts = append(conflicts, item)
	}
	return conflicts, rows.Err()
}

func (r *CalendarRepository) ListBusyEventsInRange(
	ctx context.Context,
	userIDs []int64,
	from, to time.Time,
	excludeEventID *uuid.UUID,
) ([]CalendarConflictRow, error) {
	if len(userIDs) == 0 {
		return []CalendarConflictRow{}, nil
	}

	q := `
		SELECT
			ea.user_id,
			COALESCE(NULLIF(TRIM(CONCAT_WS(' ', u.first_name, u.last_name)), ''), u.username, u.email, 'Пользователь') AS user_name,
			u.username,
			u.email,
			u.avatar_url,
			e.id,
			e.title,
			e.start_time,
			e.end_time,
			ea.status,
			e.recurrence_rule,
			e.recurrence_interval,
			e.recurrence_until,
			e.is_all_day
		FROM event_attendees ea
		JOIN events e ON e.id = ea.event_id
		JOIN users u ON u.id = ea.user_id
		WHERE ea.user_id = ANY($1)
		  AND e.deleted_at IS NULL
		  AND ea.status IN ('accepted', 'pending', 'tentative')
		  AND ($4::uuid IS NULL OR e.id <> $4)
		  AND (
			  (e.recurrence_rule IS NULL AND e.start_time < $3 AND e.end_time > $2)
			  OR (e.recurrence_rule IS NOT NULL
				  AND e.start_time < $3
				  AND (e.recurrence_until IS NULL OR e.recurrence_until >= $2))
			  OR (e.recurrence_rule IS NOT NULL AND EXISTS (
				  SELECT 1 FROM event_exceptions ex
				  WHERE ex.event_id = e.id
					AND ex.exception_type = 'modified'
					AND ex.start_time < $3
					AND ex.end_time > $2
			  ))
		  )
		UNION
		SELECT
			e.user_id,
			COALESCE(NULLIF(TRIM(CONCAT_WS(' ', u.first_name, u.last_name)), ''), u.username, u.email, 'Пользователь') AS user_name,
			u.username,
			u.email,
			u.avatar_url,
			e.id,
			e.title,
			e.start_time,
			e.end_time,
			'accepted' AS status,
			e.recurrence_rule,
			e.recurrence_interval,
			e.recurrence_until,
			e.is_all_day
		FROM events e
		JOIN users u ON u.id = e.user_id
		WHERE e.user_id = ANY($1)
		  AND e.deleted_at IS NULL
		  AND NOT EXISTS (
			  SELECT 1 FROM event_attendees ea WHERE ea.event_id = e.id AND ea.user_id = e.user_id
		  )
		  AND ($4::uuid IS NULL OR e.id <> $4)
		  AND (
			  (e.recurrence_rule IS NULL AND e.start_time < $3 AND e.end_time > $2)
			  OR (e.recurrence_rule IS NOT NULL
				  AND e.start_time < $3
				  AND (e.recurrence_until IS NULL OR e.recurrence_until >= $2))
			  OR (e.recurrence_rule IS NOT NULL AND EXISTS (
				  SELECT 1 FROM event_exceptions ex
				  WHERE ex.event_id = e.id
					AND ex.exception_type = 'modified'
					AND ex.start_time < $3
					AND ex.end_time > $2
			  ))
		  )
		ORDER BY start_time ASC, user_id ASC
	`

	rows, err := r.db.Query(ctx, q, userIDs, from, to, excludeEventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]CalendarConflictRow, 0)
	for rows.Next() {
		var row CalendarConflictRow
		if err := rows.Scan(
			&row.UserID,
			&row.UserName,
			&row.Username,
			&row.Email,
			&row.AvatarURL,
			&row.EventID,
			&row.EventTitle,
			&row.StartTime,
			&row.EndTime,
			&row.Status,
			&row.RecurrenceRule,
			&row.RecurrenceInterval,
			&row.RecurrenceUntil,
			&row.IsAllDay,
		); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func (r *CalendarRepository) UpdateMeetingInfo(ctx context.Context, eventID uuid.UUID, info domain.CalendarMeetingInfo) error {
	q := `
		UPDATE events
		SET meeting_provider = $2,
		    meeting_room_id = $3,
		    meeting_url = $4,
		    livekit_room_name = $5,
		    cisco_meeting_id = $6,
		    cisco_join_url = $7,
		    online_meeting_status = $8,
		    updated_at = NOW()
		WHERE id = $1
		  AND deleted_at IS NULL
	`
	_, err := r.db.Exec(
		ctx,
		q,
		eventID,
		info.Provider,
		info.MeetingRoomID,
		info.MeetingURL,
		info.LiveKitRoomName,
		info.CiscoMeetingID,
		info.CiscoJoinURL,
		info.Status,
	)
	return err
}
