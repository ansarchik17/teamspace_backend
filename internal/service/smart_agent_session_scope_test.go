package service

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestAgentSessionStorageScopeCannotBeChosenByClient(t *testing.T) {
	user := int64(42)
	org := uuid.New()
	caller, err := domain.NewAIPaidUserCaller(org, user)
	require.NoError(t, err)
	ctx := domain.WithAIPaidCaller(context.Background(), caller)
	first, err := smartAgentSessionStorageKey(ctx, user, "shared-client-session")
	require.NoError(t, err)
	other, err := domain.NewAIPaidUserCaller(uuid.New(), user)
	require.NoError(t, err)
	second, err := smartAgentSessionStorageKey(domain.WithAIPaidCaller(context.Background(), other), user, "shared-client-session")
	require.NoError(t, err)
	require.NotEqual(t, first, second)
	_, err = smartAgentSessionStorageKey(ctx, 7, "shared-client-session")
	require.Error(t, err)
	_, err = smartAgentSessionStorageKey(context.Background(), user, "shared-client-session")
	require.Error(t, err)
	require.NotContains(t, first, "shared-client-session")
}
