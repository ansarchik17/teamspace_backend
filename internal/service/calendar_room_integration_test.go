package service

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

// Uses the production services, repositories and real migrations, never a mutex mock.
func roomIntegrationService(t *testing.T, beforeCompanyMigration ...func(*pgxpool.Pool)) (*CalendarService, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("TEST_MESSENGER_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_MESSENGER_DATABASE_URL to a disposable PostgreSQL database")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	schema := "cal006_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	cfg, err := pgxpool.ParseConfig(dsn)
	require.NoError(t, err)
	cfg.MaxConns = 24
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	})
	_, err = pool.Exec(ctx, `
		CREATE TABLE users (
		 id BIGINT PRIMARY KEY, keycloak_id TEXT, iin TEXT, email TEXT NOT NULL DEFAULT '', username TEXT NOT NULL DEFAULT '', password_hash TEXT,
		 first_name TEXT NOT NULL DEFAULT '',last_name TEXT NOT NULL DEFAULT '',avatar_url TEXT,bio TEXT,position TEXT,is_active BOOLEAN NOT NULL DEFAULT TRUE,
		 is_first_login BOOLEAN NOT NULL DEFAULT FALSE,last_login_at TIMESTAMPTZ,last_seen_at TIMESTAMPTZ,created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		 updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),deleted_at TIMESTAMPTZ,role TEXT NOT NULL DEFAULT 'user',availability_status TEXT,full_name_local TEXT,
			 can_view_full_drive_activity BOOLEAN NOT NULL DEFAULT FALSE,organization_id UUID,team_id UUID,manager_id BIGINT,ministry_id BIGINT,department_id BIGINT
		);
		CREATE TABLE user_calendars(id UUID PRIMARY KEY,user_id BIGINT NOT NULL REFERENCES users(id),name TEXT NOT NULL,color TEXT NOT NULL,
		 is_default BOOLEAN NOT NULL DEFAULT FALSE,created_at TIMESTAMPTZ NOT NULL DEFAULT now(),updated_at TIMESTAMPTZ NOT NULL DEFAULT now());
		CREATE UNIQUE INDEX user_calendars_default_idx ON user_calendars(user_id) WHERE is_default;
		CREATE TABLE events(id UUID PRIMARY KEY,user_id BIGINT NOT NULL REFERENCES users(id),calendar_id UUID REFERENCES user_calendars(id),
		 title TEXT NOT NULL,description TEXT,start_time TIMESTAMPTZ NOT NULL,end_time TIMESTAMPTZ NOT NULL,location TEXT,is_all_day BOOLEAN NOT NULL DEFAULT FALSE,
		 is_online BOOLEAN NOT NULL DEFAULT FALSE,recurrence_rule TEXT,recurrence_interval INT NOT NULL DEFAULT 1,recurrence_until TIMESTAMPTZ,color TEXT,
		 meeting_provider TEXT NOT NULL DEFAULT 'none',meeting_room_id TEXT,meeting_url TEXT,livekit_room_name TEXT,cisco_meeting_id TEXT,cisco_join_url TEXT,
		 online_meeting_status TEXT NOT NULL DEFAULT 'none',created_at TIMESTAMPTZ NOT NULL DEFAULT now(),updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),url TEXT,deleted_at TIMESTAMPTZ);
		CREATE TABLE event_attendees(event_id UUID REFERENCES events(id),user_id BIGINT REFERENCES users(id),status TEXT NOT NULL,comment TEXT,
		 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),updated_at TIMESTAMPTZ,PRIMARY KEY(event_id,user_id));
		CREATE TABLE event_reminders(id UUID PRIMARY KEY DEFAULT gen_random_uuid(),event_id UUID REFERENCES events(id),user_id BIGINT REFERENCES users(id),minutes_before INT,
		 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),last_sent_occurrence_start TIMESTAMPTZ,last_sent_at TIMESTAMPTZ,UNIQUE(event_id,user_id));
		INSERT INTO users(id,role,username,email,first_name) VALUES (1,'admin','owner','private@example.com','Owner'),(2,'user','other','other@example.com','Other');
	`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `CREATE TABLE organizations(id uuid PRIMARY KEY,name text NOT NULL,status text NOT NULL DEFAULT 'active',owner_id bigint);
INSERT INTO organizations(id,name,owner_id) VALUES('11111111-1111-4111-8111-111111111111','Test company',1);
UPDATE users SET organization_id='11111111-1111-4111-8111-111111111111';`)
	require.NoError(t, err)
	// Install extension in public so independent disposable schemas can share it.
	_, err = admin.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS btree_gist WITH SCHEMA public")
	require.NoError(t, err)
	for _, path := range []string{
		"../../db/migrations/20260929000200_calendar_event_exceptions.sql",
		"../../db/migrations/20260929000400_create_meeting_rooms_and_bookings.sql",
		"../../db/migrations/20260929000500_calendar_integrations_tz_qmeet.sql",
		"../../db/migrations/20261009000100_user_language.sql",
		"../../db/migrations/20261002000100_organization_access.sql",
		"../../db/migrations/20261006000100_iform_access.sql",
		"../../db/migrations/20261014000200_company_meeting_rooms.sql",
	} {
		if strings.Contains(path, "20261014000200") {
			for _, seed := range beforeCompanyMigration {
				seed(pool)
			}
		}
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		_, err = pool.Exec(ctx, strings.Split(string(data), "-- +goose Down")[0])
		require.NoError(t, err)
	}
	repo := repository.NewCalendarRepository(pool, zap.NewNop())
	users := repository.NewUserRepository(pool, zap.NewNop())
	return &CalendarService{repo: repo, userRepo: users, logger: zap.NewNop()}, pool
}

