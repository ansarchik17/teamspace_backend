package repository

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

type RoomBookingBuilder func([]domain.CalendarEventException) ([]domain.RoomBooking, error)

type RoomBookingPlan struct {
	ReminderMinutesSet bool
	ReminderMinutes    *int
	ExpectedUpdatedAt  *time.Time
	Build              RoomBookingBuilder
}

// All edits lock the event first, then every affected room in UUID order.
func (r *CalendarRepository) lockCalendarEventTx(ctx context.Context, tx pgx.Tx, eventID uuid.UUID, userID int64, expected *time.Time) error {
	var ownerID int64
	var updated time.Time
	err := tx.QueryRow(ctx, `SELECT user_id, updated_at FROM events WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, eventID).Scan(&ownerID, &updated)
	if errors.Is(err, pgx.ErrNoRows) {
		return apperrors.NotFound("event not found")
	}
	if err != nil {
		return err
	}
	if ownerID != userID {
		return apperrors.Forbidden("only the organizer can change the room booking")
	}
	if expected != nil && !updated.Equal(*expected) {
		return apperrors.Conflict("event changed; reload before editing")
	}
	return nil
}

func (r *CalendarRepository) replaceEventRoomBookingsTx(ctx context.Context, tx pgx.Tx, eventID uuid.UUID, bookings []domain.RoomBooking) error {
	rooms := make(map[uuid.UUID][]domain.RoomBooking)
	type slotKey struct {
		room       uuid.UUID
		start, end int64
	}
	wanted := make(map[slotKey]domain.RoomBooking)
	for _, b := range bookings {
		if b.EventID == nil || *b.EventID != eventID {
			return apperrors.BadRequest("booking must belong to the event")
		}
		key := slotKey{b.RoomID, b.StartTime.UnixMicro(), b.EndTime.UnixMicro()}
		if _, exists := wanted[key]; exists {
			return apperrors.RoomAlreadyBooked("recurring room slots overlap")
		}
		wanted[key] = b
		rooms[b.RoomID] = nil
	}
	type storedSlot struct {
		id  uuid.UUID
		key slotKey
	}
	var stored []storedSlot
	rows, err := tx.Query(ctx, `SELECT id,room_id,start_time,end_time FROM room_bookings WHERE event_id=$1 AND deleted_at IS NULL AND status='confirmed'`, eventID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, roomID uuid.UUID
		var start, end time.Time
		if err := rows.Scan(&id, &roomID, &start, &end); err != nil {
			rows.Close()
			return err
		}
		stored = append(stored, storedSlot{id, slotKey{roomID, start.UnixMicro(), end.UnixMicro()}})
		if _, ok := rooms[roomID]; !ok {
			rooms[roomID] = nil
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	ids := make([]uuid.UUID, 0, len(rooms))
	for id := range rooms {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
	for _, id := range ids {
		if err := r.AcquireRoomAdvisoryLockTx(ctx, tx, id); err != nil {
			return err
		}
	}
	for _, old := range stored {
		if b, ok := wanted[old.key]; ok {
			if _, err := tx.Exec(ctx, `UPDATE room_bookings SET title=$2,description=$3,updated_at=clock_timestamp() WHERE id=$1`, old.id, b.Title, b.Description); err != nil {
				return err
			}
			delete(wanted, old.key)
		} else {
			if _, err := tx.Exec(ctx, `UPDATE room_bookings SET status='cancelled',deleted_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=$1`, old.id); err != nil {
				return err
			}
		}
	}
	for _, b := range wanted {
		rooms[b.RoomID] = append(rooms[b.RoomID], b)
	}
	for _, id := range ids {
		if err := r.BookRoomSlotsTx(ctx, tx, id, rooms[id], nil); err != nil {
			return err
		}
	}
	return nil
}

// Exception data and all room reservations commit together or roll back together.
func (r *CalendarRepository) SaveOccurrenceWithRoomBookings(ctx context.Context, event domain.Event, item *domain.CalendarEventException, restoreID *uuid.UUID, build RoomBookingBuilder) (*domain.CalendarEventException, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := r.lockCalendarEventTx(ctx, tx, event.ID, event.UserID, &event.UpdatedAt); err != nil {
		return nil, err
	}
	var saved *domain.CalendarEventException
	if restoreID != nil {
		tag, err := tx.Exec(ctx, `DELETE FROM event_exceptions WHERE event_id=$1 AND id=$2`, event.ID, *restoreID)
		if err != nil {
			return nil, err
		}
		if tag.RowsAffected() == 0 {
			return nil, apperrors.NotFound("exception not found")
		}
	} else {
		if item == nil || item.EventID != event.ID {
			return nil, apperrors.BadRequest("invalid occurrence exception")
		}
		saved, err = upsertEventException(ctx, tx, *item)
		if err != nil {
			return nil, err
		}
	}
	exceptions, err := listEventExceptions(ctx, tx, []uuid.UUID{event.ID})
	if err != nil {
		return nil, err
	}
	bookings, err := build(exceptions)
	if err != nil {
		return nil, err
	}
	if err := r.replaceEventRoomBookingsTx(ctx, tx, event.ID, bookings); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE events SET updated_at=clock_timestamp() WHERE id=$1`, event.ID); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return saved, nil
}
