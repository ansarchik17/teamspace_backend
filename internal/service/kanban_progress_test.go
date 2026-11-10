package service

import "testing"

func TestComputeBoardCompletionPercent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		manual    *int
		completed int
		total     int
		want      int
	}{
		{
			name:      "computed percent rounds from tasks",
			completed: 1,
			total:     6,
			want:      17,
		},
		{
			name:   "manual percent overrides task ratio",
			manual: intPtr(5),
			total:  6,
			want:   5,
		},
		{
			name:   "manual percent clamps low values",
			manual: intPtr(-10),
			want:   0,
		},
		{
			name:   "manual percent clamps high values",
			manual: intPtr(120),
			want:   100,
		},
		{
			name: "empty board without manual progress stays zero",
			want: 0,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := computeBoardCompletionPercent(tt.manual, tt.completed, tt.total)
			if got != tt.want {
				t.Fatalf("computeBoardCompletionPercent() = %d, want %d", got, tt.want)
			}
		})
	}
}

func intPtr(v int) *int {
	return &v
}
