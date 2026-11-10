package websocket

import (
	"testing"

	"go.uber.org/zap"
)

func TestSendToUserSkipsExcludedSessionButKeepsOtherSessionsSynced(t *testing.T) {
	hub := NewHub(zap.NewNop(), nil, nil)

	currentSession := &Client{
		UserID:     7,
		SessionKey: BuildSessionKey("token-current"),
		send:       make(chan *Message, 1),
		logger:     zap.NewNop(),
	}
	otherSession := &Client{
		UserID:     7,
		SessionKey: BuildSessionKey("token-other"),
		send:       make(chan *Message, 1),
		logger:     zap.NewNop(),
	}

	hub.clients[7] = map[*Client]bool{
		currentSession: true,
		otherSession:   true,
	}

	msg := &Message{
		Type:              "new_message",
		To:                7,
		ExcludeSessionKey: currentSession.SessionKey,
	}

	if sent := hub.SendToUser(7, msg); sent != 1 {
		t.Fatalf("SendToUser() sent %d messages, want 1", sent)
	}

	select {
	case <-currentSession.send:
		t.Fatal("current session should not receive the echoed message")
	default:
	}

	select {
	case got := <-otherSession.send:
		if got != msg {
			t.Fatal("other session received an unexpected message instance")
		}
	default:
		t.Fatal("other session should stay in sync and receive the message")
	}
}

func TestSendToUserDoesNotReportFullBufferAsDelivered(t *testing.T) {
	hub := NewHub(zap.NewNop(), nil, nil)
	client := &Client{UserID: 9, send: make(chan *Message, 1), logger: zap.NewNop()}
	client.send <- &Message{Type: "already_queued"}
	hub.clients[9] = map[*Client]bool{client: true}

	if sent := hub.SendToUser(9, &Message{Type: "new_message"}); sent != 0 {
		t.Fatalf("SendToUser() sent %d messages with a full buffer, want 0", sent)
	}
}

func TestSendToUserWithoutExcludedSessionStillDeliversToAllSessions(t *testing.T) {
	hub := NewHub(zap.NewNop(), nil, nil)

	firstSession := &Client{
		UserID:     11,
		SessionKey: BuildSessionKey("token-a"),
		send:       make(chan *Message, 1),
		logger:     zap.NewNop(),
	}
	secondSession := &Client{
		UserID:     11,
		SessionKey: BuildSessionKey("token-b"),
		send:       make(chan *Message, 1),
		logger:     zap.NewNop(),
	}

	hub.clients[11] = map[*Client]bool{
		firstSession:  true,
		secondSession: true,
	}

	msg := &Message{
		Type: "new_message",
		To:   11,
	}

	hub.SendToUser(11, msg)

	select {
	case <-firstSession.send:
	default:
		t.Fatal("first session should receive the message")
	}

	select {
	case <-secondSession.send:
	default:
		t.Fatal("second session should receive the message")
	}
}

func TestCloseUserSessionsBySessionKeys_ClosesOnlyMatchingSessions(t *testing.T) {
	hub := NewHub(zap.NewNop(), nil, nil)

	currentSession := &Client{
		UserID:     15,
		SessionKey: BuildSessionKey("web_device_id:web-device-current"),
		send:       make(chan *Message, 1),
		logger:     zap.NewNop(),
	}
	oldSession := &Client{
		UserID:     15,
		SessionKey: BuildSessionKey("linked_device:web-device-old"),
		send:       make(chan *Message, 1),
		logger:     zap.NewNop(),
	}
	anotherSession := &Client{
		UserID:     15,
		SessionKey: BuildSessionKey("web_device_id:web-device-other"),
		send:       make(chan *Message, 1),
		logger:     zap.NewNop(),
	}

	hub.clients[15] = map[*Client]bool{
		currentSession: true,
		oldSession:     true,
		anotherSession: true,
	}

	closed := hub.CloseUserSessionsBySessionKeys(15, []string{
		BuildSessionKey("linked_device:web-device-old"),
		BuildSessionKey("web_device_id:web-device-old"),
	})

	if closed != 1 {
		t.Fatalf("expected 1 closed session, got %d", closed)
	}
	if _, exists := hub.clients[15][oldSession]; exists {
		t.Fatal("old session should be removed from hub")
	}
	if _, exists := hub.clients[15][currentSession]; !exists {
		t.Fatal("current session must stay connected")
	}
	if _, exists := hub.clients[15][anotherSession]; !exists {
		t.Fatal("other session must stay connected")
	}
}
