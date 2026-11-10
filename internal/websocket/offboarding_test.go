package websocket

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	gws "github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type offboardingSocketRepo struct{ active atomic.Bool }

func (*offboardingSocketRepo) UpdateLastSeen(context.Context, int64, time.Time) error { return nil }
func (r *offboardingSocketRepo) IsUserActive(context.Context, int64) (bool, error) {
	return r.active.Load(), nil
}

func TestOffboardedOpenWebsocketCannotReceiveQueuedData(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	repo := &offboardingSocketRepo{}
	repo.active.Store(true)
	hub := NewHub(zap.NewNop(), repo, nil)
	go hub.Run(ctx)
	ready := make(chan *Client, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&gws.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		client := NewClient(77, "session", conn, hub, zap.NewNop())
		ready <- client
		client.WritePump(ctx)
	}))
	defer server.Close()
	peer, _, err := gws.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	require.NoError(t, err)
	defer peer.Close()
	client := <-ready
	client.EnqueueMessage(&Message{Type: "before"})
	require.NoError(t, peer.SetReadDeadline(time.Now().Add(2*time.Second)))
	_, data, err := peer.ReadMessage()
	require.NoError(t, err)
	require.Contains(t, string(data), "before")
	repo.active.Store(false)
	client.EnqueueMessage(&Message{Type: "confidential"})
	_, data, err = peer.ReadMessage()
	require.Error(t, err)
	require.NotContains(t, string(data), "confidential")
}
