package service

import (
	"context"
	"time"

	"github.com/west6ide/workspace-backend/internal/domain"
)

func (s *CalendarService) GetUpcomingEvents(ctx context.Context, userID int64, limit int) ([]domain.Event, error) {
	if limit <= 0 {
		limit = 3
	}
	if limit > 100 {
		limit = 100
	}

	now := time.Now()
	horizon := now.AddDate(1, 0, 0)
	base, err := s.repo.List(ctx, userID, userID, now, horizon)
	if err != nil {
		return nil, err
	}
	events, err := s.expandEventsWithExceptions(ctx, base, now, horizon)
	if err != nil {
		return nil, err
	}
	if len(events) > limit {
		events = events[:limit]
	}

	// Set IsOrganizer flag for mobile apps
	for i := range events {
		events[i].IsOrganizer = (events[i].UserID == userID)
	}

	return events, nil
}
