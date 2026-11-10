package service

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

// ListRooms returns meeting rooms filtered according to query criteria.
func (s *CalendarService) ListRooms(ctx context.Context, filter domain.RoomFilter) ([]domain.MeetingRoom, error) {
	org, manage, err := s.repo.MeetingRoomScope(ctx, filter.RequesterID)
	if err != nil {
		return nil, err
	}
	filter.OrganizationID = &org
	if !manage {
		filter.OnlyActive = true
	}
	rooms, err := s.repo.ListMeetingRooms(ctx, filter)
	if err != nil {
		return nil, calendarOperationError(err, "Не удалось получить список переговорных")
	}
	return rooms, nil
}

// GetRoom retrieves a meeting room by ID.
func (s *CalendarService) GetRoom(ctx context.Context, id uuid.UUID) (*domain.MeetingRoom, error) {
	return nil, apperrors.Forbidden("requester required")
}

func (s *CalendarService) GetRoomForUser(ctx context.Context, userID int64, id uuid.UUID) (*domain.MeetingRoom, error) {
	room, err := s.getCompanyRoom(ctx, userID, id)
	if err != nil {
		return nil, calendarOperationError(err, "Не удалось получить информацию о переговорной")
	}
	if room == nil {
		return nil, apperrors.RoomNotFound()
	}
	return room, nil
}

// CreateRoom adds a new meeting room.
func (s *CalendarService) CreateRoom(ctx context.Context, userID int64, req domain.CreateMeetingRoomRequest) (*domain.MeetingRoom, error) {
	if err := s.requireRoomAdmin(ctx, userID); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, apperrors.Validation("Название переговорной обязательно")
	}
	if req.Capacity <= 0 {
		return nil, apperrors.Validation("Вместимость переговорной должна быть больше нуля")
	}

	isActive := true
	if req.IsActive != nil {
		isActive = *req.IsActive
	}

	color := "#4F46E5"
	if req.Color != nil && strings.TrimSpace(*req.Color) != "" {
		color = strings.TrimSpace(*req.Color)
	}

	room := &domain.MeetingRoom{
		ID:          uuid.New(),
		SiteID:      req.SiteID,
		PhotoURL:    req.PhotoURL,
		Name:        name,
		Description: req.Description,
		Capacity:    req.Capacity,
		Floor:       req.Floor,
		Location:    req.Location,
		Color:       &color,
		Equipment:   req.Equipment,
		IsActive:    isActive,
	}

	org, err := s.roomScope(ctx, userID, true)
	if err != nil {
		return nil, err
	}
	room.OrganizationID = &org
	if room.SiteID == nil {
		sites, e := s.repo.ListMeetingSites(ctx, org, false)
		if e != nil {
			return nil, e
		}
		if len(sites) == 0 {
			return nil, apperrors.Validation("site required")
		}
		room.SiteID = &sites[0].ID
	}
	if err := validateRoomFields(room); err != nil {
		return nil, err
	}
	if err := s.repo.CreateMeetingRoom(ctx, room); err != nil {
		if apperrors.IsAppError(err) {
			return nil, err
		}
		return nil, calendarOperationError(err, "Не удалось создать переговорную")
	}

	s.logger.Info("Meeting room created",
		zap.String("room_id", room.ID.String()),
		zap.String("name", room.Name),
		zap.Int("capacity", room.Capacity),
	)
	return room, nil
}

