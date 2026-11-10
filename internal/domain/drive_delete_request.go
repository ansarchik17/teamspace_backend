package domain

import (
	"time"

	"github.com/google/uuid"
)

type DriveDeleteRequestStatus string

const (
	DriveDeleteRequestPending  DriveDeleteRequestStatus = "pending"
	DriveDeleteRequestApproved DriveDeleteRequestStatus = "approved"
	DriveDeleteRequestRejected DriveDeleteRequestStatus = "rejected"
	DriveDeleteRequestExecuted DriveDeleteRequestStatus = "executed"
)

type DriveDeleteRequest struct {
	ID              uuid.UUID                `json:"id" db:"id"`
	ResourceType    ResourceType             `json:"resource_type" db:"resource_type"`
	ResourceID      uuid.UUID                `json:"resource_id" db:"resource_id"`
	ResourceName    string                   `json:"resource_name" db:"resource_name"`
	ParentFolderID  *uuid.UUID               `json:"parent_folder_id,omitempty" db:"parent_folder_id"`
	OwnerID         int64                    `json:"owner_id" db:"owner_id"`
	RequestedBy     int64                    `json:"requested_by" db:"requested_by"`
	ResolvedBy      *int64                   `json:"resolved_by,omitempty" db:"resolved_by"`
	Recursive       bool                     `json:"recursive" db:"recursive"`
	Status          DriveDeleteRequestStatus `json:"status" db:"status"`
	RejectionReason *string                  `json:"rejection_reason,omitempty" db:"rejection_reason"`
	CreatedAt       time.Time                `json:"created_at" db:"created_at"`
	ResolvedAt      *time.Time               `json:"resolved_at,omitempty" db:"resolved_at"`
	ExecutedAt      *time.Time               `json:"executed_at,omitempty" db:"executed_at"`
}

type DriveDeleteOperationResult struct {
	Status          string     `json:"status"`
	Message         string     `json:"message"`
	DeleteRequestID *uuid.UUID `json:"delete_request_id,omitempty"`
}
