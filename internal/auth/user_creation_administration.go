package auth

import (
	"context"

	"github.com/west6ide/workspace-backend/internal/apperrors"
)

type userCreationAuthorizer interface {
	CanCreateUser(context.Context, int64) (bool, error)
}

// Public employee creation is reserved for platform administrators and current
// company owners. iForm and external administration keep their own guards.
func (s *Service) RequireUserCreationAdministration(ctx context.Context, actor int64) error {
	if actor <= 0 {
		return apperrors.Forbidden("administrator required")
	}
	authorizer, ok := s.userRepo.(userCreationAuthorizer)
	if !ok {
		return apperrors.Internal("User creation authorization is unavailable")
	}
	allowed, err := authorizer.CanCreateUser(ctx, actor)
	if err != nil {
		return apperrors.InternalWithErr("Failed to authorize user creation", err)
	}
	if !allowed {
		return apperrors.Forbidden("administrator required")
	}
	return nil
}
