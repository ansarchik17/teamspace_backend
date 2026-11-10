package websocket

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type syncHandlerStub struct {
	userID       int64
	deviceID     string
	lastSequence int64
	limit        int
}

func (s *syncHandlerStub) HandleReconnection(_ context.Context, userID int64, deviceID string, lastSequence int64, limit int) (interface{}, error) {
	s.userID = userID
	s.deviceID = deviceID
	s.lastSequence = lastSequence
	s.limit = limit
	return map[string]interface{}{"last_sequence": lastSequence}, nil
}

func TestHandleSyncRequestForwardsAuthenticatedUserAndCursor(t *testing.T) {
	stub := &syncHandlerStub{}
	hub := NewHub(zap.NewNop(), nil, nil)
	hub.SyncHandler = stub
	client := &Client{UserID: 42, SessionKey: "session-1", hub: hub, send: make(chan *Message, 1), logger: zap.NewNop()}

	hub.HandleSyncRequest(client, &Message{DeviceID: "web-device-1", LastSequence: 99, SyncLimit: 25})

	select {
	case response := <-client.send:
		require.Equal(t, "sync_response", response.Type)
	case <-time.After(time.Second):
		t.Fatal("sync response was not enqueued")
	}
	require.Equal(t, int64(42), stub.userID)
	require.Equal(t, "web-device-1", stub.deviceID)
	require.Equal(t, int64(99), stub.lastSequence)
	require.Equal(t, 25, stub.limit)
}