func roomIntegrationRoom(t *testing.T, s *CalendarService) uuid.UUID {
	t.Helper()
	room, err := s.CreateRoom(context.Background(), 1, domain.CreateMeetingRoomRequest{Name: "Test room " + uuid.NewString(), Capacity: 8})
	require.NoError(t, err)
	return room.ID
}

func TestRoomBookingIntegrationConcurrentTransactionsAndConstraint(t *testing.T) {
	s, pool := roomIntegrationService(t)
	roomID := roomIntegrationRoom(t, s)
	start := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	const workers = 20
	gate := make(chan struct{})
	results := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-gate
			_, err := s.BookRoom(ctx, 1, roomID, domain.BookRoomRequest{Title: "Private concurrent meeting", StartTime: start, EndTime: start.Add(time.Hour), CreateCalendarEvent: true})
			results <- err
		}()
	}
	close(gate)
	wg.Wait()
	close(results)
	success, conflicts := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else {
			require.Equal(t, 409, apperrors.ToAppError(err).StatusCode)
			conflicts++
		}
	}
	require.Equal(t, 1, success)
	require.Equal(t, 19, conflicts)
	var count int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM room_bookings WHERE room_id=$1 AND deleted_at IS NULL`, roomID).Scan(&count))
	require.Equal(t, 1, count)
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM events WHERE room_id=$1 AND deleted_at IS NULL`, roomID).Scan(&count))
	require.Equal(t, 1, count) // Failed reservations must not leave orphan calendar events.
	// Adjacent half-open intervals are valid.
	_, err := s.BookRoom(ctx, 2, roomID, domain.BookRoomRequest{Title: "Adjacent", StartTime: start.Add(time.Hour), EndTime: start.Add(2 * time.Hour)})
	require.NoError(t, err)
	// The exclusion constraint also protects writes that bypass advisory locking.
	_, err = pool.Exec(ctx, `INSERT INTO room_bookings(room_id,user_id,title,start_time,end_time) VALUES ($1,2,'Bypass',$2,$3)`, roomID, start, start.Add(time.Hour))
	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	require.Equal(t, "23P01", pgErr.Code)
}

