package domain

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// UnmarshalJSON distinguishes an omitted optional field from an explicit null.
func (p *UpdateTaskPatch) UnmarshalJSON(data []byte) error {
	type plain UpdateTaskPatch
	var next plain
	if err := json.Unmarshal(data, &next); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if fields == nil {
		return fmt.Errorf("task patch must be an object")
	}
	for _, name := range []string{"title", "priority", "original_estimate_sec", "assignee_ids", "assignee_id", "column_id", "status"} {
		if raw, ok := fields[name]; ok && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return fmt.Errorf("%s cannot be null", name)
		}
	}
	if raw, ok := fields["due_at"]; ok && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		next.ClearDueAt = true
	}
	if raw, ok := fields["description"]; ok && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		next.ClearDescription = true
	}
	*p = UpdateTaskPatch(next)
	return nil
}
