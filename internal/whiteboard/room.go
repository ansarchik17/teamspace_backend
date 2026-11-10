package whiteboard

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

const (
	maxStrokeWidth   = 100
	minStrokeWidth   = 1
	maxPointsPerDraw = 5000
)

type inboundMessage struct {
	client *Client
	msg    domain.WSMessage
}

type Room struct {
	id         uuid.UUID
	clients    map[*Client]struct{}
	register   chan *Client
	unregister chan *Client
	inbound    chan inboundMessage
	inspect    chan chan roomSnapshot

	store  StateStore
	access AccessController
	logger *zap.Logger
	state  []domain.WSMessage
	dirty  bool
	idleAt *time.Time
	remove func(uuid.UUID)

	saveEvery time.Duration
	idleTTL   time.Duration

	closeOnce sync.Once
	closed    chan struct{}
	stop      chan struct{}
}

type roomSnapshot struct {
	stateLen int
	dirty    bool
	clients  int
}

func NewRoom(
	roomID uuid.UUID,
	state []domain.WSMessage,
	store StateStore,
	access AccessController,
	logger *zap.Logger,
	saveEvery time.Duration,
	idleTTL time.Duration,
	_ int64,
	remove func(uuid.UUID),
) *Room {
	return &Room{
		id:         roomID,
		clients:    make(map[*Client]struct{}),
		register:   make(chan *Client, 32),
		unregister: make(chan *Client, 32),
		inbound:    make(chan inboundMessage, 256),
		inspect:    make(chan chan roomSnapshot, 8),
		store:      store,
		access:     access,
		logger:     logger.With(zap.String("component", "whiteboard_room"), zap.String("room_id", roomID.String())),
		state:      cloneState(state),
		remove:     remove,
		saveEvery:  saveEvery,
		idleTTL:    idleTTL,
		closed:     make(chan struct{}),
		stop:       make(chan struct{}),
	}
}

func (r *Room) Run() {
	defer close(r.closed)

	ticker := time.NewTicker(r.saveEvery)
	defer ticker.Stop()

	for {
		select {
		case client := <-r.register:
			r.clients[client] = struct{}{}
			r.idleAt = nil
			r.logger.Info("Whiteboard client connected", zap.Int64("user_id", client.userID), zap.Int("clients", len(r.clients)))
			client.enqueue(r.newInitMessage())

		case client := <-r.unregister:
			if _, ok := r.clients[client]; ok {
				delete(r.clients, client)
				close(client.send)
				r.logger.Info("Whiteboard client disconnected", zap.Int64("user_id", client.userID), zap.Int("clients", len(r.clients)))
				if len(r.clients) == 0 {
					now := time.Now()
					r.idleAt = &now
					r.persist(context.Background(), client.userID)
				}
			}

		case event := <-r.inbound:
			r.handleInbound(event)

		case responseCh := <-r.inspect:
			responseCh <- roomSnapshot{
				stateLen: len(r.state),
				dirty:    r.dirty,
				clients:  len(r.clients),
			}

		case <-ticker.C:
			if r.dirty {
				r.persist(context.Background(), 0)
			}
			if len(r.clients) == 0 && r.idleAt != nil && time.Since(*r.idleAt) >= r.idleTTL {
				r.persist(context.Background(), 0)
				if r.remove != nil {
					r.remove(r.id)
				}
				return
			}

		case <-r.stop:
			r.persist(context.Background(), 0)
			for client := range r.clients {
				client.closeWithoutRoom()
				close(client.send)
			}
			r.clients = map[*Client]struct{}{}
			if r.remove != nil {
				r.remove(r.id)
			}
			return
		}
	}
}

func (r *Room) Close(ctx context.Context) {
	r.closeOnce.Do(func() {
		close(r.stop)
	})

	select {
	case <-r.closed:
	case <-ctx.Done():
	}
}

func (r *Room) Register(client *Client) {
	r.register <- client
}

func (r *Room) Unregister(client *Client) {
	r.unregister <- client
}

func (r *Room) HandleClientMessage(client *Client, msg domain.WSMessage) {
	r.inbound <- inboundMessage{client: client, msg: msg}
}

func (r *Room) Snapshot() roomSnapshot {
	responseCh := make(chan roomSnapshot, 1)
	r.inspect <- responseCh
	return <-responseCh
}

