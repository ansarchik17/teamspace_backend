package websocket

import (
	"context"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

func (c *Client) handleKanbanTaskMoved(msg *Message) {
	for _, id := range []*uuid.UUID{msg.BoardID, msg.TaskID, msg.FromColumnID, msg.ToColumnID} {
		if id == nil || *id == uuid.Nil {
			c.kanbanError("invalid_payload")
			return
		}
	}
	if c.hub == nil || c.hub.KanbanTaskMovedResolver == nil {
		c.kanbanError("unavailable")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	recipients, err := c.hub.KanbanTaskMovedResolver(ctx, c.UserID, *msg.BoardID, *msg.TaskID, *msg.FromColumnID, *msg.ToColumnID)
	if err != nil {
		c.logger.Warn("kanban task move event rejected", zap.Error(err))
		c.kanbanError("move_rejected")
		return
	}

	// Construct a fresh event: client-supplied routing and sender fields are untrusted.
	event := &Message{
		Type: "kanban_task_moved", From: c.UserID, Timestamp: time.Now().Unix(),
		BoardID: msg.BoardID, TaskID: msg.TaskID,
		FromColumnID: msg.FromColumnID, ToColumnID: msg.ToColumnID,
	}
	seen := make(map[int64]bool, len(recipients))
	for _, userID := range recipients {
		if userID > 0 && !seen[userID] {
			seen[userID] = true
			c.hub.SendToUser(userID, event)
		}
	}
}

func (c *Client) kanbanError(code string) {
	c.enqueue(&Message{
		Type: "error", To: c.UserID, Timestamp: time.Now().Unix(),
		Content: map[string]string{"event": "kanban_task_moved", "code": code},
	})
}
