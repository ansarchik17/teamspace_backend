package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

// DeleteUser is the legacy directory endpoint. Explicit organization scopes
// remain exclusive to platform administrators, including on this endpoint.
func (r *OrganizationRepository) DeleteUser(ctx context.Context, actor, target int64) error {
	var organizationID *uuid.UUID
	if err := r.db.QueryRow(ctx, `SELECT organization_id FROM users WHERE id=$1 AND deleted_at IS NULL`, target).Scan(&organizationID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return apperrors.NotFound("employee")
		}
		return err
	}
	if organizationID == nil {
		return deletionProtected()
	}
	var role domain.Role
	if err := r.db.QueryRow(ctx, `SELECT role FROM users WHERE id=$1 AND deleted_at IS NULL AND is_active`, actor).Scan(&role); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return apperrors.Forbidden("administrator required")
		}
		return err
	}
	if role != domain.RoleSuperAdmin {
		organizationID = nil
	}
	return r.DeleteAdministrationUser(ctx, actor, target, organizationID)
}

func deletionProtected() error {
	return apperrors.New("ADMIN_DELETION_PROTECTED", "protected account", 409)
}

func (r *OrganizationRepository) DeleteAdministrationUser(ctx context.Context, actor, target int64, org *uuid.UUID) error {
	if target <= 0 {
		return apperrors.Validation("invalid user id")
	}
	scope, err := r.accessScope(ctx, actor, org)
	if err != nil {
		return err
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var owner *int64
	var status string
	if err = tx.QueryRow(ctx, `SELECT owner_id,status FROM organizations WHERE id=$1 FOR UPDATE`, scope.ID).Scan(&owner, &status); err != nil {
		return err
	}
	if err = authorizeAdministrationLocked(ctx, tx, actor, owner, org != nil); err != nil {
		return err
	}
	if status == "deleted" {
		return domain.ErrOrganizationNotFound
	}
	var role domain.Role
	if err = tx.QueryRow(ctx, `SELECT role FROM users WHERE id=$1 AND organization_id=$2 AND deleted_at IS NULL FOR UPDATE`, target, scope.ID).Scan(&role); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return apperrors.NotFound("employee")
		}
		return err
	}
	if !domain.CanDeleteEmployee(actor, target, role, owner) {
		return deletionProtected()
	}
	if _, err = tx.Exec(ctx, `INSERT INTO administrative_identity_deletions(user_id,keycloak_id) SELECT id,keycloak_id FROM users WHERE id=$1 AND NULLIF(keycloak_id,'') IS NOT NULL ON CONFLICT(user_id) DO NOTHING`, target); err != nil {
		return err
	}
	before, err := administrationSnapshot(ctx, tx, scope.ID, "user_deletion", fmt.Sprint(target))
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE users SET deleted_at=now(),is_active=false,team_id=NULL,manager_id=NULL,updated_at=now() WHERE id=$1`, target); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE users SET manager_id=NULL,updated_at=now() WHERE organization_id=$1 AND manager_id=$2`, scope.ID, target); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE teams SET manager_id=NULL,updated_at=now() WHERE organization_id=$1 AND manager_id=$2`, scope.ID, target); err != nil {
		return err
	}
	after, err := administrationSnapshot(ctx, tx, scope.ID, "user_deletion", fmt.Sprint(target))
	if err != nil {
		return err
	}
	if err = writeAdministrationAudit(ctx, tx, scope.ID, actor, "user.delete", "user_deletion", fmt.Sprint(target), before, after); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Related documents, accounting and identities stay available to historical
// records. All live members are deleted atomically with the company tombstone.
func (r *OrganizationRepository) DeletePlatformOrganization(ctx context.Context, actor int64, org uuid.UUID) ([]int64, error) {
	if err := r.ensurePlatformAdmin(ctx, actor); err != nil {
		return nil, err
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var status string
	if err = tx.QueryRow(ctx, `SELECT status FROM organizations WHERE id=$1 FOR UPDATE`, org).Scan(&status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrOrganizationNotFound
		}
		return nil, err
	}
	if err = authorizeAdministrationLocked(ctx, tx, actor, nil, true); err != nil {
		return nil, err
	}
	if status == "deleted" {
		return nil, domain.ErrOrganizationNotFound
	}
	rows, err := tx.Query(ctx, `SELECT id,role FROM users WHERE organization_id=$1 AND deleted_at IS NULL ORDER BY id FOR UPDATE`, org)
	if err != nil {
		return nil, err
	}
	ids := []int64{}
	protected := false
	for rows.Next() {
		var id int64
		var role domain.Role
		if err = rows.Scan(&id, &role); err != nil {
			rows.Close()
			return nil, err
		}
		protected = protected || id == actor || role == domain.RoleSuperAdmin
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if protected {
		return nil, deletionProtected()
	}
	if _, err = tx.Exec(ctx, `INSERT INTO administrative_identity_deletions(user_id,keycloak_id) SELECT id,keycloak_id FROM users WHERE organization_id=$1 AND deleted_at IS NULL AND NULLIF(keycloak_id,'') IS NOT NULL ON CONFLICT(user_id) DO NOTHING`, org); err != nil {
		return nil, err
	}
	before, err := administrationSnapshot(ctx, tx, org, "organization", org.String())
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE users SET deleted_at=now(),is_active=false,team_id=NULL,manager_id=NULL,updated_at=now() WHERE organization_id=$1 AND deleted_at IS NULL`, org); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE teams SET deleted_at=COALESCE(deleted_at,now()),manager_id=NULL,updated_at=now() WHERE organization_id=$1`, org); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE organizations SET status='deleted',owner_id=NULL,updated_at=now() WHERE id=$1`, org); err != nil {
		return nil, err
	}
	after, err := administrationSnapshot(ctx, tx, org, "organization", org.String())
	if err != nil {
		return nil, err
	}
	if err = writeAdministrationAudit(ctx, tx, org, actor, "organization.delete", "organization", org.String(), before, after); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return ids, nil
}
