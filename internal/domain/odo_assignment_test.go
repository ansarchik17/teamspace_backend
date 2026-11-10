package domain

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func validODOEvent() ODOAssignmentEvent {
	date := "2026-10-15"
	link := "https://odo.example.test/documents/doc-1"
	return ODOAssignmentEvent{SchemaVersion: 1, EventID: uuid.New(), EventType: "created", OccurredAt: time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC), Revision: 1, OrganizationID: uuid.New(), SourceSystem: "documentolog", IdentityIssuer: "https://id.example.test/realms/workspace", AssignmentID: "asg-1", DocumentID: "doc-1", AuthorSub: "author", AssigneeSub: "assignee", Title: "Поручение", Description: "Текст", Priority: "medium", DueDate: &date, Timezone: "Asia/Almaty", Status: "assigned", SourceURL: &link}
}

func TestODOAssignmentDecodeAndHash(t *testing.T) {
	e := validODOEvent()
	body, err := json.Marshal(e)
	require.NoError(t, err)
	decoded, err := DecodeODOAssignmentEvent(body, "https://odo.example.test")
	require.NoError(t, err)
	require.Equal(t, e, decoded)
	full, snapshot := e.Hashes()
	e.EventID = uuid.New()
	e.EventType = "updated"
	e.OccurredAt = e.OccurredAt.Add(time.Hour)
	otherFull, otherSnapshot := e.Hashes()
	require.NotEqual(t, full, otherFull)
	require.Equal(t, snapshot, otherSnapshot)
	e.Description = ""
	_, otherSnapshot = e.Hashes()
	require.NotEqual(t, snapshot, otherSnapshot)
	require.Equal(t, "2026-10-15T18:59:59.999999Z", decoded.DueAt().Format(time.RFC3339Nano))
	examples, err := os.ReadFile("../../docs/contracts/examples/odo-assignment-events.json")
	require.NoError(t, err)
	var container []json.RawMessage
	require.NoError(t, json.Unmarshal(examples, &container))
	require.NotEmpty(t, container)
	for _, body := range container {
		_, err := DecodeODOAssignmentEvent(body, "https://odo.example.test")
		require.NoError(t, err)
	}
}

func TestODOAssignmentRejectsInvalidSnapshots(t *testing.T) {
	e := validODOEvent()
	body, _ := json.Marshal(e)
	for _, test := range []struct {
		name   string
		change func(map[string]any)
	}{
		{"missing clear field", func(m map[string]any) { delete(m, "description") }},
		{"unknown field", func(m map[string]any) { m["board_id"] = uuid.NewString() }},
		{"null title", func(m map[string]any) { m["title"] = nil }},
		{"invalid date", func(m map[string]any) { m["due_date"] = "2026-02-30" }},
		{"local zone", func(m map[string]any) { m["timezone"] = "Local" }},
		{"unknown zone", func(m map[string]any) { m["timezone"] = "Asia/Unknown" }},
		{"non UTC", func(m map[string]any) { m["occurred_at"] = "2026-10-14T12:00:00+00:00" }},
		{"legacy identity", func(m map[string]any) { m["assignee_sub"] = "workspace:3" }},
		{"unsafe revision", func(m map[string]any) { m["revision"] = 9007199254740992 }},
		{"fractional revision", func(m map[string]any) { m["revision"] = 1.5 }},
		{"legacy priority", func(m map[string]any) { m["priority"] = "normal" }},
		{"legacy status", func(m map[string]any) { m["status"] = "done" }},
		{"empty title", func(m map[string]any) { m["title"] = "  " }},
		{"foreign origin", func(m map[string]any) { m["source_url"] = "https://foreign.example.test/doc/1" }},
		{"credentials", func(m map[string]any) { m["source_url"] = "https://user:secret@odo.example.test/doc/1" }},
		{"token query", func(m map[string]any) { m["source_url"] = "https://odo.example.test/doc/1?token=secret" }},
		{"illegal completion", func(m map[string]any) { m["completed_at"] = "2026-10-14T12:00:00Z" }},
		{"illegal close", func(m map[string]any) { m["closed_at"] = "2026-10-14T12:00:00Z" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			var m map[string]any
			require.NoError(t, json.Unmarshal(body, &m))
			test.change(m)
			b, _ := json.Marshal(m)
			_, err := DecodeODOAssignmentEvent(b, "https://odo.example.test")
			require.Error(t, err)
		})
	}
	for _, b := range []string{string(body) + " {}", strings.Replace(string(body), `"revision":1`, `"revision":1,"revision":2`, 1), `null`, `[]`} {
		_, err := DecodeODOAssignmentEvent([]byte(b), "https://odo.example.test")
		require.Error(t, err)
	}
	e.Title = strings.Repeat("Я", 255)
	require.NoError(t, e.Validate("https://odo.example.test"))
	e.Title += "Я"
	require.Error(t, e.Validate("https://odo.example.test"))
}
