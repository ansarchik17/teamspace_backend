package repository

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func (r *TaskRepository) ODOCompletionTarget(ctx context.Context, actor int64, taskID uuid.UUID) (domain.ODOCommandTarget, error) {
	var target domain.ODOCommandTarget
	org, err := r.ODOReaderOrganization(ctx, actor)
	if err != nil {
		return target, err
	}
	var managed, allowed bool
	err = r.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM organization_access_apps WHERE organization_id=$1 AND application='odo' AND enabled),
 EXISTS(SELECT 1 FROM organization_access_assignments a JOIN organization_position_permissions p ON p.organization_id=a.organization_id AND p.position_id=a.position_id WHERE a.organization_id=$1 AND a.user_id=u.id AND p.application='odo' AND 'assignment.complete'=ANY(p.permissions)) FROM users u JOIN organizations o ON o.id=u.organization_id WHERE u.id=$2 AND o.id=$1`, org, actor).Scan(&managed, &allowed)
	if err != nil {
		return target, err
	}
	if managed && !allowed {
		return target, apperrors.Forbidden("assignment completion access required")
	}
	err = r.db.QueryRow(ctx, `SELECT t.source_organization_id,t.source_assignment_id,t.source_document_id,COALESCE(t.source_identity_issuer,''),COALESCE(u.keycloak_id::text,'')
 FROM tasks t JOIN task_assignees a ON a.task_id=t.id AND a.user_id=$2
 JOIN users u ON u.id=a.user_id AND u.organization_id=$3 AND u.is_active AND u.deleted_at IS NULL
 JOIN boards b ON b.id=t.board_id AND b.deleted_at IS NULL AND b.system_key='documentolog-assignments:'||$3::text
 WHERE t.id=$1 AND t.source_system='documentolog' AND t.source_organization_id=$3 AND t.source_revision>0 AND t.deleted_at IS NULL`, taskID, actor, org).Scan(&target.OrganizationID, &target.AssignmentID, &target.DocumentID, &target.IdentityIssuer, &target.ActorSub)
	if errors.Is(err, pgx.ErrNoRows) {
		return target, apperrors.NotFound("ODO assignment")
	}
	if err != nil {
		return target, err
	}
	if target.ActorSub == "" || target.IdentityIssuer == "" {
		return target, apperrors.Forbidden("verified assignment identity required")
	}
	return target, nil
}
