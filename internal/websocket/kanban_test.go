package websocket

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

func TestKanbanTaskMovedRouting(t *testing.T) {
	hub := NewHub(zap.NewNop(), nil, nil)
	clients := []*Client{}
	for _, uid := range []int64{1, 1, 2, 3} {
		c := &Client{UserID: uid, hub: hub, send: make(chan *Message, 4), logger: zap.NewNop()}
		hub.registerClient(c)
		clients = append(clients, c)
	}
	board, task, from, to := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	hub.KanbanTaskMovedResolver = func(ctx context.Context, userID int64, b, taskID, f, dest uuid.UUID) ([]int64, error) {
		if userID != 1 || b != board || taskID != task || f != from || dest != to {
			t.Fatal("resolver received incorrect identity or IDs")
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("resolver must have a timeout")
		}
		return []int64{1, 2, 2}, nil
	}
	// Untrusted routing fields must not redirect the event to the outsider.
	clients[0].handleInbound(&Message{Type: "kanban_task_moved", From: 3, To: 3,
		BoardID: &board, TaskID: &task, FromColumnID: &from, ToColumnID: &to,
		ConversationID: &task, Content: "untrusted"})
	for i, c := range clients {
		want := 1
		if i == 3 {
			want = 0
		}
		if len(c.send) != want {
			t.Fatalf("client %d got %d events, want %d", i, len(c.send), want)
		}
		if want == 0 {
			continue
		}
		got := <-c.send
		if got.From != 1 || got.To != 0 || got.ConversationID != nil || got.Content != nil || got.Timestamp == 0 {
			t.Fatalf("unsafe event: %+v", got)
		}
		data, err := MarshalMessage(got)
		if err != nil {
			t.Fatal(err)
		}
		roundTrip, err := UnmarshalMessage(data)
		if err != nil || *roundTrip.BoardID != board || *roundTrip.TaskID != task || *roundTrip.FromColumnID != from || *roundTrip.ToColumnID != to {
			t.Fatalf("event fields lost: %s (%v)", data, err)
		}
	}
}

func TestKanbanTaskMovedRejected(t *testing.T) {
	for _, scenario := range []string{"missing", "zero", "unavailable", "denied"} {
		t.Run(scenario, func(t *testing.T) {
			hub := NewHub(zap.NewNop(), nil, nil)
			c := &Client{UserID: 1, hub: hub, send: make(chan *Message, 2), logger: zap.NewNop()}
			other := &Client{UserID: 2, hub: hub, send: make(chan *Message, 2), logger: zap.NewNop()}
			hub.registerClient(c)
			hub.registerClient(other)
			id := uuid.New()
			msg := &Message{Type: "kanban_task_moved", BoardID: &id, TaskID: &id, FromColumnID: &id, ToColumnID: &id}
			called := false
			hub.KanbanTaskMovedResolver = func(context.Context, int64, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID) ([]int64, error) {
				called = true
				return []int64{2}, errors.New("denied")
			}
			switch scenario {
			case "missing":
				msg.TaskID = nil
			case "zero":
				msg.BoardID = &uuid.Nil
			case "unavailable":
				hub.KanbanTaskMovedResolver = nil
			}
			c.handleInbound(msg)
			if called != (scenario == "denied") || len(other.send) != 0 || len(hub.broadcast) != 0 {
				t.Fatal("invalid event was resolved or broadcast")
			}
			if len(c.send) != 1 || (<-c.send).Type != "error" {
				t.Fatal("sender must receive an error")
			}
		})
	}
}
