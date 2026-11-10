package service

import (
	"context"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"testing"
)

func TestAIPersonalUserContextWithoutCompany(t *testing.T) {
	store := &aiAccountingStub{}
	executor := NewAIPaidCallerService(store)
	ctx, err := aiPaidUserContext(executor, alemPayerStub{user: &domain.User{ID: 42, IsActive: true}}, context.Background(), 42)
	require.NoError(t, err)
	caller, err := (domain.AIPaidCallParams{}).ResolveCaller(ctx)
	require.NoError(t, err)
	require.Equal(t, uuid.Nil, caller.OrganizationID)
	require.EqualValues(t, 42, *caller.UserID)
	require.Zero(t, store.reserveCalls, "identity resolution must not spend or open a window")
}
