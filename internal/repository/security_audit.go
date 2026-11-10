package repository

import (
	"context"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

// The actor comes only from verified server authentication; no request body is stored.
func (r *OrganizationRepository) RecordSecurityEvent(ctx context.Context, e domain.SecurityAuditEvent) error {
	_, err := r.db.Exec(ctx, `INSERT INTO security_audit_events(organization_id,actor_id,action,outcome,ip,request_id,method,route,status,object_type,object_id)
 VALUES(COALESCE($1::uuid,(SELECT organization_id FROM users WHERE id=$2)),$2,$3,$4,NULLIF($5,'')::inet,$6,$7,$8,$9,$10,$11)`, e.OrganizationID, e.ActorID, e.Action, e.Outcome, e.IP, e.RequestID, e.Method, e.Route, e.Status, e.ObjectType, e.ObjectID)
	return err
}

func (r *OrganizationRepository) SecurityAudit(ctx context.Context, actor int64, org *uuid.UUID, platform bool, f domain.SecurityAuditFilter) (*domain.SecurityAuditPage, error) {
	if platform {
		if err := r.ensurePlatformAdmin(ctx, actor); err != nil {
			return nil, err
		}
		if org != nil {
			if _, err := r.platformOrganizationScope(ctx, actor, *org); err != nil {
				return nil, err
			}
		}
	} else {
		if org != nil {
			return nil, apperrors.Forbidden("administrator required")
		}
		scope, err := r.accessScope(ctx, actor, nil)
		if err != nil {
			return nil, err
		}
		org = &scope.ID
	}
	if !f.Valid() {
		return nil, apperrors.Validation("invalid audit filter")
	}
	out := &domain.SecurityAuditPage{Items: []domain.SecurityAuditEvent{}, SnapshotID: f.MaxID}
	if out.SnapshotID == 0 {
		if err := r.db.QueryRow(ctx, `SELECT COALESCE(max(id),0) FROM security_audit_events WHERE ($1::uuid IS NULL OR organization_id=$1)`, org).Scan(&out.SnapshotID); err != nil {
			return nil, err
		}
	}
	rows, err := r.db.Query(ctx, `SELECT id,organization_id,actor_id,action,outcome,host(ip),request_id,method,route,status,administration_audit_id,created_at,object_type,object_id
 FROM security_audit_events WHERE ($1::uuid IS NULL OR organization_id=$1) AND id<=$2
 AND ($3::bigint=0 OR id<$3) AND ($4::bigint IS NULL OR actor_id=$4)
 AND ($5='' OR action=$5) AND ($6='' OR outcome=$6)
 AND ($7='' OR ip <<= NULLIF($7,'')::inet)
 AND ($8::timestamptz IS NULL OR created_at>=$8) AND ($9::timestamptz IS NULL OR created_at<$9)
 ORDER BY id DESC LIMIT $10`, org, out.SnapshotID, f.BeforeID, f.ActorID, f.Action, f.Outcome, f.IP, f.From, f.To, f.Limit+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var e domain.SecurityAuditEvent
		if err := rows.Scan(&e.ID, &e.OrganizationID, &e.ActorID, &e.Action, &e.Outcome, &e.IP, &e.RequestID, &e.Method, &e.Route, &e.Status, &e.AdministrationAuditID, &e.CreatedAt, &e.ObjectType, &e.ObjectID); err != nil {
			return nil, err
		}
		out.Items = append(out.Items, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out.Items) > f.Limit {
		out.Items = out.Items[:f.Limit]
		last := out.Items[len(out.Items)-1].ID
		out.NextBeforeID = &last
	}
	return out, nil
}
