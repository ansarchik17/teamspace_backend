package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestCalendarBatchRequiresAuthenticatedOrganizer(t *testing.T) {
	for _, id := range []int64{0, -1} {
		_, err := (&CalendarService{}).BatchCreateEvents(context.Background(), id, &domain.BatchCreateEventRequest{})
		require.Equal(t, 401, apperrors.ToAppError(err).StatusCode)
	}
}

func TestCalendarBatchOwnershipIntegration(t *testing.T) {
	s, db := roomIntegrationService(t)
	ctx := context.Background()
	_, err := db.Exec(ctx, `INSERT INTO organizations(id,name,owner_id) VALUES('22222222-2222-4222-8222-222222222222','Other company',3);
INSERT INTO users(id,role,username,email,first_name,organization_id) VALUES(3,'user','foreign','foreign@example.com','Foreign','22222222-2222-4222-8222-222222222222');`)
	require.NoError(t, err)
	start := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Second)
	request := func(items ...domain.BatchEventItem) *domain.BatchCreateEventRequest {
		return &domain.BatchCreateEventRequest{DateFrom: start.Format(time.RFC3339), DateTo: start.Add(time.Hour).Format(time.RFC3339), Items: items}
	}
	count := func() int {
		var n int
		require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM events`).Scan(&n))
		return n
	}
	for _, actor := range []struct {
		id         int64
		own, other string
	}{{1, "private@example.com", "other@example.com"}, {2, "other@example.com", "private@example.com"}} {
		for _, target := range []string{actor.other, "foreign@example.com", "missing@example.com"} {
			t.Run(fmt.Sprintf("actor_%d_cannot_write_%s", actor.id, target), func(t *testing.T) {
				before := count()
				_, err := s.BatchCreateEvents(ctx, actor.id, request(
					domain.BatchEventItem{Mail: actor.own, MeetingName: "Own item before forbidden item"},
					domain.BatchEventItem{Mail: target, MeetingName: "Forged organizer"},
				))
				require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
				require.Equal(t, before, count(), "a mixed unauthorized batch must not write even its first allowed item")
			})
		}
	}
	// Mail is only a self-calendar selector, with normal email normalization.
	created, err := s.BatchCreateEvents(ctx, 2, request(domain.BatchEventItem{Mail: " OTHER@EXAMPLE.COM ", MeetingName: "Own batch event"}))
	require.NoError(t, err)
	require.Len(t, created, 1)
	require.Equal(t, int64(2), created[0].UserID)
	require.NotEqual(t, uuid.Nil, created[0].ID)
	require.NotEqual(t, uuid.Nil, created[0].CalendarID)
	var owner int64
	require.NoError(t, db.QueryRow(ctx, `SELECT c.user_id FROM events e JOIN user_calendars c ON c.id=e.calendar_id WHERE e.id=$1`, created[0].ID).Scan(&owner))
	require.Equal(t, int64(2), owner)
	before := count()
	_, err = s.BatchCreateEvents(ctx, 2, request(domain.BatchEventItem{Mail: "other@example.com", MeetingName: "Overlapping own event"}))
	require.Equal(t, 409, apperrors.ToAppError(err).StatusCode)
	require.Equal(t, before, count())

	// Colleagues are participants of the authenticated organizer's event,
	// rather than forged owners of new calendar records.
	next := start.Add(2 * time.Hour)
	event, err := s.CreateEvent(ctx, 2, &domain.CreateEventRequest{Title: "Invite colleague", StartTime: next, EndTime: next.Add(time.Hour), AttendeeIDs: []int64{1}})
	require.NoError(t, err)
	require.Equal(t, int64(2), event.UserID)
	var status string
	require.NoError(t, db.QueryRow(ctx, `SELECT status FROM event_attendees WHERE event_id=$1 AND user_id=1`, event.ID).Scan(&status))
	require.Equal(t, "pending", status)
	before = count()
	_, err = s.CreateEvent(ctx, 2, &domain.CreateEventRequest{Title: "Other company", StartTime: next.Add(2 * time.Hour), EndTime: next.Add(3 * time.Hour), AttendeeIDs: []int64{3}})
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
	require.Equal(t, before, count())
	foreignCalendar, err := s.repo.EnsureDefaultCalendar(ctx, 1)
	require.NoError(t, err)
	_, err = s.CreateEvent(ctx, 2, &domain.CreateEventRequest{Title: "Other calendar", CalendarID: &foreignCalendar.ID, StartTime: next.Add(4 * time.Hour), EndTime: next.Add(5 * time.Hour)})
	require.Error(t, err)
	require.Equal(t, before, count())
}
