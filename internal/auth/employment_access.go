package auth

import (
	"context"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

type employmentAdmissionReader interface {
	GetEmploymentAdmission(context.Context, uuid.UUID, uuid.UUID) (*domain.EmploymentAdmission, error)
}

// Never derive admission from JWT claims or the password preparation flag.
func (s *Service) checkLoginAccess(ctx context.Context, user int64) error {
	if lookup, ok := s.userRepo.(interface {
		IsUserActive(context.Context, int64) (bool, error)
	}); ok {
		allowed, err := lookup.IsUserActive(ctx, user)
		if err != nil {
			return apperrors.InternalWithErr("Account admission lookup failed", err)
		}
		if !allowed {
			return apperrors.New("EMPLOYMENT_ACCESS_DENIED", "account access is not allowed", 403)
		}
	}
	return nil
}

func (s *Service) checkIssuedKeycloakAccess(ctx context.Context, token string) error {
	subject := extractSubFromJWT(token)
	if subject == "" {
		return apperrors.TokenInvalid()
	}
	user, err := s.userRepo.GetByKeycloakID(ctx, subject)
	if err != nil || user == nil {
		return apperrors.Unauthorized("Local account not found")
	}
	return s.checkLoginAccess(ctx, user.ID)
}
