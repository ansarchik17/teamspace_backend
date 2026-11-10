package service

import (
	"errors"
	"net/http"
	"strings"

	"github.com/west6ide/workspace-backend/internal/alem"
)

// isModelAccessDenied identifies a provider response caused by a model access
// policy, rather than a general authentication or transport failure. This lets
// old conversations recover once to the configured production model.
func isModelAccessDenied(err error) bool {
	var apiErr *alem.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusForbidden {
		return false
	}
	text := strings.ToLower(strings.Join([]string{
		apiErr.Details.Code,
		apiErr.Details.Type,
		apiErr.Details.Message,
	}, " "))
	return strings.Contains(text, "model") &&
		(strings.Contains(text, "access") || strings.Contains(text, "allowed") || strings.Contains(text, "denied"))
}
