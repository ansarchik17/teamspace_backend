package domain

import (
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"time"
)

type TaskChecklistItem struct {
	ID          uuid.UUID `json:"id"`
	TaskID      uuid.UUID `json:"task_id"`
	Title       string    `json:"title"`
	IsCompleted bool      `json:"is_completed"`
	Position    int       `json:"position"`
	CreatedBy   int64     `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type CreateTaskChecklistItem struct {
	Title       string `json:"title" binding:"required"`
	Position    *int   `json:"position"`
	IsCompleted bool   `json:"is_completed"`
}

type UpdateTaskChecklistItem struct {
	Title       *string `json:"title"`
	IsCompleted *bool   `json:"is_completed"`
	Position    *int    `json:"position"`
}

// MarshalJSON keeps the canonical API and the frontend aliases consistent.
func (i TaskChecklistItem) MarshalJSON() ([]byte, error) {
	type item TaskChecklistItem
	return json.Marshal(struct {
		item
		Text      string `json:"text"`
		Completed bool   `json:"completed"`
	}{item(i), i.Title, i.IsCompleted})
}

func checklistAliases(data []byte) (*string, *bool, *int, error) {
	var v struct {
		Title       *string `json:"title"`
		Text        *string `json:"text"`
		IsCompleted *bool   `json:"is_completed"`
		Completed   *bool   `json:"completed"`
		Position    *int    `json:"position"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, nil, nil, err
	}
	if v.Title != nil && v.Text != nil && *v.Title != *v.Text {
		return nil, nil, nil, fmt.Errorf("conflicting title and text")
	}
	if v.IsCompleted != nil && v.Completed != nil && *v.IsCompleted != *v.Completed {
		return nil, nil, nil, fmt.Errorf("conflicting completion values")
	}
	if v.Title == nil {
		v.Title = v.Text
	}
	if v.IsCompleted == nil {
		v.IsCompleted = v.Completed
	}
	return v.Title, v.IsCompleted, v.Position, nil
}
func (r *CreateTaskChecklistItem) UnmarshalJSON(data []byte) error {
	title, done, pos, err := checklistAliases(data)
	if err != nil {
		return err
	}
	*r = CreateTaskChecklistItem{Position: pos}
	if title != nil {
		r.Title = *title
	}
	if done != nil {
		r.IsCompleted = *done
	}
	return nil
}
func (r *UpdateTaskChecklistItem) UnmarshalJSON(data []byte) error {
	title, done, pos, err := checklistAliases(data)
	if err != nil {
		return err
	}
	*r = UpdateTaskChecklistItem{Title: title, IsCompleted: done, Position: pos}
	return nil
}
