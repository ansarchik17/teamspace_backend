package service

import (
	"testing"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestDocumentologDoneColumnFromList(t *testing.T) {
	fallback := uuid.New()
	done := uuid.New()
	got := documentologDoneColumnFromList([]*domain.KanbanColumn{
		{ID: fallback, Key: domain.ColTODO},
		{ID: done, Key: domain.ColDone},
	}, fallback)
	if got != done {
		t.Fatalf("done column = %s, want %s", got, done)
	}
}
