package repository

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func (r *CalendarRepository) LoadEventMeetingDetails(ctx context.Context, events []domain.Event) error {
	if len(events) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, len(events))
	for i := range events {
		ids[i] = events[i].ID
	}
	rows, err := r.db.Query(ctx, `SELECT id,room_id,room_snapshot FROM events WHERE id=ANY($1)`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	rooms := map[uuid.UUID]*domain.MeetingRoom{}
	roomIDs := map[uuid.UUID]*uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		var roomID *uuid.UUID
		var data []byte
		if err = rows.Scan(&id, &roomID, &data); err != nil {
			return err
		}
		roomIDs[id] = roomID
		if len(data) > 0 && string(data) != "null" {
			var room domain.MeetingRoom
			if err = json.Unmarshal(data, &room); err != nil {
				return err
			}
			rooms[id] = &room
		}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	rows.Close()
	attendees, err := r.getAttendeesByEventIDs(ctx, ids)
	if err != nil {
		return err
	}
	for i := range events {
		events[i].Attendees = attendees[events[i].ID]
		events[i].RoomID = roomIDs[events[i].ID]
		events[i].Room = rooms[events[i].ID]
		events[i].UpdateAttendanceSummary()
	}
	return nil
}
func (r *CalendarRepository) enrichMeetingEvent(ctx context.Context, event *domain.Event) error {
	items := []domain.Event{*event}
	if err := r.LoadEventMeetingDetails(ctx, items); err != nil {
		return err
	}
	*event = items[0]
	return nil
}

func (r *CalendarRepository) UpdateAttendeeResponse(ctx context.Context, eventID uuid.UUID, userID int64, status string, comment, attendance *string, attendanceSet bool) (string, bool, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback(ctx)
	// Lock the event before the attendee, matching organizer edits.
	var event domain.Event
	if err = tx.QueryRow(ctx, `SELECT room_id,location,is_online,meeting_url FROM events WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, eventID).Scan(&event.RoomID, &event.Location, &event.IsOnline, &event.MeetingURL); err != nil {
		return "", false, calendarResourceError(err)
	}
	var previous string
	var oldComment, oldAttendance *string
	if err = tx.QueryRow(ctx, `SELECT status,comment,attendance FROM event_attendees WHERE event_id=$1 AND user_id=$2 FOR UPDATE`, eventID, userID).Scan(&previous, &oldComment, &oldAttendance); err != nil {
		return "", false, calendarResourceError(err)
	}
	mode := oldAttendance
	if attendanceSet || attendance != nil {
		mode = attendance
	} else if mode == nil {
		mode = event.DefaultAttendanceMode()
	}
	if mode == nil {
		mode = event.DefaultAttendanceMode()
	}
	if !event.AttendanceAllowed(mode) {
		return "", false, apperrors.New("ATTENDANCE_NOT_ALLOWED", "attendance not allowed", 422)
	}
	if comment == nil {
		comment = oldComment
	}
	changed := previous != status || !sameStringPointer(oldComment, comment) || !sameStringPointer(oldAttendance, mode)
	if changed {
		if _, err = tx.Exec(ctx, `UPDATE event_attendees SET status=$3,comment=$4,attendance=$5,updated_at=now() WHERE event_id=$1 AND user_id=$2`, eventID, userID, status, comment, mode); err != nil {
			return "", false, calendarResourceError(err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return "", false, err
	}
	return previous, changed, nil
}
func sameStringPointer(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

type MeetingChange struct {
	ID       int64
	EventID  *uuid.UUID
	UserID   int64
	Kind     string
	Metadata map[string]string
}

func (r *CalendarRepository) PendingMeetingChanges(ctx context.Context) ([]MeetingChange, error) {
	rows, err := r.db.Query(ctx, `WITH pending AS (SELECT id FROM calendar_meeting_changes WHERE delivered_at IS NULL AND (locked_until IS NULL OR locked_until<now()) ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 100) UPDATE calendar_meeting_changes c SET locked_until=now()+interval '2 minutes' FROM pending p WHERE c.id=p.id RETURNING c.id,c.event_id,c.user_id,c.kind,c.metadata`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []MeetingChange{}
	for rows.Next() {
		var item MeetingChange
		var data []byte
		if err = rows.Scan(&item.ID, &item.EventID, &item.UserID, &item.Kind, &data); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(data, &item.Metadata); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
func (r *CalendarRepository) MarkMeetingChangeDelivered(ctx context.Context, id int64) error {
	_, err := r.db.Exec(ctx, `UPDATE calendar_meeting_changes SET delivered_at=now() WHERE id=$1`, id)
	return err
}

func (r *CalendarRepository) ListUnassignedMeetingRooms(ctx context.Context) ([]domain.MeetingRoom, error) {
	rows, err := r.db.Query(ctx, "SELECT "+meetingRoomColumns+" FROM meeting_rooms r LEFT JOIN meeting_sites s ON s.id=r.site_id WHERE r.organization_id IS NULL AND r.deleted_at IS NULL ORDER BY r.name,r.id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.MeetingRoom{}
	for rows.Next() {
		room, err := scanMeetingRoom(rows)
		if err != nil {
			return nil, err
		}
		room.Name = strings.TrimSpace(room.Name)
		items = append(items, *room)
	}
	return items, rows.Err()
}
