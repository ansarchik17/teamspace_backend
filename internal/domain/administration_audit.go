package domain

import (
	"encoding/json"
	"github.com/google/uuid"
	"time"
)

type AdministrationAudit struct {
	ID             int64           `json:"id"`
	OrganizationID uuid.UUID       `json:"organization_id"`
	ActorID        int64           `json:"actor_id"`
	Action         string          `json:"action"`
	ObjectType     string          `json:"object_type"`
	ObjectID       string          `json:"object_id"`
	Before         json.RawMessage `json:"before"`
	After          json.RawMessage `json:"after"`
	CreatedAt      time.Time       `json:"created_at"`
}

type AdministrationAuditFilter struct {
	Limit    int
	BeforeID int64
}

type AdministrationAuditPage struct {
	Items        []AdministrationAudit `json:"items"`
	NextBeforeID *int64                `json:"next_before_id,omitempty"`
}
