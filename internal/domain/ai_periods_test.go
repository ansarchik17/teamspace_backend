package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAIPeriodAt(t *testing.T) {
	anchor := time.Date(2026, 10, 16, 5, 20, 0, 123456000, time.UTC)
	for _, duration := range []time.Duration{AIFiveHourDuration, AIWeeklyDuration} {
		for _, tt := range []struct {
			name string
			now  time.Time
			n    int
		}{
			{"first", anchor, 0},
			{"before end", anchor.Add(duration - time.Microsecond), 0},
			{"exact end", anchor.Add(duration), 1},
			{"after end", anchor.Add(duration + time.Microsecond), 1},
			{"idle", anchor.Add(19*duration + time.Minute), 19},
			{"other timezone", anchor.Add(duration).In(time.FixedZone("UTC+5", 5*3600)), 1},
		} {
			t.Run(duration.String()+"/"+tt.name, func(t *testing.T) {
				window, err := AIPeriodAt(anchor, tt.now, duration)
				require.NoError(t, err)
				require.Equal(t, anchor.Add(time.Duration(tt.n)*duration), window.StartsAt)
				require.Equal(t, duration, window.EndsAt.Sub(window.StartsAt))
				require.True(t, window.Contains(tt.now))
				require.False(t, window.Contains(window.EndsAt))
				require.True(t, window.Ended(window.EndsAt))
				require.False(t, window.Ended(window.EndsAt.Add(-time.Nanosecond)))
			})
		}
	}
	_, err := AIPeriodAt(anchor, anchor.Add(-time.Microsecond), AIFiveHourDuration)
	require.ErrorIs(t, err, ErrAIInvalidPeriodTime)
	_, err = AIPeriodAt(anchor, anchor, 24*time.Hour)
	require.ErrorIs(t, err, ErrAIInvalidPeriodTime)
	_, err = AIPeriodAt(anchor, time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC), AIWeeklyDuration)
	require.ErrorIs(t, err, ErrAIInvalidPeriodTime)
	_, err = AIPeriodAt(time.Date(9999, 12, 31, 23, 0, 0, 0, time.UTC), time.Date(9999, 12, 31, 23, 0, 0, 0, time.UTC), AIWeeklyDuration)
	require.ErrorIs(t, err, ErrAIInvalidPeriodTime)
}

func TestAIPeriodAtLongIdleAndPrecision(t *testing.T) {
	anchor := time.Date(1600, 1, 1, 0, 0, 0, 123456789, time.UTC)
	now := time.Date(2600, 1, 1, 0, 0, 0, 0, time.UTC)
	window, err := AIPeriodAt(anchor, now, AIWeeklyDuration)
	require.NoError(t, err)
	require.True(t, window.Contains(now))
	require.Zero(t, window.StartsAt.Nanosecond()%1000)
	require.Equal(t, int64(0), (window.StartsAt.UnixMicro()-anchor.Truncate(time.Microsecond).UnixMicro())%AIWeeklyDuration.Microseconds())
}
