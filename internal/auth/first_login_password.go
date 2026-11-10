package auth

import (
	"context"

	"github.com/west6ide/workspace-backend/internal/domain"
)

// Native hires keep their temporary-password requirement across SSO and token
// renewal. Only the password-change endpoint may complete their first login.
func (s *Service) firstLoginPasswordRequirement(ctx context.Context, user *domain.User, allowLegacy bool) (bool, bool) {
	if isIFormFirstLogin(user) {
		return true, true
	}
	if user == nil || !allowLegacy || !user.IsFirstLogin || !IsWebLoginClient(ctx) {
		return false, false
	}
	// Preserve the existing policy for accounts outside native onboarding.
	if err := s.verifyPasswordKeycloak(ctx, user.Email, "Qwerty12!"); err == nil {
		return true, true
	}
	user.IsFirstLogin = false
	_ = s.userRepo.CompleteOnboarding(ctx, user.ID)
	return false, false
}
