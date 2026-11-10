package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

type CalendarRepository struct {
	db     *pgxpool.Pool
	logger *zap.Logger
}

func NewCalendarRepository(db *pgxpool.Pool, logger *zap.Logger) *CalendarRepository {
	return &CalendarRepository{
		db:     db,
		logger: logger.With(zap.String("repository", "calendar")),
	}
}

func (r *CalendarRepository) Create(ctx context.Context, event *domain.Event, attendeeIDs []int64) error {
	return r.CreateWithReminder(ctx, event, attendeeIDs, nil)
}

func (r *CalendarRepository) CreateWithReminder(ctx context.Context, event *domain.Event, attendeeIDs []int64, reminderMinutes *int) error {
	var bookings []domain.RoomBooking
	if event.RoomID != nil {
		bookings = []domain.RoomBooking{{
			RoomID:    *event.RoomID,
			EventID:   &event.ID,
			UserID:    event.UserID,
			Title:     event.Title,
			StartTime: event.StartTime,
			EndTime:   event.EndTime,
		}}
	}
	return r.CreateEventWithRoomBookings(ctx, event, attendeeIDs, reminderMinutes, bookings)
}

func (r *CalendarRepository) CreateEventWithRoomBookings(
	ctx context.Context,
	event *domain.Event,
	attendeeIDs []int64,
	reminderMinutes *int,
	bookings []domain.RoomBooking,
	reminderMinutesSet ...bool,
) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if strings.TrimSpace(event.Timezone) == "" {
		event.Timezone = "Asia/Almaty"
	}

	query := `
      INSERT INTO events (
       id, user_id, calendar_id, title, description, start_time, end_time, timezone, location, room_id, is_all_day, is_online,
       recurrence_rule, recurrence_interval, recurrence_until, color, meeting_provider, meeting_room_id,
       meeting_url, livekit_room_name, cisco_meeting_id, cisco_join_url, online_meeting_status, created_at, updated_at,
       url
      ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26)
      RETURNING id
    `
	err = tx.QueryRow(ctx, query,
		event.ID, event.UserID, event.CalendarID, event.Title, event.Description, event.StartTime, event.EndTime,
		event.Timezone,
		event.Location, event.RoomID, event.IsAllDay, event.IsOnline,
		event.RecurrenceRule,
		event.RecurrenceInterval, event.RecurrenceUntil,
		event.Color, event.MeetingProvider, event.MeetingRoomID,
		event.MeetingURL, event.LiveKitRoomName, event.CiscoMeetingID, event.CiscoJoinURL, event.MeetingStatus,
		event.CreatedAt, event.UpdatedAt,
		event.URL,
	).Scan(&event.ID)
	if err != nil {
		return fmt.Errorf("insert event error: %w", err)
	}
	if err := r.replaceEventRoomBookingsTx(ctx, tx, event.ID, bookings); err != nil {
		return err
	}

	// Prepare batch insert rows
	rows := make([][]any, 0, len(attendeeIDs)+1)

	// Add owner
	rows = append(rows, []any{event.ID, event.UserID, "accepted", time.Now(), event.DefaultAttendanceMode()})

	seen := make(map[int64]bool)
	seen[event.UserID] = true

	for _, uid := range attendeeIDs {
		if !seen[uid] {
			rows = append(rows, []any{event.ID, uid, "pending", time.Now(), event.DefaultAttendanceMode()})
			seen[uid] = true
		}
	}

	_, err = tx.CopyFrom(
		ctx,
		pgx.Identifier{"event_attendees"},
		[]string{"event_id", "user_id", "status", "created_at", "attendance"},
		pgx.CopyFromRows(rows),
	)
	if err != nil {
		return fmt.Errorf("batch insert attendees: %w", err)
	}

	if reminderMinutes == nil && !(len(reminderMinutesSet) > 0 && reminderMinutesSet[0]) {
		defaultMinutes := 15
		reminderMinutes = &defaultMinutes
	}
	event.ReminderMinutes = reminderMinutes
	if reminderMinutes != nil {
		userIDs := make([]int64, 0, len(rows))
		for _, row := range rows {
			if len(row) < 2 {
				continue
			}
			userID, ok := row[1].(int64)
			if !ok {
				continue
			}
			userIDs = append(userIDs, userID)
		}
		if err := r.upsertRemindersTx(ctx, tx, event.ID, userIDs, *reminderMinutes); err != nil {
			return fmt.Errorf("bulk upsert reminders: %w", err)
		}
	}

	return tx.Commit(ctx)
}

func (r *CalendarRepository) Update(ctx context.Context, event *domain.Event, attendeeIDs []int64) error {
	var bookings []domain.RoomBooking
	if event.RoomID != nil {
		bookings = []domain.RoomBooking{{
			RoomID:    *event.RoomID,
			EventID:   &event.ID,
			UserID:    event.UserID,
			Title:     event.Title,
			StartTime: event.StartTime,
			EndTime:   event.EndTime,
		}}
	}
	return r.UpdateEventWithRoomBookings(ctx, event, attendeeIDs, bookings)
}

