package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
)

func (s *CalendarService) ownedRecurringEvent(ctx context.Context, userID int64, eventID uuid.UUID) (*domain.Event, error) {
	event, err := s.repo.GetByID(ctx, eventID)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to load event", err)
	}
	if event == nil {
		return nil, apperrors.NotFound("event not found")
	}
	if event.UserID != userID {
		return nil, apperrors.Forbidden("only the organizer can change an occurrence")
	}
	if event.RecurrenceRule == nil {
		return nil, apperrors.BadRequest("event is not recurring")
	}
	return event, nil
}

func originalOccurrence(event domain.Event, start time.Time) (*domain.Event, error) {
	if start.Nanosecond() != 0 {
		return nil, apperrors.BadRequest("occurrence timestamp must have second precision")
	}
	for _, candidate := range expandRecurring(event, start, start.Add(time.Second)) {
		if candidate.StartTime.Unix() == start.Unix() {
			return &candidate, nil
		}
	}
	return nil, apperrors.NotFound("occurrence not found in this series")
}

func (s *CalendarService) UpdateOccurrence(ctx context.Context, userID int64, eventID uuid.UUID, occurrenceStart time.Time, req domain.UpdateOccurrenceRequest) (*domain.CalendarEventException, error) {
	force := req.Force != nil && *req.Force
	return s.UpdateOccurrenceWithOptions(ctx, userID, eventID, occurrenceStart, req, force)
}

func (s *CalendarService) UpdateOccurrenceWithOptions(ctx context.Context, userID int64, eventID uuid.UUID, occurrenceStart time.Time, req domain.UpdateOccurrenceRequest, force bool) (*domain.CalendarEventException, error) {
	event, err := s.ownedRecurringEvent(ctx, userID, eventID)
	if err != nil {
		return nil, err
	}
	occ, err := originalOccurrence(*event, occurrenceStart)
	if err != nil {
		return nil, err
	}
	if req.Title == nil && req.Description == nil && req.StartTime == nil && req.EndTime == nil && req.Location == nil && req.IsAllDay == nil && req.RoomID == nil {
		return nil, apperrors.BadRequest("at least one editable field is required")
	}
	// Start from the existing override so PATCH-like updates do not discard it.
	previous, err := s.repo.ListEventExceptions(ctx, []uuid.UUID{eventID})
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to load exceptions", err)
	}
	details := domain.OccurrenceDetails{Title: occ.Title, Description: occ.Description, StartTime: occ.StartTime, EndTime: occ.EndTime, Location: occ.Location, RoomID: occ.RoomID, Room: occ.Room, IsAllDay: occ.IsAllDay}
	for _, item := range previous {
		if item.OccurrenceDate.Equal(occurrenceStart) && item.ExceptionType == "modified" && item.EventData != nil {
			details = *item.EventData
			if details.RoomID == nil {
				details.RoomID = occ.RoomID
			}
			break
		}
	}
	if req.Title != nil {
		details.Title = strings.TrimSpace(*req.Title)
	}
	if req.Description != nil {
		details.Description = req.Description
	}
	if req.StartTime != nil {
		details.StartTime = *req.StartTime
	}
	if req.EndTime != nil {
		details.EndTime = *req.EndTime
	}
	if req.Location != nil {
		details.Location = req.Location
	}
	if req.RoomID != nil {
		details.RoomID = req.RoomID
		details.Room = nil
		if *req.RoomID != uuid.Nil {
			room, roomErr := s.getCompanyRoom(ctx, userID, *req.RoomID)
			if roomErr != nil {
				return nil, roomErr
			}
			if !room.IsActive {
				return nil, apperrors.RoomInactive()
			}
			details.Room = room
		}
	}
	if req.IsAllDay != nil {
		details.IsAllDay = *req.IsAllDay
	}
	if details.Title == "" || len([]rune(details.Title)) > 255 || details.StartTime.IsZero() || details.EndTime.IsZero() || !details.EndTime.After(details.StartTime) || (details.Location != nil && len([]rune(*details.Location)) > 255) {
		return nil, apperrors.BadRequest("invalid occurrence title or time interval")
	}

	if (req.StartTime != nil || req.EndTime != nil) && !force && s.availabilityService != nil {
		attendeeIDs := make([]int64, 0, len(event.Attendees))
		for _, a := range event.Attendees {
			attendeeIDs = append(attendeeIDs, a.UserID)
		}
		if len(attendeeIDs) == 0 && event.UserID > 0 {
			attendeeIDs = append(attendeeIDs, event.UserID)
		}
		availability, err := s.availabilityService.checkAvailability(ctx, userID, domain.CalendarAvailabilityRequest{
			StartTime:      details.StartTime,
			EndTime:        details.EndTime,
			AttendeeIDs:    attendeeIDs,
			ExcludeEventID: &eventID,
		}, &occurrenceStart)
		if err != nil {
			return nil, err
		}
		if !availability.IsAvailable {
			return nil, apperrors.Conflict("calendar_conflict").WithDetails(map[string]any{
				"conflicts":       availability.Conflicts,
				"alerts":          availability.Alerts,
				"suggested_slots": availability.SuggestedSlots,
			})
		}
	}

	item, err := s.repo.SaveOccurrenceWithRoomBookings(ctx, *event, &domain.CalendarEventException{EventID: eventID, OccurrenceDate: occurrenceStart, ExceptionType: "modified", EventData: &details}, nil, roomBookingBuilder(*event))
	if err != nil {
		if apperrors.IsAppError(err) {
			return nil, err
		}
		return nil, apperrors.InternalWithErr("failed to save occurrence", err)
	}
	return item, nil
}