// UpdateRoom updates properties of an existing meeting room.
func (s *CalendarService) UpdateRoom(ctx context.Context, userID int64, id uuid.UUID, req domain.UpdateMeetingRoomRequest) (*domain.MeetingRoom, error) {
	if err := s.requireRoomAdmin(ctx, userID); err != nil {
		return nil, err
	}
	room, err := s.getCompanyRoom(ctx, userID, id)
	if err != nil {
		return nil, calendarOperationError(err, "Не удалось найти переговорную")
	}
	if room == nil {
		return nil, apperrors.RoomNotFound()
	}

	if req.SiteID != nil {
		room.SiteID = req.SiteID
	}
	if req.PhotoURL != nil {
		room.PhotoURL = req.PhotoURL
	}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			return nil, apperrors.Validation("Название переговорной не может быть пустым")
		}
		room.Name = name
	}
	if req.Description != nil {
		room.Description = req.Description
	}
	if req.Capacity != nil {
		if *req.Capacity <= 0 {
			return nil, apperrors.Validation("Вместимость переговорной должна быть больше нуля")
		}
		room.Capacity = *req.Capacity
	}
	if req.Floor != nil {
		room.Floor = req.Floor
	}
	if req.Location != nil {
		room.Location = req.Location
	}
	if req.Color != nil {
		room.Color = req.Color
	}
	if req.Equipment != nil {
		room.Equipment = *req.Equipment
	}
	if req.IsActive != nil {
		room.IsActive = *req.IsActive
	}

	if err := validateRoomFields(room); err != nil {
		return nil, err
	}
	if err := s.repo.UpdateMeetingRoom(ctx, room); err != nil {
		if apperrors.IsAppError(err) {
			return nil, err
		}
		return nil, calendarOperationError(err, "Не удалось обновить переговорную")
	}
	s.dispatchMeetingChanges(ctx)
	return room, nil
}

// DeleteRoom soft-deletes a meeting room.
func (s *CalendarService) DeleteRoom(ctx context.Context, userID int64, id uuid.UUID) error {
	if err := s.requireRoomAdmin(ctx, userID); err != nil {
		return err
	}
	room, err := s.getCompanyRoom(ctx, userID, id)
	if err != nil {
		return calendarOperationError(err, "Не удалось найти переговорную")
	}
	if room == nil {
		return apperrors.RoomNotFound()
	}

	if err := s.repo.DeleteMeetingRoom(ctx, id); err != nil {
		if apperrors.IsAppError(err) {
			return err
		}
		return calendarOperationError(err, "Не удалось удалить переговорную")
	}
	return nil
}

