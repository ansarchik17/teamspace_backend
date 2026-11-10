package domain

import (
	"github.com/google/uuid"
	"time"
)

type ContactOffboardingRequest struct {
	SuccessorID int64  `json:"successor_id"`
	Reason      string `json:"reason"`
}

type ContactOffboardingResult struct {
	UserID             int64      `json:"user_id"`
	SuccessorID        int64      `json:"successor_id"`
	ActorID            int64      `json:"actor_id"`
	Reason             string     `json:"reason"`
	DryRun             bool       `json:"dry_run"`
	Applied            bool       `json:"applied"`
	OffboardedAt       time.Time  `json:"offboarded_at"`
	Boards             int        `json:"boards"`
	Projects           int        `json:"projects"`
	Files              int        `json:"files"`
	Folders            int        `json:"folders"`
	Units              int        `json:"units"`
	DirectReports      int        `json:"direct_reports"`
	HandoverFolderID   *uuid.UUID `json:"handover_folder_id,omitempty"`
	IdentitySyncStatus string     `json:"identity_sync_status"`
}
