package websocket

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 10 * time.Second
	pingPeriod     = 9 * time.Second
	maxMessageSize = 10 * 1024 * 1024 // Increased from 512 bytes to 10MB for large messages
	sendBufSize    = 1024             // Increased from 256 to 1024
)

type Client struct {
	UserID     int64
	SessionKey string
	conn       *websocket.Conn
	hub        *Hub
	send       chan *Message
	logger     *zap.Logger

	closeOnce sync.Once
	sendMu    sync.RWMutex
}

func NewClient(userID int64, sessionKey string, conn *websocket.Conn, hub *Hub, logger *zap.Logger) *Client {
	c := &Client{
		UserID:     userID,
		SessionKey: sessionKey,
		conn:       conn,
		hub:        hub,
		send:       make(chan *Message, sendBufSize),
		logger:     logger.With(zap.Int64("user_id", userID)),
	}

	c.conn.SetReadLimit(maxMessageSize)
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	return c
}

func (c *Client) Close() {
	c.closeOnce.Do(func() {
		if c.hub != nil {
			c.hub.Unregister(c)
		}
		if c.conn != nil {
			_ = c.conn.WriteControl(
				websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
				time.Now().Add(writeWait),
			)
			_ = c.conn.Close()
		}
	})
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
				c.logger.Error("websocket read error", zap.Error(err))
			}
			return
		}

		if !c.accountActive(ctx) {
			return
		}
		var msg Message
		if err := json.Unmarshal(data, &msg); err != nil {
			c.logger.Warn("invalid websocket payload", zap.Error(err))
			continue
		}

		msg.From = c.UserID
		msg.Timestamp = time.Now().Unix()

		c.handleInbound(&msg)
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
			if !c.accountActive(ctx) {
				return
			}
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				return
			}

			b, err := json.Marshal(msg)
			if err != nil {
				c.logger.Error("websocket marshal error", zap.Error(err))
				continue
			}

			if err := c.conn.WriteMessage(websocket.TextMessage, b); err != nil {
				if !isExpectedClose(err) {
					c.logger.Error("websocket write error", zap.Error(err))
				}
				return
			}

		case <-ticker.C:
			if !c.accountActive(ctx) {
				return
			}
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// Recheck on all instances, including already-established sockets. Fail closed
// on lookup errors rather than continuing to deliver company data.
func (c *Client) accountActive(ctx context.Context) bool {
	if c.hub == nil {
		return true
	}
	lookup, ok := c.hub.userUpdater.(interface {
		IsUserActive(context.Context, int64) (bool, error)
	})
	if !ok {
		return true
	}
	checkCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	active, err := lookup.IsUserActive(checkCtx, c.UserID)
	return err == nil && active
}

func (c *Client) handleInbound(msg *Message) {
	switch msg.Type {
	case "kanban_task_moved":
		c.handleKanbanTaskMoved(msg)
	case "ping":
		c.enqueue(&Message{
			Type:      "pong",
			From:      0,
			To:        c.UserID,
			Timestamp: time.Now().Unix(),
		})
	default:
		c.logger.Warn("unknown websocket message type", zap.String("type", msg.Type))
	}
}

func (c *Client) enqueue(msg *Message) {
	c.sendMu.RLock()
	defer c.sendMu.RUnlock()
	select {
	case c.send <- msg:
	default:
		// Log warning instead of immediately closing - give client a chance
		c.logger.Warn("send buffer full, dropping message",
			zap.String("message_type", msg.Type),
		)
		if c.hub != nil {
			c.hub.recordBufferFull("client_enqueue", c.UserID, msg.Type, len(c.send), cap(c.send))
		}
	}
}

// EnqueueMessage enqueues a message to be sent to the client
func (c *Client) EnqueueMessage(msg *Message) {
	c.enqueue(msg)
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
