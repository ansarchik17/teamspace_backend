package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

// Only explicit database projections are accepted as snapshots. Never marshal
// request DTOs, user records, provider settings or error messages into this log.
func writeAdministrationAudit(ctx context.Context, tx pgx.Tx, org uuid.UUID, actor int64, action, kind, id string, before, after json.RawMessage) error {
	if len(before) == 0 {
		before = json.RawMessage(`null`)
	}
	if len(after) == 0 {
		after = json.RawMessage(`null`)
	}

	meta := domain.SecurityMetadata(ctx)
	if meta != nil {
		if _, err := tx.Exec(ctx, `SELECT set_config('workspace.audit_ip',$1,true),set_config('workspace.audit_request_id',$2,true)`, meta.IP, meta.RequestID); err != nil {
			return err
		}
	}
	_, err := tx.Exec(ctx, `INSERT INTO organization_admin_audit(organization_id,actor_id,action,object_type,object_id,before_value,after_value) VALUES($1,$2,$3,$4,$5,$6,$7)`, org, actor, action, kind, id, before, after)
	if err == nil && meta != nil {
		meta.AdministrationRecorded = true
	}
	return err
}

func administrationSnapshot(ctx context.Context, db administrationQuerier, org uuid.UUID, kind, id string) (json.RawMessage, error) {
	var query string
	switch kind {
	case "employment_timezone":
		query = `SELECT jsonb_build_object('timezone',timezone,'revision',revision) FROM organization_employment_settings WHERE organization_id=$1 AND organization_id::text=$2`
	case "storage_quota":
		query = `SELECT jsonb_build_object('quota_bytes',quota_bytes,'revision',revision) FROM organization_storage_accounts WHERE organization_id=$1 AND organization_id::text=$2`
	case "user_storage_quota":
		query = `SELECT jsonb_build_object('quota_bytes',COALESCE(a.quota_bytes,p.quota_bytes),'revision',COALESCE(a.revision,0)) FROM users u CROSS JOIN drive_storage_policy p LEFT JOIN drive_storage_accounts a ON a.user_id=u.id WHERE u.organization_id=$1 AND u.id::text=$2 AND u.deleted_at IS NULL`
	case "module":
		query = `SELECT jsonb_build_object('available',available,'enabled',enabled,'revision',revision) FROM organization_modules WHERE organization_id=$1 AND application=$2`
	case "organization":
		query = `SELECT jsonb_build_object('name',name,'slug',slug,'status',status,'owner_id',owner_id) FROM organizations WHERE id=$1 AND id::text=$2`
	case "unit":
		query = `SELECT jsonb_build_object('name',name,'parent_unit_id',parent_team_id,'unit_type',unit_type,'manager_id',manager_id,'display_order',display_order,'archived',deleted_at IS NOT NULL) FROM teams WHERE organization_id=$1 AND id::text=$2`
	case "user_assignment":
		query = `SELECT jsonb_build_object('unit_id',team_id,'manager_id',manager_id) FROM users WHERE organization_id=$1 AND id::text=$2 AND deleted_at IS NULL`
	case "user_deletion":
		query = `SELECT jsonb_build_object('is_active',is_active,'deleted',deleted_at IS NOT NULL) FROM users WHERE organization_id=$1 AND id::text=$2`
	case "user_status":
		query = `SELECT jsonb_build_object('is_active',is_active,'blocked',EXISTS(SELECT 1 FROM organization_user_blocks b WHERE b.user_id=u.id)) FROM users u WHERE organization_id=$1 AND id::text=$2 AND deleted_at IS NULL`
	case "growy_roles":
		query = `SELECT COALESCE(jsonb_agg(jsonb_build_object('position',position_name,'roles',growy_roles) ORDER BY position_key),'[]'::jsonb) FROM organization_growy_position_roles WHERE organization_id=$1 AND $2=''`
	case "identity_provider":
		query = `SELECT jsonb_build_object('provider_type',provider_type,'alias',alias,'display_name',display_name,'enabled',enabled,'secret_set',secret_set,'status',status,'updated_at',updated_at) FROM organization_identity_providers WHERE organization_id=$1 AND id::text=$2`
	default:
		return nil, fmt.Errorf("unsupported audit object type")
	}
	var data json.RawMessage
	err := db.QueryRow(ctx, query, org, id).Scan(&data)
	if err == pgx.ErrNoRows {
		return json.RawMessage(`null`), nil
	}
	return data, err
}