func (s *CalendarService) DeleteOccurrence(ctx context.Context, userID int64, eventID uuid.UUID, occurrenceStart time.Time) (*domain.CalendarEventException, error) {
	event, err := s.ownedRecurringEvent(ctx, userID, eventID)
	if err != nil {
		return nil, err
	}
	if _, err := originalOccurrence(*event, occurrenceStart); err != nil {
		return nil, err
	}
	item, err := s.repo.SaveOccurrenceWithRoomBookings(ctx, *event, &domain.CalendarEventException{EventID: eventID, OccurrenceDate: occurrenceStart, ExceptionType: "deleted"}, nil, roomBookingBuilder(*event))
	if err != nil {
		if apperrors.IsAppError(err) {
			return nil, err
		}
		return nil, apperrors.InternalWithErr("failed to delete occurrence", err)
	}
	return item, nil
}

func (s *CalendarService) ListOccurrenceExceptions(ctx context.Context, userID int64, eventID uuid.UUID) ([]domain.CalendarEventException, error) {
	if _, err := s.GetEvent(ctx, userID, eventID); err != nil {
		return nil, err
	}
	return s.repo.ListEventExceptions(ctx, []uuid.UUID{eventID})
}

func (s *CalendarService) RestoreOccurrence(ctx context.Context, userID int64, eventID, exceptionID uuid.UUID) error {
	event, err := s.ownedRecurringEvent(ctx, userID, eventID)
	if err != nil {
		return err
	}
	_, err = s.repo.SaveOccurrenceWithRoomBookings(ctx, *event, nil, &exceptionID, roomBookingBuilder(*event))
	if err != nil {
		if apperrors.IsAppError(err) {
			return err
		}
		return apperrors.InternalWithErr("failed to restore occurrence", err)
	}
	return nil
}

func roomBookingBuilder(event domain.Event) repository.RoomBookingBuilder {
	return func(exceptions []domain.CalendarEventException) ([]domain.RoomBooking, error) {
		return buildEventRoomBookings(event, exceptions)
	}
}

func applyCalendarExceptions(events []domain.Event, exceptions []domain.CalendarEventException, from, to time.Time) []domain.Event {
	byEvent := make(map[uuid.UUID]map[int64]domain.CalendarEventException)
	for _, item := range exceptions {
		if byEvent[item.EventID] == nil {
			byEvent[item.EventID] = make(map[int64]domain.CalendarEventException)
		}
		byEvent[item.EventID][item.OccurrenceDate.Unix()] = item
	}
	result := make([]domain.Event, 0, len(events))
	for _, event := range events {
		if event.RecurrenceRule == nil {
			result = append(result, event)
			continue
		}
		seen := make(map[int64]bool)
		for _, occ := range expandRecurring(event, from, to) {
			key := occ.StartTime.Unix()
			seen[key] = true
			if item, ok := byEvent[event.ID][key]; ok {
				if item.ExceptionType == "deleted" {
					continue
				}
				applyOccurrenceDetails(&occ, item.EventData)
			}
			if occ.EndTime.After(from) && occ.StartTime.Before(to) {
				result = append(result, occ)
			}
		}
		for key, item := range byEvent[event.ID] {
			if seen[key] || item.ExceptionType != "modified" || item.EventData == nil || !item.EventData.EndTime.After(from) || !item.EventData.StartTime.Before(to) {
				continue
			}
			// An override may move the occurrence into the requested range.
			original, err := originalOccurrence(event, item.OccurrenceDate)
			if err != nil {
				continue
			}
			applyOccurrenceDetails(original, item.EventData)
			result = append(result, *original)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].StartTime.Before(result[j].StartTime) })
	return result
}

func applyOccurrenceDetails(event *domain.Event, details *domain.OccurrenceDetails) {
	if details == nil {
		return
	}
	event.Title, event.Description, event.StartTime, event.EndTime = details.Title, details.Description, details.StartTime, details.EndTime
	event.Location, event.IsAllDay = details.Location, details.IsAllDay
	if details.RoomID != nil {
		if event.RoomID == nil || *event.RoomID != *details.RoomID {
			event.Room = nil
		}
		event.RoomID = details.RoomID
		if details.Room != nil {
			event.Room = details.Room
		}
		if *details.RoomID == uuid.Nil {
			event.RoomID, event.Room = nil, nil
		}
	}
	event.UpdateAttendanceSummary()
}

func exceptionEventIDs(events []domain.Event) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(events))
	for _, event := range events {
		if event.RecurrenceRule != nil {
			ids = append(ids, event.ID)
		}
	}
	return ids
}

func (s *CalendarService) expandEventsWithExceptions(ctx context.Context, events []domain.Event, from, to time.Time) ([]domain.Event, error) {
	items, err := s.repo.ListEventExceptions(ctx, exceptionEventIDs(events))
	if err != nil {
		return nil, fmt.Errorf("load calendar exceptions: %w", err)
	}
	return applyCalendarExceptions(events, items, from, to), nil
}

func (s *CalendarService) expandGroupCalendarGridEventsWithExceptions(ctx context.Context, events []domain.GroupEventInfo, from, to time.Time) ([]domain.GroupEventInfo, error) {
	base := make([]domain.Event, len(events))
	for i := range events {
		base[i] = events[i].Event
	}
	items, err := s.repo.ListEventExceptions(ctx, exceptionEventIDs(base))
	if err != nil {
		return nil, err
	}
	var result []domain.GroupEventInfo
	for _, groupEvent := range events {
		for _, occurrence := range applyCalendarExceptions([]domain.Event{groupEvent.Event}, items, from, to) {
			copyEvent := groupEvent
			copyEvent.Event = occurrence
			result = append(result, copyEvent)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].StartTime.Before(result[j].StartTime) })
	return result, nil
}
