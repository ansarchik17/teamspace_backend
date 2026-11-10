package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestCreateTaskValidation(t *testing.T) {
	for _, body := range []string{
		`{"title":"   "}`, `{"priority":"urgent"}`, `{"original_estimate_sec":-1}`,
		`{"assignee_ids":[0]}`, `{"assignee_id":-1}`, `{"due_at":"0001-01-01T00:00:00Z"}`,
	} {
		req := CreateTaskRequest{BoardID: uuid.New(), Title: "Task"}
		if err := json.Unmarshal([]byte(body), &req); err != nil {
			t.Fatal(err)
		}
		if err := validateCreateTask(&req); err == nil {
			t.Errorf("accepted %s", body)
		}
	}
	req := CreateTaskRequest{BoardID: uuid.New(), Title: "  Задача  "}
	if err := validateCreateTask(&req); err != nil {
		t.Fatal(err)
	}
	if req.Title != "Задача" || req.Priority != "medium" {
		t.Fatalf("wrong defaults: %+v", req)
	}
	// The service must reject bad input even when bypassing HTTP validation.
	s := &KanbanService{}
	if _, err := s.CreateTask(context.Background(), 1, CreateTaskRequest{}); err == nil {
		t.Fatal("missing board/title accepted")
	}
}

func TestTaskPatchValidation(t *testing.T) {
	for _, body := range []string{
		`{"title":"\t "}`, `{"priority":""}`, `{"priority":"urgent"}`,
		`{"original_estimate_sec":-1}`, `{"assignee_ids":[1,-2]}`,
		`{"description":"x","clear_description":true}`,
		`{"due_at":"2026-10-01T12:00:00Z","clear_due_at":true}`,
	} {
		var p domain.UpdateTaskPatch
		if err := json.Unmarshal([]byte(body), &p); err != nil {
			t.Fatal(err)
		}
		if err := validateTaskPatch(&p); err == nil {
			t.Errorf("accepted %s", body)
		}
	}
	var p domain.UpdateTaskPatch
	if err := json.Unmarshal([]byte(`{"title":" Test ","priority":" HIGH ","assignee_ids":[2,2],"assignee_id":3,"due_at":"2020-01-01T00:00:00Z"}`), &p); err != nil {
		t.Fatal(err)
	}
	if err := validateTaskPatch(&p); err != nil {
		t.Fatal(err)
	}
	if *p.Title != "Test" || *p.Priority != "high" || len(p.AssigneeIDs) != 2 {
		t.Fatalf("wrong normalization: %+v", p)
	}
	for _, body := range []string{`{}`, `{"assignee_ids":[]}`} {
		if err := json.Unmarshal([]byte(body), &p); err != nil {
			t.Fatal(err)
		}
		if err := validateTaskPatch(&p); err != nil {
			t.Fatal(err)
		}
		if (p.AssigneeIDs == nil) != (body == `{}`) {
			t.Fatal("lost omitted versus empty assignees")
		}
	}
	s := &KanbanService{}
	if _, err := s.UpdateTask(context.Background(), 1, uuid.New(), nil); err == nil {
		t.Fatal("nil patch accepted")
	}
}
