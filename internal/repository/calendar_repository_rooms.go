package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

const meetingRoomColumns = "r.id,r.name,r.description,r.capacity,r.floor,r.location,r.color,COALESCE(r.equipment,ARRAY[]::text[]),r.is_active,r.created_at,r.updated_at,r.organization_id,r.site_id,s.name,s.address,r.photo_url"

func scanMeetingRoom(row pgx.Row) (*domain.MeetingRoom, error) {
	var rm domain.MeetingRoom
	err := row.Scan(&rm.ID, &rm.Name, &rm.Description, &rm.Capacity, &rm.Floor, &rm.Location, &rm.Color, &rm.Equipment, &rm.IsActive, &rm.CreatedAt, &rm.UpdatedAt, &rm.OrganizationID, &rm.SiteID, &rm.SiteName, &rm.Address, &rm.PhotoURL)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if rm.Equipment == nil {
		rm.Equipment = []string{}
	}
	return &rm, err
}
func (r *CalendarRepository) ListMeetingRooms(ctx context.Context, filter domain.RoomFilter) ([]domain.MeetingRoom, error) {
	rooms := []domain.MeetingRoom{}
	if filter.OrganizationID == nil {
		return rooms, nil
	}
	var q strings.Builder
	q.WriteString("SELECT " + meetingRoomColumns + " FROM meeting_rooms r LEFT JOIN meeting_sites s ON s.id=r.site_id WHERE r.organization_id=$1 AND r.deleted_at IS NULL")
	args := []any{*filter.OrganizationID}
	if filter.OnlyActive {
		q.WriteString(" AND r.is_active AND s.is_active AND s.deleted_at IS NULL")
	}
	add := func(clause string, value any) {
		args = append(args, value)
		q.WriteString(fmt.Sprintf(clause, len(args)))
	}
	if filter.SiteID != nil {
		add(" AND r.site_id=$%d", *filter.SiteID)
	}
	if filter.MinCapacity != nil {
		add(" AND r.capacity >= $%d", *filter.MinCapacity)
	}
	if filter.Floor != nil {
		add(" AND r.floor=$%d", *filter.Floor)
	}
	if strings.TrimSpace(filter.Search) != "" {
		add(" AND concat_ws(' ',r.name,r.location,r.description) ILIKE $%d", "%"+strings.TrimSpace(filter.Search)+"%")
	}
	if len(filter.Equipment) > 0 {
		add(" AND r.equipment @> $%d", filter.Equipment)
	}
	q.WriteString(" ORDER BY r.floor NULLS LAST,r.capacity,r.name,r.id")
	rows, err := r.db.Query(ctx, q.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		room, err := scanMeetingRoom(rows)
		if err != nil {
			return nil, err
		}
		rooms = append(rooms, *room)
	}
	return rooms, rows.Err()
}
func (r *CalendarRepository) GetMeetingRoomByID(ctx context.Context, id uuid.UUID) (*domain.MeetingRoom, error) {
	return scanMeetingRoom(r.db.QueryRow(ctx, "SELECT "+meetingRoomColumns+" FROM meeting_rooms r LEFT JOIN meeting_sites s ON s.id=r.site_id WHERE r.id=$1 AND r.deleted_at IS NULL", id))
}
func (r *CalendarRepository) GetMeetingRoomsByIDs(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]domain.MeetingRoom, error) {
	result := map[uuid.UUID]domain.MeetingRoom{}
	if len(ids) == 0 {
		return result, nil
	}
	rows, err := r.db.Query(ctx, "SELECT "+meetingRoomColumns+" FROM meeting_rooms r LEFT JOIN meeting_sites s ON s.id=r.site_id WHERE r.id=ANY($1) AND r.deleted_at IS NULL", ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		room, err := scanMeetingRoom(rows)
		if err != nil {
			return nil, err
		}
		result[room.ID] = *room
	}
	return result, rows.Err()
}
func (r *CalendarRepository) CreateMeetingRoom(ctx context.Context, room *domain.MeetingRoom) error {
	return r.saveMeetingRoom(ctx, room, true)
}
func (r *CalendarRepository) UpdateMeetingRoom(ctx context.Context, room *domain.MeetingRoom) error {
	return r.saveMeetingRoom(ctx, room, false)
}
func (r *CalendarRepository) saveMeetingRoom(ctx context.Context, room *domain.MeetingRoom, create bool) error {
	if room.OrganizationID == nil || room.SiteID == nil {
		return apperrors.Validation("site required")
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var site domain.MeetingSite
	if err = tx.QueryRow(ctx, "SELECT id,name,address FROM meeting_sites WHERE id=$1 AND organization_id=$2 AND deleted_at IS NULL FOR SHARE", *room.SiteID, *room.OrganizationID).Scan(&site.ID, &site.Name, &site.Address); err != nil {
		return calendarResourceError(err)
	}
	room.SiteName = &site.Name
	room.Address = &site.Address
	if room.ID == uuid.Nil {
		room.ID = uuid.New()
	}
	if room.Equipment == nil {
		room.Equipment = []string{}
	}
	if err = r.AcquireRoomAdvisoryLockTx(ctx, tx, room.ID); err != nil {
		return err
	}
	if create {
		err = tx.QueryRow(ctx, "INSERT INTO meeting_rooms(id,name,description,capacity,floor,location,color,equipment,is_active,organization_id,site_id,photo_url) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING created_at,updated_at", room.ID, room.Name, room.Description, room.Capacity, room.Floor, room.Location, room.Color, room.Equipment, room.IsActive, *room.OrganizationID, *room.SiteID, room.PhotoURL).Scan(&room.CreatedAt, &room.UpdatedAt)
	} else {
		err = tx.QueryRow(ctx, "UPDATE meeting_rooms SET name=$2,description=$3,capacity=$4,floor=$5,location=$6,color=$7,equipment=$8,is_active=$9,site_id=$11,photo_url=$12,updated_at=clock_timestamp() WHERE id=$1 AND organization_id=$10 AND deleted_at IS NULL RETURNING created_at,updated_at", room.ID, room.Name, room.Description, room.Capacity, room.Floor, room.Location, room.Color, room.Equipment, room.IsActive, *room.OrganizationID, *room.SiteID, room.PhotoURL).Scan(&room.CreatedAt, &room.UpdatedAt)
	}
	if err != nil {
		return calendarResourceError(err)
	}
	return tx.Commit(ctx)
}
func (r *CalendarRepository) DeleteMeetingRoom(ctx context.Context, id uuid.UUID) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = r.AcquireRoomAdvisoryLockTx(ctx, tx, id); err != nil {
		return err
	}
	var roomID uuid.UUID
	if err = tx.QueryRow(ctx, "SELECT id FROM meeting_rooms WHERE id=$1 AND deleted_at IS NULL FOR UPDATE", id).Scan(&roomID); err != nil {
		return calendarResourceError(err)
	}
	var future bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM room_bookings WHERE room_id=$1 AND deleted_at IS NULL AND status='confirmed' AND end_time>now())", id).Scan(&future); err != nil {
		return err
	}
	if future {
		return apperrors.New("ROOM_HAS_BOOKINGS", "room has future bookings", 409)
	}
	if _, err = tx.Exec(ctx, "UPDATE meeting_rooms SET deleted_at=now(),is_active=false,updated_at=now() WHERE id=$1", id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// AcquireRoomAdvisoryLockTx acquires a transaction-level advisory lock on the room UUID.
// It serializes concurrent bookings for the exact same room, preventing race conditions.
func (r *CalendarRepository) AcquireRoomAdvisoryLockTx(ctx context.Context, tx pgx.Tx, roomID uuid.UUID) error {
	query := `SELECT pg_advisory_xact_lock(hashtext('room_booking'), hashtext($1::text))`
	_, err := tx.Exec(ctx, query, roomID.String())
	if err != nil {
		return fmt.Errorf("acquire room advisory lock: %w", err)
	}
	return nil
}

// CheckRoomSlotAvailableTx checks if a given time slot [startTime, endTime) is free in room_bookings under a transaction.
func (r *CalendarRepository) CheckRoomSlotAvailableTx(
	ctx context.Context,
	tx pgx.Tx,
	roomID uuid.UUID,
	startTime, endTime time.Time,
	excludeBookingID, excludeEventID *uuid.UUID,
) (bool, *domain.RoomBooking, error) {
	query := `
		SELECT rb.id, rb.room_id, rb.event_id, rb.user_id, rb.title, rb.description,
		       rb.start_time, rb.end_time, rb.status, rb.created_at, rb.updated_at,
		       COALESCE(u.username, '') AS username,
		       COALESCE(u.first_name, '') AS first_name,
		       COALESCE(u.last_name, '') AS last_name
		FROM room_bookings rb
		LEFT JOIN users u ON u.id = rb.user_id
		WHERE rb.room_id = $1
		  AND rb.deleted_at IS NULL
		  AND rb.status = 'confirmed'
		  AND ($2::uuid IS NULL OR rb.id != $2)
		  AND ($3::uuid IS NULL OR rb.event_id IS DISTINCT FROM $3)
		  AND tstzrange(rb.start_time, rb.end_time, '[)') && tstzrange($4, $5, '[)')
		ORDER BY rb.start_time ASC
		LIMIT 1
	`
	var b domain.RoomBooking
	var username, firstName, lastName string
	err := tx.QueryRow(ctx, query, roomID, excludeBookingID, excludeEventID, startTime, endTime).Scan(
		&b.ID, &b.RoomID, &b.EventID, &b.UserID, &b.Title, &b.Description,
		&b.StartTime, &b.EndTime, &b.Status, &b.CreatedAt, &b.UpdatedAt,
		&username, &firstName, &lastName,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return true, nil, nil
		}
		return false, nil, fmt.Errorf("check room slot: %w", err)
	}

	b.User = &domain.UserResponse{
		ID:        b.UserID,
		Username:  username,
		FirstName: firstName,
		LastName:  lastName,
	}
	return false, &b, nil
}

