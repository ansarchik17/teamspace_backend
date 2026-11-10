package domain

import "testing"

func TestNormalizeAnalyticsSection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "basic", input: "workspace", want: "workspace"},
		{name: "hyphen normalized", input: "alem-contact", want: "alem_contact"},
		{name: "trimmed and lowered", input: "  BOARD  ", want: "board"},
		{name: "unsupported", input: "admin", wantErr: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := NormalizeAnalyticsSection(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

func TestNormalizeAnalyticsPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "plain path", input: "/board?task=1", want: "/board"},
		{name: "absolute url", input: "https://example.com/web/notes#tab", want: "/web/notes"},
		{name: "missing slash", input: "calendar", want: "/calendar"},
		{name: "invalid", input: "://", want: ""},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := NormalizeAnalyticsPath(tt.input); got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}
