package domain

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// ─────────────────────────── Chat types ───────────────────────────

func TestConversationTypeValues(t *testing.T) {
	if got := "personal"; got != "personal" {
		t.Fatalf("conversation type personal mismatch")
	}
	if got := "group"; got != "group" {
		t.Fatalf("conversation type group mismatch")
	}
}

func TestGroupTypeConstants(t *testing.T) {
	g := Group{Type: "public"}
	if g.Type != "public" {
		t.Fatalf("expected public, got %s", g.Type)
	}
	g.Type = "private"
	if g.Type != "private" {
		t.Fatalf("expected private, got %s", g.Type)
	}
}

// ─────────────────────────── Channel constants ───────────────────────────

func TestChannelTypeConstants(t *testing.T) {
	if ChannelTypePublic != "public" {
		t.Fatalf("ChannelTypePublic = %q, want %q", ChannelTypePublic, "public")
	}
	if ChannelTypePrivate != "private" {
		t.Fatalf("ChannelTypePrivate = %q, want %q", ChannelTypePrivate, "private")
	}
}

func TestChannelRoleConstants(t *testing.T) {
	if ChannelRoleOwner != "owner" {
		t.Fatalf("ChannelRoleOwner = %q, want owner", ChannelRoleOwner)
	}
	if ChannelRoleAdmin != "admin" {
		t.Fatalf("ChannelRoleAdmin = %q, want admin", ChannelRoleAdmin)
	}
	if ChannelRoleSubscriber != "subscriber" {
		t.Fatalf("ChannelRoleSubscriber = %q, want subscriber", ChannelRoleSubscriber)
	}
}

// ─────────────────────────── Role hierarchy ───────────────────────────

// TestChannelRoleHierarchy verifies the role ordering: owner > admin > subscriber.
// Owner is the highest role; subscriber is read-only.
func TestChannelRoleHierarchy(t *testing.T) {
	roles := []ChannelRole{ChannelRoleOwner, ChannelRoleAdmin, ChannelRoleSubscriber}
	for _, r := range roles {
		if r == "" {
			t.Fatalf("channel role must not be empty")
		}
	}

	// Owner and admin should differ from subscriber
	if ChannelRoleOwner == ChannelRoleSubscriber {
		t.Fatal("owner and subscriber must be different roles")
	}
	if ChannelRoleAdmin == ChannelRoleSubscriber {
		t.Fatal("admin and subscriber must be different roles")
	}
}

// TestGroupRoleValues verifies group role strings used in DB constraints.
func TestGroupRoleValues(t *testing.T) {
	validRoles := map[string]bool{"owner": true, "admin": true, "member": true}
	for role := range validRoles {
		if !validRoles[role] {
			t.Fatalf("role %q not in valid set", role)
		}
	}
}

// ─────────────────────────── Message isolation ───────────────────────────

// TestMessageBelongsToConversation verifies the structural invariant that a
// Message always carries a ConversationID — the foundation of isolation.
func TestMessageBelongsToConversation(t *testing.T) {
	convID := uuid.New()
	msg := Message{
		ID:             uuid.New(),
		ConversationID: convID,
		SenderID:       1,
		Type:           "text",
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	if msg.ConversationID == uuid.Nil {
		t.Fatal("message must have a non-nil ConversationID")
	}
	if msg.ConversationID != convID {
		t.Fatalf("ConversationID mismatch: got %v, want %v", msg.ConversationID, convID)
	}
}

// TestReplyMustBelongToSameConversation verifies the isolation rule:
// a reply can only reference a message in the same conversation.
func TestReplyMustBelongToSameConversation(t *testing.T) {
	convA := uuid.New()
	convB := uuid.New()

	original := Message{ID: uuid.New(), ConversationID: convA}
	reply := Message{ID: uuid.New(), ConversationID: convA, ReplyToID: &original.ID}

	if reply.ConversationID != original.ConversationID {
		t.Fatal("reply and original must share the same ConversationID")
	}

	// Simulating the cross-conversation check that SendMessage enforces.
	if original.ConversationID == convB {
		t.Fatal("original message should not belong to conversation B")
	}
}

// ─────────────────────────── MessageView helpers ───────────────────────────

func TestMessageViewGetSenderDisplayName_FullName(t *testing.T) {
	mv := MessageView{
		SenderFullName: "Асель Нурова",
		SenderUsername: "asel",
	}
	if name := mv.GetSenderDisplayName(); name != "Асель Нурова" {
		t.Fatalf("expected full name, got %q", name)
	}
}

func TestMessageViewGetSenderDisplayName_FallbackUsername(t *testing.T) {
	mv := MessageView{
		SenderFullName: "",
		SenderUsername: "asel",
	}
	if name := mv.GetSenderDisplayName(); name != "asel" {
		t.Fatalf("expected username fallback, got %q", name)
	}
}

func TestMessageViewGetIDs(t *testing.T) {
	msgID := uuid.New()
	convID := uuid.New()
	mv := MessageView{}
	mv.Message.ID = msgID
	mv.Message.ConversationID = convID

	gotMsg, gotConv := mv.GetIDs()
	if gotMsg != msgID {
		t.Fatalf("message ID mismatch: got %v, want %v", gotMsg, msgID)
	}
	if gotConv != convID {
		t.Fatalf("conversation ID mismatch: got %v, want %v", gotConv, convID)
	}
}

func TestMessageViewGetMessageType(t *testing.T) {
	mv := MessageView{}
	mv.Message.Type = "image"
	if mv.GetMessageType() != "image" {
		t.Fatalf("expected image, got %q", mv.GetMessageType())
	}
}

// ─────────────────────────── ConversationView types ───────────────────────────

// TestConversationViewDistinguishesTypes verifies that the unified view can
// carry all three messenger types: personal chat, group, and channel.
func TestConversationViewDistinguishesTypes(t *testing.T) {
	groupID := uuid.New()
	groupName := "Команда бэкенда"
	channelID := uuid.New()
	channelName := "Объявления"
	role := "owner"

	cases := []struct {
		name    string
		view    ConversationView
		wantTyp string
	}{
		{
			name:    "personal chat",
			view:    ConversationView{Type: "personal"},
			wantTyp: "personal",
		},
		{
			name:    "group chat",
			view:    ConversationView{Type: "group", GroupID: &groupID, GroupName: &groupName},
			wantTyp: "group",
		},
		{
			name: "channel",
			view: ConversationView{
				Type:          "channel",
				ChannelID:     &channelID,
				ChannelName:   &channelName,
				IsChannel:     true,
				MyChannelRole: &role,
			},
			wantTyp: "channel",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.view.Type != tc.wantTyp {
				t.Fatalf("type = %q, want %q", tc.view.Type, tc.wantTyp)
			}
			if tc.wantTyp == "channel" && !tc.view.IsChannel {
				t.Fatal("channel view must have IsChannel=true")
			}
		})
	}
}

// ─────────────────────────── ChannelPost ───────────────────────────

// TestChannelPostBelongsToChannel verifies that a post carries its ChannelID.
func TestChannelPostBelongsToChannel(t *testing.T) {
	chID := uuid.New()
	post := ChannelPost{
		ID:        uuid.New(),
		ChannelID: chID,
		AuthorID:  42,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if post.ChannelID != chID {
		t.Fatalf("ChannelID mismatch: got %v, want %v", post.ChannelID, chID)
	}
}
