package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func (r *CalendarRepository) LinkEventToGroup(ctx context.Context, groupID, eventID uuid.UUID, createdBy int64) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO group_calendar_events (group_id, event_id, created_by, created_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (group_id, event_id) DO NOTHING
	`, groupID, eventID, createdBy)
	if err != nil {
		return fmt.Errorf("link event to group: %w", err)
	}

	return nil
}

func (r *CalendarRepository) ListUpcomingByGroup(ctx context.Context, groupID uuid.UUID, from time.Time, limit int) ([]domain.Event, error) {
	if limit <= 0 {
		limit = 10
	}

	query := `
		SELECT
			e.id, e.user_id, e.title, e.description, e.start_time, e.end_time,
			e.location, e.is_all_day, e.is_online,
			e.recurrence_rule, e.recurrence_interval, e.recurrence_until,
			e.color, e.meeting_provider, e.meeting_room_id, e.meeting_url, e.livekit_room_name,
			e.cisco_meeting_id, e.cisco_join_url, e.online_meeting_status, e.created_at, e.updated_at,
			e.url
		FROM group_calendar_events gce
		JOIN events e ON e.id = gce.event_id
		WHERE gce.group_id = $1
		  AND e.deleted_at IS NULL
		  AND e.end_time >= $2
		ORDER BY e.start_time ASC
		LIMIT $3
	`

	rows, err := r.db.Query(ctx, query, groupID, from, limit)
	if err != nil {
		return nil, fmt.Errorf("list upcoming group events: %w", err)
	}
	defer rows.Close()

	events := make([]domain.Event, 0, limit)
	ids := make([]uuid.UUID, 0, limit)

	for rows.Next() {
		var e domain.Event
		if err := rows.Scan(
			&e.ID, &e.UserID, &e.Title, &e.Description, &e.StartTime, &e.EndTime,
			&e.Location, &e.IsAllDay, &e.IsOnline,
			&e.RecurrenceRule, &e.RecurrenceInterval, &e.RecurrenceUntil,
			&e.Color, &e.MeetingProvider, &e.MeetingRoomID, &e.MeetingURL, &e.LiveKitRoomName,
			&e.CiscoMeetingID, &e.CiscoJoinURL, &e.MeetingStatus, &e.CreatedAt, &e.UpdatedAt,
			&e.URL,
		); err != nil {
			return nil, fmt.Errorf("scan group event: %w", err)
		}

		events = append(events, e)
		ids = append(ids, e.ID)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate group events: %w", err)
	}

	if len(ids) == 0 {
		return events, nil
	}

	attMap, err := r.getAttendeesByEventIDs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("load group event attendees: %w", err)
	}

	for i := range events {
		events[i].Attendees = attMap[events[i].ID]
	}

	return events, nil
}
