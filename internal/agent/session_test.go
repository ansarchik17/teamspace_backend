package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestSessionService_GetOrCreateCreatesSession(t *testing.T) {
	repo := newMemoryRepo()
	svc := NewSessionService(config.AgentConfig{SessionTTL: time.Hour}, repo)

	session, err := svc.GetOrCreate(context.Background(), domain.AgentUserContext{UserID: 7}, "", map[string]interface{}{"locale": "ru"})

	require.NoError(t, err)
	require.NotEmpty(t, session.ID)
	require.Equal(t, int64(7), session.UserID)
	require.Equal(t, "ru", session.Metadata["locale"])
}

func TestSessionService_ListMessagesIsScopedByUser(t *testing.T) {
	repo := newMemoryRepo()
	svc := NewSessionService(config.AgentConfig{SessionTTL: time.Hour}, repo)
	ctx := context.Background()

	session, err := svc.GetOrCreate(ctx, domain.AgentUserContext{UserID: 7}, "", nil)
	require.NoError(t, err)

	require.NoError(t, svc.SaveMessage(ctx, &domain.AgentMessage{
		SessionID: session.ID,
		UserID:    7,
		Role:      domain.AgentRoleUser,
		Content:   "hi",
		CreatedAt: time.Now(),
	}))

	msgs, err := svc.ListMessages(ctx, domain.AgentUserContext{UserID: 8}, session.ID)
	require.NoError(t, err)
	require.Empty(t, msgs)
}
