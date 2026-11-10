package auth

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/west6ide/workspace-backend/internal/keycloak"
)

type retiredUserIdentityCleaner interface {
	CleanupRetiredUserIdentities(context.Context, string, string, string, func(context.Context, string) error) error
}

func (s *Service) createEmployeeKeycloakIdentity(ctx context.Context, req keycloak.CreateUserRequest, iin string) (string, error) {
	id, err := s.kcAdmin.CreateUser(ctx, s.realm, req)
	if err == nil || !strings.Contains(err.Error(), "status=409") {
		return id, err
	}
	cleaner, ok := s.userRepo.(retiredUserIdentityCleaner)
	if !ok {
		return "", err
	}
	cleanupCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := cleaner.CleanupRetiredUserIdentities(cleanupCtx, req.Email, req.Username, iin, s.DeleteRetiredIdentity); err != nil {
		return "", fmt.Errorf("retired identity cleanup failed: %w", err)
	}
	// Exactly one provisioning retry, including when the worker completed the
	// same cleanup while this request waited. Unrelated SSO conflicts stay 409.
	return s.kcAdmin.CreateUser(ctx, s.realm, req)
}
