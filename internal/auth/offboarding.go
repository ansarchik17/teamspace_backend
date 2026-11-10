package auth

import (
	"context"
	"fmt"
	"github.com/west6ide/workspace-backend/internal/keycloak"
	"strings"
)

// DisableOffboardedIdentity is idempotent; called only from the persisted offboarding outbox.
func (s *Service) DisableOffboardedIdentity(ctx context.Context, userID int64) error {
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if user.IsActive {
		return fmt.Errorf("refusing to disable an active user")
	}
	if s.tokenManager != nil && s.tokenManager.tokenStorage != nil {
		if err = s.tokenManager.tokenStorage.RevokeAllUserTokens(ctx, userID); err != nil {
			return err
		}
	}
	if user.KeycloakID == nil || strings.TrimSpace(*user.KeycloakID) == "" {
		return nil
	}
	if s.kcAdmin == nil {
		return fmt.Errorf("Keycloak admin client unavailable")
	}
	enabled := false
	if err = s.kcAdmin.UpdateUser(ctx, s.realm, *user.KeycloakID, keycloak.UserUpdateRequest{Enabled: &enabled}); err != nil {
		return err
	}
	return s.kcAdmin.LogoutUser(ctx, s.realm, *user.KeycloakID)
}

func (s *Service) checkNotOffboarded(ctx context.Context, userID int64) error {
	if guard, ok := s.userRepo.(interface {
		CheckNotOffboarded(context.Context, int64) error
	}); ok {
		return guard.CheckNotOffboarded(ctx, userID)
	}
	return nil
}
