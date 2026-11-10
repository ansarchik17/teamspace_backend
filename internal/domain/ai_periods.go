package domain

import (
	"errors"
	"time"
)

const (
	AIWeeklyDuration   = 168 * time.Hour
	AIFiveHourDuration = 5 * time.Hour
)

var ErrAIInvalidPeriodTime = errors.New("invalid AI period time or duration")

type AIPeriodWindow struct {
	StartsAt time.Time
	EndsAt   time.Time
}

// AIPeriodAt computes a fixed grid, never a sliding window. Times use the same
// microsecond precision as PostgreSQL. Supported years keep integer subtraction
// exact and avoid time.Duration's ~290 year saturation for long idle accounts.
func AIPeriodAt(anchor, now time.Time, duration time.Duration) (AIPeriodWindow, error) {
	if (duration != AIWeeklyDuration && duration != AIFiveHourDuration) ||
		anchor.Year() < 1 || anchor.Year() > 9999 || now.Year() < 1 || now.Year() > 9999 {
		return AIPeriodWindow{}, ErrAIInvalidPeriodTime
	}
	anchor = anchor.UTC().Truncate(time.Microsecond)
	now = now.UTC().Truncate(time.Microsecond)
	if now.Before(anchor) {
		return AIPeriodWindow{}, ErrAIInvalidPeriodTime
	}
	step := duration.Microseconds()
	start := anchor.UnixMicro() + ((now.UnixMicro()-anchor.UnixMicro())/step)*step
	window := AIPeriodWindow{StartsAt: time.UnixMicro(start).UTC(), EndsAt: time.UnixMicro(start + step).UTC()}
	if window.EndsAt.Year() > 9999 {
		return AIPeriodWindow{}, ErrAIInvalidPeriodTime
	}
	return window, nil
}

func (w AIPeriodWindow) Contains(at time.Time) bool {
	return !at.Before(w.StartsAt) && at.Before(w.EndsAt)
}

// Ended is logical closure. History and unresolved reservations are retained.
func (w AIPeriodWindow) Ended(at time.Time) bool { return !at.Before(w.EndsAt) }