func (r *CalendarRepository) UpdateEventWithRoomBookings(
	ctx context.Context,
	event *domain.Event,
	attendeeIDs []int64,
	bookings []domain.RoomBooking,
	plans ...RoomBookingPlan,
) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var expected *time.Time
	if len(plans) > 0 {
		expected = plans[0].ExpectedUpdatedAt
	}
	if err := r.lockCalendarEventTx(ctx, tx, event.ID, event.UserID, expected); err != nil {
		return err
	}
	if len(plans) > 0 && plans[0].Build != nil {
		exceptions, err := listEventExceptions(ctx, tx, []uuid.UUID{event.ID})
		if err != nil {
			return err
		}
		bookings, err = plans[0].Build(exceptions)
		if err != nil {
			return err
		}
	}
	if err := r.replaceEventRoomBookingsTx(ctx, tx, event.ID, bookings); err != nil {
		return err
	}

	query := `
       UPDATE events
       SET title=$1, description=$2, start_time=$3, end_time=$4,
          location=$5, room_id=$6, is_all_day=$7, is_online=$8,
          recurrence_rule=$9, recurrence_interval=$10, recurrence_until=$11,
          color=$12, updated_at=$13, url=$14, calendar_id=$15,
          timezone=COALESCE(NULLIF($16, ''), timezone),
          meeting_provider=$18, meeting_room_id=$19, meeting_url=$20,
          livekit_room_name=$21, cisco_meeting_id=$22, cisco_join_url=$23, online_meeting_status=$24
       WHERE id=$17 AND deleted_at IS NULL
    `

	tag, err := tx.Exec(ctx, query,
		event.Title, event.Description, event.StartTime, event.EndTime,
		event.Location, event.RoomID, event.IsAllDay, event.IsOnline,
		event.RecurrenceRule, event.RecurrenceInterval, event.RecurrenceUntil,
		event.Color, event.UpdatedAt,
		event.URL,
		event.CalendarID,
		event.Timezone,
		event.ID,
		event.MeetingProvider, event.MeetingRoomID, event.MeetingURL,
		event.LiveKitRoomName, event.CiscoMeetingID, event.CiscoJoinURL, event.MeetingStatus,
	)
	if err != nil {
		return fmt.Errorf("update db error: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("событие не найдено")
	}

	if attendeeIDs != nil {
		keepIDs := make([]int64, 0, len(attendeeIDs)+1)
		keepIDs = append(keepIDs, event.UserID)
		seen := make(map[int64]bool)
		seen[event.UserID] = true
		for _, uid := range attendeeIDs {
			if !seen[uid] {
				keepIDs = append(keepIDs, uid)
				seen[uid] = true
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM event_attendees WHERE event_id=$1 AND NOT (user_id = ANY($2::bigint[]))`, event.ID, keepIDs); err != nil {
			return fmt.Errorf("failed to remove uninvited attendees: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO event_attendees(event_id,user_id,status,created_at)
			VALUES ($1,$2,'accepted',NOW()) ON CONFLICT (event_id,user_id) DO UPDATE SET status='accepted'`, event.ID, event.UserID); err != nil {
			return fmt.Errorf("failed to keep organizer accepted: %w", err)
		}
		if _, err := tx.Exec(ctx, `WITH added AS (INSERT INTO event_attendees(event_id,user_id,status,created_at,attendance)
			SELECT $1, invited.user_id, 'pending', NOW(), $4::text FROM unnest($2::bigint[]) AS invited(user_id)
			WHERE invited.user_id <> $3 ON CONFLICT (event_id,user_id) DO NOTHING
            RETURNING event_id, user_id)
            INSERT INTO event_reminders(id,event_id,user_id,minutes_before)
            SELECT gen_random_uuid(),event_id,user_id,15 FROM added
            ON CONFLICT (event_id,user_id) DO NOTHING`, event.ID, keepIDs, event.UserID, event.DefaultAttendanceMode()); err != nil {
			return fmt.Errorf("failed to add new attendees: %w", err)
		}
	}

	if len(plans) > 0 && plans[0].ReminderMinutesSet {
		minutes := plans[0].ReminderMinutes
		if minutes == nil {
			if _, err := tx.Exec(ctx, `DELETE FROM event_reminders WHERE event_id=$1 AND user_id=$2`, event.ID, event.UserID); err != nil {
				return fmt.Errorf("delete event reminder: %w", err)
			}
		} else if err := r.upsertRemindersTx(ctx, tx, event.ID, []int64{event.UserID}, max(0, *minutes)); err != nil {
			return fmt.Errorf("update event reminder: %w", err)
		}
	}

	return tx.Commit(ctx)
}

func (r *CalendarRepository) HasOverlap(ctx context.Context, userID int64, startTime, endTime time.Time, excludeEventID *uuid.UUID) (bool, error) {
	query := `
        SELECT EXISTS (
            SELECT 1
            FROM events e
            JOIN event_attendees ea ON e.id = ea.event_id
            WHERE ea.user_id = $1
              AND ea.status = 'accepted'
              AND e.deleted_at IS NULL
              AND e.start_time < $3
              AND e.end_time > $2
              AND ($4::uuid IS NULL OR e.id != $4)
        )
    `
	var exists bool
	err := r.db.QueryRow(ctx, query, userID, startTime, endTime, excludeEventID).Scan(&exists)
	return exists, err
}

func (r *CalendarRepository) HasBatchOverlap(
	ctx context.Context,
	userIDs []int64,
	startTime, endTime time.Time,
	excludeEventID *uuid.UUID,
) (map[int64]bool, error) {
	if len(userIDs) == 0 {
		return nil, nil
	}

	// Deduplicate
	unique := make([]int64, 0, len(userIDs))
	seen := make(map[int64]bool)
	for _, id := range userIDs {
		if !seen[id] {
			unique = append(unique, id)
			seen[id] = true
		}
	}

	query := `
        SELECT DISTINCT ea.user_id
        FROM events e
        JOIN event_attendees ea ON e.id = ea.event_id
        WHERE ea.user_id = ANY($1)
          AND ea.status = 'accepted'
          AND e.deleted_at IS NULL
          AND e.start_time < $3
          AND e.end_time > $2
          AND ($4::uuid IS NULL OR e.id != $4)
    `

	rows, err := r.db.Query(ctx, query, unique, startTime, endTime, excludeEventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	busyMap := make(map[int64]bool)
	for rows.Next() {
		var uid int64
		if err := rows.Scan(&uid); err != nil {
			return nil, err
		}
		busyMap[uid] = true
	}
	return busyMap, rows.Err()
}

func (r *CalendarRepository) Delete(ctx context.Context, id uuid.UUID) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `UPDATE events SET deleted_at = NOW() WHERE id = $1`, id); err != nil {
		return fmt.Errorf("soft delete event: %w", err)
	}

	if _, err := tx.Exec(ctx, `UPDATE room_bookings SET deleted_at = NOW(), updated_at = NOW(), status = 'cancelled' WHERE event_id = $1 AND deleted_at IS NULL`, id); err != nil {
		return fmt.Errorf("cancel room bookings: %w", err)
	}

	if _, err := tx.Exec(ctx, `DELETE FROM event_attendees WHERE event_id = $1`, id); err != nil {
		return fmt.Errorf("delete attendees: %w", err)
	}

	return tx.Commit(ctx)
}