// ListRoomBookings retrieves all confirmed bookings for a room within a time window.
func (r *CalendarRepository) ListRoomBookings(ctx context.Context, roomID uuid.UUID, from, to time.Time) ([]domain.RoomBooking, error) {
	query := `
		SELECT rb.id, rb.room_id, rb.event_id, rb.user_id, rb.title, rb.description,
		       rb.start_time, rb.end_time, rb.status, rb.created_at, rb.updated_at,
		       COALESCE(u.username, '') AS username,
		       COALESCE(u.first_name, '') AS first_name,
		       COALESCE(u.last_name, '') AS last_name,
		       u.email, u.avatar_url
		FROM room_bookings rb
		LEFT JOIN users u ON u.id = rb.user_id
		WHERE rb.room_id = $1
		  AND rb.deleted_at IS NULL
		  AND rb.status = 'confirmed'
		  AND rb.start_time < $3
		  AND rb.end_time > $2
		ORDER BY rb.start_time ASC
	`
	rows, err := r.db.Query(ctx, query, roomID, from, to)
	if err != nil {
		return nil, fmt.Errorf("list room bookings: %w", err)
	}
	defer rows.Close()

	var bookings []domain.RoomBooking
	for rows.Next() {
		var b domain.RoomBooking
		var username, firstName, lastName string
		var email, avatarURL *string
		if err := rows.Scan(
			&b.ID, &b.RoomID, &b.EventID, &b.UserID, &b.Title, &b.Description,
			&b.StartTime, &b.EndTime, &b.Status, &b.CreatedAt, &b.UpdatedAt,
			&username, &firstName, &lastName, &email, &avatarURL,
		); err != nil {
			return nil, fmt.Errorf("scan room booking: %w", err)
		}
		b.User = &domain.UserResponse{
			ID:        b.UserID,
			Username:  username,
			FirstName: firstName,
			LastName:  lastName,
		}
		if email != nil {
			b.User.Email = *email
		}
		if avatarURL != nil {
			b.User.AvatarURL = avatarURL
		}
		bookings = append(bookings, b)
	}
	return bookings, rows.Err()
}