func accessAuditSnapshot(ctx context.Context, tx pgx.Tx, org uuid.UUID, action string, data AccessChange) (json.RawMessage, error) {
	var query string
	var key any = data.ID
	switch action {
	case "position", "delete":
		query = `SELECT jsonb_build_object('id',id,'name',name,'revision',revision) FROM organization_access_positions WHERE organization_id=$1 AND id=$2`
	case "policy":
		query = `SELECT jsonb_build_object('permissions',permissions,'revision',revision) FROM organization_position_permissions WHERE organization_id=$1 AND position_id=$2 AND application=$3`
	case "application":
		key = data.Application
		query = `SELECT jsonb_build_object('enabled',enabled,'revision',revision) FROM organization_access_apps WHERE organization_id=$1 AND application=$2`
	case "member", "unassign":
		key = data.UserID
		query = `SELECT jsonb_build_object('position_id',a.position_id,'position',u.position) FROM users u LEFT JOIN organization_access_assignments a ON a.user_id=u.id AND a.organization_id=u.organization_id WHERE u.organization_id=$1 AND u.id=$2 AND u.deleted_at IS NULL`
	case "link":
		key = "sd"
		query = `SELECT jsonb_build_object('company_id',external_company_id,'is_tenant_root',is_tenant_root) FROM organization_app_links WHERE organization_id=$1 AND application=$2`
	default:
		return nil, apperrors.Validation("unknown action")
	}
	args := []any{org, key}
	if action == "policy" {
		args = append(args, data.Application)
	}
	var value json.RawMessage
	err := tx.QueryRow(ctx, query, args...).Scan(&value)
	if err == pgx.ErrNoRows {
		return json.RawMessage(`null`), nil
	}
	return value, err
}

type administrationTx struct{ pgx.Tx }

func (t administrationTx) BeginTx(ctx context.Context, options pgx.TxOptions) (pgx.Tx, error) {
	if options != (pgx.TxOptions{}) {
		return nil, fmt.Errorf("cannot change transaction options inside an administrative mutation")
	}
	return t.Begin(ctx)
}

func auditOrganizationMutation[T any](ctx context.Context, r *OrganizationRepository, actor int64, org uuid.UUID, platformOnly bool, action, kind, id string, mutation func(*OrganizationRepository) (T, error)) (T, error) {
	var zero T
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return zero, err
	}
	defer tx.Rollback(ctx)
	var owner *int64
	var status string
	if err := tx.QueryRow(ctx, `SELECT owner_id,status FROM organizations WHERE id=$1 FOR UPDATE`, org).Scan(&owner, &status); err != nil {
		if err == pgx.ErrNoRows {
			return zero, domain.ErrOrganizationNotFound
		}
		return zero, err
	}
	if err := authorizeAdministrationLocked(ctx, tx, actor, owner, platformOnly); err != nil {
		return zero, err
	}
	if status != "active" {
		return zero, apperrors.Conflict("organization suspended")
	}
	before, err := administrationSnapshot(ctx, tx, org, kind, id)
	if err != nil {
		return zero, err
	}
	result, err := mutation(&OrganizationRepository{db: administrationTx{tx}, logger: r.logger, capacity: r.capacity})
	if err != nil {
		return zero, err
	}
	if unit, ok := any(result).(*domain.OrganizationUnit); ok && id == "" {
		id = unit.ID.String()
	}
	if provider, ok := any(result).(*domain.OrganizationIdentityProvider); ok && id == "" {
		id = provider.ID.String()
	}
	after, err := administrationSnapshot(ctx, tx, org, kind, id)
	if err != nil {
		return zero, err
	}
	if err := writeAdministrationAudit(ctx, tx, org, actor, action, kind, id, before, after); err != nil {
		return zero, err
	}
	if err := tx.Commit(ctx); err != nil {
		return zero, err
	}
	return result, nil
}

func auditOrganizationMutationError(ctx context.Context, r *OrganizationRepository, actor int64, org uuid.UUID, platformOnly bool, action, kind, id string, mutation func(*OrganizationRepository) error) error {
	_, err := auditOrganizationMutation(ctx, r, actor, org, platformOnly, action, kind, id, func(inner *OrganizationRepository) (struct{}, error) { return struct{}{}, mutation(inner) })
	return err
}

func (r *OrganizationRepository) ListAdministrationAudit(ctx context.Context, actor int64, org *uuid.UUID, platform bool, filter domain.AdministrationAuditFilter) (*domain.AdministrationAuditPage, error) {
	if platform {
		if err := r.ensurePlatformAdmin(ctx, actor); err != nil {
			return nil, err
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
	if filter.Limit < 1 || filter.Limit > 100 || filter.BeforeID < 0 {
		return nil, apperrors.Validation("invalid audit pagination")
	}
	rows, err := r.db.Query(ctx, `SELECT id,organization_id,actor_id,action,object_type,object_id,before_value,after_value,created_at FROM organization_admin_audit WHERE ($1::uuid IS NULL OR organization_id=$1) AND ($2::bigint=0 OR id<$2) ORDER BY id DESC LIMIT $3`, org, filter.BeforeID, filter.Limit+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := &domain.AdministrationAuditPage{Items: []domain.AdministrationAudit{}}
	for rows.Next() {
		var item domain.AdministrationAudit
		if err := rows.Scan(&item.ID, &item.OrganizationID, &item.ActorID, &item.Action, &item.ObjectType, &item.ObjectID, &item.Before, &item.After, &item.CreatedAt); err != nil {
			return nil, err
		}
		out.Items = append(out.Items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out.Items) > filter.Limit {
		out.Items = out.Items[:filter.Limit]
		id := out.Items[len(out.Items)-1].ID
		out.NextBeforeID = &id
	}
	return out, nil
}
