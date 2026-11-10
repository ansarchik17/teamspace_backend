package whiteboard

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

type fakeStore struct {
	state     []domain.WSMessage
	saveCalls int
	mu        sync.Mutex
}

func (f *fakeStore) GetState(context.Context, uuid.UUID) ([]domain.WSMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return cloneState(f.state), nil
}

func (f *fakeStore) SaveState(_ context.Context, _ uuid.UUID, state []domain.WSMessage, _ int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state = cloneState(state)
	f.saveCalls++
	return nil
}

func (f *fakeStore) SaveCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.saveCalls
}

type fakeAccess struct{}

func (fakeAccess) CanMutate(_ context.Context, _ int64, _ uuid.UUID, messageType string) error {
	if messageType == domain.WhiteboardMessageTypeCursor || messageType == domain.WhiteboardMessageTypePing {
		return nil
	}
	return nil
}

func TestRoomDrawUndoClearAndPersist(t *testing.T) {
	roomID := uuid.New()
	store := &fakeStore{}
	room := NewRoom(roomID, nil, store, fakeAccess{}, zap.NewNop(), 10*time.Millisecond, time.Minute, 1<<20, nil)
	go room.Run()
	defer room.Close(context.Background())

	client := &Client{userID: 42, roomID: roomID, room: room, send: make(chan domain.WSMessage, 16), logger: zap.NewNop()}
	room.Register(client)

	room.HandleClientMessage(client, domain.WSMessage{
		Type:   domain.WhiteboardMessageTypeDraw,
		RoomID: roomID.String(),
		Action: &domain.DrawAction{
			ID:          "action-1",
			Tool:        domain.WhiteboardToolBrush,
			Color:       "#FF0000",
			StrokeWidth: 4,
			Points:      []domain.Point{{X: 1, Y: 2}},
		},
	})

	waitFor(t, func() bool { return room.Snapshot().stateLen == 1 })

	room.HandleClientMessage(client, domain.WSMessage{
		Type:     domain.WhiteboardMessageTypeUndo,
		RoomID:   roomID.String(),
		ActionID: "action-1",
	})

	waitFor(t, func() bool { return room.Snapshot().stateLen == 0 })

	room.HandleClientMessage(client, domain.WSMessage{
		Type:   domain.WhiteboardMessageTypeDraw,
		RoomID: roomID.String(),
		Action: &domain.DrawAction{
			ID:          "action-2",
			Tool:        domain.WhiteboardToolBrush,
			Color:       "#00FF00",
			StrokeWidth: 3,
			Points:      []domain.Point{{X: 3, Y: 4}},
		},
	})
	waitFor(t, func() bool { return room.Snapshot().stateLen == 1 })

	room.HandleClientMessage(client, domain.WSMessage{
		Type:   domain.WhiteboardMessageTypeClear,
		RoomID: roomID.String(),
	})

	waitFor(t, func() bool { return room.Snapshot().stateLen == 0 })
	waitFor(t, func() bool { return store.SaveCallCount() > 0 })
}

func TestManagerReturnsSameRoom(t *testing.T) {
	manager := NewManager(&fakeStore{}, fakeAccess{}, zap.NewNop())
	roomID := uuid.New()

	room1, err := manager.GetOrCreateRoom(context.Background(), roomID)
	if err != nil {
		t.Fatalf("GetOrCreateRoom first: %v", err)
	}
	room2, err := manager.GetOrCreateRoom(context.Background(), roomID)
	if err != nil {
		t.Fatalf("GetOrCreateRoom second: %v", err)
	}

	if room1 != room2 {
		t.Fatal("expected same room instance")
	}

	if err := manager.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown manager: %v", err)
	}
}

func TestValidateInboundMessage(t *testing.T) {
	roomID := uuid.New().String()
	err := validateInboundMessage(roomID, &domain.WSMessage{
		Type:   domain.WhiteboardMessageTypeDraw,
		RoomID: roomID,
		Action: &domain.DrawAction{
			ID:          "a1",
			Tool:        domain.WhiteboardToolBrush,
			Color:       "#000",
			StrokeWidth: 2,
			Points:      []domain.Point{{X: 1, Y: 1}},
		},
	})
	if err != nil {
		t.Fatalf("expected valid message, got %v", err)
	}

	err = validateInboundMessage(roomID, &domain.WSMessage{
		Type:   domain.WhiteboardMessageTypeDraw,
		RoomID: roomID,
		Action: &domain.DrawAction{
			ID:          "",
			Tool:        domain.WhiteboardToolBrush,
			StrokeWidth: 2,
			Points:      []domain.Point{{X: 1, Y: 1}},
		},
	})
	if err == nil || apperrors.ToAppError(err).Code != apperrors.ErrCodeBadRequest {
		t.Fatalf("expected bad request, got %v", err)
	}
}

func waitFor(t *testing.T, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met before timeout")
}
