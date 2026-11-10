package domain

import (
	"encoding/json"
	"testing"
)

func TestTaskPatchJSON(t *testing.T) {
	for _, body := range []string{`{"title":null}`, `{"priority":null}`, `{"assignee_ids":null}`, `{"due_at":"bad date"}`, `null`} {
		var p UpdateTaskPatch
		if err := json.Unmarshal([]byte(body), &p); err == nil {
			t.Errorf("accepted invalid patch %s", body)
		}
	}
	var p UpdateTaskPatch
	if err := json.Unmarshal([]byte(`{"description":null,"due_at":null,"assignee_ids":[]}`), &p); err != nil {
		t.Fatal(err)
	}
	if !p.ClearDueAt || !p.ClearDescription || p.AssigneeIDs == nil || len(p.AssigneeIDs) != 0 {
		t.Fatalf("lost explicit clearing: %+v", p)
	}
	if err := json.Unmarshal([]byte(`{}`), &p); err != nil {
		t.Fatal(err)
	}
	if p.ClearDueAt || p.ClearDescription || p.AssigneeIDs != nil {
		t.Fatalf("omitted fields must not clear values: %+v", p)
	}
}
