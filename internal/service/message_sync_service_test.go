package service

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

func TestIsValidSyncDeviceID(t *testing.T) {
	for _, deviceID := range []string{"web-device-1", "ios:550e8400-e29b", "desktop_1.2"} {
		require.True(t, IsValidSyncDeviceID(deviceID), deviceID)
	}
	for _, deviceID := range []string{"", "contains space", "../device", string(make([]byte, 129))} {
		require.False(t, IsValidSyncDeviceID(deviceID), deviceID)
	}
}

func TestDeduplicateMessagesKeepsFirstOccurrenceAndOrder(t *testing.T) {
	firstID := uuid.New()
	secondID := uuid.New()
	service := &MessageSyncService{logger: zap.NewNop()}
	messages := []*domain.MessageView{
		{Message: domain.Message{ID: firstID}},
		nil,
		{Message: domain.Message{ID: secondID}},
		{Message: domain.Message{ID: firstID}},
	}

	result := service.DeduplicateMessages(messages)
	require.Len(t, result, 2)
	require.Equal(t, firstID, result[0].ID)
	require.Equal(t, secondID, result[1].ID)
}
