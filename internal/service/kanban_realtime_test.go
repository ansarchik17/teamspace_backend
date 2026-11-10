package service

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/websocket"
	"go.uber.org/zap"
)

type realtimeBoardStub struct {
	allowed bool
	board   *domain.Board
	columns map[uuid.UUID]*domain.KanbanColumn
	members []*domain.BoardMember
	err     error
}

type realtimeSenderStub struct {
	recipients []int64
	events     []*websocket.Message
}

func (s *realtimeSenderStub) SendToUser(userID int64, event *websocket.Message) int {
	s.recipients = append(s.recipients, userID)
	s.events = append(s.events, event)
	return 1
}

type realtimeRecipientStub struct {
	realtimeBoardStub
	membersErr  error
	contextErr  error
	hasDeadline bool
}

func (s *realtimeRecipientStub) GetMembers(ctx context.Context, _ uuid.UUID) ([]*domain.BoardMember, error) {
	s.contextErr = ctx.Err()
	_, s.hasDeadline = ctx.Deadline()
	return s.members, s.membersErr
}

func TestKanbanRealtimeNotifier(t *testing.T) {
	for _, eventType := range []string{"kanban_task_moved", "kanban_column_moved"} {
		for _, fail := range []bool{false, true} {
			t.Run(eventType+map[bool]string{false: "/success", true: "/lookup_failure"}[fail], func(t *testing.T) {
				boardID := uuid.New()
				boards := &realtimeRecipientStub{realtimeBoardStub: realtimeBoardStub{
					board:   &domain.Board{ID: boardID, OwnerID: 1},
					members: []*domain.BoardMember{{UserID: 1}, {UserID: 2}, {UserID: 2}, {UserID: 0}},
				}}
				if fail {
					boards.membersErr = errors.New("database unavailable")
				}
				sender := &realtimeSenderStub{}
				n := &kanbanRealtimeNotifier{boards: boards, sender: sender, logger: zap.NewNop()}
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				event := &websocket.Message{Type: eventType, From: 2, BoardID: &boardID}
				n.notify(ctx, event)
				if boards.contextErr != nil || !boards.hasDeadline {
					t.Fatal("delivery must have an independent bounded context")
				}
				if fail {
					if len(sender.recipients) != 0 {
						t.Fatal("failed lookup must not deliver events")
					}
					return
				}
				if !reflect.DeepEqual(sender.recipients, []int64{1, 2}) {
					t.Fatalf("unexpected recipients: %v", sender.recipients)
				}
				for _, got := range sender.events {
					if got != event || got.Timestamp == 0 {
						t.Fatalf("incorrect event: %+v", got)
					}
				}
			})
		}
	}
}

func TestMoveColumnRequiresPosition(t *testing.T) {
	s := &KanbanService{}
	if err := s.MoveColumn(context.Background(), 1, uuid.New(), MoveColumnRequest{}); err == nil {
		t.Fatal("missing position must be rejected before accessing the repository")
	}
}

func (s realtimeBoardStub) HasKanbanAccess(context.Context, uuid.UUID, int64) (bool, error) {
	return s.allowed, s.err
}
func (s realtimeBoardStub) GetBoard(context.Context, uuid.UUID) (*domain.Board, error) {
	return s.board, nil
}
func (s realtimeBoardStub) GetColumn(_ context.Context, id uuid.UUID) (*domain.KanbanColumn, error) {
	return s.columns[id], nil
}
func (s realtimeBoardStub) GetMembers(context.Context, uuid.UUID) ([]*domain.BoardMember, error) {
	return s.members, nil
}

type realtimeTaskStub struct{ task *domain.Task }

func (s realtimeTaskStub) GetByID(context.Context, uuid.UUID) (*domain.Task, error) {
	return s.task, nil
}

func TestResolveTaskMovedRecipients(t *testing.T) {
	for _, scenario := range []string{"valid", "denied", "db_error", "wrong_board", "stale_move", "foreign_column", "missing_column", "missing_task", "missing_board"} {
		t.Run(scenario, func(t *testing.T) {
			boardID, taskID, from, to := uuid.New(), uuid.New(), uuid.New(), uuid.New()
			boards := realtimeBoardStub{allowed: true, board: &domain.Board{ID: boardID, OwnerID: 1},
				columns: map[uuid.UUID]*domain.KanbanColumn{from: {BoardID: boardID}, to: {BoardID: boardID}},
				members: []*domain.BoardMember{{UserID: 1}, {UserID: 2}, {UserID: 2}}}
			tasks := realtimeTaskStub{task: &domain.Task{ID: taskID, BoardID: boardID, ColumnID: to}}
			switch scenario {
			case "denied":
				boards.allowed = false
			case "db_error":
				boards.err = errors.New("database unavailable")
			case "wrong_board":
				tasks.task.BoardID = uuid.New()
			case "stale_move":
				tasks.task.ColumnID = from
			case "foreign_column":
				boards.columns[from].BoardID = uuid.New()
			case "missing_column":
				delete(boards.columns, to)
			case "missing_task":
				tasks.task = nil
			case "missing_board":
				boards.board = nil
			}
			got, err := resolveTaskMovedRecipients(context.Background(), boards, tasks, 2, boardID, taskID, from, to)
			if scenario == "valid" {
				if err != nil || !reflect.DeepEqual(got, []int64{1, 2}) {
					t.Fatalf("got %v, %v", got, err)
				}
			} else if err == nil || len(got) != 0 {
				t.Fatalf("expected rejection, got %v, %v", got, err)
			}
		})
	}
}