// GetRoomAvailability returns schedule, busy intervals, free slots, and grid for a specific room.
func (s *CalendarService) GetRoomAvailability(
	ctx context.Context,
	userID int64,
	roomID uuid.UUID,
	query domain.RoomAvailabilityQuery,
) (*domain.RoomAvailabilityResponse, error) {
	query, err := normalizeRoomAvailabilityQuery(query)
	if err != nil {
		return nil, err
	}
	room, err := s.getCompanyRoom(ctx, userID, roomID)
	if err != nil {
		return nil, calendarOperationError(err, "Не удалось найти переговорную")
	}
	if room == nil {
		return nil, apperrors.RoomNotFound()
	}

	start := query.StartTime
	end := query.EndTime
	if start.IsZero() {
		now := time.Now()
		start = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	}
	if end.IsZero() {
		end = start.Add(24 * time.Hour)
	}
	if !end.After(start) {
		return nil, apperrors.BadRequest("Конец интервала должен быть позже начала")
	}

	rawBookings, err := s.repo.ListRoomBookings(ctx, roomID, start, end)
	if err != nil {
		return nil, calendarOperationError(err, "Не удалось получить бронирования переговорной")
	}

	// Filter out excluded bookings / events
	bookings := make([]domain.RoomBooking, 0, len(rawBookings))
	for _, b := range rawBookings {
		if query.ExcludeBookingID != nil && b.ID == *query.ExcludeBookingID {
			continue
		}
		if query.ExcludeEventID != nil && b.EventID != nil && *b.EventID == *query.ExcludeEventID {
			continue
		}
		bookings = append(bookings, b)
	}
	for i := range bookings {
		bookings[i] = domain.RedactRoomBooking(bookings[i], userID)
	}

	sort.Slice(bookings, func(i, j int) bool {
		return bookings[i].StartTime.Before(bookings[j].StartTime)
	})

	// Check if available for the requested range
	isAvailable := true
	for _, b := range bookings {
		if b.StartTime.Before(end) && b.EndTime.After(start) {
			isAvailable = false
			break
		}
	}

	// Compute free slots
	var freeSlots []domain.RoomFreeSlot
	cur := start
	for _, b := range bookings {
		bStart := b.StartTime
		if bStart.Before(start) {
			bStart = start
		}
		bEnd := b.EndTime
		if bEnd.After(end) {
			bEnd = end
		}

		if bStart.After(cur) {
			dur := int(bStart.Sub(cur).Minutes())
			if dur > 0 {
				freeSlots = append(freeSlots, domain.RoomFreeSlot{
					StartTime:       cur,
					EndTime:         bStart,
					DurationMinutes: dur,
				})
			}
		}
		if bEnd.After(cur) {
			cur = bEnd
		}
	}
	if cur.Before(end) {
		dur := int(end.Sub(cur).Minutes())
		if dur > 0 {
			freeSlots = append(freeSlots, domain.RoomFreeSlot{
				StartTime:       cur,
				EndTime:         end,
				DurationMinutes: dur,
			})
		}
	}

	// Compute grid slots if step is specified
	step := query.SlotStepMinutes
	if step <= 0 {
		step = 30
	}
	if step < 5 {
		step = 5
	}
	if step > 240 {
		step = 240
	}

	var gridSlots []domain.RoomSlot
	slotDuration := time.Duration(step) * time.Minute
	for sTime := start; sTime.Before(end); sTime = sTime.Add(slotDuration) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		sEnd := sTime.Add(slotDuration)
		if sEnd.After(end) {
			sEnd = end
		}

		slot := domain.RoomSlot{
			StartTime: sTime,
			EndTime:   sEnd,
			IsBusy:    false,
		}

		for _, b := range bookings {
			if b.StartTime.Before(sEnd) && b.EndTime.After(sTime) {
				slot.IsBusy = true
				if b.UserID == userID {
					slot.BookingID = &b.ID
					slot.UserID = &b.UserID
				}
				slot.EventID = b.EventID
				slot.Title = &b.Title
				if b.User != nil {
					fullName := strings.TrimSpace(b.User.FirstName + " " + b.User.LastName)
					if fullName != "" {
						slot.UserName = &fullName
					} else {
						slot.UserName = &b.User.Username
					}
				}
				break
			}
		}
		gridSlots = append(gridSlots, slot)
	}

	return &domain.RoomAvailabilityResponse{
		Room:        *room,
		StartTime:   start,
		EndTime:     end,
		IsAvailable: isAvailable,
		Bookings:    bookings,
		FreeSlots:   freeSlots,
		Slots:       gridSlots,
	}, nil
}

// GetRoomsAvailability returns availability summary for all rooms matching filter criteria.
func (s *CalendarService) GetRoomsAvailability(
	ctx context.Context,
	userID int64,
	query domain.RoomAvailabilityQuery,
) ([]domain.RoomAvailabilityResponse, error) {
	query, err := normalizeRoomAvailabilityQuery(query)
	if err != nil {
		return nil, err
	}
	start := query.StartTime
	end := query.EndTime
	if start.IsZero() {
		start = time.Now()
	}
	if end.IsZero() {
		end = start.Add(time.Hour)
	}
	if !end.After(start) {
		return nil, apperrors.BadRequest("Конец интервала должен быть позже начала")
	}

	filter := domain.RoomFilter{
		OnlyActive:  true,
		RequesterID: userID,
		MinCapacity: query.Capacity,
		Equipment:   query.Equipment,
	}

	rooms, err := s.ListRooms(ctx, filter)
	if err != nil {
		return nil, calendarOperationError(err, "Не удалось получить список комнат")
	}
	if len(rooms) > 100 {
		return nil, apperrors.BadRequest("too many rooms; narrow the room filter")
	}
	slots := int((query.EndTime.Sub(query.StartTime)-1)/(time.Duration(query.SlotStepMinutes)*time.Minute)) + 1
	if slots*len(rooms) > 100000 {
		return nil, apperrors.BadRequest("room availability grid is too large")
	}

	results := make([]domain.RoomAvailabilityResponse, 0, len(rooms))
	for _, room := range rooms {
		resp, err := s.GetRoomAvailability(ctx, userID, room.ID, query)
		if err != nil {
			return nil, err
		}
		results = append(results, *resp)
	}
	return results, nil
}

