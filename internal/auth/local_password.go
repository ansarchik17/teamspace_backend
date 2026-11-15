package auth

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	appservice "github.com/west6ide/workspace-backend/internal/service"
	"golang.org/x/crypto/bcrypt"
)

// Re-read local credentials under the same lock used for password changes.
// A slow Keycloak failure must not let an in-flight login authenticate using
// the old password snapshot after a local password change has completed.
func (s *Service) localPasswordTokens(ctx context.Context, original *domain.User, password string) (*TokenPair, error) {
	var pair *TokenPair
	mint := func(u *domain.User) error {
		if u.KeycloakID != nil && *u.KeycloakID != "" || u.PasswordHash == "" || bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
			return apperrors.InvalidCredentials()
		}
		var err error
		pair, err = s.tokenManager.GenerateTokenPair(ctx, u.ID, derefStringValue(original.IIN), u.Email, u.Username, original.FirstName, original.LastName, false, nil)
		return err
	}
	if s.recovery != nil && s.recovery.repo != nil {
		err := s.recovery.repo.WithPasswordUser(ctx, original.ID, func(_ *pgxpool.Conn, u *domain.User) error { return mint(u) })
		return pair, err
	}
	err := mint(original)
	return pair, err
}

func (s *Service) changeLocalPassword(ctx context.Context, c *pgxpool.Conn, u *domain.User, oldPassword, newPassword string, backup *appservice.SaveKeyBackupRequest) error {
	if len(oldPassword) == 0 || len(oldPassword) > 1024 || bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(oldPassword)) != nil {
		return apperrors.New("RECOVERY_REAUTH_FAILED", "Current password is incorrect", 400)
	}
	if err := recoveryPassword(newPassword); err != nil {
		return err
	}
	// bcrypt rejects longer inputs; never silently truncate a new password.
	if len(newPassword) > 72 {
		return apperrors.New("RECOVERY_PASSWORD_POLICY", "Local passwords must fit within 72 UTF-8 bytes", 400)
	}
	if backup != nil {
		return apperrors.Validation("e2ee_backup is not supported for local accounts")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), 12)
	if err != nil {
		return apperrors.InternalWithErr("failed to secure new password", err)
	}
	cutoff, err := s.recovery.repo.ChangeLocalPassword(ctx, c, u, string(hash))
	if err != nil {
		return err
	}
	// Access and refresh JWT timestamps have second precision. Allow a fresh
	// password login immediately after the successful response, including
	// when the change and previous login happened in the same second.
	if delay := time.Until(cutoff); delay > 0 && delay <= time.Second {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
		}
	}
	if s.recovery.closeSessions != nil {
		s.recovery.closeSessions(u.ID)
	}
	return nil
}
