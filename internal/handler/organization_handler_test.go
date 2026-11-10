package handler

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestOrganizationAppErrorCurrentOrganizationNotFound(t *testing.T) {
	err := fmt.Errorf("resolve organization scope: %w", domain.ErrOrganizationNotFound)
	appErr := organizationAppError(err)

	if appErr.StatusCode != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d", http.StatusNotFound, appErr.StatusCode)
	}
	if appErr.Code != apperrors.ErrCodeNotFound {
		t.Fatalf("expected code %q, got %q", apperrors.ErrCodeNotFound, appErr.Code)
	}
}

func TestOrganizationAppErrorScopeFailureIsInternal(t *testing.T) {
	appErr := organizationAppError(fmt.Errorf("resolve organization scope: database unavailable"))

	if appErr.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected status %d, got %d", http.StatusInternalServerError, appErr.StatusCode)
	}
	if appErr.Code != apperrors.ErrCodeInternal {
		t.Fatalf("expected code %q, got %q", apperrors.ErrCodeInternal, appErr.Code)
	}
}
