package repository

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/west6ide/workspace-backend/internal/apperrors"
)

func (r *TaskRepository) ODOUserOrganization(ctx context.Context, actor int64) (uuid.UUID, error) {
	var org uuid.UUID
	err := r.db.QueryRow(ctx, `SELECT o.id FROM users u JOIN organizations o ON o.id=u.organization_id
 WHERE u.id=$1 AND u.is_active AND u.deleted_at IS NULL AND o.status='active'`, actor).Scan(&org)
	if errors.Is(err, pgx.ErrNoRows) {
		return org, apperrors.Forbidden("active company membership required")
	}
	if err != nil {
		return org, err
	}
	return org, requireOrganizationModule(ctx, r.db, org, "odo")
}

func (r *TaskRepository) ODOReaderOrganization(ctx context.Context, actor int64) (uuid.UUID, error) {
	org, err := r.ODOUserOrganization(ctx, actor)
	if err != nil {
		return org, err
	}
	var managed, canRead bool
	err = r.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM organization_access_apps WHERE organization_id=$1 AND application='odo' AND enabled),
 EXISTS(
 SELECT 1 FROM organization_access_assignments a JOIN organization_position_permissions p ON p.organization_id=a.organization_id AND p.position_id=a.position_id
 WHERE a.organization_id=$1 AND a.user_id=u.id AND p.application='odo' AND 'assignment.read'=ANY(p.permissions) AND p.permissions && ARRAY['document.read','document.read_department','document.read_all']::text[])
 FROM users u JOIN organizations o ON o.id=u.organization_id WHERE u.id=$2 AND o.id=$1`, org, actor).Scan(&managed, &canRead)
	if err != nil {
		return org, err
	}
	if managed && !canRead {
		return org, apperrors.Forbidden("assignment read access required")
	}
	return org, nil
}

func (r *TaskRepository) ValidateODOAssignmentScope(ctx context.Context, actor int64, org uuid.UUID, assignment, document, author, assignee, department, board string) error {
	actorOrg, err := r.ODOUserOrganization(ctx, actor)
	if err != nil {
		return err
	}
	if actorOrg != org {
		return apperrors.Forbidden("integration actor belongs to another company")
	}
	var valid bool
	err = r.db.QueryRow(ctx, `SELECT
 EXISTS(SELECT 1 FROM users WHERE keycloak_id::text=$2 AND organization_id=$1 AND deleted_at IS NULL) AND
 EXISTS(SELECT 1 FROM users WHERE keycloak_id::text=$3 AND organization_id=$1 AND is_active AND deleted_at IS NULL)`, org, author, assignee).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return apperrors.Forbidden("assignment participants are outside the company")
	}
	if department != "" {
		dept, err := uuid.Parse(department)
		if err != nil {
			return apperrors.Validation("invalid assignment department")
		}
		err = r.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM teams WHERE id=$1 AND organization_id=$2)`, dept, org).Scan(&valid)
		if err != nil {
			return err
		}
		if !valid {
			return apperrors.Forbidden("assignment department is outside the company")
		}
	}
	systemKey := "documentolog-assignments:" + org.String()
	if board != "" {
		id, err := uuid.Parse(board)
		if err != nil {
			return apperrors.Validation("invalid integration board")
		}
		err = r.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM boards b JOIN users u ON u.id=b.owner_id
 WHERE b.id=$1 AND b.system_key=$2 AND b.deleted_at IS NULL AND u.organization_id=$3)`, id, systemKey, org).Scan(&valid)
		if err != nil {
			return err
		}
		if !valid {
			return apperrors.Forbidden("integration board is outside the company")
		}
	}
	// The legacy global assignment key may collide. Reject instead of looking
	// up/updating another tenant; task 3 will replace it with a composite key.
	err = r.db.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM tasks t JOIN boards b ON b.id=t.board_id
 WHERE t.source_system='documentolog' AND t.source_assignment_id=$1 AND t.deleted_at IS NULL
 AND (b.system_key IS DISTINCT FROM $2 OR b.deleted_at IS NOT NULL OR t.source_document_id IS DISTINCT FROM $3))`, assignment, systemKey, document).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return apperrors.Forbidden("assignment does not belong to the company")
	}
	return nil
}
