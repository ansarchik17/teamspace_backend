package domain

import "github.com/google/uuid"

type ODOAssignmentBackfill struct {
	OperationID      uuid.UUID          `json:"operation_id"`
	TaskID           *uuid.UUID         `json:"task_id"`
	ExpectedTaskHash string             `json:"expected_task_hash"`
	Reason           string             `json:"reason"`
	Event            ODOAssignmentEvent `json:"event"`
}