// BookRoomAtomic performs an atomic direct booking with transaction-level advisory locking
// and exclusion constraint protection against race conditions.
func (r *CalendarRepository) BookRoomAtomic(ctx context.Context, booking *domain.RoomBooking) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Check ownership under the event lock before accepting any client EventID.
	if booking.EventID != nil {
		if err := r.lockCalendarEventTx(ctx, tx, *booking.EventID, booking.UserID, nil); err != nil {
			return err
		}
		var start, end time.Time
		var roomID *uuid.UUID
		var rule *string
		err := tx.QueryRow(ctx, `SELECT start_time,end_time,room_id,recurrence_rule,title,description FROM events WHERE id=$1`, *booking.EventID).
			Scan(&start, &end, &roomID, &rule, &booking.Title, &booking.Description)
		if err != nil {
			return err
		}
		if rule != nil || roomID != nil || !start.Equal(booking.StartTime) || !end.Equal(booking.EndTime) {
			return apperrors.BadRequest("use calendar event editing for recurring, already reserved, or differently timed events")
		}
	}

	// 1. Transactional advisory lock on the room
	if err := r.AcquireRoomAdvisoryLockTx(ctx, tx, booking.RoomID); err != nil {
		return err
	}

	// 2. Verify room exists and is active
	var isActive bool
	var roomName string
	err = tx.QueryRow(ctx, "SELECT name, is_active FROM meeting_rooms WHERE id = $1 AND deleted_at IS NULL FOR SHARE", booking.RoomID).Scan(&roomName, &isActive)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return apperrors.RoomNotFound()
		}
		return fmt.Errorf("verify room: %w", err)
	}
	if !isActive {
		return apperrors.RoomInactive()
	}

	// 3. Check for overlapping slot
	free, conflicting, err := r.CheckRoomSlotAvailableTx(ctx, tx, booking.RoomID, booking.StartTime, booking.EndTime, nil, nil)
	if err != nil {
		return err
	}
	if !free && conflicting != nil {
		msg := fmt.Sprintf("Переговорная '%s' уже занята с %s по %s",
			roomName,
			conflicting.StartTime.Format("15:04"),
			conflicting.EndTime.Format("15:04"),
		)
		return apperrors.RoomAlreadyBooked(msg).WithDetails(map[string]any{
			"room_id":          booking.RoomID,
			"room_name":        roomName,
			"conflicting_slot": map[string]any{"start": conflicting.StartTime, "end": conflicting.EndTime},
		})
	}

	// 4. Insert booking
	if booking.ID == uuid.Nil {
		booking.ID = uuid.New()
	}
	booking.Status = "confirmed"
	now := time.Now()
	booking.CreatedAt = now
	booking.UpdatedAt = now

	insertQuery := `
		INSERT INTO room_bookings (
			id, room_id, event_id, user_id, title, description, start_time, end_time, status, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING id
	`
	err = tx.QueryRow(ctx, insertQuery,
		booking.ID, booking.RoomID, booking.EventID, booking.UserID, booking.Title,
		booking.Description, booking.StartTime, booking.EndTime, booking.Status,
		booking.CreatedAt, booking.UpdatedAt,
	).Scan(&booking.ID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23P01" {
			return apperrors.RoomAlreadyBooked(fmt.Sprintf("Переговорная '%s' уже забронирована на указанное время", roomName))
		}
		return calendarResourceError(err)
	}

	if booking.EventID != nil {
		if _, err := tx.Exec(ctx, `UPDATE events SET room_id=$2,updated_at=clock_timestamp() WHERE id=$1`, *booking.EventID, booking.RoomID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// BookRoomSlotsTx reserves multiple slots (e.g. for recurring events) within an existing transaction.
func (r *CalendarRepository) BookRoomSlotsTx(
	ctx context.Context,
	tx pgx.Tx,
	roomID uuid.UUID,
	bookings []domain.RoomBooking,
	excludeEventID *uuid.UUID,
) error {
	if len(bookings) == 0 {
		return nil
	}

	// 1. Advisory lock
	if err := r.AcquireRoomAdvisoryLockTx(ctx, tx, roomID); err != nil {
		return err
	}

	// 2. Check room active
	var isActive bool
	var roomName string
	err := tx.QueryRow(ctx, "SELECT name, is_active FROM meeting_rooms WHERE id = $1 AND deleted_at IS NULL FOR SHARE", roomID).Scan(&roomName, &isActive)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return apperrors.RoomNotFound()
		}
		return fmt.Errorf("verify room in tx: %w", err)
	}
	if !isActive {
		return apperrors.RoomInactive()
	}

	// 3. Verify each slot before inserting
	for _, b := range bookings {
		free, conflicting, err := r.CheckRoomSlotAvailableTx(ctx, tx, roomID, b.StartTime, b.EndTime, nil, excludeEventID)
		if err != nil {
			return err
		}
		if !free && conflicting != nil {
			msg := fmt.Sprintf("Переговорная '%s' уже занята %s с %s по %s",
				roomName,
				conflicting.StartTime.Format("02.01.2006"),
				conflicting.StartTime.Format("15:04"),
				conflicting.EndTime.Format("15:04"),
			)
			return apperrors.RoomAlreadyBooked(msg).WithDetails(map[string]any{
				"room_id":          roomID,
				"room_name":        roomName,
				"conflicting_slot": map[string]any{"start": conflicting.StartTime, "end": conflicting.EndTime},
			})
		}
	}

	// 4. Insert all slots
	now := time.Now()
	for i := range bookings {
		b := &bookings[i]
		if b.ID == uuid.Nil {
			b.ID = uuid.New()
		}
		b.Status = "confirmed"
		b.CreatedAt = now
		b.UpdatedAt = now

		insertQuery := `
			INSERT INTO room_bookings (
				id, room_id, event_id, user_id, title, description, start_time, end_time, status, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		`
		_, err = tx.Exec(ctx, insertQuery,
			b.ID, b.RoomID, b.EventID, b.UserID, b.Title, b.Description,
			b.StartTime, b.EndTime, b.Status, b.CreatedAt, b.UpdatedAt,
		)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23P01" {
				return apperrors.RoomAlreadyBooked(fmt.Sprintf("Переговорная '%s' уже забронирована на указанное время", roomName))
			}
			return calendarResourceError(err)
		}
	}
	return nil
}