// BookRoom creates an atomic direct booking for a meeting room with zero-race-condition guarantees.
func (s *CalendarService) BookRoom(
	ctx context.Context,
	userID int64,
	roomID uuid.UUID,
	req domain.BookRoomRequest,
) (*domain.RoomBooking, error) {
	title := strings.TrimSpace(req.Title)
	if title == "" || len([]rune(title)) > 255 {
		return nil, apperrors.Validation("Название бронирования обязательно")
	}
	if req.StartTime.IsZero() || req.EndTime.IsZero() || !req.EndTime.After(req.StartTime) || req.EndTime.Sub(req.StartTime) > 90*24*time.Hour {
		return nil, apperrors.BadRequest("Время окончания бронирования должно быть позже времени начала")
	}

	room, err := s.getCompanyRoom(ctx, userID, roomID)
	if err != nil {
		return nil, calendarOperationError(err, "Не удалось загрузить переговорную")
	}
	if room == nil {
		return nil, apperrors.RoomNotFound()
	}
	if !room.IsActive {
		return nil, apperrors.RoomInactive()
	}

	booking := &domain.RoomBooking{
		ID:          uuid.New(),
		RoomID:      roomID,
		EventID:     req.EventID,
		UserID:      userID,
		Title:       title,
		Description: req.Description,
		StartTime:   req.StartTime,
		EndTime:     req.EndTime,
		Status:      "confirmed",
	}

	// If requested, also create a calendar event for the user so it displays on their calendar
	if req.CreateCalendarEvent && req.EventID == nil {
		cal, err := s.repo.EnsureDefaultCalendar(ctx, userID)
		if err != nil {
			return nil, calendarOperationError(err, "Не удалось получить календарь пользователя")
		}

		event := &domain.Event{
			ID:              uuid.New(),
			UserID:          userID,
			CalendarID:      cal.ID,
			Title:           title,
			Description:     req.Description,
			StartTime:       req.StartTime,
			EndTime:         req.EndTime,
			RoomID:          &roomID,
			Room:            room,
			Location:        &room.Name,
			Color:           room.Color,
			MeetingProvider: "none",
			MeetingStatus:   "none",
		}
		booking.EventID = &event.ID
		event.CreatedAt, event.UpdatedAt = time.Now(), time.Now()

		err = s.repo.CreateEventWithRoomBookings(ctx, event, []int64{userID}, nil, []domain.RoomBooking{*booking})
		if err != nil {
			if apperrors.IsAppError(err) {
				return nil, err
			}
			return nil, calendarOperationError(err, "Не удалось создать бронирование")
		}
		booking.Room = room
		booking.CreatedAt, booking.UpdatedAt = event.CreatedAt, event.UpdatedAt
		return booking, nil
	}

	if err := s.repo.BookRoomAtomic(ctx, booking); err != nil {
		if apperrors.IsAppError(err) {
			return nil, err
		}
		return nil, calendarOperationError(err, "Не удалось сохранить бронирование")
	}

	booking.Room = room
	return booking, nil
}

func requireRoomAdmin(ctx context.Context, users calendarInviteUserRepo, userID int64) error {
	if users == nil {
		return apperrors.Forbidden("only an active administrator can manage meeting rooms")
	}
	user, err := users.GetByID(ctx, userID)
	if err != nil {
		return calendarOperationError(err, "failed to check room administrator")
	}
	if user == nil || !user.IsActive || user.DeletedAt != nil || user.Role != domain.RoleSuperAdmin {
		return apperrors.Forbidden("only an active administrator can manage meeting rooms")
	}
	return nil
}

func (s *CalendarService) requireRoomAdmin(ctx context.Context, userID int64) error {
	_, err := s.roomScope(ctx, userID, true)
	return err
}

