package auth

import (
	"context"
	"fmt"

	"github.com/west6ide/workspace-backend/internal/domain"
)

// ResolveMeetUsers returns Workspace-owned user projections for the Alem Meet
// compatibility API. Meet persists only the stable Keycloak subject and a
// snapshot of display data; it never receives database access.
func (s *Service) ResolveMeetUsers(ctx context.Context, userIDs []int64) ([]*domain.UserResponse, error) {
	users := make([]*domain.UserResponse, 0, len(userIDs))
	seen := make(map[int64]struct{}, len(userIDs))
	for _, userID := range userIDs {
		if userID <= 0 {
			return nil, fmt.Errorf("invalid user id %d", userID)
		}
		if _, ok := seen[userID]; ok {
			continue
		}
		seen[userID] = struct{}{}

		user, err := s.userRepo.GetByID(ctx, userID)
		if err != nil {
			return nil, fmt.Errorf("resolve user %d: %w", userID, err)
		}
		if user.KeycloakID == nil || *user.KeycloakID == "" || !user.IsActive {
			continue
		}
		users = append(users, user.ToResponse())
	}
	return users, nil
}
