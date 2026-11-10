package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDeleteUserRequiresManagedDeletion(t *testing.T) {
	s := &Service{}
	require.Error(t, s.DeleteUser(context.Background(), 2, 3))
	denied := errors.New("foreign employee")
	s.SetUserDeletionHandler(func(_ context.Context, actor, target int64) error {
		require.Equal(t, int64(2), actor)
		require.Equal(t, int64(3), target)
		return denied
	})
	require.ErrorIs(t, s.DeleteUser(context.Background(), 2, 3), denied)
}
