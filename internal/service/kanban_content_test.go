package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/websocket"
	"go.uber.org/zap"
)

func TestChecklistValidation(t *testing.T) {
	for _, title := range []string{"", "  ", strings.Repeat("я", 501)} {
		if err := validateChecklistFields(&title, nil); err == nil {
			t.Fatal("accepted empty or long title")
		}
	}
	title := "  Проверить  "
	zero := 0
	if err := validateChecklistFields(&title, &zero); err != nil || title != "Проверить" {
		t.Fatalf("normalization failed: %q, %v", title, err)
	}
	negative := -1
	if err := validateChecklistFields(nil, &negative); err == nil {
		t.Fatal("negative position accepted")
	}
	s := &KanbanService{}
	if _, err := s.UpdateChecklistItem(context.Background(), 1, uuid.New(), uuid.New(), domain.UpdateTaskChecklistItem{}); err == nil {
		t.Fatal("empty update accepted")
	}
}

func TestTaskCommentValidation(t *testing.T) {
	if _, err := validateTaskComment("  \n", nil); err == nil {
		t.Fatal("blank comment accepted")
	}
	if _, err := validateTaskComment(strings.Repeat("x", 10001), nil); err == nil {
		t.Fatal("oversize comment accepted")
	}
	attachment := "/api/v1/kanban/tasks/file/download"
	if _, err := validateTaskComment("", &attachment); err != nil {
		t.Fatal("photo-only comment rejected")
	}
	if got, err := validateTaskComment(" text ", nil); err != nil || got != "text" {
		t.Fatalf("invalid normalization: %q %v", got, err)
	}
}

type contentEventDelivery struct {
	userID int64
	event  *websocket.Message
}
type contentEventSender struct{ events chan contentEventDelivery }

func (s contentEventSender) SendToUser(userID int64, event *websocket.Message) int {
	s.events <- contentEventDelivery{userID, event}
	return 1
}

func TestTaskContentNotificationAndSubtaskParents(t *testing.T) {
	boardID, oldParent, newParent := uuid.New(), uuid.New(), uuid.New()
	sender := contentEventSender{events: make(chan contentEventDelivery, 10)}
	s := &KanbanService{realtime: &kanbanRealtimeNotifier{boards: realtimeBoardStub{board: &domain.Board{ID: boardID, OwnerID: 1}, members: []*domain.BoardMember{{UserID: 2}}}, sender: sender, logger: zap.NewNop()}}
	task := &domain.Task{ID: uuid.New(), BoardID: boardID, ParentID: &newParent}
	s.notifySubtaskParents(task, 2, &oldParent)
	counts := map[uuid.UUID]int{}
	for i := 0; i < 4; i++ {
		select {
		case sent := <-sender.events:
			if sent.userID != 1 && sent.userID != 2 {
				t.Fatal("event sent outside board")
			}
			if sent.event.Type != "kanban_subtasks_changed" || *sent.event.BoardID != boardID {
				t.Fatal("wrong event")
			}
			counts[*sent.event.TaskID]++
		case <-time.After(time.Second):
			t.Fatal("missing realtime update")
		}
	}
	if counts[oldParent] != 2 || counts[newParent] != 2 {
		t.Fatalf("both parents must be updated: %v", counts)
	}
}