// DeleteRoomBookingsByEventIDTx cancels and soft deletes all bookings associated with an event.
func (r *CalendarRepository) DeleteRoomBookingsByEventIDTx(ctx context.Context, tx pgx.Tx, eventID uuid.UUID) error {
	query := `
		UPDATE room_bookings
		SET deleted_at = NOW(), updated_at = NOW(), status = 'cancelled'
		WHERE event_id = $1 AND deleted_at IS NULL
	`
	_, err := tx.Exec(ctx, query, eventID)
	return err
}

// DeleteRoomBookingByOccurrenceTx cancels and soft deletes a specific occurrence booking for an event.
func (r *CalendarRepository) DeleteRoomBookingByOccurrenceTx(ctx context.Context, tx pgx.Tx, eventID uuid.UUID, startTime, endTime time.Time) error {
	query := `
		UPDATE room_bookings
		SET deleted_at = NOW(), updated_at = NOW(), status = 'cancelled'
		WHERE event_id = $1
		  AND start_time = $2
		  AND end_time = $3
		  AND deleted_at IS NULL
	`
	_, err := tx.Exec(ctx, query, eventID, startTime, endTime)
	return err
}

// GetRoomBookingByID fetches a single booking with room details.
func (r *CalendarRepository) GetRoomBookingByID(ctx context.Context, id uuid.UUID) (*domain.RoomBooking, error) {
	query := `
		SELECT rb.id, rb.room_id, rb.event_id, rb.user_id, rb.title, rb.description,
		       rb.start_time, rb.end_time, rb.status, rb.created_at, rb.updated_at
		FROM room_bookings rb
		WHERE rb.id = $1 AND rb.deleted_at IS NULL
	`
	var b domain.RoomBooking
	err := r.db.QueryRow(ctx, query, id).Scan(
		&b.ID, &b.RoomID, &b.EventID, &b.UserID, &b.Title, &b.Description,
		&b.StartTime, &b.EndTime, &b.Status, &b.CreatedAt, &b.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get room booking: %w", err)
	}
	return &b, nil
}

