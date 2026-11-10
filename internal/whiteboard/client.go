package whiteboard

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = 54 * time.Second
	sendBufferSize = 256
)

type Client struct {
	userID    int64
	roomID    uuid.UUID
	conn      *websocket.Conn
	room      *Room
	send      chan domain.WSMessage
	logger    *zap.Logger
	closeOnce sync.Once
}

func NewClient(userID int64, roomID uuid.UUID, conn *websocket.Conn, room *Room, logger *zap.Logger, maxMessageSize int64) *Client {
	client := &Client{
		userID: userID,
		roomID: roomID,
		conn:   conn,
		room:   room,
		send:   make(chan domain.WSMessage, sendBufferSize),
		logger: logger.With(zap.String("component", "whiteboard_client"), zap.Int64("user_id", userID), zap.String("room_id", roomID.String())),
	}

	client.conn.SetReadLimit(maxMessageSize)
	_ = client.conn.SetReadDeadline(time.Now().Add(pongWait))
	client.conn.SetPongHandler(func(string) error {
		_ = client.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	return client
}

func (c *Client) ReadPump(ctx context.Context) {
	defer c.Close()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		_, data, err := c.conn.ReadMessage()
		if err != nil {
			if !isExpectedClose(err) {
				c.logger.Error("Whiteboard websocket read error", zap.Error(err))
			}
			return
		}

		var msg domain.WSMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			c.logger.Warn("Invalid whiteboard websocket payload", zap.Error(err))
			c.enqueue(errorMessage(c.roomID, apperrors.BadRequest("invalid payload")))
			continue
		}

		c.room.HandleClientMessage(c, msg)
	}
}

func (c *Client) WritePump(ctx context.Context) {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.Close()
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				return
			}
			if err := c.conn.WriteJSON(msg); err != nil {
				if !isExpectedClose(err) {
					c.logger.Error("Whiteboard websocket write error", zap.Error(err))
				}
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (c *Client) Close() {
	c.closeOnce.Do(func() {
		if c.room != nil {
			c.room.Unregister(c)
		}
		c.closeWithoutRoom()
	})
}

func (c *Client) closeWithoutRoom() {
	if c.conn != nil {
		_ = c.conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
			time.Now().Add(writeWait),
		)
		_ = c.conn.Close()
	}
}

func (c *Client) enqueue(msg domain.WSMessage) {
	select {
	case c.send <- msg:
	default:
		c.logger.Warn("Whiteboard client send buffer full", zap.String("type", msg.Type))
		go c.Close()
	}
}

func isExpectedClose(err error) bool {
	if err == nil {
		return true
	}
	if websocket.IsUnexpectedCloseError(err,
		websocket.CloseGoingAway,
		websocket.CloseAbnormalClosure,
		websocket.CloseNormalClosure,
		websocket.CloseNoStatusReceived,
	) {
		return false
	}
	if errors.Is(err, websocket.ErrCloseSent) {
		return true
	}
	return true
}
