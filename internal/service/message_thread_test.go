package service

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestBuildMessageThreadBuildsNestedChronologicalTree(t *testing.T) {
	conversationID := uuid.New()
	root := threadTestMessage(uuid.New(), conversationID, nil, time.Unix(1, 0))
	firstID := uuid.New()
	secondID := uuid.New()
	first := threadTestMessage(firstID, conversationID, &root.ID, time.Unix(2, 0))
	second := threadTestMessage(secondID, conversationID, &root.ID, time.Unix(3, 0))
	nested := threadTestMessage(uuid.New(), conversationID, &firstID, time.Unix(4, 0))

	response := buildMessageThread(root, []*domain.MessageView{nested, second, first}, false)

	if response.Root.ID != root.ID || response.ReplyCount != 3 || response.Truncated {
		t.Fatalf("unexpected thread summary: %#v", response)
	}
	if len(response.Replies) != 2 {
		t.Fatalf("top-level replies = %d, want 2", len(response.Replies))
	}
	if response.Replies[0].Message.ID != firstID || response.Replies[1].Message.ID != secondID {
		t.Fatalf("top-level replies are not chronological")
	}
	if len(response.Replies[0].Replies) != 1 || response.Replies[0].Replies[0].Message.ID != nested.ID {
		t.Fatalf("nested reply was not attached to its parent")
	}
}

func TestBuildMessageThreadExcludesMessagesFromAnotherConversation(t *testing.T) {
	conversationID := uuid.New()
	root := threadTestMessage(uuid.New(), conversationID, nil, time.Unix(1, 0))
	foreign := threadTestMessage(uuid.New(), uuid.New(), &root.ID, time.Unix(2, 0))

	response := buildMessageThread(root, []*domain.MessageView{foreign}, true)

	if response.ReplyCount != 0 || len(response.Replies) != 0 || !response.Truncated {
		t.Fatalf("unexpected filtered thread: %#v", response)
	}
}

func threadTestMessage(id, conversationID uuid.UUID, replyToID *uuid.UUID, createdAt time.Time) *domain.MessageView {
	return &domain.MessageView{Message: domain.Message{
		ID:             id,
		ConversationID: conversationID,
		ReplyToID:      replyToID,
		CreatedAt:      createdAt,
	}}
}