// CancelRoomBooking cancels a direct booking if the user is the owner.
func (r *CalendarRepository) CancelRoomBooking(ctx context.Context, id uuid.UUID, userID int64) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var eventID *uuid.UUID
	err = tx.QueryRow(ctx, `SELECT event_id FROM room_bookings WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL`, id, userID).Scan(&eventID)
	if errors.Is(err, pgx.ErrNoRows) {
		return apperrors.NotFound("booking not found")
	}
	if err != nil {
		return err
	}
	if eventID != nil {
		if err := r.lockCalendarEventTx(ctx, tx, *eventID, userID, nil); err != nil {
			return err
		}
		var rule *string
		if err := tx.QueryRow(ctx, `SELECT recurrence_rule FROM events WHERE id=$1`, *eventID).Scan(&rule); err != nil {
			return err
		}
		if rule != nil {
			return apperrors.BadRequest("cancel recurring reservations through the calendar occurrence endpoint")
		}
		if _, err := tx.Exec(ctx, `UPDATE events SET room_id=NULL,location=CASE WHEN location=room_snapshot->>'name' THEN NULL ELSE location END,updated_at=clock_timestamp() WHERE id=$1`, *eventID); err != nil {
			return err
		}
	}
	query := `
		UPDATE room_bookings
		SET status = 'cancelled', deleted_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL
	`
	tag, err := tx.Exec(ctx, query, id, userID)
	if err != nil {
		return fmt.Errorf("cancel room booking: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return apperrors.NotFound("Бронирование не найдено или у вас нет прав на его отмену")
	}
	return tx.Commit(ctx)
}
