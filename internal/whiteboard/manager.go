package whiteboard

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

type StateStore interface {
	GetState(ctx context.Context, roomID uuid.UUID) ([]domain.WSMessage, error)
	SaveState(ctx context.Context, roomID uuid.UUID, state []domain.WSMessage, userID int64) error
}

type AccessController interface {
	CanMutate(ctx context.Context, userID int64, boardID uuid.UUID, messageType string) error
}

type Manager struct {
	mu         sync.RWMutex
	rooms      map[uuid.UUID]*Room
	store      StateStore
	access     AccessController
	logger     *zap.Logger
	saveEvery  time.Duration
	idleTTL    time.Duration
	maxMessage int64
}

func NewManager(store StateStore, access AccessController, logger *zap.Logger) *Manager {
	return &Manager{
		rooms:      make(map[uuid.UUID]*Room),
		store:      store,
		access:     access,
		logger:     logger.With(zap.String("component", "whiteboard_manager")),
		saveEvery:  10 * time.Second,
		idleTTL:    1 * time.Minute,
		maxMessage: 1 << 20,
	}
}

func (m *Manager) GetOrCreateRoom(ctx context.Context, roomID uuid.UUID) (*Room, error) {
	m.mu.RLock()
	room, ok := m.rooms[roomID]
	m.mu.RUnlock()
	if ok {
		return room, nil
	}

	state, err := m.store.GetState(ctx, roomID)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if room, ok = m.rooms[roomID]; ok {
		return room, nil
	}

	room = NewRoom(roomID, state, m.store, m.access, m.logger, m.saveEvery, m.idleTTL, m.maxMessage, m.removeRoom)
	m.rooms[roomID] = room
	m.logger.Info("Whiteboard room created", zap.String("room_id", roomID.String()))
	go room.Run()

	return room, nil
}

func (m *Manager) Shutdown(ctx context.Context) error {
	m.mu.RLock()
	rooms := make([]*Room, 0, len(m.rooms))
	for _, room := range m.rooms {
		rooms = append(rooms, room)
	}
	m.mu.RUnlock()

	for _, room := range rooms {
		room.Close(ctx)
	}

	return nil
}

func (m *Manager) removeRoom(roomID uuid.UUID) {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.rooms, roomID)
	m.logger.Info("Whiteboard room removed", zap.String("room_id", roomID.String()))
}
