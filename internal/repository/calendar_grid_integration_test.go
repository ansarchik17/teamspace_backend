package repository

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

func TestCalendarGridIntegrationVisibilityInvitationsAndGroupAccess(t *testing.T) {
	dsn := os.Getenv("TEST_MESSENGER_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_MESSENGER_DATABASE_URL to a disposable PostgreSQL database")
	}

	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	defer admin.Close()

	schema := "cal001_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	defer func() { _, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE") }()

	cfg, err := pgxpool.ParseConfig(dsn)
	require.NoError(t, err)
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	defer pool.Close()

	_, err = pool.Exec(ctx, `
		CREATE TABLE users (id BIGINT PRIMARY KEY);
		CREATE TABLE user_calendars (
			id UUID PRIMARY KEY, user_id BIGINT NOT NULL REFERENCES users(id), name TEXT NOT NULL,
			color TEXT NOT NULL, is_default BOOLEAN NOT NULL DEFAULT FALSE,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
		CREATE TABLE groups (
			id UUID PRIMARY KEY, name TEXT NOT NULL, is_active BOOLEAN NOT NULL DEFAULT TRUE,
			deleted_at TIMESTAMPTZ, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
		CREATE TABLE group_members (
			group_id UUID NOT NULL REFERENCES groups(id), user_id BIGINT NOT NULL REFERENCES users(id),
			joined_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), left_at TIMESTAMPTZ,
			PRIMARY KEY (group_id, user_id)
		);
		CREATE TABLE events (
			id UUID PRIMARY KEY, user_id BIGINT NOT NULL REFERENCES users(id),
			calendar_id UUID NOT NULL REFERENCES user_calendars(id), title TEXT NOT NULL,
			description TEXT, start_time TIMESTAMPTZ NOT NULL, end_time TIMESTAMPTZ NOT NULL,
			location TEXT, is_all_day BOOLEAN NOT NULL DEFAULT FALSE, is_online BOOLEAN NOT NULL DEFAULT FALSE,
			room_snapshot JSONB, url TEXT, recurrence_rule TEXT, recurrence_interval INT NOT NULL DEFAULT 1, recurrence_until TIMESTAMPTZ,
			color TEXT, meeting_provider TEXT NOT NULL DEFAULT 'none', meeting_room_id TEXT, meeting_url TEXT,
			livekit_room_name TEXT, cisco_meeting_id TEXT, cisco_join_url TEXT,
			online_meeting_status TEXT NOT NULL DEFAULT 'none', created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), deleted_at TIMESTAMPTZ
		);
		CREATE TABLE event_attendees (
			event_id UUID NOT NULL REFERENCES events(id), user_id BIGINT NOT NULL REFERENCES users(id),
			status TEXT NOT NULL, comment TEXT, attendance TEXT,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), updated_at TIMESTAMPTZ,
			PRIMARY KEY (event_id, user_id)
		);
		CREATE TABLE event_reminders (
			id UUID NOT NULL DEFAULT gen_random_uuid(),
			event_id UUID NOT NULL REFERENCES events(id), user_id BIGINT NOT NULL REFERENCES users(id),
			minutes_before INT NOT NULL, PRIMARY KEY (event_id, user_id)
		);
		CREATE TABLE group_calendar_events (
			group_id UUID NOT NULL REFERENCES groups(id), event_id UUID NOT NULL REFERENCES events(id),
			created_by BIGINT NOT NULL REFERENCES users(id), PRIMARY KEY (group_id, event_id)
		);
	`)
	require.NoError(t, err)

	migration, err := os.ReadFile("../../db/migrations/20260929000100_create_calendar_visibility_settings.sql")
	require.NoError(t, err)
	_, err = pool.Exec(ctx, strings.Split(string(migration), "-- +goose Down")[0])
	require.NoError(t, err)
	_, err = admin.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS btree_gist WITH SCHEMA public")
	require.NoError(t, err)
	migration, err = os.ReadFile("../../db/migrations/20260929000400_create_meeting_rooms_and_bookings.sql")
	require.NoError(t, err)
	_, err = pool.Exec(ctx, strings.Split(string(migration), "-- +goose Down")[0])
	require.NoError(t, err)
	migration, err = os.ReadFile("../../db/migrations/20260929000200_calendar_event_exceptions.sql")
	_, tzErr := pool.Exec(ctx, "ALTER TABLE events ADD COLUMN timezone TEXT NOT NULL DEFAULT 'Asia/Almaty'")
	require.NoError(t, tzErr)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, strings.Split(string(migration), "-- +goose Down")[0])
	require.NoError(t, err)

	ownerCalendar, organizerCalendar := uuid.New(), uuid.New()
	activeGroup, formerGroup := uuid.New(), uuid.New()
	ownedEvent, invitedEvent, declinedEvent := uuid.New(), uuid.New(), uuid.New()
	from := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 1)

	_, err = pool.Exec(ctx, `INSERT INTO users(id) VALUES (1), (2), (3)`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO user_calendars(id,user_id,name,color,is_default) VALUES
		($1,1,'Mine','#111111',TRUE), ($2,2,'Organizer','#222222',TRUE)`, ownerCalendar, organizerCalendar)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO groups(id,name) VALUES ($1,'Active team'), ($2,'Former team')`, activeGroup, formerGroup)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO group_members(group_id,user_id,left_at) VALUES ($1,1,NULL), ($2,1,NOW())`, activeGroup, formerGroup)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO events(id,user_id,calendar_id,title,start_time,end_time) VALUES
		($1,1,$4,'Owned',$6::timestamptz + INTERVAL '10 hours',$6::timestamptz + INTERVAL '11 hours'),
		($2,2,$5,'Invitation',$6::timestamptz + INTERVAL '12 hours',$6::timestamptz + INTERVAL '13 hours'),
		($3,2,$5,'Declined',$6::timestamptz + INTERVAL '14 hours',$6::timestamptz + INTERVAL '15 hours')`,
		ownedEvent, invitedEvent, declinedEvent, ownerCalendar, organizerCalendar, from)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO event_attendees(event_id,user_id,status) VALUES
		($1,1,'accepted'), ($2,1,'pending'), ($3,1,'declined')`, ownedEvent, invitedEvent, declinedEvent)
	require.NoError(t, err)

	repo := NewCalendarRepository(pool, zap.NewNop())
	events, err := repo.GetEventsForGrid(ctx, 1, from, to, []uuid.UUID{ownerCalendar})
	require.NoError(t, err)
	require.Len(t, events, 2)
	require.Equal(t, []uuid.UUID{ownedEvent, invitedEvent}, []uuid.UUID{events[0].ID, events[1].ID})

	// Hiding every owned layer must not erase invitations from other organizers.
	events, err = repo.GetEventsForGrid(ctx, 1, from, to, []uuid.UUID{})
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, invitedEvent, events[0].ID)

	layers, err := repo.GetGroupCalendarsWithSettings(ctx, 1)
	require.NoError(t, err)
	require.Len(t, layers, 1)
	require.Equal(t, activeGroup, layers[0].GroupID)

	hidden := false
	updated, err := repo.UpdateGroupCalendarVisibility(ctx, 1, activeGroup, &hidden, nil)
	require.NoError(t, err)
	require.True(t, updated)
	updated, err = repo.UpdateGroupCalendarVisibility(ctx, 1, formerGroup, &hidden, nil)
	require.NoError(t, err)
	require.False(t, updated)

	// A modified occurrence must be selectable on its new date even after the
	// series UNTIL, without exposing a declined invitation to the viewer.
	original := from.Add(10 * time.Hour)
	moved := from.AddDate(0, 0, 2).Add(10 * time.Hour)
	_, err = pool.Exec(ctx, `UPDATE events SET recurrence_rule='WEEKLY', recurrence_until=$2 WHERE id=$1`, ownedEvent, original)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE events SET recurrence_rule='WEEKLY', recurrence_until=$2 WHERE id=$1`, declinedEvent, from.Add(14*time.Hour))
	require.NoError(t, err)
	item, err := repo.UpsertEventException(ctx, domain.CalendarEventException{
		EventID: ownedEvent, OccurrenceDate: original, ExceptionType: "modified",
		EventData: &domain.OccurrenceDetails{Title: "Moved", StartTime: moved, EndTime: moved.Add(time.Hour)},
	})
	require.NoError(t, err)
	_, err = repo.UpsertEventException(ctx, domain.CalendarEventException{
		EventID: declinedEvent, OccurrenceDate: from.Add(14 * time.Hour), ExceptionType: "modified",
		EventData: &domain.OccurrenceDetails{Title: "Private", StartTime: moved, EndTime: moved.Add(time.Hour)},
	})
	require.NoError(t, err)
	exceptions, err := repo.ListEventExceptions(ctx, []uuid.UUID{ownedEvent})
	require.NoError(t, err)
	require.Len(t, exceptions, 1)
	require.Equal(t, "Moved", exceptions[0].EventData.Title)
	again, err := repo.UpsertEventException(ctx, domain.CalendarEventException{
		EventID: ownedEvent, OccurrenceDate: original, ExceptionType: "modified",
		EventData: &domain.OccurrenceDetails{Title: "Moved again", StartTime: moved, EndTime: moved.Add(time.Hour)},
	})
	require.NoError(t, err)
	require.Equal(t, item.ID, again.ID)
	moveDay := from.AddDate(0, 0, 2)
	events, err = repo.GetEventsForGrid(ctx, 1, moveDay, moveDay.AddDate(0, 0, 1), []uuid.UUID{ownerCalendar})
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, ownedEvent, events[0].ID)
	deleted, err := repo.DeleteEventException(ctx, ownedEvent, item.ID)
	require.NoError(t, err)
	require.True(t, deleted)

	// CAL-004 migration must repair legacy values before validating its CHECK.
	_, err = pool.Exec(ctx, `INSERT INTO event_attendees(event_id,user_id,status) VALUES ($1,2,'legacy_unknown')`, ownedEvent)
	require.NoError(t, err)
	migration, err = os.ReadFile("../../db/migrations/20260929000300_add_tentative_status_to_attendees.sql")
	require.NoError(t, err)
	_, err = pool.Exec(ctx, strings.Split(string(migration), "-- +goose Down")[0])
	require.NoError(t, err)
	var status string
	require.NoError(t, pool.QueryRow(ctx, `SELECT status FROM event_attendees WHERE event_id=$1 AND user_id=2`, ownedEvent).Scan(&status))
	require.Equal(t, "pending", status)

	_, err = pool.Exec(ctx, `UPDATE event_attendees SET status='tentative' WHERE event_id=$1 AND user_id=2`, ownedEvent)
	require.NoError(t, err)
	updatedEvent := &domain.Event{
		ID: ownedEvent, UserID: 1, CalendarID: ownerCalendar, Title: "Edited",
		StartTime: original, EndTime: original.Add(time.Hour), RecurrenceInterval: 1,
	}
	require.NoError(t, repo.Update(ctx, updatedEvent, []int64{2, 3}))
	require.NoError(t, pool.QueryRow(ctx, `SELECT status FROM event_attendees WHERE event_id=$1 AND user_id=2`, ownedEvent).Scan(&status))
	require.Equal(t, "tentative", status, "editing an event must preserve existing RSVP")
	require.NoError(t, pool.QueryRow(ctx, `SELECT status FROM event_attendees WHERE event_id=$1 AND user_id=3`, ownedEvent).Scan(&status))
	require.Equal(t, "pending", status, "new attendee must start pending")
	require.NoError(t, repo.Update(ctx, updatedEvent, []int64{3}))
	var remaining int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM event_attendees WHERE event_id=$1 AND user_id=2`, ownedEvent).Scan(&remaining))
	require.Zero(t, remaining)
	note := "I will attend"
	previous, changed, err := repo.UpdateAttendeeStatus(ctx, ownedEvent, 3, "accepted", &note)
	require.NoError(t, err)
	require.Equal(t, "pending", previous)
	require.True(t, changed)
	previous, changed, err = repo.UpdateAttendeeStatus(ctx, ownedEvent, 3, "accepted", nil)
	require.NoError(t, err)
	require.Equal(t, "accepted", previous)
	require.False(t, changed, "repeated RSVP must not produce another transition")
	var savedComment string
	require.NoError(t, pool.QueryRow(ctx, `SELECT comment FROM event_attendees WHERE event_id=$1 AND user_id=3`, ownedEvent).Scan(&savedComment))
	require.Equal(t, note, savedComment, "omitting comment must preserve the previous note")
	_, err = pool.Exec(ctx, `INSERT INTO event_reminders(event_id,user_id,minutes_before) VALUES ($1,1,15),($2,1,15)
	 ON CONFLICT(event_id,user_id) DO UPDATE SET minutes_before=EXCLUDED.minutes_before`, ownedEvent, declinedEvent)
	require.NoError(t, err)
	reminders, err := repo.ListRemindersWithEvents(ctx, from, to)
	require.NoError(t, err)
	var viewerReminders []ReminderWithEventRow
	for _, reminder := range reminders {
		if reminder.ReminderUserID == 1 {
			viewerReminders = append(viewerReminders, reminder)
		}
	}
	require.Len(t, viewerReminders, 1, "declined invitees must not receive reminders")
	require.Equal(t, ownedEvent, viewerReminders[0].EventID)
}
