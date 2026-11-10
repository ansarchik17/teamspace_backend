package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/alem"
	"github.com/west6ide/workspace-backend/internal/domain"
)

type calendarCancellationService interface {
	GetEvent(context.Context, int64, uuid.UUID) (*domain.Event, error)
	DeleteEvent(context.Context, int64, uuid.UUID) error
}

// The selector ("tomorrow") is user input, not an authoritative event title.
// Read the live event with the caller's ACL before deleting it.
func cancelCalendarActionEvent(ctx context.Context, calendar calendarCancellationService, userID int64, eventID uuid.UUID) (*domain.Event, error) {
	event, err := calendar.GetEvent(ctx, userID, eventID)
	if err != nil {
		return nil, err
	}
	if event == nil || event.ID != eventID {
		return nil, fmt.Errorf("calendar event is unavailable")
	}
	if err := calendar.DeleteEvent(ctx, userID, eventID); err != nil {
		return nil, err
	}
	return event, nil
}

func chatCalendarActionFields(args map[string]any, event *domain.Event) []alem.DataCardField {
	fields := chatActionFields(args)
	if event == nil {
		return fields
	}
	start, end := event.StartTime, event.EndTime
	if loc, err := time.LoadLocation(event.Timezone); event.Timezone != "" && err == nil {
		start, end = start.In(loc), end.In(loc)
	}
	fields = append(fields, alem.DataCardField{Label: "Название", Value: event.Title}, alem.DataCardField{Label: "Начало", Value: start.Format("2006-01-02T15:04:05Z07:00")}, alem.DataCardField{Label: "Окончание", Value: end.Format("2006-01-02T15:04:05Z07:00")})
	return fields
}