func (r *CalendarRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.Event, error) {
	query := `
		SELECT id, user_id, calendar_id, title, description, start_time, end_time,
		       COALESCE(timezone, 'Asia/Almaty'),
		       location, room_id, is_all_day, is_online,
		       recurrence_rule, recurrence_interval, recurrence_until,
		       color, meeting_provider, meeting_room_id, meeting_url, livekit_room_name,
		       cisco_meeting_id, cisco_join_url, online_meeting_status, created_at, updated_at,
		       url
		FROM events
		WHERE id=$1 AND deleted_at IS NULL
	`

	var e domain.Event
	err := r.db.QueryRow(ctx, query, id).Scan(
		&e.ID, &e.UserID, &e.CalendarID, &e.Title, &e.Description, &e.StartTime, &e.EndTime,
		&e.Timezone,
		&e.Location, &e.RoomID, &e.IsAllDay, &e.IsOnline,
		&e.RecurrenceRule, &e.RecurrenceInterval, &e.RecurrenceUntil,
		&e.Color, &e.MeetingProvider, &e.MeetingRoomID, &e.MeetingURL, &e.LiveKitRoomName,
		&e.CiscoMeetingID, &e.CiscoJoinURL, &e.MeetingStatus, &e.CreatedAt, &e.UpdatedAt,
		&e.URL,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	if e.RoomID != nil {
		e.Room, _ = r.GetMeetingRoomByID(ctx, *e.RoomID)
	}

	e.Attendees, err = r.getAttendees(ctx, e.ID)
	if err != nil {
		return nil, err
	}

	if err := r.enrichMeetingEvent(ctx, &e); err != nil {
		return nil, err
	}
	return &e, nil
}

func (r *CalendarRepository) GetByLiveKitRoomName(ctx context.Context, roomName string) (*domain.Event, error) {
	query := `
		SELECT id, user_id, calendar_id, title, description, start_time, end_time,
		       COALESCE(timezone, 'Asia/Almaty'),
		       location, is_all_day, is_online,
		       recurrence_rule, recurrence_interval, recurrence_until,
		       color, meeting_provider, meeting_room_id, meeting_url, livekit_room_name,
		       cisco_meeting_id, cisco_join_url, online_meeting_status, created_at, updated_at,
		       url
		FROM events
		WHERE deleted_at IS NULL
		  AND (
			livekit_room_name = $1
			OR meeting_room_id = $1
			OR location = $1
		  )
		ORDER BY updated_at DESC
		LIMIT 1
	`

	var e domain.Event
	err := r.db.QueryRow(ctx, query, roomName).Scan(
		&e.ID, &e.UserID, &e.CalendarID, &e.Title, &e.Description, &e.StartTime, &e.EndTime,
		&e.Timezone,
		&e.Location, &e.IsAllDay, &e.IsOnline,
		&e.RecurrenceRule, &e.RecurrenceInterval, &e.RecurrenceUntil,
		&e.Color, &e.MeetingProvider, &e.MeetingRoomID, &e.MeetingURL, &e.LiveKitRoomName,
		&e.CiscoMeetingID, &e.CiscoJoinURL, &e.MeetingStatus, &e.CreatedAt, &e.UpdatedAt,
		&e.URL,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	e.Attendees, err = r.getAttendees(ctx, e.ID)
	if err != nil {
		return nil, err
	}

	if err := r.enrichMeetingEvent(ctx, &e); err != nil {
		return nil, err
	}
	return &e, nil
}

func (r *CalendarRepository) List(ctx context.Context, targetUserID, viewerID int64, from, to time.Time) ([]domain.Event, error) {
	query := `
		SELECT
			e.id, e.user_id, e.calendar_id, e.title, e.description, e.start_time, e.end_time,
			COALESCE(e.timezone, 'Asia/Almaty'),
			e.location, e.room_id, e.is_all_day, e.is_online,
			e.recurrence_rule, e.recurrence_interval, e.recurrence_until,
			e.color, e.meeting_provider, e.meeting_room_id, e.meeting_url, e.livekit_room_name,
			e.cisco_meeting_id, e.cisco_join_url, e.online_meeting_status, e.created_at, e.updated_at,
			er.minutes_before AS reminder_minutes,
			ea.status,
			ea_viewer.status,
			e.url
		FROM events e
		JOIN event_attendees ea ON ea.event_id = e.id
		LEFT JOIN event_reminders er ON er.event_id = e.id AND er.user_id = $2
		LEFT JOIN event_attendees ea_viewer ON ea_viewer.event_id = e.id AND ea_viewer.user_id = $2
		WHERE e.deleted_at IS NULL
		  AND ea.user_id = $1
		  AND ea.status != 'declined'
		  AND (
			 (e.recurrence_rule IS NULL AND e.end_time >= $3 AND e.start_time <= $4)
			 OR
			 (e.recurrence_rule IS NOT NULL
			    AND e.start_time <= $4
			    AND (e.recurrence_until IS NULL OR e.recurrence_until >= $3)
			 )
			 OR (e.recurrence_rule IS NOT NULL AND EXISTS (SELECT 1 FROM event_exceptions ex WHERE ex.event_id=e.id
			    AND ex.exception_type='modified' AND ex.start_time < $4 AND ex.end_time > $3)
			 )
		  )
		ORDER BY start_time ASC
	`

	rows, err := r.db.Query(ctx, query, targetUserID, viewerID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := make([]domain.Event, 0, 50)
	// We no longer build a massive list of IDs to fetch all attendees

	for rows.Next() {
		var e domain.Event
		var rm *int
		var targetStatus string
		var viewerStatus *string

		if err := rows.Scan(
			&e.ID, &e.UserID, &e.CalendarID, &e.Title, &e.Description, &e.StartTime, &e.EndTime,
			&e.Timezone,
			&e.Location, &e.RoomID, &e.IsAllDay, &e.IsOnline,
			&e.RecurrenceRule, &e.RecurrenceInterval, &e.RecurrenceUntil,
			&e.Color, &e.MeetingProvider, &e.MeetingRoomID, &e.MeetingURL, &e.LiveKitRoomName,
			&e.CiscoMeetingID, &e.CiscoJoinURL, &e.MeetingStatus, &e.CreatedAt, &e.UpdatedAt,
			&rm,
			&targetStatus,
			&viewerStatus,
			&e.URL,
		); err != nil {
			return nil, err
		}

		e.ReminderMinutes = rm

		// If viewer is an attendee (or owner), we populate Attendees with just their record
		// This suffices for 'sanitizeEventForViewer' to know they have access.
		if viewerStatus != nil {
			e.Attendees = []domain.EventAttendee{
				{
					EventID: e.ID,
					UserID:  viewerID,
					Status:  *viewerStatus,
				},
			}
		}

		events = append(events, e)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	var roomIDs []uuid.UUID
	for _, ev := range events {
		if ev.RoomID != nil {
			roomIDs = append(roomIDs, *ev.RoomID)
		}
	}
	if len(roomIDs) > 0 {
		roomMap, err := r.GetMeetingRoomsByIDs(ctx, roomIDs)
		if err == nil {
			for i := range events {
				if events[i].RoomID != nil {
					if rm, ok := roomMap[*events[i].RoomID]; ok {
						events[i].Room = &rm
					}
				}
			}
		}
	}

	rows.Close()
	if err := r.LoadEventMeetingDetails(ctx, events); err != nil {
		return nil, err
	}
	return events, nil
}

func (r *CalendarRepository) ListAll(ctx context.Context, from, to time.Time) ([]domain.Event, error) {
	query := `
		SELECT
			e.id, e.user_id, e.calendar_id, e.title, e.description, e.start_time, e.end_time,
			COALESCE(e.timezone, 'Asia/Almaty'),
			e.location, e.is_all_day, e.is_online,
			e.recurrence_rule, e.recurrence_interval, e.recurrence_until,
			e.color, e.meeting_provider, e.meeting_room_id, e.meeting_url, e.livekit_room_name,
			e.cisco_meeting_id, e.cisco_join_url, e.online_meeting_status, e.created_at, e.updated_at,
			e.url
		FROM events e
		WHERE e.deleted_at IS NULL
		  AND (
			  (e.recurrence_rule IS NULL AND e.end_time >= $1 AND e.start_time <= $2)
			  OR
			  (e.recurrence_rule IS NOT NULL
				  AND e.start_time <= $2
				  AND (e.recurrence_until IS NULL OR e.recurrence_until >= $1)
			  )
		  )
		ORDER BY e.start_time ASC
	`

	rows, err := r.db.Query(ctx, query, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := make([]domain.Event, 0, 64)
	ids := make([]uuid.UUID, 0, 64)

	for rows.Next() {
		var e domain.Event
		if err := rows.Scan(
			&e.ID, &e.UserID, &e.CalendarID, &e.Title, &e.Description, &e.StartTime, &e.EndTime,
			&e.Timezone,
			&e.Location, &e.IsAllDay, &e.IsOnline,
			&e.RecurrenceRule, &e.RecurrenceInterval, &e.RecurrenceUntil,
			&e.Color, &e.MeetingProvider, &e.MeetingRoomID, &e.MeetingURL, &e.LiveKitRoomName,
			&e.CiscoMeetingID, &e.CiscoJoinURL, &e.MeetingStatus, &e.CreatedAt, &e.UpdatedAt,
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

	rows.Close()
	if err := r.LoadEventMeetingDetails(ctx, events); err != nil {
		return nil, err
	}
	return events, nil
}

func (r *CalendarRepository) getAttendees(ctx context.Context, eventID uuid.UUID) ([]domain.EventAttendee, error) {
	query := `
		SELECT event_id, user_id, status, comment, created_at, updated_at, attendance
		FROM event_attendees
		WHERE event_id = $1
	`

	rows, err := r.db.Query(ctx, query, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	attendees := make([]domain.EventAttendee, 0, 8)
	for rows.Next() {
		var a domain.EventAttendee
		if err := rows.Scan(&a.EventID, &a.UserID, &a.Status, &a.Comment, &a.CreatedAt, &a.UpdatedAt, &a.Attendance); err != nil {
			return nil, err
		}
		attendees = append(attendees, a)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return attendees, nil
}

func (r *CalendarRepository) getAttendeesByEventIDs(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID][]domain.EventAttendee, error) {
	query := `
		SELECT event_id, user_id, status, comment, created_at, updated_at, attendance
		FROM event_attendees
		WHERE event_id = ANY($1)
	`

	rows, err := r.db.Query(ctx, query, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[uuid.UUID][]domain.EventAttendee, len(ids))
	for rows.Next() {
		var a domain.EventAttendee
		if err := rows.Scan(&a.EventID, &a.UserID, &a.Status, &a.Comment, &a.CreatedAt, &a.UpdatedAt, &a.Attendance); err != nil {
			return nil, err
		}
		out[a.EventID] = append(out[a.EventID], a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for _, id := range ids {
		if _, ok := out[id]; !ok {
			out[id] = nil
		}
	}
	return out, nil
}

func (r *CalendarRepository) GetAttendeesWithUserInfo(ctx context.Context, eventID uuid.UUID) ([]domain.EventAttendee, error) {
	query := `
		SELECT
			ea.event_id, ea.user_id, ea.status, ea.comment, ea.created_at, ea.updated_at, ea.attendance,
			u.id, u.username, u.email, u.first_name, u.last_name, u.avatar_url, u.position, u.is_active
		FROM event_attendees ea
		INNER JOIN users u ON u.id = ea.user_id
		WHERE ea.event_id = $1
		ORDER BY ea.created_at ASC
	`

	rows, err := r.db.Query(ctx, query, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	attendees := make([]domain.EventAttendee, 0, 8)
	for rows.Next() {
		var a domain.EventAttendee
		var u domain.UserResponse
		if err := rows.Scan(
			&a.EventID, &a.UserID, &a.Status, &a.Comment, &a.CreatedAt, &a.UpdatedAt, &a.Attendance,
			&u.ID, &u.Username, &u.Email, &u.FirstName, &u.LastName, &u.AvatarURL, &u.Position, &u.IsActive,
		); err != nil {
			return nil, err
		}
		u.FullName = strings.TrimSpace(u.FirstName + " " + u.LastName)
		a.User = &u
		attendees = append(attendees, a)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return attendees, nil
}

func (r *CalendarRepository) IsAttendee(ctx context.Context, eventID uuid.UUID, userID int64) (bool, error) {
	query := `SELECT EXISTS (SELECT 1 FROM event_attendees WHERE event_id=$1 AND user_id=$2)`
	var exists bool
	if err := r.db.QueryRow(ctx, query, eventID, userID).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}

func (r *CalendarRepository) UpdateAttendeeStatus(ctx context.Context, eventID uuid.UUID, userID int64, status string, comment *string) (string, bool, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var previousStatus string
	var previousComment *string
	if err := tx.QueryRow(ctx, `SELECT status, comment FROM event_attendees WHERE event_id=$1 AND user_id=$2 FOR UPDATE`, eventID, userID).Scan(&previousStatus, &previousComment); err != nil {
		return "", false, err
	}
	if comment == nil {
		// Omitted comment changes only the RSVP status; it does not erase a note.
		comment = previousComment
	}
	commentUnchanged := (previousComment == nil && comment == nil) || (previousComment != nil && comment != nil && *previousComment == *comment)
	changed := previousStatus != status || !commentUnchanged
	if changed {
		if _, err := tx.Exec(ctx, `UPDATE event_attendees SET status=$3, comment=$4, updated_at=NOW()
			WHERE event_id=$1 AND user_id=$2`, eventID, userID, status, comment); err != nil {
			return "", false, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return "", false, err
	}
	return previousStatus, changed, nil
}

func (r *CalendarRepository) GetByIDForUser(ctx context.Context, id uuid.UUID, userID int64) (*domain.Event, error) {
	query := `
		SELECT
			e.id, e.user_id, e.calendar_id, e.title, e.description, e.start_time, e.end_time,
			COALESCE(e.timezone, 'Asia/Almaty'),
			e.location, e.room_id, e.is_all_day, e.is_online,
			e.recurrence_rule, e.recurrence_interval, e.recurrence_until,
			e.color, e.meeting_provider, e.meeting_room_id, e.meeting_url, e.livekit_room_name,
			e.cisco_meeting_id, e.cisco_join_url, e.online_meeting_status, e.created_at, e.updated_at,
			er.minutes_before AS reminder_minutes,
			e.url
		FROM events e
		LEFT JOIN event_reminders er
			ON er.event_id = e.id AND er.user_id = $2
		WHERE e.id = $1 AND e.deleted_at IS NULL
	`

	var e domain.Event
	var rm *int

	err := r.db.QueryRow(ctx, query, id, userID).Scan(
		&e.ID, &e.UserID, &e.CalendarID, &e.Title, &e.Description, &e.StartTime, &e.EndTime,
		&e.Timezone,
		&e.Location, &e.RoomID, &e.IsAllDay, &e.IsOnline,
		&e.RecurrenceRule, &e.RecurrenceInterval, &e.RecurrenceUntil,
		&e.Color, &e.MeetingProvider, &e.MeetingRoomID, &e.MeetingURL, &e.LiveKitRoomName,
		&e.CiscoMeetingID, &e.CiscoJoinURL, &e.MeetingStatus, &e.CreatedAt, &e.UpdatedAt,
		&rm,
		&e.URL,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	if e.RoomID != nil {
		e.Room, _ = r.GetMeetingRoomByID(ctx, *e.RoomID)
	}

	e.ReminderMinutes = rm

	att, err := r.getAttendees(ctx, e.ID)
	if err != nil {
		return nil, err
	}
	e.Attendees = att

	if err := r.enrichMeetingEvent(ctx, &e); err != nil {
		return nil, err
	}
	return &e, nil
}

func (r *CalendarRepository) SearchByTitleAndTime(
	ctx context.Context,
	userID int64,
	title string,
	from, to time.Time,
	limit int,
) ([]domain.Event, error) {

	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	query := `
		SELECT
			e.id, e.user_id, e.title, e.description, e.start_time, e.end_time,
			COALESCE(e.timezone, 'Asia/Almaty'),
			e.location, e.is_all_day, e.is_online,
			e.recurrence_rule, e.recurrence_interval, e.recurrence_until,
			e.color, e.meeting_provider, e.meeting_room_id, e.meeting_url, e.livekit_room_name,
			e.cisco_meeting_id, e.cisco_join_url, e.online_meeting_status, e.created_at, e.updated_at,
			er.minutes_before AS reminder_minutes,
			e.url
		FROM events e
		LEFT JOIN event_reminders er ON er.event_id = e.id AND er.user_id = $1
		WHERE e.deleted_at IS NULL
		  AND e.user_id = $1
		  AND (
			e.title ILIKE '%' || $4 || '%'
			OR COALESCE(e.description, '') ILIKE '%' || $4 || '%'
			OR COALESCE(e.location, '') ILIKE '%' || $4 || '%'
		  )
		  AND e.start_time < $3
		  AND e.end_time > $2

		UNION

		SELECT
			e.id, e.user_id, e.title, e.description, e.start_time, e.end_time,
			COALESCE(e.timezone, 'Asia/Almaty'),
			e.location, e.is_all_day, e.is_online,
			e.recurrence_rule, e.recurrence_interval, e.recurrence_until,
			e.color, e.meeting_provider, e.meeting_room_id, e.meeting_url, e.livekit_room_name,
			e.cisco_meeting_id, e.cisco_join_url, e.online_meeting_status, e.created_at, e.updated_at,
			er.minutes_before AS reminder_minutes,
			e.url
		FROM events e
		JOIN event_attendees ea ON ea.event_id = e.id
		LEFT JOIN event_reminders er ON er.event_id = e.id AND er.user_id = $1
		WHERE e.deleted_at IS NULL
		  AND ea.user_id = $1
		  AND (
			e.title ILIKE '%' || $4 || '%'
			OR COALESCE(e.description, '') ILIKE '%' || $4 || '%'
			OR COALESCE(e.location, '') ILIKE '%' || $4 || '%'
		  )
		  AND e.start_time < $3
		  AND e.end_time > $2
		
		ORDER BY start_time ASC
		LIMIT $5
	`

	rows, err := r.db.Query(ctx, query, userID, from, to, title, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := make([]domain.Event, 0, 32)
	ids := make([]uuid.UUID, 0, 32)

	for rows.Next() {
		var e domain.Event
		var rm *int

		if err := rows.Scan(
			&e.ID, &e.UserID, &e.Title, &e.Description, &e.StartTime, &e.EndTime,
			&e.Timezone,
			&e.Location, &e.IsAllDay, &e.IsOnline,
			&e.RecurrenceRule, &e.RecurrenceInterval, &e.RecurrenceUntil,
			&e.Color, &e.MeetingProvider, &e.MeetingRoomID, &e.MeetingURL, &e.LiveKitRoomName,
			&e.CiscoMeetingID, &e.CiscoJoinURL, &e.MeetingStatus, &e.CreatedAt, &e.UpdatedAt,
			&rm,
			&e.URL,
		); err != nil {
			return nil, err
		}

		e.ReminderMinutes = rm
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

func (r *CalendarRepository) DeleteForUser(ctx context.Context, eventID uuid.UUID, userID int64) (ownerDeleted bool, err error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var ownerID int64
	err = tx.QueryRow(ctx, `
		SELECT user_id
		FROM events
		WHERE id = $1 AND deleted_at IS NULL
	`, eventID).Scan(&ownerID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return false, fmt.Errorf("event not found")
		}
		return false, fmt.Errorf("get owner: %w", err)
	}

	if ownerID == userID {
		tag, err := tx.Exec(ctx, `UPDATE events SET deleted_at = NOW(), updated_at = NOW() WHERE id = $1 AND deleted_at IS NULL`, eventID)
		if err != nil {
			return false, fmt.Errorf("soft delete event: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return false, fmt.Errorf("event not found")
		}

		if _, err := tx.Exec(ctx, `UPDATE room_bookings SET deleted_at = NOW(), updated_at = NOW(), status = 'cancelled' WHERE event_id = $1 AND deleted_at IS NULL`, eventID); err != nil {
			return false, fmt.Errorf("cancel room bookings: %w", err)
		}

		if _, err := tx.Exec(ctx, `DELETE FROM event_attendees WHERE event_id = $1`, eventID); err != nil {
			return false, fmt.Errorf("delete attendees: %w", err)
		}

		if err := tx.Commit(ctx); err != nil {
			return false, fmt.Errorf("commit: %w", err)
		}
		return true, nil
	}

	tag, err := tx.Exec(ctx, `
		DELETE FROM event_attendees
		WHERE event_id = $1 AND user_id = $2
	`, eventID, userID)
	if err != nil {
		return false, fmt.Errorf("delete attendee row: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return false, fmt.Errorf("permission denied")
	}

	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit: %w", err)
	}
	return false, nil
}

func (r *CalendarRepository) RemoveAttendee(ctx context.Context, eventID uuid.UUID, userID int64) error {
	query := `
		UPDATE event_attendees
		SET status='declined', updated_at=NOW()
		WHERE event_id=$1 AND user_id=$2
	`
	tag, err := r.db.Exec(ctx, query, eventID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("attendee not found")
	}
	return nil
}

func (r *CalendarRepository) MarkInviteNotificationsAsRead(ctx context.Context, userID int64, eventID string) error {
	query := `
        UPDATE notifications
        SET is_read = true
        WHERE user_id = $1
          AND is_read = false
          AND metadata->>'event_id' = $2
    `
	_, err := r.db.Exec(ctx, query, userID, eventID)
	return err
}

// GetCalendarSettings retrieves calendar visibility settings for a user
func (r *CalendarRepository) GetCalendarSettings(ctx context.Context, userID int64) ([]domain.UserCalendarSetting, error) {
	query := `
		SELECT user_id, calendar_id, is_visible, display_order, created_at, updated_at
		FROM user_calendar_settings
		WHERE user_id = $1
		ORDER BY display_order, created_at
	`

	rows, err := r.db.Query(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("query calendar settings: %w", err)
	}
	defer rows.Close()

	var settings []domain.UserCalendarSetting
	for rows.Next() {
		var s domain.UserCalendarSetting
		if err := rows.Scan(&s.UserID, &s.CalendarID, &s.IsVisible, &s.DisplayOrder, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan setting: %w", err)
		}
		settings = append(settings, s)
	}

	return settings, rows.Err()
}

// UpdateCalendarVisibility updates visibility settings for a calendar
func (r *CalendarRepository) UpdateCalendarVisibility(ctx context.Context, userID int64, calendarID uuid.UUID, isVisible *bool, displayOrder *int) error {
	query := `
		INSERT INTO user_calendar_settings (user_id, calendar_id, is_visible, display_order, created_at, updated_at)
		VALUES ($1, $2, COALESCE($3, TRUE), COALESCE($4, 0), NOW(), NOW())
		ON CONFLICT (user_id, calendar_id) DO UPDATE SET
			is_visible = COALESCE($3, user_calendar_settings.is_visible),
			display_order = COALESCE($4, user_calendar_settings.display_order),
			updated_at = NOW()
	`

	_, err := r.db.Exec(ctx, query, userID, calendarID, isVisible, displayOrder)
	return err
}

// GetCalendarsWithSettings retrieves user calendars with their visibility settings
func (r *CalendarRepository) GetCalendarsWithSettings(ctx context.Context, userID int64) ([]domain.UserCalendarWithSettings, error) {
	query := `
		SELECT
			c.id, c.user_id, c.name, c.color, c.is_default, c.created_at, c.updated_at,
			COALESCE(s.is_visible, TRUE) AS is_visible,
			COALESCE(s.display_order, 0) AS display_order
		FROM user_calendars c
		LEFT JOIN user_calendar_settings s ON s.calendar_id = c.id AND s.user_id = $1
		WHERE c.user_id = $1
		ORDER BY display_order, c.created_at
	`

	rows, err := r.db.Query(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("query calendars with settings: %w", err)
	}
	defer rows.Close()

	var calendars []domain.UserCalendarWithSettings
	for rows.Next() {
		var c domain.UserCalendarWithSettings
		if err := rows.Scan(
			&c.ID, &c.UserID, &c.Name, &c.Color, &c.IsDefault, &c.CreatedAt, &c.UpdatedAt,
			&c.IsVisible, &c.DisplayOrder,
		); err != nil {
			return nil, fmt.Errorf("scan calendar: %w", err)
		}
		calendars = append(calendars, c)
	}

	return calendars, rows.Err()
}

// GetEventsForGrid retrieves events for calendar grid view
func (r *CalendarRepository) GetEventsForGrid(ctx context.Context, userID int64, startDate, endDate time.Time, calendarIDs []uuid.UUID) ([]domain.Event, error) {
	query := `
		SELECT DISTINCT
			e.id, e.user_id, e.calendar_id, e.title, e.description, e.start_time, e.end_time,
			COALESCE(e.timezone, 'Asia/Almaty'),
			e.location, e.is_all_day, e.is_online, e.url,
			e.recurrence_rule, e.recurrence_interval, e.recurrence_until,
			e.color, e.meeting_provider, e.meeting_room_id, e.meeting_url, e.livekit_room_name,
			e.cisco_meeting_id, e.cisco_join_url, e.online_meeting_status,
			e.created_at, e.updated_at,
			er.minutes_before AS reminder_minutes
		FROM events e
		INNER JOIN event_attendees ea ON ea.event_id = e.id
		LEFT JOIN event_reminders er ON er.event_id = e.id AND er.user_id = $1
		WHERE e.deleted_at IS NULL
			AND ea.user_id = $1
			AND ea.status != 'declined'
			AND (
				(e.user_id = $1 AND e.calendar_id = ANY($4))
				OR e.user_id <> $1
			)
			AND (
				(e.recurrence_rule IS NULL AND e.start_time < $3 AND e.end_time > $2)
				OR (e.recurrence_rule IS NOT NULL
					AND e.start_time < $3
					AND (e.recurrence_until IS NULL OR e.recurrence_until >= $2))
				OR (e.recurrence_rule IS NOT NULL AND EXISTS (SELECT 1 FROM event_exceptions ex WHERE ex.event_id=e.id
					AND ex.exception_type='modified' AND ex.start_time < $3 AND ex.end_time > $2)
				)
			)
	`

	query += " ORDER BY e.start_time ASC"

	rows, err := r.db.Query(ctx, query, userID, startDate, endDate, calendarIDs)
	if err != nil {
		return nil, fmt.Errorf("query events for grid: %w", err)
	}
	defer rows.Close()

	var events []domain.Event
	for rows.Next() {
		var e domain.Event
		var rm *int

		if err := rows.Scan(
			&e.ID, &e.UserID, &e.CalendarID, &e.Title, &e.Description, &e.StartTime, &e.EndTime,
			&e.Timezone,
			&e.Location, &e.IsAllDay, &e.IsOnline, &e.URL,
			&e.RecurrenceRule, &e.RecurrenceInterval, &e.RecurrenceUntil,
			&e.Color, &e.MeetingProvider, &e.MeetingRoomID, &e.MeetingURL, &e.LiveKitRoomName,
			&e.CiscoMeetingID, &e.CiscoJoinURL, &e.MeetingStatus,
			&e.CreatedAt, &e.UpdatedAt,
			&rm,
		); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}

		e.ReminderMinutes = rm
		events = append(events, e)
	}

	return events, rows.Err()
}

// GetGroupEventsForGrid retrieves group events for calendar grid view
func (r *CalendarRepository) GetGroupEventsForGrid(ctx context.Context, userID int64, startDate, endDate time.Time, groupIDs []uuid.UUID) ([]domain.GroupEventInfo, error) {
	query := `
		SELECT DISTINCT
			e.id, e.user_id, e.calendar_id, e.title, e.description, e.start_time, e.end_time,
			COALESCE(e.timezone, 'Asia/Almaty'),
			e.location, e.is_all_day, e.is_online, e.url,
			e.recurrence_rule, e.recurrence_interval, e.recurrence_until,
			e.color, e.meeting_provider, e.meeting_room_id, e.meeting_url, e.livekit_room_name,
			e.cisco_meeting_id, e.cisco_join_url, e.online_meeting_status,
			e.created_at, e.updated_at,
			gce.group_id, g.name AS group_name, gce.created_by
		FROM events e
		INNER JOIN group_calendar_events gce ON gce.event_id = e.id
		INNER JOIN groups g ON g.id = gce.group_id
		INNER JOIN group_members gm ON gm.group_id = g.id
		WHERE e.deleted_at IS NULL
			AND gm.user_id = $1
			AND gm.left_at IS NULL
			AND g.is_active = TRUE
			AND g.deleted_at IS NULL
			AND gce.group_id = ANY($4)
			AND (
				(e.recurrence_rule IS NULL AND e.start_time < $3 AND e.end_time > $2)
				OR (e.recurrence_rule IS NOT NULL
					AND e.start_time < $3
					AND (e.recurrence_until IS NULL OR e.recurrence_until >= $2))
				OR (e.recurrence_rule IS NOT NULL AND EXISTS (SELECT 1 FROM event_exceptions ex WHERE ex.event_id=e.id
					AND ex.exception_type='modified' AND ex.start_time < $3 AND ex.end_time > $2)
				)
			)
		ORDER BY e.start_time ASC
	`

	rows, err := r.db.Query(ctx, query, userID, startDate, endDate, groupIDs)
	if err != nil {
		return nil, fmt.Errorf("query group events: %w", err)
	}
	defer rows.Close()

	var events []domain.GroupEventInfo
	for rows.Next() {
		var ge domain.GroupEventInfo

		if err := rows.Scan(
			&ge.ID, &ge.UserID, &ge.CalendarID, &ge.Title, &ge.Description, &ge.StartTime, &ge.EndTime,
			&ge.Timezone,
			&ge.Location, &ge.IsAllDay, &ge.IsOnline, &ge.URL,
			&ge.RecurrenceRule, &ge.RecurrenceInterval, &ge.RecurrenceUntil,
			&ge.Color, &ge.MeetingProvider, &ge.MeetingRoomID, &ge.MeetingURL, &ge.LiveKitRoomName,
			&ge.CiscoMeetingID, &ge.CiscoJoinURL, &ge.MeetingStatus,
			&ge.CreatedAt, &ge.UpdatedAt,
			&ge.GroupID, &ge.GroupName, &ge.CreatedBy,
		); err != nil {
			return nil, fmt.Errorf("scan group event: %w", err)
		}

		events = append(events, ge)
	}

	return events, rows.Err()
}

// GetGroupCalendarsWithSettings returns active group layers visible to a member.
func (r *CalendarRepository) GetGroupCalendarsWithSettings(ctx context.Context, userID int64) ([]domain.GroupCalendarWithSettings, error) {
	rows, err := r.db.Query(ctx, `
		SELECT g.id, g.name,
		       COALESCE(s.is_visible, TRUE), COALESCE(s.display_order, 0)
		FROM groups g
		JOIN group_members gm ON gm.group_id = g.id
		LEFT JOIN user_group_calendar_settings s
		  ON s.group_id = g.id AND s.user_id = $1
		WHERE gm.user_id = $1 AND gm.left_at IS NULL
		  AND g.is_active = TRUE AND g.deleted_at IS NULL
		ORDER BY COALESCE(s.display_order, 0), g.created_at`, userID)
	if err != nil {
		return nil, fmt.Errorf("query group calendar settings: %w", err)
	}
	defer rows.Close()

	result := make([]domain.GroupCalendarWithSettings, 0)
	for rows.Next() {
		var layer domain.GroupCalendarWithSettings
		if err := rows.Scan(&layer.GroupID, &layer.GroupName, &layer.IsVisible, &layer.DisplayOrder); err != nil {
			return nil, fmt.Errorf("scan group calendar setting: %w", err)
		}
		result = append(result, layer)
	}
	return result, rows.Err()
}

// UpdateGroupCalendarVisibility updates a layer only while the user is an active member.
func (r *CalendarRepository) UpdateGroupCalendarVisibility(ctx context.Context, userID int64, groupID uuid.UUID, isVisible *bool, displayOrder *int) (bool, error) {
	result, err := r.db.Exec(ctx, `
		INSERT INTO user_group_calendar_settings
			(user_id, group_id, is_visible, display_order, created_at, updated_at)
		SELECT $1, $2, COALESCE($3, TRUE), COALESCE($4, 0), NOW(), NOW()
		WHERE EXISTS (
			SELECT 1 FROM group_members gm
			JOIN groups g ON g.id = gm.group_id
			WHERE gm.group_id = $2 AND gm.user_id = $1 AND gm.left_at IS NULL
			  AND g.is_active = TRUE AND g.deleted_at IS NULL
		)
		ON CONFLICT (user_id, group_id) DO UPDATE SET
			is_visible = COALESCE($3, user_group_calendar_settings.is_visible),
			display_order = COALESCE($4, user_group_calendar_settings.display_order),
			updated_at = NOW()`, userID, groupID, isVisible, displayOrder)
	if err != nil {
		return false, fmt.Errorf("update group calendar visibility: %w", err)
	}
	return result.RowsAffected() > 0, nil
}

// UpdateEventMeetingInfo updates online meeting details for an event
func (r *CalendarRepository) UpdateEventMeetingInfo(
	ctx context.Context,
	eventID uuid.UUID,
	userID int64,
	provider string,
	roomID, meetingURL, livekitRoom, status string,
	isOnline bool,
) error {
	query := `
		UPDATE events
		SET meeting_provider = $1,
		    meeting_room_id = $2,
		    meeting_url = $3,
		    livekit_room_name = $4,
		    online_meeting_status = $5,
		    is_online = $6,
		    updated_at = NOW()
		WHERE id = $7 AND user_id = $8 AND deleted_at IS NULL
	`
	tag, err := r.db.Exec(ctx, query, provider, roomID, meetingURL, livekitRoom, status, isOnline, eventID, userID)
	if err != nil {
		return fmt.Errorf("update event meeting info: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("event not found")
	}
	return nil
}

// GetReminderByEventAndUser returns the reminder for a specific event and user
func (r *CalendarRepository) GetReminderByEventAndUser(ctx context.Context, eventID uuid.UUID, userID int64) (*domain.EventReminder, error) {
	query := `
		SELECT id, event_id, user_id, minutes_before, created_at, last_sent_occurrence_start, last_sent_at
		FROM event_reminders
		WHERE event_id = $1 AND user_id = $2
	`
	var rem domain.EventReminder
	err := r.db.QueryRow(ctx, query, eventID, userID).Scan(
		&rem.ID,
		&rem.EventID,
		&rem.UserID,
		&rem.MinutesBefore,
		&rem.CreatedAt,
		&rem.LastSentOccurrenceStart,
		&rem.LastSentAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &rem, nil
}
