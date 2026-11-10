package service

import (
	"testing"

	"github.com/west6ide/workspace-backend/internal/alem"
)

func TestIsModelAccessDenied(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "provider model policy error",
			err: &alem.APIError{
				StatusCode: 403,
				Details: alem.ErrorDetails{
					Type:    "team_model_access_denied",
					Message: "team not allowed to access model",
				},
			},
			want: true,
		},
		{
			name: "provider authentication error",
			err: &alem.APIError{
				StatusCode: 401,
				Details: alem.ErrorDetails{
					Type:    "invalid_api_key",
					Message: "invalid key",
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isModelAccessDenied(tt.err); got != tt.want {
				t.Fatalf("isModelAccessDenied() = %v, want %v", got, tt.want)
			}
		})
	}
}