func normalizeRoomAvailabilityQuery(query domain.RoomAvailabilityQuery) (domain.RoomAvailabilityQuery, error) {
	if query.StartTime.IsZero() {
		now := time.Now().UTC()
		query.StartTime = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	}
	if query.EndTime.IsZero() {
		query.EndTime = query.StartTime.Add(24 * time.Hour)
	}
	if !query.EndTime.After(query.StartTime) || query.EndTime.Sub(query.StartTime) > 90*24*time.Hour {
		return query, apperrors.BadRequest("room availability range must be positive and at most 90 days")
	}
	if query.SlotStepMinutes == 0 {
		query.SlotStepMinutes = 30
	}
	if query.SlotStepMinutes < 5 || query.SlotStepMinutes > 240 {
		return query, apperrors.BadRequest("slot_step_minutes must be between 5 and 240")
	}
	step := time.Duration(query.SlotStepMinutes) * time.Minute
	if int((query.EndTime.Sub(query.StartTime)-1)/step)+1 > 5000 {
		return query, apperrors.BadRequest("room availability grid cannot exceed 5000 slots")
	}
	return query, nil
}

// Every occurrence is reserved; unbounded or oversized series are rejected.
func buildEventRoomBookings(event domain.Event, exceptions []domain.CalendarEventException) ([]domain.RoomBooking, error) {
	hasRoom := event.RoomID != nil
	for _, ex := range exceptions {
		if ex.EventData != nil && ex.EventData.RoomID != nil && *ex.EventData.RoomID != uuid.Nil {
			hasRoom = true
		}
	}
	if !hasRoom {
		return nil, nil
	}
	if event.StartTime.IsZero() || !event.EndTime.After(event.StartTime) {
		return nil, apperrors.BadRequest("invalid room booking interval")
	}
	occurrences := []domain.Event{event}
	if event.RecurrenceRule != nil {
		if event.RecurrenceUntil == nil || event.RecurrenceUntil.Before(event.StartTime) || event.RecurrenceUntil.After(event.StartTime.AddDate(1, 0, 0)) {
			return nil, apperrors.BadRequest("room booking series requires recurrence_until within one year of start_time")
		}
		from, to := event.StartTime, event.RecurrenceUntil.Add(event.EndTime.Sub(event.StartTime)).Add(time.Nanosecond)
		for _, ex := range exceptions {
			if ex.EventData != nil {
				if ex.EventData.StartTime.Before(from) {
					from = ex.EventData.StartTime
				}
				if !ex.EventData.EndTime.Before(to) {
					to = ex.EventData.EndTime.Add(time.Nanosecond)
				}
			}
		}
		occurrences = applyCalendarExceptions([]domain.Event{event}, exceptions, from, to)
		if len(occurrences) > 366 {
			return nil, apperrors.BadRequest("room booking series cannot exceed 366 occurrences")
		}
	}
	bookings := make([]domain.RoomBooking, 0, len(occurrences))
	for _, occ := range occurrences {
		if occ.RoomID == nil {
			continue
		}
		bookings = append(bookings, domain.RoomBooking{ID: uuid.New(), RoomID: *occ.RoomID, EventID: &event.ID,
			UserID: event.UserID, Title: occ.Title, Description: occ.Description, StartTime: occ.StartTime,
			EndTime: occ.EndTime, Status: "confirmed"})
	}
	return bookings, nil
}

// CancelRoomBooking cancels a direct room booking.
func (s *CalendarService) CancelRoomBooking(ctx context.Context, userID int64, bookingID uuid.UUID) error {
	booking, err := s.repo.GetRoomBookingByID(ctx, bookingID)
	if err != nil {
		return calendarOperationError(err, "Не удалось проверить бронирование")
	}
	if booking == nil {
		return apperrors.NotFound("Бронирование не найдено")
	}

	// If linked to a calendar event and the user is the owner, delete the event or cancel booking
	if err := s.repo.CancelRoomBooking(ctx, bookingID, userID); err != nil {
		return err
	}

	s.logger.Info("Room booking cancelled",
		zap.String("booking_id", bookingID.String()),
		zap.Int64("user_id", userID),
	)
	return nil
}
