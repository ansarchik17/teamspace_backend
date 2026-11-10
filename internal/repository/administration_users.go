package repository

import (
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"strings"
)

func (r *OrganizationRepository) ListAdministrationUsers(ctx context.Context, actor int64, org *uuid.UUID, filter domain.AdministrationUserFilter) (*domain.AdministrationUserPage, error) {
	scope, err := r.accessScope(ctx, actor, org)
	if err != nil {
		return nil, err
	}
	if filter.Limit < 1 || filter.Limit > 100 || filter.Offset < 0 || len([]rune(filter.Query)) > 255 {
		return nil, apperrors.Validation("invalid user pagination")
	}
	// Repeatable-read keeps the count and page consistent during changes.
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	query := strings.TrimSpace(filter.Query)
	const predicate = `u.organization_id=$1 AND u.deleted_at IS NULL AND ($2='' OR strpos(lower(concat_ws(' ',u.full_name_local,u.first_name,u.last_name,u.username,u.email,u.position)),lower($2))>0)`
	out := &domain.AdministrationUserPage{Items: []domain.AdministrationUser{}, Limit: filter.Limit, Offset: filter.Offset}
	if err := tx.QueryRow(ctx, `SELECT count(*)::int FROM users u WHERE `+predicate, scope.ID, query).Scan(&out.Total); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT u.id,COALESCE(NULLIF(u.full_name_local,''),NULLIF(trim(concat_ws(' ',u.first_name,u.last_name)),''),u.username,''),COALESCE(u.username,''),COALESCE(u.email,''),u.position,u.role,u.is_active,EXISTS(SELECT 1 FROM organization_user_blocks b WHERE b.user_id=u.id),(o.owner_id=u.id) IS TRUE FROM users u JOIN organizations o ON o.id=u.organization_id WHERE `+predicate+` ORDER BY u.id LIMIT $3 OFFSET $4`, scope.ID, query, filter.Limit, filter.Offset)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var u domain.AdministrationUser
		if err := rows.Scan(&u.ID, &u.FullName, &u.Username, &u.Email, &u.Position, &u.Role, &u.IsActive, &u.IsBlocked, &u.IsDirector); err != nil {
			rows.Close()
			return nil, err
		}
		out.Items = append(out.Items, u)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *OrganizationRepository) SetEmployeeBlocked(ctx context.Context, actor, target int64, org *uuid.UUID, blocked bool) error {
	scope, err := r.accessScope(ctx, actor, org)
	if err != nil {
		return err
	}
	return auditOrganizationMutationError(ctx, r, actor, scope.ID, org != nil, "user.block.update", "user_status", fmt.Sprint(target), func(inner *OrganizationRepository) error {
		var role domain.Role
		var owner *int64
		err := inner.db.QueryRow(ctx, `SELECT u.role,o.owner_id FROM users u JOIN organizations o ON o.id=u.organization_id WHERE u.id=$1 AND u.organization_id=$2 AND u.deleted_at IS NULL FOR UPDATE OF u`, target, scope.ID).Scan(&role, &owner)
		if errors.Is(err, pgx.ErrNoRows) {
			return apperrors.NotFound("employee")
		}
		if err != nil {
			return err
		}
		if !domain.CanChangeEmployeeBlock(actor, target, role, owner) {
			return apperrors.Forbidden("protected account")
		}
		if blocked {
			_, err = inner.db.Exec(ctx, `INSERT INTO organization_user_blocks(user_id,organization_id,actor_id) VALUES($1,$2,$3) ON CONFLICT(user_id) DO NOTHING`, target, scope.ID, actor)
		} else {
			_, err = inner.db.Exec(ctx, `DELETE FROM organization_user_blocks WHERE user_id=$1`, target)
		}
		if err != nil {
			return err
		}
		_, err = inner.db.Exec(ctx, `UPDATE users SET is_active=$2,updated_at=now() WHERE id=$1`, target, !blocked)
		return err
	})
}

// Lightweight authoritative lookup for authenticated HTTP and websocket upgrades.
func (r *UserRepository) IsUserActive(ctx context.Context, id int64) (bool, error) {
	var active bool
	err := r.db.QueryRow(ctx, `SELECT workspace_user_can_login($1)`, id).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return active, err
}
