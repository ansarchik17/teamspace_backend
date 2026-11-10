package websocket

import (
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

type accountStatusTestRepo struct {
	active bool
	err    error
}

func (r *accountStatusTestRepo) UpdateLastSeen(context.Context, int64, time.Time) error { return nil }
func (r *accountStatusTestRepo) IsUserActive(context.Context, int64) (bool, error) {
	return r.active, r.err
}
func TestSocketAccountAccessFailsClosed(t *testing.T) {
	repo := &accountStatusTestRepo{active: true}
	client := &Client{UserID: 7, hub: &Hub{userUpdater: repo}}
	require.True(t, client.accountActive(context.Background()))
	repo.active = false
	require.False(t, client.accountActive(context.Background()))
	repo.active = true
	repo.err = errors.New("database unavailable")
	require.False(t, client.accountActive(context.Background()))
}
