package domain

import "github.com/google/uuid"

type DriveBatchRequest struct {
	Action         string           `json:"action"`
	Items          []DriveBatchItem `json:"items"`
	TargetParentID *uuid.UUID       `json:"target_parent_id"`
}
type DriveBatchItem struct {
	ResourceType ResourceType `json:"resource_type"`
	ResourceID   uuid.UUID    `json:"resource_id"`
	Name         *string      `json:"name,omitempty"`
	Recursive    bool         `json:"recursive,omitempty"`
}
type DriveBatchResult struct {
	ResourceType ResourceType `json:"resource_type"`
	ResourceID   uuid.UUID    `json:"resource_id"`
	Status       int          `json:"status"`
	Result       any          `json:"result,omitempty"`
	Error        string       `json:"error,omitempty"`
	ErrorCode    string       `json:"code,omitempty"`
}
type DriveBatchResponse struct {
	Results   []DriveBatchResult `json:"results"`
	Succeeded int                `json:"succeeded"`
	Failed    int                `json:"failed"`
}
