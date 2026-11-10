package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

type ReminderWithEventRow struct {
	ReminderID     uuid.UUID
	ReminderUserID int64
	EventID        uuid.UUID
	OwnerID        int64
	MinutesBefore  int

	Title              string
	Description        *string
	StartTime          time.Time
	EndTime            time.Time
	Timezone           string
	IsOnline           bool
	MeetingProvider    string
	MeetingRoomID      *string
	MeetingURL         *string
	LiveKitRoomName    *string
	MeetingStatus      string
	Location           *string
	IsAllDay           bool
	RecurrenceRule     *string
	RecurrenceInterval int
	RecurrenceUntil    *time.Time
	Color              *string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

func (r *CalendarRepository) UpsertReminder(ctx context.Context, eventID uuid.UUID, userID int64, minutesBefore int) error {
	q := `
		INSERT INTO event_reminders (event_id, user_id, minutes_before, created_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (event_id, user_id)
		DO UPDATE SET minutes_before = EXCLUDED.minutes_before
	`
	_, err := r.db.Exec(ctx, q, eventID, userID, minutesBefore)
	return err
}

func (r *CalendarRepository) UpsertRemindersBulk(ctx context.Context, eventID uuid.UUID, userIDs []int64, minutesBefore int) error {
	return r.upsertRemindersTx(ctx, r.db, eventID, userIDs, minutesBefore)
}

func (r *CalendarRepository) upsertRemindersTx(ctx context.Context, querier interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, eventID uuid.UUID, userIDs []int64, minutesBefore int) error {
	if len(userIDs) == 0 {
		return nil
	}

	q := `
		INSERT INTO event_reminders (event_id, user_id, minutes_before, created_at)
		SELECT $1, uid, $3, NOW()
		FROM unnest($2::bigint[]) AS uid
		ON CONFLICT (event_id, user_id)
		DO UPDATE SET minutes_before = EXCLUDED.minutes_before
	`
	_, err := querier.Exec(ctx, q, eventID, userIDs, minutesBefore)
	return err
}

func (r *CalendarRepository) DeleteReminder(ctx context.Context, eventID uuid.UUID, userID int64) error {
	q := `DELETE FROM event_reminders WHERE event_id = $1 AND user_id = $2`
	_, err := r.db.Exec(ctx, q, eventID, userID)
	return err
}

func (r *CalendarRepository) GetMaxReminderMinutes(ctx context.Context) (int, error) {
	q := `SELECT COALESCE(MAX(minutes_before), 0) FROM event_reminders`
	var max int
	if err := r.db.QueryRow(ctx, q).Scan(&max); err != nil {
		return 0, err
	}
	if max < 0 {
		max = 0
	}
	return max, nil
}

func (r *CalendarRepository) ListRemindersWithEvents(ctx context.Context, from, to time.Time) ([]ReminderWithEventRow, error) {
	q := `
		SELECT
			er.id,
			er.user_id,
			er.event_id,
			e.user_id as owner_id,
			er.minutes_before,

			e.title,
			e.description,
			e.start_time,
			e.end_time,
			COALESCE(e.timezone, 'Asia/Almaty'), e.is_online,
			COALESCE(e.meeting_provider, 'none'), e.meeting_room_id, e.meeting_url,
			e.livekit_room_name, COALESCE(e.online_meeting_status, 'none'),
			e.location,
			e.is_all_day,
			e.recurrence_rule,
			e.recurrence_interval,
			e.recurrence_until,
			e.color,
			e.created_at,
			e.updated_at
		FROM event_reminders er
		JOIN events e ON e.id = er.event_id
		JOIN event_attendees ea ON ea.event_id = er.event_id
			AND ea.user_id = er.user_id AND ea.status <> 'declined'
		WHERE e.deleted_at IS NULL
		  AND (
			  (e.recurrence_rule IS NULL AND e.end_time >= $1 AND e.start_time <= $2)
			  OR
			  (e.recurrence_rule IS NOT NULL
				  AND e.start_time <= $2
				  AND (e.recurrence_until IS NULL OR e.recurrence_until >= $1)
			  )
			  OR (e.recurrence_rule IS NOT NULL AND EXISTS (SELECT 1 FROM event_exceptions ex WHERE ex.event_id=e.id
				  AND ex.exception_type='modified' AND ex.start_time < $2 AND ex.end_time > $1)
			  )
		  )
	`
	rows, err := r.db.Query(ctx, q, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]ReminderWithEventRow, 0, 128)
	for rows.Next() {
		var row ReminderWithEventRow
		if err := rows.Scan(
			&row.ReminderID,
			&row.ReminderUserID,
			&row.EventID,
			&row.OwnerID,
			&row.MinutesBefore,

			&row.Title,
			&row.Description,
			&row.StartTime,
			&row.EndTime,
			&row.Timezone, &row.IsOnline,
			&row.MeetingProvider, &row.MeetingRoomID, &row.MeetingURL,
			&row.LiveKitRoomName, &row.MeetingStatus,
			&row.Location,
			&row.IsAllDay,
			&row.RecurrenceRule,
			&row.RecurrenceInterval,
			&row.RecurrenceUntil,
			&row.Color,
			&row.CreatedAt,
			&row.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *CalendarRepository) TryMarkReminderSent(ctx context.Context, reminderID uuid.UUID, occurrenceStart time.Time) (bool, error) {
	q := `
		UPDATE event_reminders
		SET last_sent_occurrence_start = $2,
		    last_sent_at = NOW()
		WHERE id = $1
		  AND (last_sent_occurrence_start IS NULL OR last_sent_occurrence_start <> $2)
	`
	tag, err := r.db.Exec(ctx, q, reminderID, occurrenceStart)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	return true, nil
}

func (r *CalendarRepository) DebugReminderRow(ctx context.Context, reminderID uuid.UUID) (string, error) {
	q := `
		SELECT
			id::text || ' event=' || event_id::text || ' user=' || user_id::text || ' minutes=' || minutes_before::text ||
			' last_occ=' || COALESCE(last_sent_occurrence_start::text, 'null') ||
			' last_at=' || COALESCE(last_sent_at::text, 'null')
		FROM event_reminders
		WHERE id = $1
	`
	var s string
	if err := r.db.QueryRow(ctx, q, reminderID).Scan(&s); err != nil {
		return "", fmt.Errorf("debug reminder row: %w", err)
	}
	return s, nil
}
