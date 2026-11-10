package repository

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestAIQuotaWindowPresentation(t *testing.T) {
	for _, tt := range []struct {
		name           string
		limit          *int64
		used, reserved int64
		state          string
		available      int64
		percent        *int
	}{
		{"unconfigured", nil, 10, 20, "unconfigured", 0, nil},
		{"zero", aiQuotaAmount(0), 10, 20, "disabled", 0, nil},
		{"exhausted", aiQuotaAmount(100), 80, 30, "exhausted", 0, aiQuotaPercent(0)},
		{"less than one percent", aiQuotaAmount(1000), 999, 0, "active", 1, aiQuotaPercent(0)},
		{"overflow safe percent", aiQuotaAmount(math.MaxInt64), 0, 0, "active", math.MaxInt64, aiQuotaPercent(100)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := domain.AIQuotaWindow{Limit: tt.limit, Used: tt.used, Reserved: tt.reserved}
			finishAIQuotaWindow(&w)
			require.Equal(t, tt.state, w.State)
			require.Equal(t, tt.percent, w.RemainingPercent)
			if tt.limit == nil {
				require.Nil(t, w.Available)
			} else {
				require.Equal(t, tt.available, *w.Available)
			}
		})
	}
}

func aiQuotaAmount(n int64) *int64 { return &n }
func aiQuotaPercent(n int) *int    { return &n }