func TestRoomBookingIntegrationCreationPrivacyAndOwnership(t *testing.T) {
	s, pool := roomIntegrationService(t)
	ctx := context.Background()
	roomID := roomIntegrationRoom(t, s)
	start := time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)
	description := "Confidential medical information"
	booking, err := s.BookRoom(ctx, 1, roomID, domain.BookRoomRequest{Title: "Secret meeting", Description: &description, StartTime: start, EndTime: start.Add(time.Hour), CreateCalendarEvent: true})
	require.NoError(t, err)
	require.NotNil(t, booking.EventID)
	event, err := s.repo.GetByID(ctx, *booking.EventID)
	require.NoError(t, err)
	require.NotNil(t, event)
	require.Equal(t, roomID, *event.RoomID)
	resp, err := s.GetRoomAvailability(ctx, 2, roomID, domain.RoomAvailabilityQuery{StartTime: start, EndTime: start.Add(time.Hour)})
	require.NoError(t, err)
	data, err := json.Marshal(resp)
	require.NoError(t, err)
	for _, secret := range []string{booking.Title, description, booking.EventID.String(), booking.ID.String(), "private@example.com"} {
		require.NotContains(t, string(data), secret)
	}
	require.Equal(t, "Занято", resp.Bookings[0].Title)
	_, err = s.BookRoom(ctx, 2, roomID, domain.BookRoomRequest{Title: "Conflict", StartTime: start, EndTime: start.Add(time.Hour)})
	require.Equal(t, 409, apperrors.ToAppError(err).StatusCode)
	data, marshalErr := json.Marshal(apperrors.ToAppError(err))
	require.NoError(t, marshalErr)
	require.NotContains(t, string(data), booking.Title)
	_, err = s.BookRoom(ctx, 2, roomID, domain.BookRoomRequest{Title: "Attach victim event", EventID: booking.EventID, StartTime: start.Add(3 * time.Hour), EndTime: start.Add(4 * time.Hour)})
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	_, err = s.CreateRoom(ctx, 2, domain.CreateMeetingRoomRequest{Name: "Unauthorized", Capacity: 4})
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	require.Equal(t, 403, apperrors.ToAppError(s.DeleteRoom(ctx, 2, roomID)).StatusCode)
	_, err = s.UpdateRoom(ctx, 2, roomID, domain.UpdateMeetingRoomRequest{})
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	require.Error(t, s.CancelRoomBooking(ctx, 2, booking.ID))
	require.NoError(t, s.CancelRoomBooking(ctx, 1, booking.ID))
	event, err = s.repo.GetByID(ctx, *booking.EventID)
	require.NoError(t, err)
	require.Nil(t, event.RoomID)
	var count int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM room_bookings WHERE event_id=$1 AND deleted_at IS NULL`, *booking.EventID).Scan(&count))
	require.Zero(t, count)
}

func TestRoomBookingIntegrationOccurrenceMoveDeleteRestoreAndRollback(t *testing.T) {
	s, pool := roomIntegrationService(t)
	ctx := context.Background()
	roomID, otherRoom := roomIntegrationRoom(t, s), roomIntegrationRoom(t, s)
	start := time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)
	until := start.AddDate(0, 0, 2)
	rule := "DAILY"
	event, err := s.CreateEventWithOptions(ctx, 1, &domain.CreateEventRequest{Title: "Series", StartTime: start, EndTime: start.Add(time.Hour), RoomID: &roomID, RecurrenceRule: &rule, RecurrenceUntil: &until}, false)
	require.NoError(t, err)
	movedStart, movedEnd := start.Add(3*time.Hour), start.Add(4*time.Hour)
	_, err = s.UpdateOccurrence(ctx, 1, event.ID, start, domain.UpdateOccurrenceRequest{StartTime: &movedStart, EndTime: &movedEnd, RoomID: &otherRoom})
	require.NoError(t, err)
	bookings, err := s.repo.ListRoomBookings(ctx, roomID, start, until.Add(time.Hour))
	require.NoError(t, err)
	require.Len(t, bookings, 2)
	bookings, err = s.repo.ListRoomBookings(ctx, otherRoom, start, start.AddDate(0, 0, 1))
	require.NoError(t, err)
	require.Len(t, bookings, 1)
	require.True(t, movedStart.Equal(bookings[0].StartTime))
	next := start.AddDate(0, 0, 1)
	deleted, err := s.DeleteOccurrence(ctx, 1, event.ID, next)
	require.NoError(t, err)
	newTitle := "Renamed series"
	_, err = s.UpdateEvent(ctx, 1, event.ID, &domain.UpdateEventRequest{Title: &newTitle})
	require.NoError(t, err)
	bookings, err = s.repo.ListRoomBookings(ctx, roomID, start, until.Add(time.Hour))
	require.NoError(t, err)
	require.Len(t, bookings, 1)
	require.True(t, until.Equal(bookings[0].StartTime))
	_, err = s.BookRoom(ctx, 2, roomID, domain.BookRoomRequest{Title: "Occupies deleted slot", StartTime: next, EndTime: next.Add(time.Hour)})
	require.NoError(t, err)
	err = s.RestoreOccurrence(ctx, 1, event.ID, deleted.ID)
	require.Equal(t, 409, apperrors.ToAppError(err).StatusCode)
	exceptions, err := s.repo.ListEventExceptions(ctx, []uuid.UUID{event.ID})
	require.NoError(t, err)
	require.Len(t, exceptions, 2)
	// Even force=true cannot bypass room occupancy or partially apply an exception.
	blockedEnd := next.Add(time.Hour)
	_, err = s.UpdateOccurrenceWithOptions(ctx, 1, event.ID, start, domain.UpdateOccurrenceRequest{StartTime: &next, EndTime: &blockedEnd, RoomID: &roomID}, true)
	require.Equal(t, 409, apperrors.ToAppError(err).StatusCode)
	bookings, err = s.repo.ListRoomBookings(ctx, otherRoom, start, start.AddDate(0, 0, 1))
	require.NoError(t, err)
	require.Len(t, bookings, 1)
	require.True(t, movedStart.Equal(bookings[0].StartTime))
	var count int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM room_bookings WHERE event_id=$1 AND deleted_at IS NULL`, event.ID).Scan(&count))
	require.Equal(t, 2, count)
	// Release a moved occurrence, then restore it into the original room atomically.
	firstDeleted, err := s.DeleteOccurrence(ctx, 1, event.ID, start)
	require.NoError(t, err)
	require.NoError(t, s.RestoreOccurrence(ctx, 1, event.ID, firstDeleted.ID))
	bookings, err = s.repo.ListRoomBookings(ctx, roomID, start, start.Add(time.Hour))
	require.NoError(t, err)
	require.Len(t, bookings, 1)
	bookings, err = s.repo.ListRoomBookings(ctx, otherRoom, movedStart, movedEnd)
	require.NoError(t, err)
	require.Empty(t, bookings)
}
