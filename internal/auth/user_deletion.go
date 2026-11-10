package auth

import (
	"context"
	"fmt"
)

func (s *Service) SetUserDeletionHandler(handler func(context.Context, int64, int64) error) {
	s.deleteUser = handler
}

func (s *Service) DeleteRetiredIdentity(ctx context.Context, identity string) error {
	if s.kcAdmin == nil {
		return fmt.Errorf("identity administration unavailable")
	}
	return s.kcAdmin.DeleteUser(ctx, s.realm, identity)
}
