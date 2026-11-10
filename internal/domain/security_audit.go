package domain

import (
	"context"
	"net/netip"
	"time"

	"github.com/google/uuid"
)

type SecurityRequestMetadata struct {
	IP                     string
	RequestID              string
	AdministrationRecorded bool
}
type securityMetadataKey struct{}

func WithSecurityMetadata(ctx context.Context, meta *SecurityRequestMetadata) context.Context {
	return context.WithValue(ctx, securityMetadataKey{}, meta)
}
func SecurityMetadata(ctx context.Context) *SecurityRequestMetadata {
	meta, _ := ctx.Value(securityMetadataKey{}).(*SecurityRequestMetadata)
	return meta
}

type SecurityAuditEvent struct {
	ID                    int64      `json:"id"`
	OrganizationID        *uuid.UUID `json:"organization_id"`
	ActorID               *int64     `json:"actor_id"`
	ObjectType            string     `json:"object_type"`
	ObjectID              string     `json:"object_id"`
	Action                string     `json:"action"`
	Outcome               string     `json:"outcome"`
	IP                    *string    `json:"ip"`
	RequestID             string     `json:"request_id"`
	Method                string     `json:"method"`
	Route                 string     `json:"route"`
	Status                int        `json:"status"`
	AdministrationAuditID *int64     `json:"administration_audit_id"`
	CreatedAt             time.Time  `json:"created_at"`
}
type SecurityAuditFilter struct {
	Limit    int
	BeforeID int64
	MaxID    int64
	ActorID  *int64
	Action   string
	Outcome  string
	IP       string
	From     *time.Time
	To       *time.Time
}

func (f SecurityAuditFilter) Valid() bool {
	if f.Limit < 1 || f.Limit > 10000 || f.BeforeID < 0 || f.MaxID < 0 || (f.ActorID != nil && *f.ActorID <= 0) || len(f.Action) > 120 {
		return false
	}
	for _, c := range f.Action {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '.' || c == '_') {
			return false
		}
	}
	if f.Outcome != "" && f.Outcome != "success" && f.Outcome != "failure" {
		return false
	}
	if f.From != nil && f.To != nil && !f.From.Before(*f.To) {
		return false
	}
	if f.IP != "" {
		if _, err := netip.ParseAddr(f.IP); err != nil {
			if _, err := netip.ParsePrefix(f.IP); err != nil {
				return false
			}
		}
	}
	return true
}

type SecurityAuditPage struct {
	Items        []SecurityAuditEvent `json:"items"`
	NextBeforeID *int64               `json:"next_before_id,omitempty"`
	SnapshotID   int64                `json:"snapshot_id"`
}