func (r *Room) handleInbound(event inboundMessage) {
	msg := event.msg
	if err := validateInboundMessage(r.id.String(), &msg); err != nil {
		event.client.enqueue(errorMessage(r.id, err))
		return
	}

	authCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	err := r.access.CanMutate(authCtx, event.client.userID, r.id, msg.Type)
	cancel()
	if err != nil {
		event.client.enqueue(errorMessage(r.id, apperrors.ToAppError(err)))
		return
	}

	msg.UserID = uint(event.client.userID)
	msg.SentAt = time.Now().UTC()

	switch msg.Type {
	case domain.WhiteboardMessageTypeDraw:
		r.state = append(r.state, msg)
		r.dirty = true
		r.broadcast(msg)
	case domain.WhiteboardMessageTypeUndo:
		if !r.removeAction(msg.ActionID) {
			event.client.enqueue(errorMessage(r.id, apperrors.BadRequest("action not found")))
			return
		}
		r.dirty = true
		r.broadcast(msg)
	case domain.WhiteboardMessageTypeClear:
		r.state = nil
		r.dirty = true
		r.broadcast(msg)
	case domain.WhiteboardMessageTypeCursor:
		r.broadcast(msg)
	case domain.WhiteboardMessageTypePing:
		event.client.enqueue(domain.WSMessage{
			Type:   domain.WhiteboardMessageTypePong,
			RoomID: r.id.String(),
			SentAt: time.Now().UTC(),
		})
	default:
		event.client.enqueue(errorMessage(r.id, apperrors.BadRequest(fmt.Sprintf("unsupported message type: %s", msg.Type))))
	}
}

func (r *Room) broadcast(msg domain.WSMessage) {
	for client := range r.clients {
		client.enqueue(msg)
	}
}

func (r *Room) removeAction(actionID string) bool {
	for idx := range r.state {
		if r.state[idx].Action != nil && r.state[idx].Action.ID == actionID {
			r.state = append(r.state[:idx], r.state[idx+1:]...)
			return true
		}
	}
	return false
}

func (r *Room) newInitMessage() domain.WSMessage {
	return domain.WSMessage{
		Type:   domain.WhiteboardMessageTypeInit,
		RoomID: r.id.String(),
		Payload: domain.WhiteboardInitPayload{
			Actions: cloneState(r.state),
		},
		SentAt: time.Now().UTC(),
	}
}

func (r *Room) persist(ctx context.Context, fallbackUserID int64) {
	if !r.dirty {
		return
	}

	saveCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	if saveCtx == nil {
		saveCtx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	}
	defer cancel()

	if err := r.store.SaveState(saveCtx, r.id, cloneState(r.state), fallbackUserID); err != nil {
		r.logger.Error("Failed to persist whiteboard state", zap.Error(err))
		return
	}

	r.dirty = false
}

func validateInboundMessage(expectedRoomID string, msg *domain.WSMessage) *apperrors.AppError {
	if msg == nil {
		return apperrors.BadRequest("message is required")
	}
	if strings.TrimSpace(msg.RoomID) == "" {
		return apperrors.BadRequest("roomId is required")
	}
	if msg.RoomID != expectedRoomID {
		return apperrors.BadRequest("roomId mismatch")
	}

	switch msg.Type {
	case domain.WhiteboardMessageTypeDraw:
		if msg.Action == nil {
			return apperrors.BadRequest("action is required for draw")
		}
		action := msg.Action
		if strings.TrimSpace(action.ID) == "" {
			return apperrors.BadRequest("action.id is required")
		}
		if action.Tool != domain.WhiteboardToolBrush && action.Tool != domain.WhiteboardToolEraser {
			return apperrors.BadRequest("invalid tool")
		}
		if action.StrokeWidth < minStrokeWidth || action.StrokeWidth > maxStrokeWidth {
			return apperrors.BadRequest("strokeWidth must be between 1 and 100")
		}
		if len(action.Points) == 0 {
			return apperrors.BadRequest("points are required")
		}
		if len(action.Points) > maxPointsPerDraw {
			return apperrors.BadRequest("too many points in a single draw action")
		}
	case domain.WhiteboardMessageTypeUndo:
		if strings.TrimSpace(msg.ActionID) == "" {
			return apperrors.BadRequest("actionId is required for undo")
		}
	case domain.WhiteboardMessageTypeClear, domain.WhiteboardMessageTypeCursor, domain.WhiteboardMessageTypePing:
		return nil
	default:
		return apperrors.BadRequest("invalid message type")
	}

	return nil
}

func errorMessage(roomID uuid.UUID, err *apperrors.AppError) domain.WSMessage {
	appErr := apperrors.ToAppError(err)
	return domain.WSMessage{
		Type:   domain.WhiteboardMessageTypeError,
		RoomID: roomID.String(),
		Payload: map[string]any{
			"code":    appErr.Code,
			"message": appErr.Message,
			"details": appErr.Details,
		},
		SentAt: time.Now().UTC(),
	}
}

func cloneState(state []domain.WSMessage) []domain.WSMessage {
	if len(state) == 0 {
		return []domain.WSMessage{}
	}
	out := make([]domain.WSMessage, len(state))
	copy(out, state)
	return out
}
