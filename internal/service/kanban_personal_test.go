package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

type deadlineStoreStub struct {
	items    []domain.TaskDeadlineReminder
	claim    bool
	listErr  error
	claimErr error
	finishes []bool
	token    uuid.UUID
}

func (s *deadlineStoreStub) ListTaskDeadlineReminders(context.Context, time.Time) ([]domain.TaskDeadlineReminder, error) {
	return s.items, s.listErr
}
func (s *deadlineStoreStub) ClaimTaskDeadlineReminder(_ context.Context, _ domain.TaskDeadlineReminder, token uuid.UUID) (bool, error) {
	s.token = token
	return s.claim, s.claimErr
}
func (s *deadlineStoreStub) FinishTaskDeadlineReminder(_ context.Context, _ domain.TaskDeadlineReminder, token uuid.UUID, success bool) error {
	if token != s.token {
		return errors.New("claim token changed")
	}
	s.finishes = append(s.finishes, success)
	return nil
}

type deadlineSenderStub struct {
	requests []domain.CreateNotificationRequest
	err      error
}

func (s *deadlineSenderStub) CreateAndSendNotification(_ context.Context, req domain.CreateNotificationRequest) error {
	s.requests = append(s.requests, req)
	return s.err
}

func TestDeadlineWorkerDelivery(t *testing.T) {
	for _, scenario := range []string{"success", "send_error", "already_claimed", "claim_error", "list_error", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			item := domain.TaskDeadlineReminder{TaskID: uuid.New(), BoardID: uuid.New(), UserID: 7, Title: "Report", TaskKey: "AB-1", DueAt: time.Now(), Kind: "overdue"}
			store := &deadlineStoreStub{items: []domain.TaskDeadlineReminder{item}, claim: true}
			sender := &deadlineSenderStub{}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch scenario {
			case "send_error":
				sender.err = errors.New("notification storage failed")
			case "already_claimed":
				store.claim = false
			case "claim_error":
				store.claimErr = errors.New("db failed")
			case "list_error":
				store.listErr = errors.New("db failed")
			case "cancelled":
				cancel()
			}
			processTaskDeadlines(ctx, store, sender, zap.NewNop())
			if scenario == "success" || scenario == "send_error" {
				if len(sender.requests) != 1 || len(store.finishes) != 1 || store.finishes[0] != (scenario == "success") {
					t.Fatalf("incorrect delivery state: %+v, %+v", sender, store)
				}
				got := sender.requests[0]
				if got.UserID != 7 || got.Type != "kanban_task_overdue" || got.Metadata["task_id"] != item.TaskID.String() {
					t.Fatalf("wrong notification: %+v", got)
				}
			} else if len(sender.requests) != 0 || len(store.finishes) != 0 {
				t.Fatal("unclaimed reminder was delivered")
			}
		})
	}
}

func TestTaskDeadlineKinds(t *testing.T) {
	for _, kind := range []string{"due_soon", "overdue"} {
		got := taskDeadlineNotification(domain.TaskDeadlineReminder{Kind: kind, DueAt: time.Now()})
		if got.Type != "kanban_task_"+kind || got.Metadata["due_at"] == "" || got.Title == "" {
			t.Fatalf("invalid payload: %+v", got)
		}
	}
}

func TestMyTasksRejectsInvalidFilters(t *testing.T) {
	s := &KanbanService{}
	for _, filter := range []domain.MyTasksFilter{{Limit: -1}, {Limit: 101}, {Offset: -1}, {Priority: "urgent"}} {
		if _, err := s.ListMyTasks(context.Background(), 1, filter); err == nil {
			t.Fatalf("accepted %+v", filter)
		}
	}
}
