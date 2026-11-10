package domain

import (
	"github.com/google/uuid"
	"time"
)

type MyTasksFilter struct {
	BoardID   *uuid.UUID
	ProjectID *uuid.UUID
	Status    string
	Priority  string
	Overdue   bool
	Limit     int
	Offset    int
}

type MyTasksResponse struct {
	Tasks   []*Task `json:"tasks"`
	Limit   int     `json:"limit"`
	Offset  int     `json:"offset"`
	HasMore bool    `json:"has_more"`
}

type TaskDeadlineReminder struct {
	TaskID  uuid.UUID
	BoardID uuid.UUID
	UserID  int64
	Title   string
	TaskKey string
	DueAt   time.Time
	Kind    string
}
