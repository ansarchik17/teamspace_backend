package repository

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestAIReservationCapacityDenied(t *testing.T) {
	reset := time.Now().Add(5 * time.Hour)
	for _, scope := range []string{"company", "weekly", "five_hour"} {
		for _, tc := range []struct {
			name                          string
			amount, used, held, available int64
			wantReason                    string
		}{
			{"full quota cannot fit request", 101, 0, 0, 100, "RESERVATION_TOO_LARGE"},
			{"positive remainder is not exhaustion", 50, 80, 0, 20, "RESERVATION_TOO_LARGE"},
			{"active holds", 50, 0, 80, 20, "RESERVATION_PENDING"},
			{"spent quota", 1, 100, 0, 0, "FIVE_HOUR_EXHAUSTED"},
			{"fully held quota", 1, 0, 100, 0, "RESERVATION_PENDING"},
			{"holds cannot explain insufficient unreserved budget", 50, 80, 10, 10, "RESERVATION_TOO_LARGE"},
		} {
			t.Run(scope+"/"+tc.name, func(t *testing.T) {
				var denied *domain.AIReservationDenied
				require.ErrorAs(t, reservationCapacityDenied("FIVE_HOUR_EXHAUSTED", scope, &reset, tc.amount, 100, tc.used, tc.held), &denied)
				require.Equal(t, scope, denied.Scope)
				require.Equal(t, tc.available, *denied.AvailableMicrocredits)
				require.Equal(t, tc.wantReason, denied.Reason)
				if tc.wantReason != "FIVE_HOUR_EXHAUSTED" {
					require.Nil(t, denied.RecheckAt)
				} else {
					require.Equal(t, "FIVE_HOUR_EXHAUSTED", denied.Reason)
					require.Equal(t, &reset, denied.RecheckAt)
				}
			})
		}
	}
}

func TestAIReservationChoosesActualLimitingWindow(t *testing.T) {
	week := time.Now().Add(7 * 24 * time.Hour)
	five := time.Now().Add(5 * time.Hour)
	gates := []aiReservationCapacity{
		{"COMPANY_WEEKLY_EXHAUSTED", "company", &week, 100_000_000_000, 0, 0},
		{"USER_WEEKLY_EXHAUSTED", "weekly", &week, 1_000_000_000, 164_180_000, 0},
		{"FIVE_HOUR_EXHAUSTED", "five_hour", &five, 200_000_000, 164_180_000, 0},
	}
	for _, amount := range []int64{35_820_001, 900_000_000, 2_000_000_000} {
		var denied *domain.AIReservationDenied
		require.ErrorAs(t, limitingAIReservationCapacity(amount, gates), &denied)
		require.Equal(t, "five_hour", denied.Scope, "35.82 remaining in five hours must win over 835.82 in the week")
		require.Equal(t, "RESERVATION_TOO_LARGE", denied.Reason)
		require.EqualValues(t, 35_820_000, *denied.AvailableMicrocredits)
		require.Nil(t, denied.RecheckAt)
	}
	require.NoError(t, limitingAIReservationCapacity(35_820_000, gates), "exactly the available balance must be accepted")
	gates[2].used = gates[2].limit
	var denied *domain.AIReservationDenied
	require.ErrorAs(t, limitingAIReservationCapacity(1, gates), &denied)
	require.Equal(t, "FIVE_HOUR_EXHAUSTED", denied.Reason)
	gates[1].used = gates[1].limit
	require.ErrorAs(t, limitingAIReservationCapacity(1, gates), &denied)
	require.Equal(t, "USER_WEEKLY_EXHAUSTED", denied.Reason, "simultaneously empty windows must use the later reset")
	require.Equal(t, &week, denied.RecheckAt)
}
